package handler

import (
	"bufio"
	"bytes"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"mime"
	"os"
	"path/filepath"
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

// fillThumbnail populates presigned URL + plaintext DEK for client-side decryption.
func (h *Handler) fillThumbnail(fi *store.FileInfo, r *model.FileRecord, kek []byte) {
	if r.ThumbnailKey == "" || r.ThumbnailWrappedDEK == "" || kek == nil {
		return
	}
	presignedURL, err := h.Store.GeneratePresignedURL(r.ThumbnailKey, 4*time.Hour)
	if err != nil {
		return
	}
	wrappedBytes, err := hex.DecodeString(r.ThumbnailWrappedDEK)
	if err != nil {
		return
	}
	thumbDEK, err := auth.UnwrapDEK(kek, wrappedBytes)
	if err != nil {
		return
	}
	fi.ThumbnailURL = presignedURL
	fi.ThumbnailDEK = hex.EncodeToString(thumbDEK)
}

func (h *Handler) handleList(c *fiber.Ctx) error {
	path := c.Query("path", "")

	resolvedPath, err := middleware.ResolvePath(c, path)
	if err != nil {
		return err
	}

	session := c.Locals("session").(*model.Session)
	records, err := h.Repos.Files.ListDirectChildren(session.UserID, resolvedPath)
	if err != nil {
		return c.Status(500).JSON(fiber.Map{"error": "list_failed"})
	}

	// Build uploadID -> task info map for processing files
	type taskInfo struct {
		TaskID   string
		Progress float64
		Phase    string
	}
	uploadTasks := make(map[string]taskInfo)
	if jobs, err := h.Repos.Jobs.ListActiveUploads(session.UserID); err == nil {
		for _, j := range jobs {
			if j.TaskID == "" {
				continue
			}
			var p struct {
				UploadID string `json:"upload_id"`
			}
			if json.Unmarshal([]byte(j.Params), &p) == nil && p.UploadID != "" {
				if task, err := h.Repos.Tasks.Get(j.TaskID); err == nil {
					uploadTasks[p.UploadID] = taskInfo{TaskID: task.TaskID, Progress: task.Progress, Phase: task.Phase}
				}
			}
		}
	}

	kek, _ := h.getFileEncryptionKey(session)

	files := make([]store.FileInfo, 0, len(records))
	for _, r := range records {
		fi := store.FileInfo{
			Name:          r.Name,
			Path:          middleware.ToAppPath(r.Path, session.Username),
			IsDir:         r.IsDir,
			Size:          r.Size,
			CreatedAt:     r.CreatedAt,
			LastModified:  r.UpdatedAt,
			ContentType:   r.ContentType,
			MediaWidth:    r.MediaWidth,
			MediaHeight:   r.MediaHeight,
			MediaDuration: r.MediaDuration,
			Status:        r.Status,
		}
		if r.Status == "processing" && r.UploadID != "" {
			if ti, ok := uploadTasks[r.UploadID]; ok {
				fi.JobID = ti.TaskID
				fi.JobProgress = ti.Progress
				fi.JobPhase = ti.Phase
			}
		}
		h.fillThumbnail(&fi, &r, kek)
		files = append(files, fi)
	}

	return c.JSON(fiber.Map{"files": files})
}

func (h *Handler) handleMkdir(c *fiber.Ctx) error {
	var body struct {
		Path string `json:"path"`
	}
	if err := c.BodyParser(&body); err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "invalid_request"})
	}

	resolvedPath, err := middleware.ResolvePath(c, body.Path)
	if err != nil {
		return err
	}

	if err := h.Store.CreateDirectory(resolvedPath); err != nil {
		return c.Status(500).JSON(fiber.Map{"error": "mkdir_failed"})
	}

	session := c.Locals("session").(*model.Session)
	dirName := filepath.Base(strings.TrimSuffix(resolvedPath, "/"))
	_ = h.Repos.Files.Upsert(session.UserID, resolvedPath, dirName, true, 0, "", "")

	// Notify WebSocket subscribers of the parent directory
	if parent := parentDirOf(resolvedPath); parent != "" {
		appPath := toAppPath(parent, session.Username)
		h.Hub.PushDirChanged(parent, appPath, "created")
	}

	return c.JSON(fiber.Map{"ok": true})
}

func (h *Handler) handleRename(c *fiber.Ctx) error {
	var body struct {
		OldPath string `json:"old_path"`
		NewPath string `json:"new_path"`
		IsDir   bool   `json:"is_dir"`
	}
	if err := c.BodyParser(&body); err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "invalid_request"})
	}

	oldResolved, err := middleware.ResolvePath(c, body.OldPath)
	if err != nil {
		return err
	}

	// Block rename for non-ready files
	if !body.IsDir {
		session := c.Locals("session").(*model.Session)
		if rec, err := h.Repos.Files.Get(session.UserID, oldResolved); err == nil && rec.Status != "ready" {
			return c.Status(409).JSON(fiber.Map{"error": "file_not_ready"})
		}
	}

	newResolved, err := middleware.ResolvePath(c, body.NewPath)
	if err != nil {
		return err
	}

	if err := h.Store.RenameObject(oldResolved, newResolved, body.IsDir); err != nil {
		return c.Status(500).JSON(fiber.Map{"error": "rename_failed"})
	}

	session := c.Locals("session").(*model.Session)
	if body.IsDir {
		_ = h.Repos.Files.MoveByPrefix(session.UserID, oldResolved, newResolved)
		_ = h.Repos.Shares.MoveByPrefix(session.UserID, oldResolved, newResolved)
	} else {
		newName := filepath.Base(newResolved)
		_ = h.Repos.Files.Move(session.UserID, oldResolved, newResolved, newName)
		_ = h.Repos.Shares.MoveByPath(session.UserID, oldResolved, newResolved)
	}

	// Notify WebSocket subscribers of both old and new parent directories
	if parent := parentDirOf(oldResolved); parent != "" {
		appPath := toAppPath(parent, session.Username)
		h.Hub.PushDirChanged(parent, appPath, "refresh")
	}
	if parent := parentDirOf(newResolved); parent != "" {
		appPath := toAppPath(parent, session.Username)
		h.Hub.PushDirChanged(parent, appPath, "refresh")
	}

	h.Audit.LogFromCtx(c, "file_rename", body.OldPath, body.NewPath, "success", 0)
	return c.JSON(fiber.Map{"ok": true})
}

func (h *Handler) handleCopy(c *fiber.Ctx) error {
	var body struct {
		SrcPath string `json:"src_path"`
		DstPath string `json:"dst_path"`
		IsDir   bool   `json:"is_dir"`
	}
	if err := c.BodyParser(&body); err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "invalid_request"})
	}

	srcResolved, err := middleware.ResolvePath(c, body.SrcPath)
	if err != nil {
		return err
	}
	dstResolved, err := middleware.ResolvePath(c, body.DstPath)
	if err != nil {
		return err
	}

	// Verify source exists in DB
	session := c.Locals("session").(*model.Session)
	var srcSize int64
	var srcContentHash string
	var srcWrappedDEK string
	if !body.IsDir {
		srcRecord, err := h.Repos.Files.Get(session.UserID, srcResolved)
		if err != nil {
			return c.Status(404).JSON(fiber.Map{"error": "source_not_found"})
		}
		if srcRecord.Status != "ready" {
			return c.Status(409).JSON(fiber.Map{"error": "file_not_ready"})
		}
		srcSize = srcRecord.Size
		srcContentHash = srcRecord.ContentHash
		srcWrappedDEK = srcRecord.WrappedDEK
	}

	// Check if SSE is requested
	if c.Get("Accept") == "text/event-stream" {
		c.Set("Content-Type", "text/event-stream")
		c.Set("Cache-Control", "no-cache")
		c.Set("Connection", "keep-alive")

		c.Context().SetBodyStreamWriter(func(w *bufio.Writer) {
			progress := func(done, total int, current string) {
				data, _ := json.Marshal(fiber.Map{"done": done, "total": total, "current": current})
				_, _ = fmt.Fprintf(w, "data: %s\n\n", data)
				_ = w.Flush()
			}

			var copyErr error
			if body.IsDir {
				copyErr = h.Store.RecursiveCopy(srcResolved, dstResolved, progress)
			} else {
				copyErr = h.Store.CopyObject(srcResolved, dstResolved)
				if copyErr == nil {
					progress(1, 1, srcResolved)
				}
			}

			if copyErr != nil {
				data, _ := json.Marshal(fiber.Map{"error": copyErr.Error()})
				_, _ = fmt.Fprintf(w, "data: %s\n\n", data)
			} else {
				if body.IsDir {
					h.cloneDirFiles(session.UserID, srcResolved, dstResolved)
				} else {
					dstName := filepath.Base(dstResolved)
					ct := mime.TypeByExtension(filepath.Ext(dstResolved))
					var copyOpts []model.UpsertFileOpts
					if srcWrappedDEK != "" {
						copyOpts = append(copyOpts, model.UpsertFileOpts{WrappedDEK: srcWrappedDEK})
					}
					_ = h.Repos.Files.Upsert(session.UserID, dstResolved, dstName, false, srcSize, ct, srcContentHash, copyOpts...)
				}
				data, _ := json.Marshal(fiber.Map{"done": true})
				_, _ = fmt.Fprintf(w, "data: %s\n\n", data)
			}
			_ = w.Flush()
		})
		return nil
	}

	// Non-SSE: simple copy
	if body.IsDir {
		if err := h.Store.RecursiveCopy(srcResolved, dstResolved, nil); err != nil {
			return c.Status(500).JSON(fiber.Map{"error": "copy_failed"})
		}
		h.cloneDirFiles(session.UserID, srcResolved, dstResolved)
	} else {
		if err := h.Store.CopyObject(srcResolved, dstResolved); err != nil {
			return c.Status(500).JSON(fiber.Map{"error": "copy_failed"})
		}
		dstName := filepath.Base(dstResolved)
		ct := mime.TypeByExtension(filepath.Ext(dstResolved))
		var copyOpts []model.UpsertFileOpts
		if srcWrappedDEK != "" {
			copyOpts = append(copyOpts, model.UpsertFileOpts{WrappedDEK: srcWrappedDEK})
		}
		_ = h.Repos.Files.Upsert(session.UserID, dstResolved, dstName, false, srcSize, ct, srcContentHash, copyOpts...)
	}

	// Notify WebSocket subscribers of the destination parent directory
	if parent := parentDirOf(dstResolved); parent != "" {
		appPath := toAppPath(parent, session.Username)
		h.Hub.PushDirChanged(parent, appPath, "refresh")
	}

	h.Audit.LogFromCtx(c, "file_copy", body.SrcPath, body.DstPath, "success", 0)
	return c.JSON(fiber.Map{"ok": true})
}

func (h *Handler) handleMove(c *fiber.Ctx) error {
	var body struct {
		SrcPath string `json:"src_path"`
		DstPath string `json:"dst_path"`
		IsDir   bool   `json:"is_dir"`
	}
	if err := c.BodyParser(&body); err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "invalid_request"})
	}

	srcResolved, err := middleware.ResolvePath(c, body.SrcPath)
	if err != nil {
		return err
	}

	// Block move for non-ready files
	if !body.IsDir {
		session := c.Locals("session").(*model.Session)
		if rec, err := h.Repos.Files.Get(session.UserID, srcResolved); err == nil && rec.Status != "ready" {
			return c.Status(409).JSON(fiber.Map{"error": "file_not_ready"})
		}
	}

	dstResolved, err := middleware.ResolvePath(c, body.DstPath)
	if err != nil {
		return err
	}

	if c.Get("Accept") == "text/event-stream" {
		c.Set("Content-Type", "text/event-stream")
		c.Set("Cache-Control", "no-cache")
		c.Set("Connection", "keep-alive")

		c.Context().SetBodyStreamWriter(func(w *bufio.Writer) {
			progress := func(done, total int, current string) {
				data, _ := json.Marshal(fiber.Map{"done": done, "total": total, "current": current})
				_, _ = fmt.Fprintf(w, "data: %s\n\n", data)
				_ = w.Flush()
			}

			var moveErr error
			if body.IsDir {
				moveErr = h.Store.RecursiveMove(srcResolved, dstResolved, progress)
			} else {
				moveErr = h.Store.MoveObject(srcResolved, dstResolved)
				if moveErr == nil {
					progress(1, 1, srcResolved)
				}
			}

			if moveErr != nil {
				data, _ := json.Marshal(fiber.Map{"error": moveErr.Error()})
				_, _ = fmt.Fprintf(w, "data: %s\n\n", data)
			} else {
				session := c.Locals("session").(*model.Session)
				if body.IsDir {
					_ = h.Repos.Files.MoveByPrefix(session.UserID, srcResolved, dstResolved)
					_ = h.Repos.Shares.MoveByPrefix(session.UserID, srcResolved, dstResolved)
				} else {
					newName := filepath.Base(dstResolved)
					_ = h.Repos.Files.Move(session.UserID, srcResolved, dstResolved, newName)
					_ = h.Repos.Shares.MoveByPath(session.UserID, srcResolved, dstResolved)
				}
				data, _ := json.Marshal(fiber.Map{"done": true})
				_, _ = fmt.Fprintf(w, "data: %s\n\n", data)
			}
			_ = w.Flush()
		})
		return nil
	}

	session := c.Locals("session").(*model.Session)
	if body.IsDir {
		if err := h.Store.RecursiveMove(srcResolved, dstResolved, nil); err != nil {
			return c.Status(500).JSON(fiber.Map{"error": "move_failed"})
		}
		_ = h.Repos.Files.MoveByPrefix(session.UserID, srcResolved, dstResolved)
		_ = h.Repos.Shares.MoveByPrefix(session.UserID, srcResolved, dstResolved)
	} else {
		if err := h.Store.MoveObject(srcResolved, dstResolved); err != nil {
			return c.Status(500).JSON(fiber.Map{"error": "move_failed"})
		}
		newName := filepath.Base(dstResolved)
		_ = h.Repos.Files.Move(session.UserID, srcResolved, dstResolved, newName)
		_ = h.Repos.Shares.MoveByPath(session.UserID, srcResolved, dstResolved)
	}

	// Notify WebSocket subscribers of both source and destination parent directories
	if parent := parentDirOf(srcResolved); parent != "" {
		appPath := toAppPath(parent, session.Username)
		h.Hub.PushDirChanged(parent, appPath, "refresh")
	}
	if parent := parentDirOf(dstResolved); parent != "" {
		appPath := toAppPath(parent, session.Username)
		h.Hub.PushDirChanged(parent, appPath, "refresh")
	}

	h.Audit.LogFromCtx(c, "file_move", body.SrcPath, body.DstPath, "success", 0)
	return c.JSON(fiber.Map{"ok": true})
}

func (h *Handler) handleDelete(c *fiber.Ctx) error {
	path := c.Query("path", "")
	if path == "" {
		return c.Status(400).JSON(fiber.Map{"error": "path_required"})
	}

	resolvedPath, err := middleware.ResolvePath(c, path)
	if err != nil {
		return err
	}

	session := c.Locals("session").(*model.Session)
	isDir := strings.HasSuffix(path, "/")

	// For non-ready files: cancel job, delete record, clean temp — no trash needed
	if !isDir {
		fileRecord, err := h.Repos.Files.Get(session.UserID, resolvedPath)
		if err != nil {
			return c.Status(404).JSON(fiber.Map{"error": "not_found"})
		}
		if fileRecord.Status != "ready" {
			// Cancel associated oss_upload job if any
			if fileRecord.UploadID != "" {
				if job, err := h.Repos.Jobs.FindActiveByParam("oss_upload", fileRecord.UploadID); err == nil {
					h.Dispatcher.Cancel(job.JobID)
				}
				tempDir := filepath.Join(config.TempDir, "upload", fileRecord.UploadID)
				_ = os.RemoveAll(tempDir)
			}
			_ = h.Repos.Files.Delete(session.UserID, resolvedPath)
			_ = h.Repos.Shares.DeleteByPath(session.UserID, resolvedPath)
			h.Audit.LogFromCtx(c, "file_delete", path, "", "success", 0)
			return c.JSON(fiber.Map{"ok": true})
		}
	}

	// Calculate size from DB for quota update
	var totalSize int64
	if isDir {
		size, err := h.Repos.Files.SumSizeByPrefix(session.UserID, resolvedPath)
		if err != nil {
			return c.Status(500).JSON(fiber.Map{"error": "internal_error"})
		}
		totalSize = size
	} else {
		fileRecord, err := h.Repos.Files.Get(session.UserID, resolvedPath)
		if err != nil {
			return c.Status(404).JSON(fiber.Map{"error": "not_found"})
		}
		totalSize = fileRecord.Size
	}

	// Move to trash instead of permanent delete
	trashUUID := uuid.New().String()
	trashKey := session.Username + "/.trash/" + trashUUID

	if isDir {
		if !strings.HasSuffix(resolvedPath, "/") {
			resolvedPath += "/"
		}
		trashKey += "/"

		if c.Get("Accept") == "text/event-stream" {
			c.Set("Content-Type", "text/event-stream")
			c.Set("Cache-Control", "no-cache")
			c.Set("Connection", "keep-alive")

			c.Context().SetBodyStreamWriter(func(w *bufio.Writer) {
				progress := func(done, total int, current string) {
					data, _ := json.Marshal(fiber.Map{"done": done, "total": total, "current": current})
					_, _ = fmt.Fprintf(w, "data: %s\n\n", data)
					_ = w.Flush()
				}

				moveErr := h.Store.RecursiveMove(resolvedPath, trashKey, progress)
				if moveErr != nil {
					data, _ := json.Marshal(fiber.Map{"error": moveErr.Error()})
					_, _ = fmt.Fprintf(w, "data: %s\n\n", data)
				} else {
					_ = h.Repos.Trash.Create(session.UserID, path, trashKey, totalSize, true)
					_ = h.Repos.Files.MoveByPrefix(session.UserID, resolvedPath, trashKey)
					_ = h.Repos.Shares.DeleteByPrefix(session.UserID, resolvedPath)
					data, _ := json.Marshal(fiber.Map{"done": true})
					_, _ = fmt.Fprintf(w, "data: %s\n\n", data)
				}
				_ = w.Flush()
			})
			return nil
		}

		if err := h.Store.RecursiveMove(resolvedPath, trashKey, nil); err != nil {
			return c.Status(500).JSON(fiber.Map{"error": "delete_failed"})
		}
	} else {
		if c.Get("Accept") == "text/event-stream" {
			c.Set("Content-Type", "text/event-stream")
			c.Set("Cache-Control", "no-cache")
			c.Set("Connection", "keep-alive")

			c.Context().SetBodyStreamWriter(func(w *bufio.Writer) {
				data, _ := json.Marshal(fiber.Map{"done": 0, "total": 1, "current": resolvedPath})
				_, _ = fmt.Fprintf(w, "data: %s\n\n", data)
				_ = w.Flush()

				moveErr := h.Store.MoveObject(resolvedPath, trashKey)
				if moveErr != nil {
					data, _ := json.Marshal(fiber.Map{"error": moveErr.Error()})
					_, _ = fmt.Fprintf(w, "data: %s\n\n", data)
				} else {
					_ = h.Repos.Trash.Create(session.UserID, path, trashKey, totalSize, false)
					_ = h.Repos.Files.Move(session.UserID, resolvedPath, trashKey, filepath.Base(trashKey))
					_ = h.Repos.Shares.DeleteByPath(session.UserID, resolvedPath)
					data, _ = json.Marshal(fiber.Map{"done": 1, "total": 1, "current": resolvedPath})
					_, _ = fmt.Fprintf(w, "data: %s\n\n", data)
					data, _ = json.Marshal(fiber.Map{"done": true})
					_, _ = fmt.Fprintf(w, "data: %s\n\n", data)
				}
				_ = w.Flush()
			})
			return nil
		}

		if err := h.Store.MoveObject(resolvedPath, trashKey); err != nil {
			return c.Status(500).JSON(fiber.Map{"error": "delete_failed"})
		}
	}

	// Record in trash table
	_ = h.Repos.Trash.Create(session.UserID, path, trashKey, totalSize, isDir)

	// Move file records to trash paths (preserves WrappedDEK and metadata)
	if isDir {
		_ = h.Repos.Files.MoveByPrefix(session.UserID, resolvedPath, trashKey)
		_ = h.Repos.Shares.DeleteByPrefix(session.UserID, resolvedPath)
	} else {
		_ = h.Repos.Files.Move(session.UserID, resolvedPath, trashKey, filepath.Base(trashKey))
		_ = h.Repos.Shares.DeleteByPath(session.UserID, resolvedPath)
	}

	h.Audit.LogFromCtx(c, "file_delete", path, "", "success", 0)
	return c.JSON(fiber.Map{"ok": true})
}

// handleFileAccess returns a presigned URL to the encrypted file on OSS plus metadata
// needed for client-side decryption via Service Worker.
func (h *Handler) handleFileAccess(c *fiber.Ctx) error {
	path := c.Query("path", "")
	if path == "" {
		return c.Status(400).JSON(fiber.Map{"error": "path_required"})
	}

	resolvedPath, err := middleware.ResolvePath(c, path)
	if err != nil {
		return err
	}

	session := c.Locals("session").(*model.Session)
	fileRecord, err := h.Repos.Files.Get(session.UserID, resolvedPath)
	if err != nil {
		return c.Status(404).JSON(fiber.Map{"error": "not_found"})
	}
	if fileRecord.Status != "ready" {
		return c.Status(409).JSON(fiber.Map{"error": "file_not_ready"})
	}

	presignedURL, err := h.Store.GeneratePresignedURL(resolvedPath, 4*time.Hour)
	if err != nil {
		return c.Status(500).JSON(fiber.Map{"error": "presign_failed"})
	}

	// Unwrap DEK server-side so KEK never leaves the server
	kek, err := h.getFileEncryptionKey(session)
	if err != nil {
		return c.Status(500).JSON(fiber.Map{"error": "internal_error"})
	}
	wrappedBytes, err := hex.DecodeString(fileRecord.WrappedDEK)
	if err != nil {
		return c.Status(500).JSON(fiber.Map{"error": "internal_error"})
	}
	dek, err := auth.UnwrapDEK(kek, wrappedBytes)
	if err != nil {
		return c.Status(500).JSON(fiber.Map{"error": "unwrap_failed"})
	}

	fileName := filepath.Base(resolvedPath)
	ct := mime.TypeByExtension(filepath.Ext(fileName))

	h.Audit.LogFromCtx(c, "file_access", path, "", "success", 0)
	return c.JSON(fiber.Map{
		"url":          presignedURL,
		"size":         fileRecord.Size,
		"name":         fileName,
		"content_type": ct,
		"chunk_size":   auth.DefaultChunkSize,
		"dek":          hex.EncodeToString(dek),
		"content_hash": fileRecord.ContentHash,
	})
}

// handlePreview generates a temporary public URL for previewing files
// via external services (e.g., Microsoft Office Online).
// Query params: path (file path), type (preview type, e.g. "office")
func (h *Handler) handlePreview(c *fiber.Ctx) error {
	path := c.Query("path", "")
	if path == "" {
		return c.Status(400).JSON(fiber.Map{"error": "path_required"})
	}
	previewType := c.Query("type", "")
	if previewType == "" {
		return c.Status(400).JSON(fiber.Map{"error": "type_required"})
	}

	switch previewType {
	case "office":
		return h.handleOfficePreview(c, path)
	default:
		return c.Status(400).JSON(fiber.Map{"error": "unsupported_preview_type"})
	}
}

func (h *Handler) handleOfficePreview(c *fiber.Ctx, path string) error {
	resolvedPath, err := middleware.ResolvePath(c, path)
	if err != nil {
		return err
	}

	session := c.Locals("session").(*model.Session)
	fileRecord, err := h.Repos.Files.Get(session.UserID, resolvedPath)
	if err != nil {
		return c.Status(404).JSON(fiber.Map{"error": "not_found"})
	}
	if fileRecord.Status != "ready" {
		return c.Status(409).JSON(fiber.Map{"error": "file_not_ready"})
	}
	kek, err := h.getFileEncryptionKey(session)
	if err != nil {
		return c.Status(500).JSON(fiber.Map{"error": "internal_error"})
	}

	// Unwrap the file's DEK
	wrappedBytes, err := hex.DecodeString(fileRecord.WrappedDEK)
	if err != nil {
		return c.Status(500).JSON(fiber.Map{"error": "invalid_wrapped_dek"})
	}
	dek, err := auth.UnwrapDEK(kek, wrappedBytes)
	if err != nil {
		return c.Status(500).JSON(fiber.Map{"error": "unwrap_dek_failed"})
	}

	// Decrypt file into memory
	reader, err := h.Store.GetObjectContent(resolvedPath)
	if err != nil {
		return c.Status(500).JSON(fiber.Map{"error": "read_file_failed"})
	}
	defer func() { _ = reader.Close() }()

	var plainBuf bytes.Buffer
	if err := auth.DecryptStream(dek, reader, &plainBuf); err != nil {
		return c.Status(500).JSON(fiber.Map{"error": "decrypt_failed"})
	}

	// Upload plaintext to a temporary key
	ext := filepath.Ext(resolvedPath)
	tempKey := fmt.Sprintf("_tmp/preview/%s%s", uuid.New().String(), ext)
	if err := h.Store.PutObjectBytes(tempKey, plainBuf.Bytes()); err != nil {
		return c.Status(500).JSON(fiber.Map{"error": "upload_temp_failed"})
	}

	// Generate a short-lived presigned URL
	presignedURL, err := h.Store.GeneratePresignedURL(tempKey, 1*time.Minute)
	if err != nil {
		_ = h.Store.DeleteObject(tempKey)
		return c.Status(500).JSON(fiber.Map{"error": "presign_failed"})
	}

	// Schedule cleanup of the temp file
	go func() {
		time.Sleep(2 * time.Minute)
		_ = h.Store.DeleteObject(tempKey)
	}()

	return c.JSON(fiber.Map{"url": presignedURL})
}
