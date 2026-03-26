package handler

import (
	"bufio"
	"encoding/json"
	"fmt"
	"log"
	"mime"
	"net/url"
	"os"
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

func (h *Handler) handleList(c *fiber.Ctx) error {
	path := c.Query("path", "")

	resolvedPath, err := middleware.ResolvePath(c, path)
	if err != nil {
		return err
	}

	session := c.Locals("session").(*model.Session)
	records, err := model.ListDirectChildren(resolvedPath)
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
	if jobs, err := model.ListActiveUploadJobs(session.UserID); err == nil {
		for _, j := range jobs {
			if j.TaskID == "" {
				continue
			}
			var p struct{ UploadID string `json:"upload_id"` }
			if json.Unmarshal([]byte(j.Params), &p) == nil && p.UploadID != "" {
				if task, err := model.GetTask(j.TaskID); err == nil {
					uploadTasks[p.UploadID] = taskInfo{TaskID: task.TaskID, Progress: task.Progress, Phase: task.Phase}
				}
			}
		}
	}

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
		if r.ThumbnailKey != "" {
			if url, err := h.Store.GeneratePresignedURL(r.ThumbnailKey, 1*time.Hour); err == nil {
				fi.ThumbnailURL = url
			}
		}
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
	_ = model.UpsertFile(session.UserID, resolvedPath, dirName, true, 0, "", "")

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
		if rec, err := model.GetFile(session.UserID, oldResolved); err == nil && rec.Status != "ready" {
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
		_ = model.MoveFilesByPrefix(session.UserID, oldResolved, newResolved)
	} else {
		newName := filepath.Base(newResolved)
		_ = model.MoveFile(session.UserID, oldResolved, newResolved, newName)
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
	if !body.IsDir {
		srcRecord, err := model.GetFile(session.UserID, srcResolved)
		if err != nil {
			return c.Status(404).JSON(fiber.Map{"error": "source_not_found"})
		}
		if srcRecord.Status != "ready" {
			return c.Status(409).JSON(fiber.Map{"error": "file_not_ready"})
		}
		srcSize = srcRecord.Size
		srcContentHash = srcRecord.ContentHash
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
					h.syncDirFiles(session.UserID, dstResolved)
				} else {
					dstName := filepath.Base(dstResolved)
					ct := mime.TypeByExtension(filepath.Ext(dstResolved))
					_ = model.UpsertFile(session.UserID, dstResolved, dstName, false, srcSize, ct, srcContentHash)
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
		h.syncDirFiles(session.UserID, dstResolved)
	} else {
		if err := h.Store.CopyObject(srcResolved, dstResolved); err != nil {
			return c.Status(500).JSON(fiber.Map{"error": "copy_failed"})
		}
		dstName := filepath.Base(dstResolved)
		ct := mime.TypeByExtension(filepath.Ext(dstResolved))
		_ = model.UpsertFile(session.UserID, dstResolved, dstName, false, srcSize, ct, srcContentHash)
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
		if rec, err := model.GetFile(session.UserID, srcResolved); err == nil && rec.Status != "ready" {
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
					_ = model.MoveFilesByPrefix(session.UserID, srcResolved, dstResolved)
				} else {
					newName := filepath.Base(dstResolved)
					_ = model.MoveFile(session.UserID, srcResolved, dstResolved, newName)
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
		_ = model.MoveFilesByPrefix(session.UserID, srcResolved, dstResolved)
	} else {
		if err := h.Store.MoveObject(srcResolved, dstResolved); err != nil {
			return c.Status(500).JSON(fiber.Map{"error": "move_failed"})
		}
		newName := filepath.Base(dstResolved)
		_ = model.MoveFile(session.UserID, srcResolved, dstResolved, newName)
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
		fileRecord, err := model.GetFile(session.UserID, resolvedPath)
		if err != nil {
			return c.Status(404).JSON(fiber.Map{"error": "not_found"})
		}
		if fileRecord.Status != "ready" {
			// Cancel associated oss_upload job if any
			if fileRecord.UploadID != "" {
				if job, err := model.FindActiveJobByParam("upload", fileRecord.UploadID); err == nil {
					h.Dispatcher.Cancel(job.JobID)
				}
				tempDir := filepath.Join(config.TempDir, "upload", fileRecord.UploadID)
				_ = os.RemoveAll(tempDir)
			}
			_ = model.DeleteFile(session.UserID, resolvedPath)
			h.Audit.LogFromCtx(c, "file_delete", path, "", "success", 0)
			return c.JSON(fiber.Map{"ok": true})
		}
	}

	// Calculate size from DB for quota update
	var totalSize int64
	if isDir {
		size, err := model.SumFileSizeByPrefix(session.UserID, resolvedPath)
		if err != nil {
			return c.Status(500).JSON(fiber.Map{"error": "internal_error"})
		}
		totalSize = size
	} else {
		fileRecord, err := model.GetFile(session.UserID, resolvedPath)
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
					_ = model.CreateTrashRecord(session.UserID, path, trashKey, totalSize, true)
					_ = model.DeleteFilesByPrefix(session.UserID, resolvedPath)
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
					_ = model.CreateTrashRecord(session.UserID, path, trashKey, totalSize, false)
					_ = model.DeleteFile(session.UserID, resolvedPath)
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
	_ = model.CreateTrashRecord(session.UserID, path, trashKey, totalSize, isDir)

	// Remove file records
	if isDir {
		_ = model.DeleteFilesByPrefix(session.UserID, resolvedPath)
	} else {
		_ = model.DeleteFile(session.UserID, resolvedPath)
	}

	h.Audit.LogFromCtx(c, "file_delete", path, "", "success", 0)
	return c.JSON(fiber.Map{"ok": true})
}

func (h *Handler) handleDownload(c *fiber.Ctx) error {
	path := c.Query("path", "")
	if path == "" {
		return c.Status(400).JSON(fiber.Map{"error": "path_required"})
	}

	resolvedPath, err := middleware.ResolvePath(c, path)
	if err != nil {
		return err
	}

	session := c.Locals("session").(*model.Session)
	if rec, err := model.GetFile(session.UserID, resolvedPath); err == nil && rec.Status != "ready" {
		return c.Status(409).JSON(fiber.Map{"error": "file_not_ready"})
	}

	h.Audit.LogFromCtx(c, "file_download", path, "", "success", 0)
	return c.JSON(fiber.Map{
		"url": "/file/content/raw?path=" + url.QueryEscape(path) + "&dl=1",
	})
}

// parseRange parses an HTTP Range header per RFC 7233.
// Supports "bytes=N-M", "bytes=N-", "bytes=-N" (single range only).
// Returns inclusive [start, end] or error.
func parseRange(header string, totalSize int64) (int64, int64, error) {
	if !strings.HasPrefix(header, "bytes=") {
		return 0, 0, fmt.Errorf("invalid range prefix")
	}
	spec := strings.TrimPrefix(header, "bytes=")

	// Reject multi-range
	if strings.Contains(spec, ",") {
		return 0, 0, fmt.Errorf("multi-range not supported")
	}

	parts := strings.SplitN(spec, "-", 2)
	if len(parts) != 2 {
		return 0, 0, fmt.Errorf("malformed range")
	}

	var start, end int64
	if parts[0] == "" {
		// Suffix range: bytes=-N (last N bytes)
		n, err := strconv.ParseInt(parts[1], 10, 64)
		if err != nil || n <= 0 {
			return 0, 0, fmt.Errorf("invalid suffix length")
		}
		start = totalSize - n
		if start < 0 {
			start = 0
		}
		end = totalSize - 1
	} else {
		var err error
		start, err = strconv.ParseInt(parts[0], 10, 64)
		if err != nil || start < 0 {
			return 0, 0, fmt.Errorf("invalid start")
		}
		if parts[1] == "" {
			// Open-ended: bytes=N-
			end = totalSize - 1
		} else {
			end, err = strconv.ParseInt(parts[1], 10, 64)
			if err != nil {
				return 0, 0, fmt.Errorf("invalid end")
			}
		}
	}

	if start > end || start >= totalSize {
		return 0, 0, fmt.Errorf("unsatisfiable range")
	}
	if end >= totalSize {
		end = totalSize - 1
	}
	return start, end, nil
}

func (h *Handler) handleRawFile(c *fiber.Ctx) error {
	path := c.Query("path", "")
	if path == "" {
		return c.Status(400).JSON(fiber.Map{"error": "path_required"})
	}

	resolvedPath, err := middleware.ResolvePath(c, path)
	if err != nil {
		return err
	}

	session := c.Locals("session").(*model.Session)
	key, err := h.getFileEncryptionKey(session)
	if err != nil {
		return c.Status(500).JSON(fiber.Map{"error": "internal_error"})
	}

	// Set common response headers
	fileName := filepath.Base(resolvedPath)
	ct := mime.TypeByExtension(filepath.Ext(fileName))
	if ct != "" {
		c.Set("Content-Type", ct)
	}
	if c.Query("dl") == "1" {
		c.Set("Content-Disposition", fmt.Sprintf(`attachment; filename*=UTF-8''%s`, url.PathEscape(fileName)))
	}
	c.Set("Accept-Ranges", "bytes")

	// Look up plaintext size from DB
	var fileRecord model.FileRecord
	if err := h.DB.Where("path = ?", resolvedPath).First(&fileRecord).Error; err != nil {
		return c.Status(404).JSON(fiber.Map{"error": "not_found"})
	}
	if fileRecord.Status != "ready" {
		return c.Status(409).JSON(fiber.Map{"error": "file_not_ready"})
	}
	plaintextSize := fileRecord.Size

	// Check for Range header
	rangeHeader := c.Get("Range")
	if rangeHeader == "" || plaintextSize <= 0 {
		// Full file response
		reader, err := h.Store.GetObjectContent(resolvedPath)
		if err != nil {
			return c.Status(404).JSON(fiber.Map{"error": "not_found"})
		}
		if plaintextSize > 0 {
			c.Set("Content-Length", strconv.FormatInt(plaintextSize, 10))
		}
		c.Context().SetBodyStreamWriter(func(w *bufio.Writer) {
			defer func() { _ = reader.Close() }()
			if err := auth.DecryptStream(key, reader, w); err != nil {
				log.Printf("[raw] decrypt error for %s: %v", resolvedPath, err)
			}
			_ = w.Flush()
		})
		return nil
	}

	// Range request
	pStart, pEnd, err := parseRange(rangeHeader, plaintextSize)
	if err != nil {
		c.Set("Content-Range", fmt.Sprintf("bytes */%d", plaintextSize))
		return c.SendStatus(416)
	}

	chunkSz := int64(auth.DefaultChunkSize)
	encChunkSz := int64(auth.NonceSize + auth.DefaultChunkSize + auth.TagSize)
	headerSz := int64(5)

	startChunk := pStart / chunkSz
	endChunk := pEnd / chunkSz

	cipherStart := headerSz + startChunk*encChunkSz
	cipherEnd := headerSz + (endChunk+1)*encChunkSz - 1

	// Clamp cipherEnd to actual encrypted file size to avoid OSS returning full file
	lastChunkPlain := plaintextSize % chunkSz
	if lastChunkPlain == 0 && plaintextSize > 0 {
		lastChunkPlain = chunkSz
	}
	totalChunks := (plaintextSize + chunkSz - 1) / chunkSz
	cipherTotal := headerSz + (totalChunks-1)*encChunkSz + int64(auth.NonceSize) + lastChunkPlain + int64(auth.TagSize)
	if cipherEnd >= cipherTotal {
		cipherEnd = cipherTotal - 1
	}

	reader, err := h.Store.GetObjectContentRange(resolvedPath, cipherStart, cipherEnd)
	if err != nil {
		return c.Status(404).JSON(fiber.Map{"error": "not_found"})
	}

	contentLength := pEnd - pStart + 1
	trimStart := pStart - startChunk*chunkSz
	trimEnd := trimStart + contentLength - 1

	c.Status(206)
	c.Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", pStart, pEnd, plaintextSize))
	c.Set("Content-Length", strconv.FormatInt(contentLength, 10))

	c.Context().SetBodyStreamWriter(func(w *bufio.Writer) {
		defer func() { _ = reader.Close() }()
		if err := auth.DecryptRange(key, reader, w, auth.DefaultChunkSize, uint64(startChunk), trimStart, trimEnd); err != nil {
			log.Printf("[raw] range decrypt error for %s: %v", resolvedPath, err)
		}
		_ = w.Flush()
	})
	return nil
}
