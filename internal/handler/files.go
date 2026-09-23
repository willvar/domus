package handler

import (
	"bufio"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"mime"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/willvar/dofs"
	"gorm.io/gorm"

	"domus/internal/auth"
	"domus/internal/middleware"
	"domus/internal/model"
	"domus/internal/store"
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
	defer dofs.Clear(thumbDEK)
	fi.ThumbnailURL = presignedURL
	fi.ThumbnailDEK = hex.EncodeToString(thumbDEK)
}

type uploadTaskInfo struct {
	TaskID   string
	Progress float64
	Phase    string
}

// activeUploadTasksByID returns recent running upload tasks keyed by task_id.
func (h *Handler) activeUploadTasksByID(userID string) map[string]uploadTaskInfo {
	out := make(map[string]uploadTaskInfo)
	tasks, err := h.Repos.Tasks.ListRecent(userID)
	if err != nil {
		return out
	}
	for _, task := range tasks {
		if task.Type != "upload" {
			continue
		}
		if task.Status != "running" && task.Status != "pending" {
			continue
		}
		out[task.TaskID] = uploadTaskInfo{
			TaskID:   task.TaskID,
			Progress: task.Progress,
			Phase:    task.Phase,
		}
	}
	return out
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

	uploadTasks := h.activeUploadTasksByID(session.UserID)

	kek, _ := h.getFileEncryptionKey(session)

	files := make([]store.FileInfo, 0, len(records))
	for _, r := range records {
		if r.Path == "/.domus/" {
			continue
		}
		fi := store.FileInfo{
			Inode:         r.ID,
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
			MediaCodecs:   r.MediaCodecs,
			MediaMeta:     r.MediaMeta,
			Status:        r.Status,
		}
		if r.Status != "ready" && r.TaskID != "" {
			if ti, ok := uploadTasks[r.TaskID]; ok {
				fi.TaskID = ti.TaskID
				fi.TaskProgress = ti.Progress
				fi.TaskPhase = ti.Phase
			}
		}
		h.fillThumbnail(&fi, &r, kek)
		files = append(files, fi)
	}

	return c.JSON(fiber.Map{"files": files})
}

func (h *Handler) handleSearch(c *fiber.Ctx) error {
	query := strings.TrimSpace(c.Query("query", ""))
	if query == "" {
		return c.Status(400).JSON(fiber.Map{"error": "query_required"})
	}

	limit := c.QueryInt("limit", 100)
	if limit <= 0 {
		limit = 100
	}

	session := c.Locals("session").(*model.Session)
	results, err := h.Repos.Files.SearchFiles(session.UserID, query, limit)
	if err != nil {
		return c.Status(500).JSON(fiber.Map{"error": "search_failed"})
	}

	items := make([]fiber.Map, 0, len(results))
	for _, r := range results {
		items = append(items, fiber.Map{
			"inode":         r.ID,
			"path":          middleware.ToAppPath(r.Path, session.Username),
			"parent":        middleware.ToAppPath(r.Parent, session.Username),
			"name":          r.Name,
			"is_dir":        r.IsDir,
			"size":          r.Size,
			"content_type":  r.ContentType,
			"created_at":    r.CreatedAt,
			"last_modified": r.UpdatedAt,
			"rank":          r.Rank,
		})
	}

	return c.JSON(fiber.Map{"results": items})
}

func (h *Handler) handleMkdir(c *fiber.Ctx) error {
	var body struct {
		Path string `json:"path"`
	}
	if err := c.BodyParser(&body); err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "invalid_request"})
	}
	if isProtectedMutationRoot(body.Path) {
		return c.Status(400).JSON(fiber.Map{"error": "invalid_path"})
	}

	resolvedPath, err := middleware.ResolvePath(c, body.Path)
	if err != nil {
		return err
	}

	session := c.Locals("session").(*model.Session)
	dirName := filepath.Base(strings.TrimSuffix(resolvedPath, "/"))
	if err := h.Repos.Files.Upsert(session.UserID, resolvedPath, dirName, true, 0, "", ""); err != nil {
		return c.Status(500).JSON(fiber.Map{"error": "mkdir_failed"})
	}

	// Notify WebSocket subscribers of the parent directory
	if parent := parentDirOf(resolvedPath); parent != "" {
		appPath := toAppPath(parent, session.Username)
		h.Hub.PushDirChanged(session.UserID, parent, appPath, "created")
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
	if isProtectedMutationRoot(body.OldPath) || isProtectedMutationRoot(body.NewPath) {
		return c.Status(400).JSON(fiber.Map{"error": "invalid_path"})
	}

	oldResolved, err := middleware.ResolvePath(c, body.OldPath)
	if err != nil {
		return err
	}
	session := c.Locals("session").(*model.Session)

	// Block rename for non-ready files
	if !body.IsDir {
		if rec, err := h.Repos.Files.Get(session.UserID, oldResolved); err == nil && rec.Status != "ready" {
			return c.Status(409).JSON(fiber.Map{"error": "file_not_ready"})
		}
	}

	newResolved, err := middleware.ResolvePath(c, body.NewPath)
	if err != nil {
		return err
	}

	if err := movePathViaStore(h, session.UserID, oldResolved, newResolved, body.IsDir, nil); err != nil {
		return c.Status(500).JSON(fiber.Map{"error": "rename_failed"})
	}

	if err := syncMovedFileRecords(
		h, session.UserID, oldResolved, newResolved, body.OldPath, body.NewPath, body.IsDir,
	); err != nil {
		return c.Status(500).JSON(fiber.Map{"error": "rename_failed"})
	}

	// Notify WebSocket subscribers of both old and new parent directories
	if parent := parentDirOf(oldResolved); parent != "" {
		appPath := toAppPath(parent, session.Username)
		h.Hub.PushDirChanged(session.UserID, parent, appPath, "refresh")
	}
	if parent := parentDirOf(newResolved); parent != "" {
		appPath := toAppPath(parent, session.Username)
		h.Hub.PushDirChanged(session.UserID, parent, appPath, "refresh")
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
	if isProtectedMutationRoot(body.SrcPath) || isProtectedMutationRoot(body.DstPath) {
		return c.Status(400).JSON(fiber.Map{"error": "invalid_path"})
	}

	srcResolved, err := middleware.ResolvePath(c, body.SrcPath)
	if err != nil {
		return err
	}
	dstResolved, err := middleware.ResolvePath(c, body.DstPath)
	if err != nil {
		return err
	}

	session := c.Locals("session").(*model.Session)
	if h.FileSystem == nil {
		return c.Status(503).JSON(fiber.Map{"error": "dofs_unavailable"})
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

			copyErr := h.FileSystem.Copy(session.UserID, srcResolved, dstResolved, progress)
			if copyErr != nil {
				data, _ := json.Marshal(fiber.Map{"error": copyErr.Error()})
				_, _ = fmt.Fprintf(w, "data: %s\n\n", data)
			} else {
				data, _ := json.Marshal(fiber.Map{"done": true})
				_, _ = fmt.Fprintf(w, "data: %s\n\n", data)
			}
			_ = w.Flush()
		})
		return nil
	}

	if err := h.FileSystem.Copy(session.UserID, srcResolved, dstResolved, nil); err != nil {
		return c.Status(500).JSON(fiber.Map{"error": "copy_failed"})
	}

	// Notify WebSocket subscribers of the destination parent directory
	if parent := parentDirOf(dstResolved); parent != "" {
		appPath := toAppPath(parent, session.Username)
		h.Hub.PushDirChanged(session.UserID, parent, appPath, "refresh")
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
	if isProtectedMutationRoot(body.SrcPath) || isProtectedMutationRoot(body.DstPath) {
		return c.Status(400).JSON(fiber.Map{"error": "invalid_path"})
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

	session := c.Locals("session").(*model.Session)
	if err := h.ensureParentDirRecords(session.UserID, dstResolved, body.IsDir); err != nil {
		return c.Status(500).JSON(fiber.Map{"error": "move_failed"})
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

			moveErr := movePathViaStore(h, session.UserID, srcResolved, dstResolved, body.IsDir, progress)

			if moveErr != nil {
				data, _ := json.Marshal(fiber.Map{"error": moveErr.Error()})
				_, _ = fmt.Fprintf(w, "data: %s\n\n", data)
			} else if syncErr := syncMovedFileRecords(h, session.UserID, srcResolved, dstResolved, body.SrcPath, body.DstPath, body.IsDir); syncErr != nil {
				data, _ := json.Marshal(fiber.Map{"error": syncErr.Error()})
				_, _ = fmt.Fprintf(w, "data: %s\n\n", data)
			} else {
				data, _ := json.Marshal(fiber.Map{"done": true})
				_, _ = fmt.Fprintf(w, "data: %s\n\n", data)
			}
			_ = w.Flush()
		})
		return nil
	}

	if err := movePathViaStore(h, session.UserID, srcResolved, dstResolved, body.IsDir, nil); err != nil {
		return c.Status(500).JSON(fiber.Map{"error": "move_failed"})
	}
	if err := syncMovedFileRecords(h, session.UserID, srcResolved, dstResolved, body.SrcPath, body.DstPath, body.IsDir); err != nil {
		return c.Status(500).JSON(fiber.Map{"error": "move_failed"})
	}

	// Notify WebSocket subscribers of both source and destination parent directories
	if parent := parentDirOf(srcResolved); parent != "" {
		appPath := toAppPath(parent, session.Username)
		h.Hub.PushDirChanged(session.UserID, parent, appPath, "refresh")
	}
	if parent := parentDirOf(dstResolved); parent != "" {
		appPath := toAppPath(parent, session.Username)
		h.Hub.PushDirChanged(session.UserID, parent, appPath, "refresh")
	}

	h.Audit.LogFromCtx(c, "file_move", body.SrcPath, body.DstPath, "success", 0)
	return c.JSON(fiber.Map{"ok": true})
}

func (h *Handler) handleDelete(c *fiber.Ctx) error {
	requestedPath := c.Query("path", "")
	if requestedPath == "" {
		return c.Status(400).JSON(fiber.Map{"error": "path_required"})
	}
	if normalizeAppPath(requestedPath) == "/" {
		return c.Status(400).JSON(fiber.Map{"error": "invalid_path"})
	}
	if c.Query("permanent") != "1" && !strings.EqualFold(c.Query("permanent"), "true") {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "use_trash_endpoint"})
	}
	resolvedPath, err := middleware.ResolvePath(c, requestedPath)
	if err != nil {
		return err
	}
	if resolvedPath == "/" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid_path"})
	}
	session := c.Locals("session").(*model.Session)
	record, err := h.Repos.Files.Get(session.UserID, resolvedPath)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		// Idempotent cleanup remains useful after a completed retry. If a new
		// inode appears at this path it is protected by the check below.
		return c.JSON(fiber.Map{"ok": true})
	}
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "delete_failed"})
	}
	expectedInode, parseErr := strconv.ParseInt(c.Query("expected_inode"), 10, 64)
	if parseErr != nil || expectedInode <= 0 {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "expected_inode_required"})
	}
	if record.ID != expectedInode {
		return c.Status(fiber.StatusConflict).JSON(fiber.Map{"error": "stale_file"})
	}
	if record.Status != "ready" {
		if record.OSSUploadID != "" {
			_ = h.Store.AbortMultipartUpload(record.StorageKey(), record.OSSUploadID)
		}
		if h.FileSystem != nil {
			_ = h.FileSystem.AbortDirectUpload(c.UserContext(), session.UserID, record.UploadID)
		} else {
			_ = h.Repos.Files.Delete(session.UserID, resolvedPath)
		}
		h.Audit.LogFromCtx(c, "file_delete", requestedPath, "upload_aborted", "success", 0)
		return c.JSON(fiber.Map{"ok": true})
	}
	if c.Get("Accept") == "text/event-stream" && record.IsDir {
		c.Set("Content-Type", "text/event-stream")
		c.Set("Cache-Control", "no-cache")
		c.Set("Connection", "keep-alive")
		c.Context().SetBodyStreamWriter(func(w *bufio.Writer) {
			progress := func(done, total int, current string) {
				data, _ := json.Marshal(fiber.Map{"done": done, "total": total, "current": current})
				_, _ = fmt.Fprintf(w, "data: %s\n\n", data)
				_ = w.Flush()
			}
			if deleteErr := h.permanentlyDeletePathWithProgress(session.UserID, resolvedPath, true, progress); deleteErr != nil {
				data, _ := json.Marshal(fiber.Map{"error": deleteErr.Error()})
				_, _ = fmt.Fprintf(w, "data: %s\n\n", data)
			} else {
				data, _ := json.Marshal(fiber.Map{"done": true})
				_, _ = fmt.Fprintf(w, "data: %s\n\n", data)
			}
			_ = w.Flush()
		})
		return nil
	}
	if err := h.permanentlyDeletePath(session.UserID, resolvedPath, record.IsDir); err != nil {
		return c.Status(500).JSON(fiber.Map{"error": "delete_failed"})
	}
	if parent := parentDirOf(resolvedPath); parent != "" {
		appPath := toAppPath(parent, session.Username)
		h.Hub.PushDirChanged(session.UserID, parent, appPath, "refresh")
	}
	h.Audit.LogFromCtx(c, "file_delete", requestedPath, "", "success", 0)
	return c.JSON(fiber.Map{"ok": true})
}

// handleFileAccess returns a presigned URL to the encrypted file on OSS plus metadata
// needed for client-side decryption via Service Worker.
func (h *Handler) handleFileAccess(c *fiber.Ctx) error {
	path := c.Query("path", "")
	if path == "" {
		return c.Status(400).JSON(fiber.Map{"error": "path_required"})
	}

	resolveAccessPath := middleware.ResolvePath
	if c.Query("internal") == "true" {
		resolveAccessPath = middleware.ResolveInternalPath
	}
	resolvedPath, err := resolveAccessPath(c, path)
	if err != nil {
		return err
	}

	session := c.Locals("session").(*model.Session)
	fileRecord, err := h.Repos.Files.Get(session.UserID, resolvedPath)
	if err != nil {
		if c.Query("optional") == "true" {
			return c.SendStatus(204)
		}
		return c.Status(404).JSON(fiber.Map{"error": "not_found"})
	}
	if fileRecord.Status != "ready" {
		return c.Status(409).JSON(fiber.Map{"error": "file_not_ready"})
	}
	return h.sendFileAccessResponse(c, session, fileRecord, filepath.Base(resolvedPath), path)
}

func (h *Handler) sendFileAccessResponse(c *fiber.Ctx, session *model.Session, fileRecord *model.FileRecord, fileName, auditResource string) error {
	if fileRecord == nil || fileRecord.Status != "ready" || fileRecord.IsDir {
		return c.Status(fiber.StatusConflict).JSON(fiber.Map{"error": "file_not_ready"})
	}
	presignedURL, err := h.Store.GeneratePresignedURL(fileRecord.StorageKey(), 4*time.Hour)
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
	defer dofs.Clear(dek)

	ct := fileRecord.ContentType
	if ct == "" {
		ct = mime.TypeByExtension(filepath.Ext(fileName))
	}

	h.Audit.LogFromCtx(c, "file_access", auditResource, "", "success", 0)
	return c.JSON(fiber.Map{
		"inode":        fileRecord.ID,
		"url":          presignedURL,
		"size":         fileRecord.Size,
		"name":         fileName,
		"content_type": ct,
		"chunk_size":   auth.DefaultChunkSize,
		"dek":          hex.EncodeToString(dek),
		"content_hash": fileRecord.ContentHash,
		"generation":   fileRecord.Generation,
		"media_codecs": fileRecord.MediaCodecs,
		"media_meta":   fileRecord.MediaMeta,
	})
}
