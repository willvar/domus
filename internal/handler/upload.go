package handler

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"

	"zephyr/config"
	"zephyr/internal/auth"
	"zephyr/internal/middleware"
	"zephyr/internal/model"
	"zephyr/internal/service"
	"zephyr/internal/store"
)

// OSSUploadParams holds parameters for the async oss_upload job.
type OSSUploadParams struct {
	UploadID string `json:"upload_id"`
	OSSKey   string `json:"oss_key"`
	FileName string `json:"file_name"`
	FileSize int64  `json:"file_size"`
	UserID   string `json:"user_id"`
	Username string `json:"username"`
	TempDir  string `json:"temp_dir"`
}

// handleUploadDispatch routes POST /file/upload to init, complete, or conflict check.
func (h *Handler) handleUploadDispatch(c *fiber.Ctx) error {
	var peek struct {
		UploadID string   `json:"upload_id"`
		Names    []string `json:"names"`
	}
	if err := c.BodyParser(&peek); err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "invalid_request"})
	}
	if peek.UploadID != "" {
		return h.handleUploadComplete(c)
	}
	if len(peek.Names) > 0 {
		return h.handleUploadConflictCheck(c)
	}
	return h.handleUploadInit(c)
}

// hashFile computes the SHA-256 hex digest of a file.
func hashFile(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// Per-upload mutex to prevent concurrent writes to the same file
var uploadMu sync.Map // map[string]*sync.Mutex

func getUploadMutex(uploadID string) *sync.Mutex {
	v, _ := uploadMu.LoadOrStore(uploadID, &sync.Mutex{})
	return v.(*sync.Mutex)
}

// nextAvailableName generates a conflict-free filename like "file (1).txt"
func nextAvailableName(name string, usedNames map[string]struct{}) string {
	base, ext := splitFileName(name)
	index := 1
	for {
		candidate := fmt.Sprintf("%s (%d)%s", base, index, ext)
		if _, exists := usedNames[candidate]; !exists {
			return candidate
		}
		index++
	}
}

func splitFileName(name string) (string, string) {
	dot := strings.LastIndex(name, ".")
	if dot <= 0 {
		return name, ""
	}
	return name[:dot], name[dot:]
}

// handleUploadConflictCheck checks which files already exist at the target path.
func (h *Handler) handleUploadConflictCheck(c *fiber.Ctx) error {
	var body struct {
		Path  string   `json:"path"`
		Names []string `json:"names"`
	}
	if err := c.BodyParser(&body); err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "invalid_request"})
	}

	session := c.Locals("session").(*model.Session)
	dirPath := body.Path
	if dirPath != "" && !strings.HasSuffix(dirPath, "/") {
		dirPath += "/"
	}

	resolvedDir, err := middleware.ResolvePath(c, dirPath)
	if err != nil {
		return err
	}

	allFiles, _ := model.ListAllChildren(session.UserID, resolvedDir)
	existingByName := make(map[string]*model.FileRecord, len(allFiles))
	for i := range allFiles {
		existingByName[allFiles[i].Name] = &allFiles[i]
	}

	var conflicts []fiber.Map
	for _, name := range body.Names {
		if existing, ok := existingByName[name]; ok {
			conflicts = append(conflicts, fiber.Map{
				"name":          existing.Name,
				"size":          existing.Size,
				"is_dir":        existing.IsDir,
				"last_modified": existing.UpdatedAt,
			})
		}
	}

	_ = session // used by ResolvePath via locals
	return c.JSON(fiber.Map{"conflicts": conflicts})
}

func (h *Handler) handleUploadInit(c *fiber.Ctx) error {
	var body struct {
		Path             string `json:"path"`
		FileName         string `json:"file_name"`
		FileSize         int64  `json:"file_size"`
		ConflictStrategy string `json:"conflict_strategy"`
	}
	if err := c.BodyParser(&body); err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "invalid_request"})
	}

	session := c.Locals("session").(*model.Session)

	// Check file size limit
	if body.FileSize > h.Config.Upload.MaxFileSize {
		return c.Status(400).JSON(fiber.Map{"error": "file_too_large"})
	}

	// Build OSS key
	dirPath := body.Path
	if dirPath != "" && !strings.HasSuffix(dirPath, "/") {
		dirPath += "/"
	}

	fileName := body.FileName
	filePath := dirPath + fileName

	resolvedPath, err := middleware.ResolvePath(c, filePath)
	if err != nil {
		return err
	}

	// Check if file already exists (in DB, covers all statuses: uploading/processing/ready)
	existing, err := model.GetFile(session.UserID, resolvedPath)
	if err == nil && existing != nil {
		// File exists — act based on strategy
		switch body.ConflictStrategy {
		case "replace":
			// Delete the existing record so the new upload can take its place
			_ = model.DeleteFile(session.UserID, resolvedPath)
		case "rename":
			// Collect existing names from the database (all statuses to avoid collisions)
			resolvedDir, dirErr := middleware.ResolvePath(c, dirPath)
			if dirErr != nil {
				return dirErr
			}
			allFiles, _ := model.ListAllChildren(session.UserID, resolvedDir)
			usedNames := make(map[string]struct{})
			for _, f := range allFiles {
				usedNames[f.Name] = struct{}{}
			}
			fileName = nextAvailableName(fileName, usedNames)
			filePath = dirPath + fileName
			resolvedPath, err = middleware.ResolvePath(c, filePath)
			if err != nil {
				return err
			}
		default:
			// No strategy — return conflict
			return c.Status(409).JSON(fiber.Map{
				"error": "file_already_exists",
				"existing": fiber.Map{
					"name":          existing.Name,
					"size":          existing.Size,
					"is_dir":        existing.IsDir,
					"last_modified": existing.UpdatedAt,
				},
			})
		}
	}

	// Generate upload ID and create temp directory
	uploadID := uuid.New().String()
	tempDir := filepath.Join(config.TempDir, "upload", uploadID)
	if err := os.MkdirAll(tempDir, 0755); err != nil {
		return c.Status(500).JSON(fiber.Map{"error": "temp_dir_failed"})
	}

	chunkSize := int(config.UploadChunkSize)
	if err := model.CreateUploadFile(session.UserID, uploadID, resolvedPath, fileName, body.FileSize, chunkSize); err != nil {
		_ = os.RemoveAll(tempDir)
		return c.Status(500).JSON(fiber.Map{"error": "record_upload_failed"})
	}

	totalParts := int(body.FileSize / config.UploadChunkSize)
	if body.FileSize%config.UploadChunkSize != 0 {
		totalParts++
	}

	// Create a user-facing task to track the full upload lifecycle
	taskID := uuid.New().String()
	_ = model.CreateTask(session.UserID, taskID, "upload", fileName)

	// Notify directory so file list shows the uploading placeholder
	if h.Hub != nil {
		h.notifyParentDir(session.Username, resolvedPath)
	}

	return c.JSON(fiber.Map{
		"upload_id":   uploadID,
		"chunk_size":  chunkSize,
		"total_parts": totalParts,
		"file_name":   fileName,
		"task_id":     taskID,
	})
}

func (h *Handler) handleUploadPart(c *fiber.Ctx) error {
	uploadID := c.FormValue("upload_id")
	partNumber := c.FormValue("part_number")
	if uploadID == "" || partNumber == "" {
		return c.Status(400).JSON(fiber.Map{"error": "upload_params_required"})
	}

	pn, err := strconv.Atoi(partNumber)
	if err != nil || pn < 1 {
		return c.Status(400).JSON(fiber.Map{"error": "invalid_part_number"})
	}

	session := c.Locals("session").(*model.Session)
	if _, err := model.GetUploadFile(session.UserID, uploadID); err != nil {
		return c.Status(404).JSON(fiber.Map{"error": "upload_not_found"})
	}

	fileHeader, err := c.FormFile("chunk")
	if err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "chunk_required"})
	}

	file, err := fileHeader.Open()
	if err != nil {
		return c.Status(500).JSON(fiber.Map{"error": "read_chunk_failed"})
	}
	defer func() { _ = file.Close() }()

	// Write chunk to disk at the correct offset
	tempDir := filepath.Join(config.TempDir, "upload", uploadID)
	localFile := filepath.Join(tempDir, "data")

	mu := getUploadMutex(uploadID)
	mu.Lock()

	f, err := os.OpenFile(localFile, os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		mu.Unlock()
		return c.Status(500).JSON(fiber.Map{"error": "open_temp_failed"})
	}

	offset := int64(pn-1) * config.UploadChunkSize
	if _, err := f.Seek(offset, io.SeekStart); err != nil {
		_ = f.Close()
		mu.Unlock()
		return c.Status(500).JSON(fiber.Map{"error": "seek_failed"})
	}

	written, err := io.Copy(f, file)
	_ = f.Close()
	if err != nil {
		mu.Unlock()
		return c.Status(500).JSON(fiber.Map{"error": "write_chunk_failed"})
	}

	// Track completed parts
	freshRecord, err := model.GetUploadFile(session.UserID, uploadID)
	if err == nil {
		var parts []int
		_ = json.Unmarshal([]byte(freshRecord.CompletedParts), &parts)
		parts = append(parts, pn)
		partsJSON, _ := json.Marshal(parts)
		_ = model.UpdateUploadFileParts(uploadID, string(partsJSON))
	}
	mu.Unlock()

	return c.JSON(fiber.Map{
		"part_number": pn,
		"size":        written,
	})
}

func (h *Handler) handleUploadComplete(c *fiber.Ctx) error {
	var body struct {
		UploadID string `json:"upload_id"`
		TaskID   string `json:"task_id"`
	}
	if err := c.BodyParser(&body); err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "invalid_request"})
	}

	session := c.Locals("session").(*model.Session)
	record, err := model.GetUploadFile(session.UserID, body.UploadID)
	if err != nil {
		return c.Status(404).JSON(fiber.Map{"error": "upload_not_found"})
	}

	// Queue for async server-side processing
	_ = model.UpdateFileStatus(body.UploadID, "processing")
	uploadMu.Delete(body.UploadID)

	// Create a dispatcher job linked to the user-facing task
	jobID := uuid.New().String()
	taskID := body.TaskID
	params := OSSUploadParams{
		UploadID: body.UploadID,
		OSSKey:   record.Path,
		FileName: record.Name,
		FileSize: record.Size,
		UserID:   session.UserID,
		Username: session.Username,
		TempDir:  filepath.Join(config.TempDir, "upload", body.UploadID),
	}
	paramsJSON, _ := json.Marshal(params)
	_ = model.CreateJobDirect(&model.Job{
		UserID: session.UserID,
		JobID:  jobID,
		TaskID: taskID,
		Type:   "oss_upload",
		Status: "pending",
		Params: string(paramsJSON),
	})

	// Update the task phase to indicate server processing has begun
	if taskID != "" {
		_ = model.UpdateTaskProgress(taskID, 0, "processing")
	}

	h.Audit.LogFromCtx(c, "file_upload", record.Path, record.Name, "processing", 0)
	return c.JSON(fiber.Map{"ok": true})
}

// RunOSSUploadJob encrypts and uploads the assembled file to OSS.
func (h *Handler) RunOSSUploadJob(ctx context.Context, job *model.Job) error {
	var params OSSUploadParams
	if err := json.Unmarshal([]byte(job.Params), &params); err != nil {
		return fmt.Errorf("parse params: %w", err)
	}

	localFile := filepath.Join(params.TempDir, "data")

	// Hashing
	_ = model.UpdateJobProgress(job.JobID, 0.1, "hashing")
	contentHash, err := hashFile(localFile)
	if err != nil {
		_ = model.UpdateFileStatus(params.UploadID, "failed")
		return fmt.Errorf("hash file: %w", err)
	}

	if ctx.Err() != nil {
		return ctx.Err()
	}

	// Index for full-text search (before encryption, file is still plaintext)
	indexContent := h.getUserIndexContentPref(params.Username)
	h.indexFile(params.UserID, params.OSSKey, params.FileName, localFile, indexContent)

	// Generate thumbnail before encryption (while plaintext is still on disk)
	var thumbnailKey, thumbnailWrappedDEKHex string
	var mediaWidth, mediaHeight int
	var mediaDuration float64
	if mediaType := service.DetectMediaType(params.FileName); mediaType == "image" || mediaType == "video" {
		_ = model.UpdateJobProgress(job.JobID, 0.25, "thumbnail")
		thumbKey := fmt.Sprintf("%s/.user/thumbnails/%s_%d.webp", params.Username, contentHash, params.FileSize)

		result, err := h.GenerateThumbnailFromPlaintext(ctx, localFile, mediaType, params.TempDir)
		if err == nil && result != nil {
			if encHex, uploadErr := h.encryptAndUploadThumbnail(params.UserID, thumbKey, result.ThumbPath); uploadErr == nil {
				thumbnailKey = thumbKey
				thumbnailWrappedDEKHex = encHex
				mediaWidth = result.Width
				mediaHeight = result.Height
				mediaDuration = result.Duration
			}
		}
	}

	// Encrypting
	_ = model.UpdateJobProgress(job.JobID, 0.3, "encrypting")
	kek, err := h.loadUserKEK(params.UserID)
	if err != nil {
		_ = model.UpdateFileStatus(params.UploadID, "failed")
		return fmt.Errorf("load user KEK: %w", err)
	}
	dek, err := auth.GenerateDEK()
	if err != nil {
		_ = model.UpdateFileStatus(params.UploadID, "failed")
		return fmt.Errorf("generate DEK: %w", err)
	}
	encFile := localFile + ".enc"
	if err := auth.EncryptFile(dek, localFile, encFile); err != nil {
		_ = model.UpdateFileStatus(params.UploadID, "failed")
		return fmt.Errorf("encrypt: %w", err)
	}
	wrappedDEK, err := auth.WrapDEK(kek, dek)
	if err != nil {
		_ = model.UpdateFileStatus(params.UploadID, "failed")
		return fmt.Errorf("wrap DEK: %w", err)
	}
	wrappedDEKHex := hex.EncodeToString(wrappedDEK)
	defer func() { _ = os.Remove(encFile) }()

	if ctx.Err() != nil {
		return ctx.Err()
	}

	// Transferring to OSS
	_ = model.UpdateJobProgress(job.JobID, 0.5, "transferring")
	if err := h.Store.UploadFromFileCtx(ctx, params.OSSKey, encFile); err != nil {
		if ctx.Err() != nil {
			_ = h.Store.DeleteObject(params.OSSKey)
			_ = os.RemoveAll(params.TempDir)
			return ctx.Err()
		}
		_ = model.UpdateFileStatus(params.UploadID, "failed")
		return fmt.Errorf("upload to oss: %w", err)
	}

	// Upload succeeded, but check if file record was deleted during transfer
	if _, err := model.GetUploadFile(params.UserID, params.UploadID); err != nil {
		_ = h.Store.DeleteObject(params.OSSKey)
		_ = os.RemoveAll(params.TempDir)
		return fmt.Errorf("file record deleted during upload")
	}

	// Finalize file record
	fileSize := params.FileSize
	if fileSize <= 0 {
		if stat, err := os.Stat(localFile); err == nil {
			fileSize = stat.Size()
		}
	}
	ct := mime.TypeByExtension(filepath.Ext(params.FileName))
	_ = model.UpsertFile(params.UserID, params.OSSKey, params.FileName, false, fileSize, ct, contentHash, model.UpsertFileOpts{WrappedDEK: wrappedDEKHex})
	_ = model.UpdateFileStatus(params.UploadID, "ready")

	// Update thumbnail info (generated before encryption)
	if thumbnailKey != "" {
		_ = model.UpdateFileThumbnail(params.UserID, params.OSSKey, thumbnailKey, thumbnailWrappedDEKHex, mediaWidth, mediaHeight, mediaDuration)
	}

	// Clean up temp files
	_ = os.RemoveAll(params.TempDir)

	resultJSON, _ := json.Marshal(map[string]string{"oss_key": params.OSSKey})
	_ = model.UpdateJobResult(job.JobID, string(resultJSON))

	// Notify directory subscribers about the new file
	if h.Hub != nil {
		parent := parentDirOf(params.OSSKey)
		if parent != "" {
			appPath := toAppPath(parent, params.Username)
			h.Hub.PushDirChanged(parent, appPath, "refresh")
		}
	}

	return nil
}

func (h *Handler) handleUploadAbort(c *fiber.Ctx) error {
	uploadID := c.Query("upload_id", "")
	if uploadID == "" {
		return c.Status(400).JSON(fiber.Map{"error": "upload_id required"})
	}

	session := c.Locals("session").(*model.Session)
	record, err := model.GetUploadFile(session.UserID, uploadID)
	if err != nil {
		return c.Status(404).JSON(fiber.Map{"error": "upload_not_found"})
	}

	// Clean up temp files
	tempDir := filepath.Join(config.TempDir, "upload", uploadID)
	_ = os.RemoveAll(tempDir)

	// Delete the file record (it was only a placeholder)
	_ = model.DeleteFile(session.UserID, record.Path)
	uploadMu.Delete(uploadID)

	return c.JSON(fiber.Map{"ok": true})
}

func (h *Handler) handleUploadStatus(c *fiber.Ctx) error {
	uploadID := c.Query("upload_id", "")
	if uploadID == "" {
		return c.Status(400).JSON(fiber.Map{"error": "upload_id required"})
	}

	session := c.Locals("session").(*model.Session)
	record, err := model.GetUploadFile(session.UserID, uploadID)
	if err != nil {
		return c.Status(404).JSON(fiber.Map{"error": "upload_not_found"})
	}

	// Parse completed parts from DB
	var completedParts []int
	_ = json.Unmarshal([]byte(record.CompletedParts), &completedParts)

	// Build part info from DB record
	var parts []store.UploadPartInfo
	for _, pn := range completedParts {
		parts = append(parts, store.UploadPartInfo{
			PartNumber: pn,
		})
	}
	if parts == nil {
		parts = []store.UploadPartInfo{}
	}

	return c.JSON(fiber.Map{
		"upload_id":  record.UploadID,
		"file_name":  record.Name,
		"file_size":  record.Size,
		"chunk_size": record.ChunkSize,
		"status":     record.Status,
		"parts":      parts,
	})
}
