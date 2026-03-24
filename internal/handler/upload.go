package handler

import (
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

// handleUploadDispatch routes POST /file/upload to init or complete based on upload_id presence.
func (h *Handler) handleUploadDispatch(c *fiber.Ctx) error {
	var peek struct {
		UploadID string `json:"upload_id"`
	}
	if err := c.BodyParser(&peek); err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "invalid_request"})
	}
	if peek.UploadID != "" {
		return h.handleUploadComplete(c)
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

	// Check if file already exists
	existingInfo, err := h.Store.GetObjectInfo(resolvedPath)
	if err == nil && existingInfo != nil {
		// File exists — act based on strategy
		switch body.ConflictStrategy {
		case "replace":
			// Proceed with overwrite — no action needed
		case "rename":
			// Resolve the directory path for listing
			resolvedDir, dirErr := middleware.ResolvePath(c, dirPath)
			if dirErr != nil {
				return dirErr
			}
			// Collect existing names in the directory
			usedNames := make(map[string]struct{})
			marker := ""
			for {
				result, listErr := h.Store.ListObjects(resolvedDir, marker, 1000)
				if listErr != nil {
					break
				}
				for _, f := range result.Files {
					usedNames[f.Name] = struct{}{}
				}
				if !result.IsTruncated {
					break
				}
				marker = result.NextMarker
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
					"name":          existingInfo.Name,
					"size":          existingInfo.Size,
					"is_dir":        existingInfo.IsDir,
					"last_modified": existingInfo.LastModified,
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
	if err := model.CreateUploadRecord(session.UserID, uploadID, resolvedPath, fileName, body.FileSize, chunkSize); err != nil {
		_ = os.RemoveAll(tempDir)
		return c.Status(500).JSON(fiber.Map{"error": "record_upload_failed"})
	}

	totalParts := int(body.FileSize / config.UploadChunkSize)
	if body.FileSize%config.UploadChunkSize != 0 {
		totalParts++
	}

	return c.JSON(fiber.Map{
		"upload_id":   uploadID,
		"chunk_size":  chunkSize,
		"total_parts": totalParts,
		"file_name":   fileName,
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

	record, err := model.GetUploadRecord(uploadID)
	if err != nil {
		return c.Status(404).JSON(fiber.Map{"error": "upload_not_found"})
	}

	session := c.Locals("session").(*model.Session)
	if record.UserID != session.UserID && session.Role != "admin" {
		return c.Status(403).JSON(fiber.Map{"error": "access_denied"})
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
	freshRecord, err := model.GetUploadRecord(uploadID)
	if err == nil {
		var parts []int
		_ = json.Unmarshal([]byte(freshRecord.CompletedParts), &parts)
		parts = append(parts, pn)
		partsJSON, _ := json.Marshal(parts)
		_ = model.UpdateUploadParts(uploadID, string(partsJSON))
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
	}
	if err := c.BodyParser(&body); err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "invalid_request"})
	}

	record, err := model.GetUploadRecord(body.UploadID)
	if err != nil {
		return c.Status(404).JSON(fiber.Map{"error": "upload_not_found"})
	}

	session := c.Locals("session").(*model.Session)
	if record.UserID != session.UserID && session.Role != "admin" {
		return c.Status(403).JSON(fiber.Map{"error": "access_denied"})
	}

	tempDir := filepath.Join(config.TempDir, "upload", body.UploadID)
	localFile := filepath.Join(tempDir, "data")

	// Compute content hash before encryption
	contentHash, err := hashFile(localFile)
	if err != nil {
		return c.Status(500).JSON(fiber.Map{"error": "hash_failed"})
	}

	// Encrypt and upload assembled file to OSS
	key, err := h.getFileEncryptionKey(session, record.OSSKey)
	if err != nil {
		return c.Status(500).JSON(fiber.Map{"error": "encryption_failed"})
	}
	encFile := localFile + ".enc"
	if err := auth.EncryptFile(key, localFile, encFile); err != nil {
		return c.Status(500).JSON(fiber.Map{"error": "encryption_failed"})
	}
	defer func() { _ = os.Remove(encFile) }()
	if err := h.Store.UploadFromFile(record.OSSKey, encFile); err != nil {
		return c.Status(500).JSON(fiber.Map{"error": "upload_storage_failed"})
	}

	_ = model.UpdateUploadStatus(body.UploadID, "completed")
	uploadMu.Delete(body.UploadID)

	// Get actual file size
	fileSize := record.FileSize
	if fileSize <= 0 {
		if stat, err := os.Stat(localFile); err == nil {
			fileSize = stat.Size()
		}
	}

	// Record file metadata
	ct := mime.TypeByExtension(filepath.Ext(record.FileName))
	_ = model.UpsertFile(session.UserID, record.OSSKey, record.FileName, false, fileSize, ct, contentHash)

	// Create thumbnail job for images/videos
	if mediaType := service.DetectMediaType(record.FileName); mediaType == "image" || mediaType == "video" {
		thumbKey := fmt.Sprintf("%s/.thumbnails/%s_%d.webp", session.Username, contentHash, fileSize)
		thumbJobID := uuid.New().String()
		params := ThumbnailParams{
			SourceKey:    record.OSSKey,
			ThumbnailKey: thumbKey,
			FileName:     record.FileName,
			MediaType:    mediaType,
			TempDir:      filepath.Join(config.TempDir, "thumbnail", thumbJobID),
		}
		paramsJSON, _ := json.Marshal(params)
		_, _ = model.CreateJob(session.UserID, thumbJobID, "thumbnail", string(paramsJSON))
	}

	// Clean up temp files
	_ = os.RemoveAll(tempDir)

	h.Audit.LogFromCtx(c, "file_upload", record.OSSKey, record.FileName, "success", 0)
	return c.JSON(fiber.Map{"ok": true})
}

func (h *Handler) handleUploadAbort(c *fiber.Ctx) error {
	uploadID := c.Query("upload_id", "")
	if uploadID == "" {
		return c.Status(400).JSON(fiber.Map{"error": "upload_id required"})
	}

	record, err := model.GetUploadRecord(uploadID)
	if err != nil {
		return c.Status(404).JSON(fiber.Map{"error": "upload_not_found"})
	}

	session := c.Locals("session").(*model.Session)
	if record.UserID != session.UserID && session.Role != "admin" {
		return c.Status(403).JSON(fiber.Map{"error": "access_denied"})
	}

	// Clean up temp files
	tempDir := filepath.Join(config.TempDir, "upload", uploadID)
	_ = os.RemoveAll(tempDir)

	_ = model.UpdateUploadStatus(uploadID, "aborted")
	uploadMu.Delete(uploadID)

	return c.JSON(fiber.Map{"ok": true})
}

func (h *Handler) handleUploadStatus(c *fiber.Ctx) error {
	uploadID := c.Query("upload_id", "")
	if uploadID == "" {
		return c.Status(400).JSON(fiber.Map{"error": "upload_id required"})
	}

	record, err := model.GetUploadRecord(uploadID)
	if err != nil {
		return c.Status(404).JSON(fiber.Map{"error": "upload_not_found"})
	}

	session := c.Locals("session").(*model.Session)
	if record.UserID != session.UserID && session.Role != "admin" {
		return c.Status(403).JSON(fiber.Map{"error": "access_denied"})
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
		"file_name":  record.FileName,
		"file_size":  record.FileSize,
		"chunk_size": record.ChunkSize,
		"status":     record.Status,
		"parts":      parts,
	})
}
