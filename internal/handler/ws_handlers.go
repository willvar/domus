package handler

import (
	"bytes"
	"encoding/json"
	"io"
	"mime"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"

	"zephyr/config"
	"zephyr/internal/auth"
	"zephyr/internal/model"
	"zephyr/internal/service"
	"zephyr/internal/store"
	"zephyr/internal/ws"
)

// registerWSActions registers all WebSocket action handlers on the Hub's router.
func (h *Handler) registerWSActions() {
	r := h.Hub.Router()

	// --- Auth ---
	r.Handle("auth.config", 0, h.wsAuthConfig)
	r.Handle("auth.logout", 0, h.wsLogout)

	// --- User ---
	r.Handle("user.me", 0, h.wsMe)
	r.Handle("user.security", 0, h.wsSecurityStatus)
	r.Handle("user.changePassword", 0, h.wsChangePassword)
	r.Handle("user.bindEmail", 0, h.wsBindEmail)
	r.Handle("user.verifyBindEmail", 0, h.wsVerifyBindEmail)
	r.Handle("user.unbindEmail", 0, h.wsUnbindEmail)
	r.Handle("user.otpSetup", 0, h.wsOTPSetup)
	r.Handle("user.otpEnable", 0, h.wsOTPEnable)
	r.Handle("user.otpDisable", 0, h.wsOTPDisable)

	// --- Bookmarks ---
	r.Handle("bookmark.list", 0, h.wsBookmarkList)
	r.Handle("bookmark.create", 0, h.wsBookmarkCreate)
	r.Handle("bookmark.update", 0, h.wsBookmarkUpdate)
	r.Handle("bookmark.delete", 0, h.wsBookmarkDelete)

	// --- Files ---
	r.Handle("file.list", ws.PermRead, h.wsFileList)
	r.Handle("file.mkdir", ws.PermUpload, h.wsFileMkdir)
	r.Handle("file.rename", ws.PermEdit, h.wsFileRename)
	r.Handle("file.copy", ws.PermEdit, h.wsFileCopy)
	r.Handle("file.move", ws.PermEdit, h.wsFileMove)
	r.Handle("file.delete", ws.PermDelete, h.wsFileDelete)
	r.Handle("file.patchContent", ws.PermEdit, h.wsFilePatchContent)

	// --- Upload progress ---
	r.Handle("upload.progress", ws.PermUpload, h.wsUploadProgress)

	// --- Trash ---
	r.Handle("trash.list", ws.PermRead, h.wsTrashList)
	r.Handle("trash.restore", ws.PermDelete, h.wsTrashRestore)
	r.Handle("trash.delete", ws.PermDelete, h.wsTrashDelete)
	r.Handle("trash.clear", ws.PermDelete, h.wsTrashClear)

	// --- Tasks ---
	r.Handle("task.list", 0, h.wsTaskList)
	r.Handle("task.create", ws.PermEdit, h.wsTaskCreate)
	r.Handle("task.cancel", 0, h.wsTaskCancel)
	r.Handle("task.clearDone", 0, h.wsTaskClearDone)

	// --- Audit ---
	r.Handle("audit.preview", 0, h.wsAuditPreview)
	r.HandleRoot("audit.logs", h.wsAuditLogs)

	// --- Admin ---
	r.HandleRoot("admin.listUsers", h.wsAdminListUsers)
	r.HandleRoot("admin.createUser", h.wsAdminCreateUser)
	r.HandleRoot("admin.updateUser", h.wsAdminUpdateUser)
	r.HandleRoot("admin.deleteUser", h.wsAdminDeleteUser)
	r.HandleRoot("admin.resetUserOTP", h.wsAdminResetUserOTP)
	r.HandleRoot("admin.resetUserEmail", h.wsAdminResetUserEmail)

	// --- Subscriptions ---
	r.Handle("subscribe.directory", 0, h.wsSubscribeDirectory)
	r.Handle("unsubscribe.directory", 0, h.wsUnsubscribeDirectory)
}

// --- Shared path resolution (no *fiber.Ctx dependency) ---

func resolvePath(username, path string) (string, error) {
	if containsDotDot(path) {
		return "", errInvalidPath
	}
	if len(path) == 0 || path[0] != '/' {
		path = "/" + path
	}
	return username + path, nil
}

func containsDotDot(s string) bool {
	for i := 0; i < len(s)-1; i++ {
		if s[i] == '.' && s[i+1] == '.' {
			return true
		}
	}
	return false
}

func toAppPath(ossPath, username string) string {
	prefix := username + "/"
	if len(ossPath) > len(prefix) && ossPath[:len(prefix)] == prefix {
		return "/" + ossPath[len(prefix):]
	}
	return "/" + ossPath
}

var errInvalidPath = &wsError{Code: "invalid_path"}

type wsError struct {
	Code string
}

func (e *wsError) Error() string { return e.Code }

// --- Auth actions ---

func (h *Handler) wsAuthConfig(_ *ws.Conn, _ string, _ json.RawMessage) (any, error) {
	return map[string]any{
		"smtp_enabled": h.Config.SMTP.Host != "",
	}, nil
}

func (h *Handler) wsLogout(conn *ws.Conn, _ string, _ json.RawMessage) (any, error) {
	h.Audit.Log(conn.Session.UserID, conn.Session.Username, "", "logout", "", "", "success", 0)
	h.Sessions.Delete(conn.SessionID)
	conn.Close()
	return map[string]any{"ok": true}, nil
}

// --- User actions ---

func (h *Handler) wsMe(conn *ws.Conn, _ string, _ json.RawMessage) (any, error) {
	user, err := model.GetUserByID(conn.Session.UserID)
	if err != nil {
		return nil, &wsError{Code: "user_not_found"}
	}
	return map[string]any{
		"id":           user.ID,
		"username":     user.Username,
		"role":         user.Role,
		"permissions":  user.Permissions,
		"email":        user.Email,
		"totp_enabled": user.TOTPEnabled,
	}, nil
}

func (h *Handler) wsSecurityStatus(conn *ws.Conn, _ string, _ json.RawMessage) (any, error) {
	user, err := model.GetUserByID(conn.Session.UserID)
	if err != nil {
		return nil, &wsError{Code: "internal_error"}
	}
	return map[string]any{
		"email":        user.Email,
		"has_email":    user.Email != "",
		"totp_enabled": user.TOTPEnabled,
		"smtp_enabled": h.Config.SMTP.Host != "",
	}, nil
}

func (h *Handler) wsChangePassword(conn *ws.Conn, _ string, data json.RawMessage) (any, error) {
	var p struct {
		OldPassword string `json:"old_password"`
		NewPassword string `json:"new_password"`
	}
	if err := json.Unmarshal(data, &p); err != nil {
		return nil, &wsError{Code: "invalid_request"}
	}
	if p.NewPassword == "" {
		return nil, &wsError{Code: "new_password_required"}
	}

	user, err := model.GetUserByID(conn.Session.UserID)
	if err != nil {
		return nil, &wsError{Code: "internal_error"}
	}
	if !model.CheckPassword(user.PasswordHash, p.OldPassword) {
		return nil, &wsError{Code: "wrong_password"}
	}
	if err := model.UpdateUserPassword(user.ID, p.NewPassword); err != nil {
		return nil, &wsError{Code: "update_password_failed"}
	}
	h.Sessions.DeleteByUserIDExcept(user.ID, conn.SessionID)
	return map[string]any{"ok": true}, nil
}

func (h *Handler) wsBindEmail(conn *ws.Conn, _ string, data json.RawMessage) (any, error) {
	if h.Config.SMTP.Host == "" {
		return nil, &wsError{Code: "email_not_configured"}
	}
	var p struct {
		Email string `json:"email"`
	}
	if err := json.Unmarshal(data, &p); err != nil || !isValidEmail(p.Email) {
		return nil, &wsError{Code: "invalid_email"}
	}
	code := service.GenerateEmailCode()
	h.Challenges.StoreEmailBindCode(conn.Session.UserID, p.Email, code)
	go func() { _ = service.SendVerificationEmail(h.Config.SMTP, p.Email, code) }()
	return map[string]any{"ok": true}, nil
}

func (h *Handler) wsVerifyBindEmail(conn *ws.Conn, _ string, data json.RawMessage) (any, error) {
	var p struct {
		Email string `json:"email"`
		Code  string `json:"code"`
	}
	if err := json.Unmarshal(data, &p); err != nil {
		return nil, &wsError{Code: "invalid_request"}
	}
	if !h.Challenges.VerifyEmailBindCode(conn.Session.UserID, p.Email, p.Code) {
		return nil, &wsError{Code: "invalid_code"}
	}
	if err := model.UpdateUserEmail(conn.Session.UserID, p.Email); err != nil {
		return nil, &wsError{Code: "update_email_failed"}
	}
	return map[string]any{"ok": true}, nil
}

func (h *Handler) wsUnbindEmail(conn *ws.Conn, _ string, _ json.RawMessage) (any, error) {
	if err := model.UpdateUserEmail(conn.Session.UserID, ""); err != nil {
		return nil, &wsError{Code: "unbind_email_failed"}
	}
	return map[string]any{"ok": true}, nil
}

func (h *Handler) wsOTPSetup(conn *ws.Conn, _ string, _ json.RawMessage) (any, error) {
	user, err := model.GetUserByID(conn.Session.UserID)
	if err != nil {
		return nil, &wsError{Code: "internal_error"}
	}
	if user.TOTPEnabled {
		return nil, &wsError{Code: "otp_already_enabled"}
	}
	secret, err := auth.GenerateTOTPSecret()
	if err != nil {
		return nil, &wsError{Code: "internal_error"}
	}
	h.Challenges.StorePendingTOTP(conn.Session.UserID, secret)
	return map[string]any{
		"secret": secret,
		"uri":    auth.GenerateTOTPURI(secret, user.Username),
	}, nil
}

func (h *Handler) wsOTPEnable(conn *ws.Conn, _ string, data json.RawMessage) (any, error) {
	var p struct {
		Code string `json:"code"`
	}
	if err := json.Unmarshal(data, &p); err != nil {
		return nil, &wsError{Code: "invalid_request"}
	}
	secret := h.Challenges.GetPendingTOTP(conn.Session.UserID)
	if secret == "" {
		return nil, &wsError{Code: "no_pending_otp"}
	}
	if !auth.ValidateTOTP(secret, p.Code) {
		return nil, &wsError{Code: "invalid_code"}
	}
	if err := model.UpdateUserTOTP(conn.Session.UserID, secret, true); err != nil {
		return nil, &wsError{Code: "enable_otp_failed"}
	}
	h.Challenges.DeletePendingTOTP(conn.Session.UserID)
	return map[string]any{"ok": true}, nil
}

func (h *Handler) wsOTPDisable(conn *ws.Conn, _ string, _ json.RawMessage) (any, error) {
	if err := model.UpdateUserTOTP(conn.Session.UserID, "", false); err != nil {
		return nil, &wsError{Code: "disable_otp_failed"}
	}
	return map[string]any{"ok": true}, nil
}

// --- Bookmark actions ---

func (h *Handler) wsBookmarkList(conn *ws.Conn, _ string, _ json.RawMessage) (any, error) {
	bookmarks, err := model.ListBookmarks(conn.Session.UserID)
	if err != nil {
		return nil, &wsError{Code: "list_bookmarks_failed"}
	}
	if bookmarks == nil {
		bookmarks = []model.Bookmark{}
	}
	return bookmarks, nil
}

func (h *Handler) wsBookmarkCreate(conn *ws.Conn, _ string, data json.RawMessage) (any, error) {
	var p struct {
		Name      string `json:"name"`
		Path      string `json:"path"`
		Icon      string `json:"icon"`
		SortOrder int    `json:"sort_order"`
	}
	if err := json.Unmarshal(data, &p); err != nil {
		return nil, &wsError{Code: "invalid_request"}
	}
	if p.Icon == "" {
		p.Icon = "folder"
	}
	bookmark, err := model.CreateBookmark(conn.Session.UserID, p.Name, p.Path, p.Icon, p.SortOrder)
	if err != nil {
		return nil, &wsError{Code: "create_bookmark_failed"}
	}
	return bookmark, nil
}

func (h *Handler) wsBookmarkUpdate(conn *ws.Conn, _ string, data json.RawMessage) (any, error) {
	var p struct {
		ID        int64  `json:"id"`
		Name      string `json:"name"`
		Path      string `json:"path"`
		Icon      string `json:"icon"`
		SortOrder int    `json:"sort_order"`
	}
	if err := json.Unmarshal(data, &p); err != nil {
		return nil, &wsError{Code: "invalid_request"}
	}
	if err := model.UpdateBookmark(p.ID, conn.Session.UserID, p.Name, p.Path, p.Icon, p.SortOrder); err != nil {
		return nil, &wsError{Code: "update_bookmark_failed"}
	}
	return map[string]any{"ok": true}, nil
}

func (h *Handler) wsBookmarkDelete(conn *ws.Conn, _ string, data json.RawMessage) (any, error) {
	var p struct {
		ID int64 `json:"id"`
	}
	if err := json.Unmarshal(data, &p); err != nil {
		return nil, &wsError{Code: "invalid_request"}
	}
	if err := model.DeleteBookmark(p.ID, conn.Session.UserID); err != nil {
		return nil, &wsError{Code: "delete_bookmark_failed"}
	}
	return map[string]any{"ok": true}, nil
}

// --- File actions ---

func (h *Handler) wsFileList(conn *ws.Conn, _ string, data json.RawMessage) (any, error) {
	var p struct {
		Path string `json:"path"`
	}
	if err := json.Unmarshal(data, &p); err != nil {
		return nil, &wsError{Code: "invalid_request"}
	}

	resolvedPath, err := resolvePath(conn.Session.Username, p.Path)
	if err != nil {
		return nil, err
	}

	records, err := model.ListDirectChildren(resolvedPath)
	if err != nil {
		return nil, &wsError{Code: "list_failed"}
	}

	// Build uploadID → task info map for processing files
	type taskInfo struct {
		TaskID   string
		Progress float64
		Phase    string
	}
	uploadTasks := make(map[string]taskInfo)
	if jobs, err := model.ListActiveUploadJobs(conn.Session.UserID); err == nil {
		for _, j := range jobs {
			if j.TaskID == "" {
				continue
			}
			var jp struct {
				UploadID string `json:"upload_id"`
			}
			if json.Unmarshal([]byte(j.Params), &jp) == nil && jp.UploadID != "" {
				if task, err := model.GetTask(j.TaskID); err == nil {
					uploadTasks[jp.UploadID] = taskInfo{TaskID: task.TaskID, Progress: task.Progress, Phase: task.Phase}
				}
			}
		}
	}

	files := make([]store.FileInfo, 0, len(records))
	for _, r := range records {
		fi := store.FileInfo{
			Name:          r.Name,
			Path:          toAppPath(r.Path, conn.Session.Username),
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
	return map[string]any{"files": files}, nil
}

func (h *Handler) wsFileMkdir(conn *ws.Conn, _ string, data json.RawMessage) (any, error) {
	var p struct {
		Path string `json:"path"`
	}
	if err := json.Unmarshal(data, &p); err != nil {
		return nil, &wsError{Code: "invalid_request"}
	}

	resolvedPath, err := resolvePath(conn.Session.Username, p.Path)
	if err != nil {
		return nil, err
	}

	if err := h.Store.CreateDirectory(resolvedPath); err != nil {
		return nil, &wsError{Code: "mkdir_failed"}
	}

	dirName := filepath.Base(strings.TrimSuffix(resolvedPath, "/"))
	_ = model.UpsertFile(conn.Session.UserID, resolvedPath, dirName, true, 0, "", "")

	h.notifyParentDir(conn.Session.Username, resolvedPath)
	return map[string]any{"ok": true}, nil
}

func (h *Handler) wsFileRename(conn *ws.Conn, _ string, data json.RawMessage) (any, error) {
	var p struct {
		OldPath string `json:"old_path"`
		NewPath string `json:"new_path"`
		IsDir   bool   `json:"is_dir"`
	}
	if err := json.Unmarshal(data, &p); err != nil {
		return nil, &wsError{Code: "invalid_request"}
	}

	oldResolved, err := resolvePath(conn.Session.Username, p.OldPath)
	if err != nil {
		return nil, err
	}

	if !p.IsDir {
		if rec, err := model.GetFile(conn.Session.UserID, oldResolved); err == nil && rec.Status != "ready" {
			return nil, &wsError{Code: "file_not_ready"}
		}
	}

	newResolved, err := resolvePath(conn.Session.Username, p.NewPath)
	if err != nil {
		return nil, err
	}

	if err := h.Store.RenameObject(oldResolved, newResolved, p.IsDir); err != nil {
		return nil, &wsError{Code: "rename_failed"}
	}

	if p.IsDir {
		_ = model.MoveFilesByPrefix(conn.Session.UserID, oldResolved, newResolved)
	} else {
		newName := filepath.Base(newResolved)
		_ = model.MoveFile(conn.Session.UserID, oldResolved, newResolved, newName)
	}

	h.Audit.Log(conn.Session.UserID, conn.Session.Username, "", "file_rename", p.OldPath, p.NewPath, "success", 0)
	h.notifyParentDir(conn.Session.Username, oldResolved)
	return map[string]any{"ok": true}, nil
}

func (h *Handler) wsFileCopy(conn *ws.Conn, _ string, data json.RawMessage) (any, error) {
	var p struct {
		SrcPath string `json:"src_path"`
		DstPath string `json:"dst_path"`
		IsDir   bool   `json:"is_dir"`
	}
	if err := json.Unmarshal(data, &p); err != nil {
		return nil, &wsError{Code: "invalid_request"}
	}

	srcResolved, err := resolvePath(conn.Session.Username, p.SrcPath)
	if err != nil {
		return nil, err
	}
	dstResolved, err := resolvePath(conn.Session.Username, p.DstPath)
	if err != nil {
		return nil, err
	}

	var srcSize int64
	var srcContentHash string
	if !p.IsDir {
		srcRecord, err := model.GetFile(conn.Session.UserID, srcResolved)
		if err != nil {
			return nil, &wsError{Code: "source_not_found"}
		}
		if srcRecord.Status != "ready" {
			return nil, &wsError{Code: "file_not_ready"}
		}
		srcSize = srcRecord.Size
		srcContentHash = srcRecord.ContentHash
	}

	srcName := filepath.Base(strings.TrimSuffix(p.SrcPath, "/"))
	taskID := uuid.New().String()
	_ = model.CreateTask(conn.Session.UserID, taskID, "copy", srcName)

	go func() {
		progress := func(done, total int, current string) {
			updateTaskOp(taskID, done, total, "copying")
		}
		var copyErr error
		if p.IsDir {
			copyErr = h.Store.RecursiveCopy(srcResolved, dstResolved, progress)
		} else {
			copyErr = h.Store.CopyObject(srcResolved, dstResolved)
			if copyErr == nil {
				progress(1, 1, srcResolved)
			}
		}
		if copyErr != nil {
			_ = model.UpdateTaskStatus(taskID, "failed")
			return
		}
		if p.IsDir {
			h.syncDirFiles(conn.Session.UserID, dstResolved)
		} else {
			dstName := filepath.Base(dstResolved)
			ct := mime.TypeByExtension(filepath.Ext(dstResolved))
			_ = model.UpsertFile(conn.Session.UserID, dstResolved, dstName, false, srcSize, ct, srcContentHash)
		}
		_ = model.UpdateTaskStatus(taskID, "completed")
		h.notifyParentDir(conn.Session.Username, dstResolved)
	}()

	return map[string]any{"task_id": taskID}, nil
}

func (h *Handler) wsFileMove(conn *ws.Conn, _ string, data json.RawMessage) (any, error) {
	var p struct {
		SrcPath string `json:"src_path"`
		DstPath string `json:"dst_path"`
		IsDir   bool   `json:"is_dir"`
	}
	if err := json.Unmarshal(data, &p); err != nil {
		return nil, &wsError{Code: "invalid_request"}
	}

	srcResolved, err := resolvePath(conn.Session.Username, p.SrcPath)
	if err != nil {
		return nil, err
	}
	if !p.IsDir {
		if rec, err := model.GetFile(conn.Session.UserID, srcResolved); err == nil && rec.Status != "ready" {
			return nil, &wsError{Code: "file_not_ready"}
		}
	}
	dstResolved, err := resolvePath(conn.Session.Username, p.DstPath)
	if err != nil {
		return nil, err
	}

	srcName := filepath.Base(strings.TrimSuffix(p.SrcPath, "/"))
	taskID := uuid.New().String()
	_ = model.CreateTask(conn.Session.UserID, taskID, "move", srcName)

	go func() {
		progress := func(done, total int, current string) {
			updateTaskOp(taskID, done, total, "moving")
		}
		var moveErr error
		if p.IsDir {
			moveErr = h.Store.RecursiveMove(srcResolved, dstResolved, progress)
		} else {
			moveErr = h.Store.MoveObject(srcResolved, dstResolved)
			if moveErr == nil {
				progress(1, 1, srcResolved)
			}
		}
		if moveErr != nil {
			_ = model.UpdateTaskStatus(taskID, "failed")
			return
		}
		if p.IsDir {
			_ = model.MoveFilesByPrefix(conn.Session.UserID, srcResolved, dstResolved)
		} else {
			newName := filepath.Base(dstResolved)
			_ = model.MoveFile(conn.Session.UserID, srcResolved, dstResolved, newName)
		}
		_ = model.UpdateTaskStatus(taskID, "completed")
		h.notifyParentDir(conn.Session.Username, srcResolved)
		h.notifyParentDir(conn.Session.Username, dstResolved)
	}()

	return map[string]any{"task_id": taskID}, nil
}

func (h *Handler) wsFileDelete(conn *ws.Conn, _ string, data json.RawMessage) (any, error) {
	var p struct {
		Path string `json:"path"`
	}
	if err := json.Unmarshal(data, &p); err != nil {
		return nil, &wsError{Code: "invalid_request"}
	}
	if p.Path == "" {
		return nil, &wsError{Code: "path_required"}
	}

	resolvedPath, err := resolvePath(conn.Session.Username, p.Path)
	if err != nil {
		return nil, err
	}

	isDir := strings.HasSuffix(p.Path, "/")

	// Non-ready files: cancel job, delete record, clean temp
	if !isDir {
		fileRecord, err := model.GetFile(conn.Session.UserID, resolvedPath)
		if err != nil {
			return nil, &wsError{Code: "not_found"}
		}
		if fileRecord.Status != "ready" {
			if fileRecord.UploadID != "" {
				if job, err := model.FindActiveJobByParam("upload", fileRecord.UploadID); err == nil {
					h.Dispatcher.Cancel(job.JobID)
				}
				tempDir := filepath.Join(config.TempDir, "upload", fileRecord.UploadID)
				_ = os.RemoveAll(tempDir)
			}
			_ = model.DeleteFile(conn.Session.UserID, resolvedPath)
			h.Audit.Log(conn.Session.UserID, conn.Session.Username, "", "file_delete", p.Path, "", "success", 0)
			h.notifyParentDir(conn.Session.Username, resolvedPath)
			return map[string]any{"ok": true}, nil
		}
	}

	var totalSize int64
	if isDir {
		size, err := model.SumFileSizeByPrefix(conn.Session.UserID, resolvedPath)
		if err != nil {
			return nil, &wsError{Code: "internal_error"}
		}
		totalSize = size
	} else {
		fileRecord, err := model.GetFile(conn.Session.UserID, resolvedPath)
		if err != nil {
			return nil, &wsError{Code: "not_found"}
		}
		totalSize = fileRecord.Size
	}

	trashUUID := uuid.New().String()
	trashKey := conn.Session.Username + "/.trash/" + trashUUID
	if isDir {
		if !strings.HasSuffix(resolvedPath, "/") {
			resolvedPath += "/"
		}
		trashKey += "/"
	}

	deleteName := filepath.Base(strings.TrimSuffix(p.Path, "/"))
	taskID := uuid.New().String()
	_ = model.CreateTask(conn.Session.UserID, taskID, "delete", deleteName)

	go func() {
		progress := func(done, total int, current string) {
			updateTaskOp(taskID, done, total, "deleting")
		}
		var moveErr error
		if isDir {
			moveErr = h.Store.RecursiveMove(resolvedPath, trashKey, progress)
		} else {
			moveErr = h.Store.MoveObject(resolvedPath, trashKey)
			if moveErr == nil {
				progress(1, 1, resolvedPath)
			}
		}
		if moveErr != nil {
			_ = model.UpdateTaskStatus(taskID, "failed")
			return
		}
		_ = model.CreateTrashRecord(conn.Session.UserID, p.Path, trashKey, totalSize, isDir)
		if isDir {
			_ = model.DeleteFilesByPrefix(conn.Session.UserID, resolvedPath)
		} else {
			_ = model.DeleteFile(conn.Session.UserID, resolvedPath)
		}
		_ = model.UpdateTaskStatus(taskID, "completed")
		h.Audit.Log(conn.Session.UserID, conn.Session.Username, "", "file_delete", p.Path, "", "success", 0)
		h.notifyParentDir(conn.Session.Username, resolvedPath)
		h.notifyTrash(conn.Session.UserID)
	}()

	return map[string]any{"task_id": taskID}, nil
}

func (h *Handler) wsFilePatchContent(conn *ws.Conn, _ string, data json.RawMessage) (any, error) {
	var p struct {
		Path     string `json:"path"`
		BaseSize int64  `json:"base_size"`
		Edits    []Edit `json:"edits"`
	}
	if err := json.Unmarshal(data, &p); err != nil {
		return nil, &wsError{Code: "invalid_request"}
	}
	if p.Path == "" {
		return nil, &wsError{Code: "path_required"}
	}
	if len(p.Edits) == 0 {
		return nil, &wsError{Code: "edits_required"}
	}

	resolvedPath, err := resolvePath(conn.Session.Username, p.Path)
	if err != nil {
		return nil, err
	}

	var fileRecord model.FileRecord
	if err := h.DB.Where("path = ?", resolvedPath).First(&fileRecord).Error; err != nil {
		return nil, &wsError{Code: "file_not_found"}
	}
	if p.BaseSize != fileRecord.Size {
		return nil, &wsError{Code: "base_size_mismatch"}
	}

	sort.Slice(p.Edits, func(i, j int) bool {
		return p.Edits[i].Offset < p.Edits[j].Offset
	})
	for i, edit := range p.Edits {
		if edit.Offset < 0 || edit.Delete < 0 {
			return nil, &wsError{Code: "invalid_edit"}
		}
		if edit.Offset+edit.Delete > p.BaseSize {
			return nil, &wsError{Code: "edit_out_of_bounds"}
		}
		if i > 0 {
			prev := p.Edits[i-1]
			if edit.Offset < prev.Offset+prev.Delete {
				return nil, &wsError{Code: "overlapping_edits"}
			}
		}
	}

	newSize := p.BaseSize
	for _, edit := range p.Edits {
		newSize += int64(len(edit.Insert)) - edit.Delete
	}
	if newSize < 0 {
		return nil, &wsError{Code: "invalid_result_size"}
	}

	encKey, err := h.getFileEncryptionKey(conn.Session)
	if err != nil {
		return nil, &wsError{Code: "internal_error"}
	}

	reader, err := h.Store.GetObjectContent(resolvedPath)
	if err != nil {
		return nil, &wsError{Code: "read_file_failed"}
	}

	decR, decW := io.Pipe()
	editR, editW := io.Pipe()
	var pipelineErr error
	var encryptedBuf bytes.Buffer

	done1 := make(chan struct{})
	go func() {
		defer close(done1)
		err := auth.DecryptStream(encKey, reader, decW)
		_ = reader.Close()
		_ = decW.CloseWithError(err)
	}()

	done2 := make(chan struct{})
	go func() {
		defer close(done2)
		err := applyEdits(decR, editW, p.Edits)
		_ = editW.CloseWithError(err)
	}()

	done3 := make(chan struct{})
	go func() {
		defer close(done3)
		pipelineErr = auth.EncryptStream(encKey, editR, &encryptedBuf)
	}()

	<-done1
	<-done2
	<-done3

	if pipelineErr != nil {
		return nil, &wsError{Code: "pipeline_failed"}
	}

	if err := h.Store.PutObjectBytes(resolvedPath, encryptedBuf.Bytes()); err != nil {
		return nil, &wsError{Code: "save_file_failed"}
	}

	fileName := filepath.Base(resolvedPath)
	ct := mime.TypeByExtension(filepath.Ext(resolvedPath))
	_ = model.UpsertFile(conn.Session.UserID, resolvedPath, fileName, false, newSize, ct, "")

	h.Audit.Log(conn.Session.UserID, conn.Session.Username, "", "file_write", p.Path, "", "success", 0)
	h.notifyParentDir(conn.Session.Username, resolvedPath)
	return map[string]any{"ok": true, "new_size": newSize}, nil
}

// --- Trash actions ---

func (h *Handler) wsTrashList(conn *ws.Conn, _ string, _ json.RawMessage) (any, error) {
	items, err := model.ListTrash(conn.Session.UserID)
	if err != nil {
		return nil, &wsError{Code: "list_trash_failed"}
	}
	if items == nil {
		items = []model.TrashItem{}
	}
	return items, nil
}

func (h *Handler) wsTrashRestore(conn *ws.Conn, _ string, data json.RawMessage) (any, error) {
	var p struct {
		ID int64 `json:"id"`
	}
	if err := json.Unmarshal(data, &p); err != nil {
		return nil, &wsError{Code: "invalid_request"}
	}

	item, err := model.GetTrashItem(p.ID, conn.Session.UserID)
	if err != nil {
		return nil, &wsError{Code: "trash_not_found"}
	}

	originalResolved, err := resolvePath(conn.Session.Username, item.OriginalPath)
	if err != nil {
		return nil, err
	}

	if item.IsDir {
		if err := h.Store.RecursiveMove(item.TrashKey, originalResolved, nil); err != nil {
			return nil, &wsError{Code: "restore_failed"}
		}
		h.syncDirFiles(conn.Session.UserID, originalResolved)
	} else {
		if err := h.Store.MoveObject(item.TrashKey, originalResolved); err != nil {
			return nil, &wsError{Code: "restore_failed"}
		}
		fileName := filepath.Base(originalResolved)
		ct := mime.TypeByExtension(filepath.Ext(originalResolved))
		_ = model.UpsertFile(conn.Session.UserID, originalResolved, fileName, false, item.Size, ct, "")
	}

	_ = model.DeleteTrashRecord(item.ID)
	h.notifyParentDir(conn.Session.Username, originalResolved)
	h.notifyTrash(conn.Session.UserID)
	return map[string]any{"ok": true}, nil
}

func (h *Handler) wsTrashDelete(conn *ws.Conn, _ string, data json.RawMessage) (any, error) {
	var p struct {
		ID int64 `json:"id"`
	}
	if err := json.Unmarshal(data, &p); err != nil {
		return nil, &wsError{Code: "invalid_request"}
	}

	item, err := model.GetTrashItem(p.ID, conn.Session.UserID)
	if err != nil {
		return nil, &wsError{Code: "trash_not_found"}
	}

	if item.IsDir {
		if err := h.Store.RecursiveDelete(item.TrashKey, nil); err != nil {
			return nil, &wsError{Code: "delete_from_storage_failed"}
		}
	} else {
		if err := h.Store.DeleteObject(item.TrashKey); err != nil {
			return nil, &wsError{Code: "delete_from_storage_failed"}
		}
	}

	if err := model.DeleteTrashRecord(item.ID); err != nil {
		return nil, &wsError{Code: "delete_record_failed"}
	}
	h.notifyTrash(conn.Session.UserID)
	return map[string]any{"ok": true}, nil
}

func (h *Handler) wsTrashClear(conn *ws.Conn, _ string, _ json.RawMessage) (any, error) {
	items, err := model.ListTrash(conn.Session.UserID)
	if err != nil {
		return nil, &wsError{Code: "clear_trash_failed"}
	}

	taskID := uuid.New().String()
	_ = model.CreateTask(conn.Session.UserID, taskID, "clear_trash", "")

	go func() {
		total := len(items)
		for i, item := range items {
			var clearErr error
			if item.IsDir {
				clearErr = h.Store.RecursiveDelete(item.TrashKey, nil)
			} else {
				clearErr = h.Store.DeleteObject(item.TrashKey)
			}
			if clearErr != nil {
				_ = model.UpdateTaskStatus(taskID, "failed")
				return
			}
			_ = model.DeleteTrashRecord(item.ID)
			updateTaskOp(taskID, i+1, total, "clearing")
		}
		_ = model.UpdateTaskStatus(taskID, "completed")
		h.notifyTrash(conn.Session.UserID)
	}()

	return map[string]any{"task_id": taskID}, nil
}

// --- Task actions ---

func (h *Handler) wsTaskList(conn *ws.Conn, _ string, _ json.RawMessage) (any, error) {
	tasks, err := model.ListRecentTasks(conn.Session.UserID)
	if err != nil {
		return nil, &wsError{Code: "list_tasks_failed"}
	}
	return tasks, nil
}

func (h *Handler) wsTaskCreate(conn *ws.Conn, _ string, data json.RawMessage) (any, error) {
	var peek struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(data, &peek); err != nil {
		return nil, &wsError{Code: "invalid_request"}
	}
	switch peek.Type {
	case "transcode":
		return h.wsTranscodeStart(conn, data)
	default:
		return nil, &wsError{Code: "unknown_task_type"}
	}
}

func (h *Handler) wsTaskCancel(conn *ws.Conn, _ string, data json.RawMessage) (any, error) {
	var p struct {
		TaskID string `json:"task_id"`
	}
	if err := json.Unmarshal(data, &p); err != nil {
		return nil, &wsError{Code: "invalid_request"}
	}

	task, err := model.GetTask(p.TaskID)
	if err != nil {
		return nil, &wsError{Code: "task_not_found"}
	}
	if task.UserID != conn.Session.UserID && conn.Session.Role != "root" {
		return nil, &wsError{Code: "access_denied"}
	}

	// Cancel linked jobs and clean up resources
	var jobs []model.Job
	h.DB.Where("task_id = ?", p.TaskID).Find(&jobs)
	for _, j := range jobs {
		if j.Status == "pending" || j.Status == "running" {
			h.Dispatcher.Cancel(j.JobID)
		}
		// For upload tasks: clean up placeholder file and temp dir
		if task.Type == "upload" {
			var params OSSUploadParams
			if json.Unmarshal([]byte(j.Params), &params) == nil && params.UploadID != "" {
				_ = model.DeleteFile(params.UserID, params.OSSKey)
				_ = os.RemoveAll(params.TempDir)
				h.notifyParentDir(conn.Session.Username, params.OSSKey)
			}
		}
	}

	_ = model.UpdateTaskStatus(p.TaskID, "cancelled")
	return map[string]any{"ok": true}, nil
}

func (h *Handler) wsTaskClearDone(conn *ws.Conn, _ string, _ json.RawMessage) (any, error) {
	if err := model.DeleteCompletedTasks(conn.Session.UserID); err != nil {
		return nil, &wsError{Code: "clear_tasks_failed"}
	}
	return map[string]any{"ok": true}, nil
}

func (h *Handler) wsTranscodeStart(conn *ws.Conn, data json.RawMessage) (any, error) {
	var p struct {
		Path         string `json:"path"`
		Preset       string `json:"preset"`
		OutputFormat string `json:"output_format"`
		Replace      bool   `json:"replace"`
	}
	if err := json.Unmarshal(data, &p); err != nil {
		return nil, &wsError{Code: "invalid_request"}
	}

	resolvedPath, err := resolvePath(conn.Session.Username, p.Path)
	if err != nil {
		return nil, err
	}

	fileRecord, err := model.GetFile(conn.Session.UserID, resolvedPath)
	if err != nil {
		return nil, &wsError{Code: "file_not_found"}
	}

	originalName := filepath.Base(resolvedPath)
	mediaType := service.DetectMediaType(originalName)
	if mediaType == "" {
		return nil, &wsError{Code: "unsupported_media_type"}
	}

	if p.Preset == "" {
		p.Preset = "medium"
	}
	if p.OutputFormat == "" {
		switch mediaType {
		case "video":
			p.OutputFormat = "mp4"
		case "audio":
			p.OutputFormat = "aac"
		case "image":
			p.OutputFormat = "webp"
		}
	}

	ext := service.OutputExtension(p.OutputFormat)
	nameWithoutExt := strings.TrimSuffix(originalName, filepath.Ext(originalName))
	outputName := nameWithoutExt + ext

	var targetKey string
	if p.Replace {
		targetKey = resolvedPath
	} else {
		dir := filepath.Dir(resolvedPath)
		targetKey = dir + "/" + outputName
	}

	jobID := uuid.New().String()
	tempDir := filepath.Join(config.TempDir, "transcode", jobID)

	params := TranscodeParams{
		SourceKey:    resolvedPath,
		TargetKey:    targetKey,
		MediaType:    mediaType,
		Preset:       p.Preset,
		OutputFormat: p.OutputFormat,
		Replace:      p.Replace,
		OriginalName: originalName,
		OutputName:   outputName,
		FileSize:     fileRecord.Size,
		TempDir:      tempDir,
	}
	paramsJSON, _ := json.Marshal(params)

	// Create user-facing task
	taskID := uuid.New().String()
	_ = model.CreateTask(conn.Session.UserID, taskID, "transcode", originalName)

	// Create dispatcher job linked to task
	_ = model.CreateJobDirect(&model.Job{
		UserID: conn.Session.UserID,
		JobID:  jobID,
		TaskID: taskID,
		Type:   "transcode",
		Status: "pending",
		Params: string(paramsJSON),
	})

	return map[string]any{"task_id": taskID}, nil
}

// --- Audit actions ---

func (h *Handler) wsAuditPreview(conn *ws.Conn, _ string, data json.RawMessage) (any, error) {
	var p struct {
		Path       string `json:"path"`
		DurationMs int64  `json:"duration_ms"`
		Type       string `json:"type"`
	}
	_ = json.Unmarshal(data, &p)
	if p.Path != "" {
		h.Audit.Log(conn.Session.UserID, conn.Session.Username, "", "file_preview", p.Path, p.Type, "success", p.DurationMs)
	}
	return map[string]any{"ok": true}, nil
}

func (h *Handler) wsAuditLogs(conn *ws.Conn, _ string, data json.RawMessage) (any, error) {
	var p struct {
		Page   int    `json:"page"`
		Size   int    `json:"size"`
		User   string `json:"user"`
		Action string `json:"action"`
		From   string `json:"from"`
		To     string `json:"to"`
	}
	if err := json.Unmarshal(data, &p); err != nil {
		return nil, &wsError{Code: "invalid_request"}
	}
	if p.Page < 1 {
		p.Page = 1
	}
	if p.Size < 1 || p.Size > 200 {
		p.Size = 50
	}

	q := h.DB.Model(&model.AuditLog{})
	if p.User != "" {
		q = q.Where("username = ?", p.User)
	}
	if p.Action != "" {
		q = q.Where("action = ?", p.Action)
	}
	if p.From != "" {
		if t, err := time.Parse(time.RFC3339, p.From); err == nil {
			q = q.Where("created_at >= ?", t)
		}
	}
	if p.To != "" {
		if t, err := time.Parse(time.RFC3339, p.To); err == nil {
			q = q.Where("created_at <= ?", t)
		}
	}

	var total int64
	q.Count(&total)

	var logs []model.AuditLog
	q.Order("id DESC").Offset((p.Page - 1) * p.Size).Limit(p.Size).Find(&logs)

	return map[string]any{
		"total": total,
		"page":  p.Page,
		"size":  p.Size,
		"items": logs,
	}, nil
}

// --- Admin actions ---

func (h *Handler) wsAdminListUsers(_ *ws.Conn, _ string, _ json.RawMessage) (any, error) {
	users, err := model.ListUsers()
	if err != nil {
		return nil, &wsError{Code: "list_users_failed"}
	}
	if users == nil {
		users = []model.User{}
	}
	return users, nil
}

func (h *Handler) wsAdminCreateUser(conn *ws.Conn, _ string, data json.RawMessage) (any, error) {
	var p struct {
		Username    string `json:"username"`
		Password    string `json:"password"`
		Role        string `json:"role"`
		Permissions *int64 `json:"permissions"`
	}
	if err := json.Unmarshal(data, &p); err != nil {
		return nil, &wsError{Code: "invalid_request"}
	}
	if p.Username == "" || p.Password == "" {
		return nil, &wsError{Code: "username_password_required"}
	}
	if p.Role == "" {
		p.Role = "user"
	}
	if p.Role != "root" && p.Role != "user" {
		return nil, &wsError{Code: "invalid_role"}
	}

	permissions := model.DefaultPermissions(p.Role)
	if p.Permissions != nil {
		permissions = *p.Permissions
	}

	user, err := model.CreateUser(p.Username, p.Password, p.Role, permissions)
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE") {
			return nil, &wsError{Code: "username_exists"}
		}
		return nil, &wsError{Code: "create_user_failed"}
	}

	_ = h.Store.CreateDirectory(user.Username + "/")
	_ = h.Store.CreateDirectory(user.Username + "/home/")
	_ = h.Store.CreateDirectory(user.Username + "/home/" + user.Username + "/")
	_ = model.UpsertFile(user.ID, user.Username+"/home/", "home", true, 0, "", "")
	_ = model.UpsertFile(user.ID, user.Username+"/home/"+user.Username+"/", user.Username, true, 0, "", "")

	h.Audit.Log(conn.Session.UserID, conn.Session.Username, "", "user_create", user.Username, p.Role, "success", 0)
	return user, nil
}

func (h *Handler) wsAdminUpdateUser(conn *ws.Conn, _ string, data json.RawMessage) (any, error) {
	var p struct {
		ID          string `json:"id"`
		Role        string `json:"role"`
		Password    string `json:"password"`
		Permissions *int64 `json:"permissions"`
	}
	if err := json.Unmarshal(data, &p); err != nil {
		return nil, &wsError{Code: "invalid_request"}
	}

	user, err := model.GetUserByID(p.ID)
	if err != nil {
		return nil, &wsError{Code: "user_not_found"}
	}

	if p.Password != "" {
		if err := model.UpdateUserPassword(user.ID, p.Password); err != nil {
			return nil, &wsError{Code: "update_password_failed"}
		}
	}

	role := user.Role
	if p.Role != "" {
		role = p.Role
	}
	permissions := user.Permissions
	if p.Role != "" && p.Role != user.Role && p.Permissions == nil {
		permissions = model.DefaultPermissions(role)
	}
	if p.Permissions != nil {
		permissions = *p.Permissions
	}

	if err := model.UpdateUser(user.ID, role, permissions); err != nil {
		return nil, &wsError{Code: "update_user_failed"}
	}

	if (p.Role != "" && p.Role != user.Role) || (p.Permissions != nil && *p.Permissions != user.Permissions) {
		h.Sessions.DeleteByUserID(user.ID)
		h.Hub.PushSessionExpired(user.ID)
	}

	h.Audit.Log(conn.Session.UserID, conn.Session.Username, "", "user_update", user.Username, "", "success", 0)
	return map[string]any{"ok": true}, nil
}

func (h *Handler) wsAdminDeleteUser(conn *ws.Conn, _ string, data json.RawMessage) (any, error) {
	var p struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(data, &p); err != nil {
		return nil, &wsError{Code: "invalid_request"}
	}
	if p.ID == conn.Session.UserID {
		return nil, &wsError{Code: "cannot_delete_self"}
	}

	user, err := model.GetUserByID(p.ID)
	if err != nil {
		return nil, &wsError{Code: "user_not_found"}
	}

	h.Sessions.DeleteByUserID(user.ID)
	h.Hub.DisconnectUser(user.ID)

	if err := model.DeleteUser(user.ID); err != nil {
		return nil, &wsError{Code: "delete_user_failed"}
	}

	h.Audit.Log(conn.Session.UserID, conn.Session.Username, "", "user_delete", user.Username, "", "success", 0)
	return map[string]any{"ok": true}, nil
}

func (h *Handler) wsAdminResetUserOTP(conn *ws.Conn, _ string, data json.RawMessage) (any, error) {
	var p struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(data, &p); err != nil {
		return nil, &wsError{Code: "invalid_request"}
	}
	if _, err := model.GetUserByID(p.ID); err != nil {
		return nil, &wsError{Code: "user_not_found"}
	}
	if err := model.UpdateUserTOTP(p.ID, "", false); err != nil {
		return nil, &wsError{Code: "update_failed"}
	}
	h.Sessions.DeleteByUserID(p.ID)
	h.Audit.Log(conn.Session.UserID, conn.Session.Username, "", "user_reset_otp", p.ID, "", "success", 0)
	return map[string]any{"ok": true}, nil
}

func (h *Handler) wsAdminResetUserEmail(conn *ws.Conn, _ string, data json.RawMessage) (any, error) {
	var p struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(data, &p); err != nil {
		return nil, &wsError{Code: "invalid_request"}
	}
	if _, err := model.GetUserByID(p.ID); err != nil {
		return nil, &wsError{Code: "user_not_found"}
	}
	if err := model.UpdateUserEmail(p.ID, ""); err != nil {
		return nil, &wsError{Code: "update_failed"}
	}
	h.Sessions.DeleteByUserID(p.ID)
	h.Audit.Log(conn.Session.UserID, conn.Session.Username, "", "user_reset_email", p.ID, "", "success", 0)
	return map[string]any{"ok": true}, nil
}

// --- Upload progress ---

func (h *Handler) wsUploadProgress(conn *ws.Conn, _ string, data json.RawMessage) (any, error) {
	var p struct {
		TaskID   string  `json:"task_id"`
		Progress float64 `json:"progress"`
	}
	if err := json.Unmarshal(data, &p); err != nil || p.TaskID == "" {
		return nil, &wsError{Code: "invalid_request"}
	}

	task, err := model.GetTask(p.TaskID)
	if err != nil {
		return nil, &wsError{Code: "task_not_found"}
	}
	if task.UserID != conn.Session.UserID {
		return nil, &wsError{Code: "access_denied"}
	}

	_ = model.UpdateTaskProgress(p.TaskID, p.Progress, "uploading")
	return map[string]any{"ok": true}, nil
}

// --- Subscription actions ---

func (h *Handler) wsSubscribeDirectory(conn *ws.Conn, _ string, data json.RawMessage) (any, error) {
	var p struct {
		Path string `json:"path"`
	}
	if err := json.Unmarshal(data, &p); err != nil {
		return nil, &wsError{Code: "invalid_request"}
	}

	resolvedPath, err := resolvePath(conn.Session.Username, p.Path)
	if err != nil {
		return nil, err
	}

	conn.Subscribe(resolvedPath)
	return map[string]any{"ok": true}, nil
}

func (h *Handler) wsUnsubscribeDirectory(conn *ws.Conn, _ string, data json.RawMessage) (any, error) {
	var p struct {
		Path string `json:"path"`
	}
	if err := json.Unmarshal(data, &p); err != nil {
		return nil, &wsError{Code: "invalid_request"}
	}

	resolvedPath, err := resolvePath(conn.Session.Username, p.Path)
	if err != nil {
		return nil, err
	}

	conn.Unsubscribe(resolvedPath)
	return map[string]any{"ok": true}, nil
}

// --- Helpers ---

// updateTaskOp updates a task's progress from a done/total count.
func updateTaskOp(taskID string, done, total int, phase string) {
	var progress float64
	if total > 0 {
		progress = float64(done) / float64(total)
	}
	_ = model.UpdateTaskProgress(taskID, progress, phase)
}

// notifyTrash notifies all connections of a user that the trash list changed.
// Uses SendToUser because trash is a virtual view, not a real directory subscription.
func (h *Handler) notifyTrash(userID string) {
	h.Hub.SendToUser(userID, map[string]any{
		"event": "dir.changed",
		"data": map[string]any{
			"path":        "__trash__/",
			"change_type": "refresh",
		},
	})
}

// notifyParentDir notifies subscribers of the parent directory that it changed.
func (h *Handler) notifyParentDir(username, resolvedPath string) {
	parent := parentDirOf(resolvedPath)
	if parent != "" {
		appPath := toAppPath(parent, username)
		h.Hub.PushDirChanged(parent, appPath, "refresh")
	}
}

// parentDirOf returns the parent directory of a resolved path.
func parentDirOf(path string) string {
	p := strings.TrimSuffix(path, "/")
	if idx := strings.LastIndex(p, "/"); idx >= 0 {
		return p[:idx+1]
	}
	return ""
}
