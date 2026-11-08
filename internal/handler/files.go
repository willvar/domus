package handler

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"mime"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/willvar/dofs"
	"gorm.io/gorm"

	"domus/internal/auth"
	"domus/internal/httpx"
	"domus/internal/middleware"
	"domus/internal/model"
	"domus/internal/store"
)

// fillThumbnail populates presigned URL + plaintext DEK for client-side decryption.
func (h *Handler) fillThumbnail(fi *store.FileInfo, r *model.FileRecord, kek []byte) {
	fi.ThumbnailStatus, fi.ThumbnailError, fi.ThumbnailAttempts = r.ThumbnailStatus, r.ThumbnailError, r.ThumbnailAttempts
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

func (h *Handler) handleList(w http.ResponseWriter, r *http.Request) error {
	path := httpx.Query(r, "path", "")

	resolvedPath, err := middleware.ResolvePath(r, path)
	if err != nil {
		return err
	}

	session := middleware.Session(r)
	records, err := h.Repos.Files.ListDirectChildren(session.UserID, resolvedPath)
	if err != nil {
		return httpx.JSON(w, 500, map[string]any{"error": "list_failed"})
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
			Generation:    r.Generation,
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

	return httpx.JSON(w, http.StatusOK, map[string]any{"files": files})
}

func (h *Handler) handleSearch(w http.ResponseWriter, r *http.Request) error {
	query := strings.TrimSpace(httpx.Query(r, "query", ""))
	if query == "" {
		return httpx.JSON(w, 400, map[string]any{"error": "query_required"})
	}

	limit := httpx.QueryInt(r, "limit", 100)
	if limit <= 0 {
		limit = 100
	}

	session := middleware.Session(r)
	results, err := h.Repos.Files.SearchFiles(session.UserID, query, limit)
	if err != nil {
		return httpx.JSON(w, 500, map[string]any{"error": "search_failed"})
	}

	items := make([]map[string]any, 0, len(results))
	kek, _ := h.getFileEncryptionKey(session)
	for _, r := range results {
		var thumbnail store.FileInfo
		h.fillThumbnail(&thumbnail, &r.FileRecord, kek)
		items = append(items, map[string]any{
			"thumbnail_url": thumbnail.ThumbnailURL, "thumbnail_dek": thumbnail.ThumbnailDEK,
			"thumbnail_status": thumbnail.ThumbnailStatus, "thumbnail_error": thumbnail.ThumbnailError, "thumbnail_attempts": thumbnail.ThumbnailAttempts,
			"inode":         r.ID,
			"generation":    r.Generation,
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

	return httpx.JSON(w, http.StatusOK, map[string]any{"results": items})
}

func (h *Handler) handleRetryThumbnail(w http.ResponseWriter, r *http.Request) error {
	if h.FileSystem == nil {
		return httpx.JSON(w, 503, map[string]any{"error": "dofs_unavailable"})
	}
	var body struct {
		Path string `json:"path"`
	}
	if err := httpx.DecodeJSON(r, &body); err != nil {
		return httpx.JSON(w, 400, map[string]any{"error": "invalid_request"})
	}
	path, err := middleware.ResolvePath(r, body.Path)
	if err != nil {
		return err
	}
	record, err := h.FileSystem.Get(middleware.Session(r).UserID, path)
	if err != nil {
		return httpx.JSON(w, 404, map[string]any{"error": "file_not_found"})
	}
	if record.IsDir || record.Status != "ready" || !(strings.HasPrefix(record.ContentType, "image/") || strings.HasPrefix(record.ContentType, "video/") || record.ContentType == "application/pdf") {
		return httpx.JSON(w, 400, map[string]any{"error": "thumbnail_not_supported"})
	}
	if err := h.FileSystem.SetThumbnailState(*record, "pending", "", 0, nil); err != nil {
		return httpx.JSON(w, 500, map[string]any{"error": "operation_failed"})
	}
	h.notifyParentDir(record.UserID, record.Path)
	return httpx.JSON(w, 200, map[string]any{"ok": true})
}

func (h *Handler) handleMkdir(w http.ResponseWriter, r *http.Request) error {
	var body struct {
		Path string `json:"path"`
	}
	if err := httpx.DecodeJSON(r, &body); err != nil {
		return httpx.JSON(w, 400, map[string]any{"error": "invalid_request"})
	}
	if isProtectedMutationRoot(body.Path) {
		return httpx.JSON(w, 400, map[string]any{"error": "invalid_path"})
	}

	resolvedPath, err := middleware.ResolvePath(r, body.Path)
	if err != nil {
		return err
	}

	session := middleware.Session(r)
	dirName := filepath.Base(strings.TrimSuffix(resolvedPath, "/"))
	if err := h.Repos.Files.Upsert(session.UserID, resolvedPath, dirName, true, 0, "", ""); err != nil {
		return httpx.JSON(w, 500, map[string]any{"error": "mkdir_failed"})
	}

	// Notify WebSocket subscribers of the parent directory
	if parent := parentDirOf(resolvedPath); parent != "" {
		appPath := toAppPath(parent, session.Username)
		h.Hub.PushDirChanged(session.UserID, parent, appPath, "created")
	}

	return httpx.JSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (h *Handler) handleRename(w http.ResponseWriter, r *http.Request) error {
	var body struct {
		OldPath string `json:"old_path"`
		NewPath string `json:"new_path"`
		IsDir   bool   `json:"is_dir"`
	}
	if err := httpx.DecodeJSON(r, &body); err != nil {
		return httpx.JSON(w, 400, map[string]any{"error": "invalid_request"})
	}
	if isProtectedMutationRoot(body.OldPath) || isProtectedMutationRoot(body.NewPath) {
		return httpx.JSON(w, 400, map[string]any{"error": "invalid_path"})
	}

	oldResolved, err := middleware.ResolvePath(r, body.OldPath)
	if err != nil {
		return err
	}
	session := middleware.Session(r)

	// Block rename for non-ready files
	if !body.IsDir {
		if rec, err := h.Repos.Files.Get(session.UserID, oldResolved); err == nil && rec.Status != "ready" {
			return httpx.JSON(w, 409, map[string]any{"error": "file_not_ready"})
		}
	}

	newResolved, err := middleware.ResolvePath(r, body.NewPath)
	if err != nil {
		return err
	}

	if err := movePathViaStore(h, session.UserID, oldResolved, newResolved, body.IsDir, nil); err != nil {
		return httpx.JSON(w, 500, map[string]any{"error": "rename_failed"})
	}

	if err := syncMovedFileRecords(
		h, session.UserID, oldResolved, newResolved, body.OldPath, body.NewPath, body.IsDir,
	); err != nil {
		return httpx.JSON(w, 500, map[string]any{"error": "rename_failed"})
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

	h.audit(r, "file_rename", body.OldPath, body.NewPath, "success", 0)
	return httpx.JSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (h *Handler) handleCopy(w http.ResponseWriter, r *http.Request) error {
	var body struct {
		SrcPath string `json:"src_path"`
		DstPath string `json:"dst_path"`
		IsDir   bool   `json:"is_dir"`
	}
	if err := httpx.DecodeJSON(r, &body); err != nil {
		return httpx.JSON(w, 400, map[string]any{"error": "invalid_request"})
	}
	if isProtectedMutationRoot(body.SrcPath) || isProtectedMutationRoot(body.DstPath) {
		return httpx.JSON(w, 400, map[string]any{"error": "invalid_path"})
	}

	srcResolved, err := middleware.ResolvePath(r, body.SrcPath)
	if err != nil {
		return err
	}
	dstResolved, err := middleware.ResolvePath(r, body.DstPath)
	if err != nil {
		return err
	}

	session := middleware.Session(r)
	if h.FileSystem == nil {
		return httpx.JSON(w, 503, map[string]any{"error": "dofs_unavailable"})
	}

	// Check if SSE is requested
	if r.Header.Get("Accept") == "text/event-stream" {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")

		func() {
			progress := func(done, total int, current string) {
				data, _ := json.Marshal(map[string]any{"done": done, "total": total, "current": current})
				_, _ = fmt.Fprintf(w, "data: %s\n\n", data)
				_ = http.NewResponseController(w).Flush()
			}

			copyErr := h.FileSystem.Copy(session.UserID, srcResolved, dstResolved, progress)
			if copyErr != nil {
				data, _ := json.Marshal(map[string]any{"error": copyErr.Error()})
				_, _ = fmt.Fprintf(w, "data: %s\n\n", data)
			} else {
				data, _ := json.Marshal(map[string]any{"done": true})
				_, _ = fmt.Fprintf(w, "data: %s\n\n", data)
			}
			_ = http.NewResponseController(w).Flush()
		}()
		return nil
	}

	if err := h.FileSystem.Copy(session.UserID, srcResolved, dstResolved, nil); err != nil {
		return httpx.JSON(w, 500, map[string]any{"error": "copy_failed"})
	}

	// Notify WebSocket subscribers of the destination parent directory
	if parent := parentDirOf(dstResolved); parent != "" {
		appPath := toAppPath(parent, session.Username)
		h.Hub.PushDirChanged(session.UserID, parent, appPath, "refresh")
	}

	h.audit(r, "file_copy", body.SrcPath, body.DstPath, "success", 0)
	return httpx.JSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (h *Handler) handleMove(w http.ResponseWriter, r *http.Request) error {
	var body struct {
		SrcPath string `json:"src_path"`
		DstPath string `json:"dst_path"`
		IsDir   bool   `json:"is_dir"`
	}
	if err := httpx.DecodeJSON(r, &body); err != nil {
		return httpx.JSON(w, 400, map[string]any{"error": "invalid_request"})
	}
	if isProtectedMutationRoot(body.SrcPath) || isProtectedMutationRoot(body.DstPath) {
		return httpx.JSON(w, 400, map[string]any{"error": "invalid_path"})
	}

	srcResolved, err := middleware.ResolvePath(r, body.SrcPath)
	if err != nil {
		return err
	}

	// Block move for non-ready files
	if !body.IsDir {
		session := middleware.Session(r)
		if rec, err := h.Repos.Files.Get(session.UserID, srcResolved); err == nil && rec.Status != "ready" {
			return httpx.JSON(w, 409, map[string]any{"error": "file_not_ready"})
		}
	}

	dstResolved, err := middleware.ResolvePath(r, body.DstPath)
	if err != nil {
		return err
	}

	session := middleware.Session(r)
	if err := h.ensureParentDirRecords(session.UserID, dstResolved, body.IsDir); err != nil {
		return httpx.JSON(w, 500, map[string]any{"error": "move_failed"})
	}

	if r.Header.Get("Accept") == "text/event-stream" {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")

		func() {
			progress := func(done, total int, current string) {
				data, _ := json.Marshal(map[string]any{"done": done, "total": total, "current": current})
				_, _ = fmt.Fprintf(w, "data: %s\n\n", data)
				_ = http.NewResponseController(w).Flush()
			}

			moveErr := movePathViaStore(h, session.UserID, srcResolved, dstResolved, body.IsDir, progress)

			if moveErr != nil {
				data, _ := json.Marshal(map[string]any{"error": moveErr.Error()})
				_, _ = fmt.Fprintf(w, "data: %s\n\n", data)
			} else if syncErr := syncMovedFileRecords(h, session.UserID, srcResolved, dstResolved, body.SrcPath, body.DstPath, body.IsDir); syncErr != nil {
				data, _ := json.Marshal(map[string]any{"error": syncErr.Error()})
				_, _ = fmt.Fprintf(w, "data: %s\n\n", data)
			} else {
				data, _ := json.Marshal(map[string]any{"done": true})
				_, _ = fmt.Fprintf(w, "data: %s\n\n", data)
			}
			_ = http.NewResponseController(w).Flush()
		}()
		return nil
	}

	if err := movePathViaStore(h, session.UserID, srcResolved, dstResolved, body.IsDir, nil); err != nil {
		return httpx.JSON(w, 500, map[string]any{"error": "move_failed"})
	}
	if err := syncMovedFileRecords(h, session.UserID, srcResolved, dstResolved, body.SrcPath, body.DstPath, body.IsDir); err != nil {
		return httpx.JSON(w, 500, map[string]any{"error": "move_failed"})
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

	h.audit(r, "file_move", body.SrcPath, body.DstPath, "success", 0)
	return httpx.JSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (h *Handler) handleDelete(w http.ResponseWriter, r *http.Request) error {
	requestedPath := httpx.Query(r, "path", "")
	if requestedPath == "" {
		return httpx.JSON(w, 400, map[string]any{"error": "path_required"})
	}
	if normalizeAppPath(requestedPath) == "/" {
		return httpx.JSON(w, 400, map[string]any{"error": "invalid_path"})
	}
	if httpx.Query(r, "permanent") != "1" && !strings.EqualFold(httpx.Query(r, "permanent"), "true") {
		return httpx.JSON(w, http.StatusBadRequest, map[string]any{"error": "use_trash_endpoint"})
	}
	resolvedPath, err := middleware.ResolvePath(r, requestedPath)
	if err != nil {
		return err
	}
	if resolvedPath == "/" {
		return httpx.JSON(w, http.StatusBadRequest, map[string]any{"error": "invalid_path"})
	}
	session := middleware.Session(r)
	record, err := h.Repos.Files.Get(session.UserID, resolvedPath)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		// Idempotent cleanup remains useful after a completed retry. If a new
		// inode appears at this path it is protected by the check below.
		return httpx.JSON(w, http.StatusOK, map[string]any{"ok": true})
	}
	if err != nil {
		return httpx.JSON(w, http.StatusInternalServerError, map[string]any{"error": "delete_failed"})
	}
	expectedInode, parseErr := strconv.ParseInt(httpx.Query(r, "expected_inode"), 10, 64)
	if parseErr != nil || expectedInode <= 0 {
		return httpx.JSON(w, http.StatusBadRequest, map[string]any{"error": "expected_inode_required"})
	}
	if record.ID != expectedInode {
		return httpx.JSON(w, http.StatusConflict, map[string]any{"error": "stale_file"})
	}
	if record.Status != "ready" {
		if record.OSSUploadID != "" {
			_ = h.Store.AbortMultipartUpload(record.StorageKey(), record.OSSUploadID)
		}
		if h.FileSystem != nil {
			_ = h.FileSystem.AbortDirectUpload(r.Context(), session.UserID, record.UploadID)
		} else {
			_ = h.Repos.Files.Delete(session.UserID, resolvedPath)
		}
		h.audit(r, "file_delete", requestedPath, "upload_aborted", "success", 0)
		return httpx.JSON(w, http.StatusOK, map[string]any{"ok": true})
	}
	if r.Header.Get("Accept") == "text/event-stream" && record.IsDir {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		func() {
			progress := func(done, total int, current string) {
				data, _ := json.Marshal(map[string]any{"done": done, "total": total, "current": current})
				_, _ = fmt.Fprintf(w, "data: %s\n\n", data)
				_ = http.NewResponseController(w).Flush()
			}
			if deleteErr := h.permanentlyDeletePathWithProgress(session.UserID, resolvedPath, true, progress); deleteErr != nil {
				data, _ := json.Marshal(map[string]any{"error": deleteErr.Error()})
				_, _ = fmt.Fprintf(w, "data: %s\n\n", data)
			} else {
				data, _ := json.Marshal(map[string]any{"done": true})
				_, _ = fmt.Fprintf(w, "data: %s\n\n", data)
			}
			_ = http.NewResponseController(w).Flush()
		}()
		return nil
	}
	if err := h.permanentlyDeletePath(session.UserID, resolvedPath, record.IsDir); err != nil {
		return httpx.JSON(w, 500, map[string]any{"error": "delete_failed"})
	}
	if parent := parentDirOf(resolvedPath); parent != "" {
		appPath := toAppPath(parent, session.Username)
		h.Hub.PushDirChanged(session.UserID, parent, appPath, "refresh")
	}
	h.audit(r, "file_delete", requestedPath, "", "success", 0)
	return httpx.JSON(w, http.StatusOK, map[string]any{"ok": true})
}

// handleFileAccess returns a presigned URL to the encrypted file on OSS plus metadata
// needed for client-side decryption via Service Worker.
func (h *Handler) handleFileAccess(w http.ResponseWriter, r *http.Request) error {
	path := httpx.Query(r, "path", "")
	if path == "" {
		return httpx.JSON(w, 400, map[string]any{"error": "path_required"})
	}

	resolveAccessPath := middleware.ResolvePath
	if httpx.Query(r, "internal") == "true" {
		resolveAccessPath = middleware.ResolveInternalPath
	}
	resolvedPath, err := resolveAccessPath(r, path)
	if err != nil {
		return err
	}

	session := middleware.Session(r)
	fileRecord, err := h.Repos.Files.Get(session.UserID, resolvedPath)
	if err != nil {
		if httpx.Query(r, "optional") == "true" {
			return httpx.Status(w, 204)
		}
		return httpx.JSON(w, 404, map[string]any{"error": "not_found"})
	}
	if fileRecord.Status != "ready" {
		return httpx.JSON(w, 409, map[string]any{"error": "file_not_ready"})
	}
	return h.sendFileAccessResponse(w, r, session, fileRecord, filepath.Base(resolvedPath), path)
}

func (h *Handler) handleFileHistory(w http.ResponseWriter, r *http.Request) error {
	if h.FileSystem == nil {
		return httpx.JSON(w, 503, map[string]any{"error": "dofs_unavailable"})
	}
	pathValue := httpx.Query(r, "path", "")
	if pathValue == "" {
		return httpx.JSON(w, 400, map[string]any{"error": "path_required"})
	}
	resolvedPath, err := middleware.ResolvePath(r, pathValue)
	if err != nil {
		return err
	}
	session := middleware.Session(r)
	record, versions, err := h.FileSystem.ListHistory(r.Context(), session.UserID, resolvedPath)
	if errors.Is(err, gorm.ErrRecordNotFound) || errors.Is(err, dofs.ErrNotFound) {
		return httpx.JSON(w, 404, map[string]any{"error": "not_found"})
	}
	if errors.Is(err, dofs.ErrTypeMismatch) {
		return httpx.JSON(w, 400, map[string]any{"error": "not_file"})
	}
	if err != nil {
		return httpx.JSON(w, 500, map[string]any{"error": "history_failed"})
	}
	return httpx.JSON(w, http.StatusOK, map[string]any{
		"file": map[string]any{
			"inode":      record.ID,
			"name":       record.Name,
			"path":       pathValue,
			"size":       record.Size,
			"generation": record.Generation,
		},
		"versions": versions,
	})
}

func historyGeneration(r *http.Request) (int64, error) {
	raw := strings.TrimSpace(httpx.Query(r, "generation", ""))
	if raw == "" {
		return 0, errors.New("generation required")
	}
	number, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || number <= 0 {
		return 0, errors.New("invalid generation")
	}
	return number, nil
}

func (h *Handler) handleFileHistoryAccess(w http.ResponseWriter, r *http.Request) error {
	if h.FileSystem == nil {
		return httpx.JSON(w, 503, map[string]any{"error": "dofs_unavailable"})
	}
	pathValue := httpx.Query(r, "path", "")
	if pathValue == "" {
		return httpx.JSON(w, 400, map[string]any{"error": "path_required"})
	}
	generation, err := historyGeneration(r)
	if err != nil {
		return httpx.JSON(w, 400, map[string]any{"error": err.Error()})
	}
	resolvedPath, err := middleware.ResolvePath(r, pathValue)
	if err != nil {
		return err
	}
	session := middleware.Session(r)
	record, err := h.FileSystem.GenerationAccess(r.Context(), session.UserID, resolvedPath, generation)
	if errors.Is(err, gorm.ErrRecordNotFound) || errors.Is(err, dofs.ErrNotFound) {
		return httpx.JSON(w, 404, map[string]any{"error": "not_found"})
	}
	if errors.Is(err, dofs.ErrTypeMismatch) {
		return httpx.JSON(w, 400, map[string]any{"error": "not_file"})
	}
	if err != nil {
		return httpx.JSON(w, 500, map[string]any{"error": "history_access_failed"})
	}
	return h.sendFileAccessResponse(w, r, session, &record, filepath.Base(resolvedPath), pathValue+"#"+strconv.FormatInt(generation, 10))
}

func (h *Handler) handleFileHistoryRestore(w http.ResponseWriter, r *http.Request) error {
	if h.FileSystem == nil {
		return httpx.JSON(w, 503, map[string]any{"error": "dofs_unavailable"})
	}
	var body struct {
		Path               string `json:"path"`
		Generation         int64  `json:"generation"`
		ExpectedGeneration int64  `json:"expected_generation"`
	}
	if err := httpx.DecodeJSON(r, &body); err != nil || body.Path == "" || body.Generation <= 0 || body.ExpectedGeneration <= 0 {
		return httpx.JSON(w, 400, map[string]any{"error": "invalid_request"})
	}
	resolvedPath, err := middleware.ResolvePath(r, body.Path)
	if err != nil {
		return err
	}
	session := middleware.Session(r)
	restored, err := h.FileSystem.RestoreGeneration(r.Context(), session.UserID, resolvedPath, body.Generation, body.ExpectedGeneration)
	if errors.Is(err, dofs.ErrConflict) {
		return httpx.JSON(w, 409, map[string]any{"error": "generation_conflict"})
	}
	if errors.Is(err, dofs.ErrQuotaExceeded) {
		return httpx.JSON(w, 507, map[string]any{"error": "quota_exceeded"})
	}
	if errors.Is(err, gorm.ErrRecordNotFound) || errors.Is(err, dofs.ErrNotFound) {
		return httpx.JSON(w, 404, map[string]any{"error": "not_found"})
	}
	if errors.Is(err, dofs.ErrTypeMismatch) {
		return httpx.JSON(w, 400, map[string]any{"error": "not_file"})
	}
	if err != nil {
		return httpx.JSON(w, 500, map[string]any{"error": "history_restore_failed"})
	}
	h.audit(r, "file_history_restore", body.Path, strconv.FormatInt(body.Generation, 10), "success", 0)
	return httpx.JSON(w, http.StatusOK, map[string]any{
		"ok":         true,
		"generation": restored.Generation,
		"size":       restored.Size,
	})
}

func (h *Handler) sendFileAccessResponse(w http.ResponseWriter, r *http.Request, session *model.Session, fileRecord *model.FileRecord, fileName, auditResource string) error {
	if fileRecord == nil || fileRecord.Status != "ready" || fileRecord.IsDir {
		return httpx.JSON(w, http.StatusConflict, map[string]any{"error": "file_not_ready"})
	}
	presignedURL, err := h.Store.GeneratePresignedURL(fileRecord.StorageKey(), 4*time.Hour)
	if err != nil {
		return httpx.JSON(w, 500, map[string]any{"error": "presign_failed"})
	}

	// Unwrap DEK server-side so KEK never leaves the server
	kek, err := h.getFileEncryptionKey(session)
	if err != nil {
		return httpx.JSON(w, 500, map[string]any{"error": "internal_error"})
	}
	wrappedBytes, err := hex.DecodeString(fileRecord.WrappedDEK)
	if err != nil {
		return httpx.JSON(w, 500, map[string]any{"error": "internal_error"})
	}
	dek, err := auth.UnwrapDEK(kek, wrappedBytes)
	if err != nil {
		return httpx.JSON(w, 500, map[string]any{"error": "unwrap_failed"})
	}
	defer dofs.Clear(dek)

	ct := fileRecord.ContentType
	if ct == "" {
		ct = mime.TypeByExtension(filepath.Ext(fileName))
	}

	h.audit(r, "file_access", auditResource, "", "success", 0)
	return httpx.JSON(w, http.StatusOK, map[string]any{
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
