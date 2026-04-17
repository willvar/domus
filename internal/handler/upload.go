package handler

import (
	"encoding/hex"
	"fmt"
	"mime"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"

	"zephyr/config"
	"zephyr/internal/auth"
	"zephyr/internal/middleware"
	"zephyr/internal/model"
	"zephyr/internal/store"
)

// presignExpiry is the lifetime of presigned upload URLs.
const presignExpiry = 1 * time.Hour

// encryptedFileSize returns the on-disk size of a file after AES-256-GCM
// chunked encryption:  5-byte header + plaintext + 28 bytes per chunk.
func encryptedFileSize(plainSize int64) int64 {
	if plainSize <= 0 {
		return 5
	}
	chunkPlain := int64(auth.DefaultChunkSize) // 65536
	const overhead int64 = 28                  // 12 nonce + 16 tag
	numChunks := (plainSize + chunkPlain - 1) / chunkPlain
	return 5 + plainSize + numChunks*overhead
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

// ── helpers ──────────────────────────────────────────────────────────────────

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

// ── conflict check (unchanged) ──────────────────────────────────────────────

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

	allFiles, _ := h.Repos.Files.ListAllChildren(session.UserID, resolvedDir)
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

	return c.JSON(fiber.Map{"conflicts": conflicts})
}

// ── init ─────────────────────────────────────────────────────────────────────

func (h *Handler) handleUploadInit(c *fiber.Ctx) error {
	var body struct {
		Path             string `json:"path"`
		FileName         string `json:"file_name"`
		FileSize         int64  `json:"file_size"`
		ContentType      string `json:"content_type"`
		ConflictStrategy string `json:"conflict_strategy"`
		ClientInstanceID string `json:"client_instance_id"`
		Internal         bool   `json:"internal"`
	}
	if err := c.BodyParser(&body); err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "invalid_request"})
	}

	session := c.Locals("session").(*model.Session)

	if body.FileSize > h.Config.Upload.MaxFileSize {
		return c.Status(400).JSON(fiber.Map{"error": "file_too_large"})
	}

	// Resolve path
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

	// Conflict handling
	existing, err := h.Repos.Files.Get(session.UserID, resolvedPath)
	if err == nil && existing != nil {
		switch body.ConflictStrategy {
		case "replace":
			_ = h.Repos.Files.Delete(session.UserID, resolvedPath)
		case "rename":
			resolvedDir, dirErr := middleware.ResolvePath(c, dirPath)
			if dirErr != nil {
				return dirErr
			}
			allFiles, _ := h.Repos.Files.ListAllChildren(session.UserID, resolvedDir)
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

	// Encrypted file geometry
	encSize := encryptedFileSize(body.FileSize)
	partSize := config.UploadChunkSize
	totalParts := int((encSize + partSize - 1) / partSize)
	if totalParts < 1 {
		totalParts = 1
	}

	// Create S3 multipart upload
	ossUploadID, err := h.Store.CreateMultipartUpload(resolvedPath)
	if err != nil {
		return c.Status(500).JSON(fiber.Map{"error": "create_multipart_failed"})
	}

	// Task + FileRecord
	uploadID := uuid.New().String()
	taskID := ""
	if !body.Internal {
		taskID = uuid.New().String()
		if err := h.Repos.Tasks.Create(session.UserID, taskID, "upload", fileName); err != nil {
			_ = h.Store.AbortMultipartUpload(resolvedPath, ossUploadID)
			return c.Status(500).JSON(fiber.Map{"error": "create_task_failed"})
		}
	}
	if err := h.Repos.Files.CreateUpload(session.UserID, uploadID, taskID, ossUploadID, resolvedPath, fileName, body.FileSize, body.ClientInstanceID); err != nil {
		if taskID != "" {
			_ = h.Repos.Tasks.Delete(taskID)
		}
		_ = h.Store.AbortMultipartUpload(resolvedPath, ossUploadID)
		return c.Status(500).JSON(fiber.Map{"error": "record_upload_failed"})
	}

	if h.Hub != nil {
		h.notifyParentDir(session.Username, resolvedPath)
	}

	return c.JSON(fiber.Map{
		"upload_id":     uploadID,
		"task_id":       taskID,
		"oss_key":       resolvedPath,
		"file_name":     fileName,
		"oss_upload_id": ossUploadID,
		"part_size":     partSize,
		"total_parts":   totalParts,
	})
}

// ── presign (batch) ──────────────────────────────────────────────────────────

func (h *Handler) handleUploadPresign(c *fiber.Ctx) error {
	uploadID := c.Query("upload_id", "")
	if uploadID == "" {
		return c.Status(400).JSON(fiber.Map{"error": "upload_id required"})
	}
	start, _ := strconv.Atoi(c.Query("start", "1"))
	count, _ := strconv.Atoi(c.Query("count", "100"))
	if start < 1 {
		start = 1
	}
	maxBatch := h.Config.OSS.MaxPresignBatch
	if maxBatch <= 0 {
		maxBatch = 100
	}
	if count < 1 || count > maxBatch {
		count = maxBatch
	}

	session := c.Locals("session").(*model.Session)
	record, err := h.Repos.Files.GetUpload(session.UserID, uploadID)
	if err != nil {
		return c.Status(404).JSON(fiber.Map{"error": "upload_not_found"})
	}
	if record.Status != "uploading" {
		return c.Status(409).JSON(fiber.Map{"error": "upload_not_active"})
	}
	_ = h.Repos.Files.TouchUpload(uploadID, time.Now())

	type partURL struct {
		PartNumber   int    `json:"part_number"`
		PresignedURL string `json:"presigned_url"`
	}
	parts := make([]partURL, 0, count)
	for i := 0; i < count; i++ {
		pn := start + i
		url, err := h.Store.PresignedUploadPart(record.Path, record.OSSUploadID, pn, presignExpiry)
		if err != nil {
			return c.Status(500).JSON(fiber.Map{"error": "presign_failed"})
		}
		parts = append(parts, partURL{PartNumber: pn, PresignedURL: url})
	}

	return c.JSON(fiber.Map{
		"parts":      parts,
		"expires_in": int(presignExpiry.Seconds()),
	})
}

// ── complete ─────────────────────────────────────────────────────────────────

func (h *Handler) handleUploadComplete(c *fiber.Ctx) error {
	var body struct {
		UploadID          string               `json:"upload_id"`
		DEK               string               `json:"dek"`
		ContentHash       string               `json:"content_hash"`
		EncryptedSize     int64                `json:"encrypted_size"`
		Parts             []store.CompletePart `json:"parts"`
		SearchText        string               `json:"search_text"`
		MediaWidth        int                  `json:"media_width"`
		MediaHeight       int                  `json:"media_height"`
		MediaDuration     float64              `json:"media_duration"`
		ThumbnailUploadID string               `json:"thumbnail_upload_id"`
	}
	if err := c.BodyParser(&body); err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "invalid_request"})
	}

	session := c.Locals("session").(*model.Session)
	record, err := h.Repos.Files.GetUpload(session.UserID, body.UploadID)
	if err != nil {
		return c.Status(404).JSON(fiber.Map{"error": "upload_not_found"})
	}

	expectedEncryptedSize := encryptedFileSize(record.Size)
	if body.EncryptedSize <= 0 || body.EncryptedSize != expectedEncryptedSize {
		return c.Status(400).JSON(fiber.Map{
			"error":         "invalid_encrypted_size",
			"expected_size": expectedEncryptedSize,
			"actual_size":   body.EncryptedSize,
		})
	}

	expectedPartSize := config.UploadChunkSize
	expectedTotalParts := int((expectedEncryptedSize + expectedPartSize - 1) / expectedPartSize)
	if expectedTotalParts < 1 {
		expectedTotalParts = 1
	}
	if len(body.Parts) != expectedTotalParts {
		return c.Status(400).JSON(fiber.Map{"error": "invalid_parts"})
	}
	for i, part := range body.Parts {
		if part.PartNumber != i+1 || strings.TrimSpace(part.ETag) == "" {
			return c.Status(400).JSON(fiber.Map{"error": "invalid_parts"})
		}
	}

	if record.OSSUploadID != "" {
		if err := h.Store.CompleteMultipartUpload(record.Path, record.OSSUploadID, body.Parts); err != nil {
			return c.Status(500).JSON(fiber.Map{"error": "complete_multipart_failed"})
		}
	}

	head, err := h.Store.HeadObject(record.Path)
	if err != nil {
		return c.Status(500).JSON(fiber.Map{"error": "verify_failed"})
	}
	if head.Size != expectedEncryptedSize {
		return c.Status(400).JSON(fiber.Map{
			"error":         "size_mismatch",
			"expected_size": expectedEncryptedSize,
			"actual_size":   head.Size,
		})
	}

	var wrappedDEKHex string
	if body.DEK != "" {
		dekBytes, err := hex.DecodeString(body.DEK)
		if err != nil || len(dekBytes) != 32 {
			return c.Status(400).JSON(fiber.Map{"error": "invalid_dek"})
		}
		kek, err := h.getFileEncryptionKey(session)
		if err != nil {
			return c.Status(500).JSON(fiber.Map{"error": "internal_error"})
		}
		wrapped, err := auth.WrapDEK(kek, dekBytes)
		if err != nil {
			return c.Status(500).JSON(fiber.Map{"error": "wrap_dek_failed"})
		}
		wrappedDEKHex = hex.EncodeToString(wrapped)
	}

	contentType := mime.TypeByExtension(filepath.Ext(record.Name))
	if err := h.Repos.Files.Upsert(
		session.UserID, record.Path, record.Name, false,
		record.Size, contentType, body.ContentHash,
		model.UpsertFileOpts{WrappedDEK: wrappedDEKHex},
	); err != nil {
		return c.Status(500).JSON(fiber.Map{"error": "record_finalize_failed"})
	}
	if err := h.Repos.Files.UpdateStatus(body.UploadID, "ready"); err != nil {
		return c.Status(500).JSON(fiber.Map{"error": "record_status_failed"})
	}

	if body.ThumbnailUploadID != "" {
		thumbRecord, err := h.Repos.Files.GetUpload(session.UserID, body.ThumbnailUploadID)
		if err == nil && thumbRecord.Status == "ready" {
			_ = h.Repos.Files.UpdateThumbnail(
				session.UserID, record.Path,
				thumbRecord.Path, thumbRecord.WrappedDEK,
				body.MediaWidth, body.MediaHeight, body.MediaDuration,
			)
		}
	} else if body.MediaWidth > 0 || body.MediaHeight > 0 {
		_ = h.Repos.Files.UpdateThumbnail(
			session.UserID, record.Path,
			"", "",
			body.MediaWidth, body.MediaHeight, body.MediaDuration,
		)
	}

	if body.SearchText != "" {
		h.indexFile(session.UserID, record.Path, record.Name, "", false)
		_ = h.Repos.Files.UpdateSearchVector(session.UserID, record.Path, record.Name+"\n"+body.SearchText)
	} else {
		h.indexFileName(session.UserID, record.Path, record.Name)
	}

	if strings.HasSuffix(record.Path, "/.user/avatar.webp") {
		h.InvalidateAvatarCache(session.Username)
	}

	if record.TaskID != "" {
		_ = h.Repos.Tasks.UpdateStatus(record.TaskID, "completed")
	}

	if h.Hub != nil {
		parent := parentDirOf(record.Path)
		if parent != "" {
			appPath := toAppPath(parent, session.Username)
			h.Hub.PushDirChanged(parent, appPath, "refresh")
		}
	}

	h.Audit.LogFromCtx(c, "file_upload", record.Path, record.Name, "completed", 0)
	return c.JSON(fiber.Map{"ok": true})
}

func (h *Handler) handleUploadHeartbeat(c *fiber.Ctx) error {
	var body struct {
		UploadID string `json:"upload_id"`
	}
	if err := c.BodyParser(&body); err != nil || strings.TrimSpace(body.UploadID) == "" {
		return c.Status(400).JSON(fiber.Map{"error": "upload_id required"})
	}
	if err := h.Repos.Files.TouchUpload(strings.TrimSpace(body.UploadID), time.Now()); err != nil {
		return c.Status(500).JSON(fiber.Map{"error": "heartbeat_failed"})
	}
	return c.JSON(fiber.Map{"ok": true})
}

func (h *Handler) handleUploadCancel(c *fiber.Ctx) error {
	var body struct {
		UploadID string `json:"upload_id"`
		TaskID   string `json:"task_id"`
		Reason   string `json:"reason"`
		Status   string `json:"status"`
	}
	if err := c.BodyParser(&body); err != nil || strings.TrimSpace(body.UploadID) == "" {
		return c.Status(400).JSON(fiber.Map{"error": "upload_id required"})
	}

	uploadID := strings.TrimSpace(body.UploadID)
	taskID := strings.TrimSpace(body.TaskID)
	reason := strings.TrimSpace(body.Reason)
	status := strings.TrimSpace(body.Status)
	if status != "failed" {
		status = "cancelled"
	}
	session := c.Locals("session").(*model.Session)
	record, err := h.Repos.Files.GetUpload(session.UserID, uploadID)
	if err != nil {
		return c.Status(404).JSON(fiber.Map{"error": "upload_not_found"})
	}
	if record.OSSUploadID != "" {
		_ = h.Store.AbortMultipartUpload(record.Path, record.OSSUploadID)
	}
	_ = h.Store.DeleteObject(record.Path)
	_ = h.Repos.Files.Delete(session.UserID, record.Path)
	if taskID == "" {
		taskID = record.TaskID
	}
	if taskID != "" {
		_ = h.Repos.Tasks.UpdateStatus(taskID, status)
	}
	if h.Hub != nil {
		h.notifyParentDir(session.Username, record.Path)
	}
	if reason != "" {
		h.Audit.LogFromCtx(c, "file_upload_abort", record.Path, reason, status, 0)
	}
	return c.JSON(fiber.Map{"ok": true})
}

func (h *Handler) handleUploadCleanup(c *fiber.Ctx) error {
	var body struct {
		ClientInstanceID string `json:"client_instance_id"`
	}
	_ = c.BodyParser(&body)
	session := c.Locals("session").(*model.Session)
	clientInstanceID := strings.TrimSpace(body.ClientInstanceID)
	cutoff := time.Now().Add(-15 * time.Second)
	records, err := h.Repos.Files.CancelUploadsForOtherInstances(session.UserID, clientInstanceID, cutoff)
	if err != nil {
		return c.Status(500).JSON(fiber.Map{"error": "cleanup_failed"})
	}
	for _, record := range records {
		if record.OSSUploadID != "" {
			_ = h.Store.AbortMultipartUpload(record.Path, record.OSSUploadID)
		}
		_ = h.Store.DeleteObject(record.Path)
		_ = h.Repos.Files.Delete(session.UserID, record.Path)
		if record.TaskID != "" {
			_ = h.Repos.Tasks.UpdateStatus(record.TaskID, "cancelled")
		}
		if h.Hub != nil {
			h.notifyParentDir(session.Username, record.Path)
		}
	}
	return c.JSON(fiber.Map{"ok": true, "count": len(records)})
}

// ── status ───────────────────────────────────────────────────────────────────

func (h *Handler) handleUploadStatus(c *fiber.Ctx) error {
	uploadID := c.Query("upload_id", "")
	if uploadID == "" {
		return c.Status(400).JSON(fiber.Map{"error": "upload_id required"})
	}

	session := c.Locals("session").(*model.Session)
	record, err := h.Repos.Files.GetUpload(session.UserID, uploadID)
	if err != nil {
		return c.Status(404).JSON(fiber.Map{"error": "upload_not_found"})
	}

	// Query completed parts from OSS
	var parts []store.PartInfo
	if record.OSSUploadID != "" && record.Status == "uploading" {
		parts, _ = h.Store.ListParts(record.Path, record.OSSUploadID)
	}
	if parts == nil {
		parts = []store.PartInfo{}
	}

	status := record.Status
	if status == "uploading" {
		status = "active"
	}

	return c.JSON(fiber.Map{
		"upload_id":     record.UploadID,
		"task_id":       record.TaskID,
		"oss_upload_id": record.OSSUploadID,
		"file_name":     record.Name,
		"file_size":     record.Size,
		"status":        status,
		"parts":         parts,
	})
}
