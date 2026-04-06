package handler

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"io"
	"mime"
	"os"
	"path"
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
	r.Handle("auth.config", h.wsAuthConfig)
	r.Handle("auth.logout", h.wsLogout)

	// --- User ---
	r.Handle("user.me", h.wsMe)
	r.Handle("user.security", h.wsSecurityStatus)
	r.Handle("user.changePassword", h.wsChangePassword)
	r.Handle("user.bindEmail", h.wsBindEmail)
	r.Handle("user.verifyBindEmail", h.wsVerifyBindEmail)
	r.Handle("user.unbindEmail", h.wsUnbindEmail)
	r.Handle("user.otpSetup", h.wsOTPSetup)
	r.Handle("user.otpEnable", h.wsOTPEnable)
	r.Handle("user.otpDisable", h.wsOTPDisable)
	r.Handle("user.updateDisplayName", h.wsUpdateDisplayName)
	r.Handle("user.storageUsage", h.wsStorageUsage)

	// --- Files ---
	r.Handle("file.list", h.wsFileList)
	r.Handle("file.mkdir", h.wsFileMkdir)
	r.Handle("file.rename", h.wsFileRename)
	r.Handle("file.copy", h.wsFileCopy)
	r.Handle("file.move", h.wsFileMove)
	r.Handle("file.delete", h.wsFileDelete)
	r.Handle("file.patchContent", h.wsFilePatchContent)
	r.Handle("file.search", h.wsFileSearch)

	// --- Shares ---
	r.Handle("share.list", h.wsShareList)
	r.Handle("share.patchContent", h.wsSharePatchContent)

	// --- Upload progress ---
	r.Handle("upload.progress", h.wsUploadProgress)

	// --- Trash ---
	r.Handle("trash.list", h.wsTrashList)
	r.Handle("trash.restore", h.wsTrashRestore)
	r.Handle("trash.delete", h.wsTrashDelete)
	r.Handle("trash.clear", h.wsTrashClear)

	// --- Tasks ---
	r.Handle("task.list", h.wsTaskList)
	r.Handle("task.create", h.wsTaskCreate)
	r.Handle("task.cancel", h.wsTaskCancel)
	r.Handle("task.clearDone", h.wsTaskClearDone)

	// --- Audit ---
	r.Handle("audit.preview", h.wsAuditPreview)
	r.HandleRoot("audit.logs", h.wsAuditLogs)

	// --- Admin ---
	r.HandleRoot("admin.listUsers", h.wsAdminListUsers)
	r.HandleRoot("admin.createUser", h.wsAdminCreateUser)
	r.HandleRoot("admin.updateUser", h.wsAdminUpdateUser)
	r.HandleRoot("admin.deleteUser", h.wsAdminDeleteUser)
	r.HandleRoot("admin.resetUserOTP", h.wsAdminResetUserOTP)
	r.HandleRoot("admin.resetUserEmail", h.wsAdminResetUserEmail)

	// --- Terminal Session ---
	r.Handle("session.open", h.wsSessionOpen)
	r.Handle("session.input", h.wsSessionInput)
	r.Handle("session.resize", h.wsSessionResize)
	r.Handle("session.close", h.wsSessionClose)
	r.Handle("session.complete", h.wsSessionComplete)

	// --- Workspace sync ---
	r.Handle("workspace.event", h.wsWorkspaceEvent)
	r.Handle("workspace.save", h.wsWorkspaceSave)
	r.Handle("workspace.load", h.wsWorkspaceLoad)
	r.Handle("workspace.clear", h.wsWorkspaceClear)

	// --- Subscriptions ---
	r.Handle("subscribe.directory", h.wsSubscribeDirectory)
	r.Handle("unsubscribe.directory", h.wsUnsubscribeDirectory)
}

// --- Shared path resolution (no *fiber.Ctx dependency) ---

func resolvePath(username, p string) (string, error) {
	if len(p) == 0 || p[0] != '/' {
		p = "/" + p
	}

	// Preserve trailing slash (directory marker) since path.Clean strips it
	trailingSlash := strings.HasSuffix(p, "/") && p != "/"

	// Clean the path (resolves /../, /./ , double slashes)
	cleaned := path.Clean(p)

	// After cleaning, reject if still contains ..
	if strings.Contains(cleaned, "..") {
		return "", errInvalidPath
	}

	if !strings.HasPrefix(cleaned, "/") {
		return "", errInvalidPath
	}

	if trailingSlash {
		cleaned += "/"
	}

	return username + cleaned, nil
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
	avatarKey := user.Username + "/.user/avatar.webp"
	var avatarURL string
	if _, err := h.Store.GetObjectInfo(avatarKey); err == nil {
		avatarURL, _ = h.Store.GeneratePresignedURL(avatarKey, 24*time.Hour)
	}
	return map[string]any{
		"id":           user.ID,
		"username":     user.Username,
		"display_name": user.DisplayName,
		"role":         user.Role,
		"email":        user.Email,
		"totp_enabled": user.TOTPEnabled,
		"avatar_url":   avatarURL,
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

func (h *Handler) wsUpdateDisplayName(conn *ws.Conn, _ string, data json.RawMessage) (any, error) {
	var p struct {
		DisplayName string `json:"display_name"`
	}
	if err := json.Unmarshal(data, &p); err != nil {
		return nil, &wsError{Code: "invalid_params"}
	}
	if err := model.UpdateUserDisplayName(conn.Session.UserID, strings.TrimSpace(p.DisplayName)); err != nil {
		return nil, &wsError{Code: "update_failed"}
	}
	return map[string]any{"ok": true}, nil
}

func (h *Handler) wsStorageUsage(conn *ws.Conn, _ string, _ json.RawMessage) (any, error) {
	user, err := model.GetUserByID(conn.Session.UserID)
	if err != nil {
		return nil, &wsError{Code: "user_not_found"}
	}
	size, count, err := h.Store.GetTotalSize(user.Username + "/")
	if err != nil {
		return nil, &wsError{Code: "internal_error"}
	}
	return map[string]any{
		"size":  size,
		"count": count,
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

	records, err := model.ListDirectChildren(conn.Session.UserID, resolvedPath)
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

	kek, _ := h.getFileEncryptionKey(conn.Session)

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
		h.fillThumbnail(&fi, &r, kek)
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

	if !strings.HasSuffix(resolvedPath, "/") {
		resolvedPath += "/"
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
		_ = model.MoveSharesByPrefix(conn.Session.UserID, oldResolved, newResolved)
	} else {
		newName := filepath.Base(newResolved)
		_ = model.MoveFile(conn.Session.UserID, oldResolved, newResolved, newName)
		_ = model.MoveSharesByPath(conn.Session.UserID, oldResolved, newResolved)
	}

	// Re-index search vector with new file name
	if !p.IsDir {
		newName := filepath.Base(newResolved)
		h.indexFileName(conn.Session.UserID, newResolved, newName)
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
	var srcWrappedDEK string
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
		srcWrappedDEK = srcRecord.WrappedDEK
	}

	srcName := filepath.Base(strings.TrimSuffix(p.SrcPath, "/"))
	taskID := uuid.New().String()
	userID := conn.Session.UserID
	_ = model.CreateTask(userID, taskID, "copy", srcName)

	go func() {
		progress := func(done, total int, current string) {
			h.updateTaskOp(userID, taskID, "copy", srcName, done, total, "copying")
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
			h.finishTaskOp(userID, taskID, "copy", srcName, "failed")
			return
		}
		if p.IsDir {
			h.cloneDirFiles(userID, srcResolved, dstResolved)
		} else {
			dstName := filepath.Base(dstResolved)
			ct := mime.TypeByExtension(filepath.Ext(dstResolved))
			var copyOpts []model.UpsertFileOpts
			if srcWrappedDEK != "" {
				copyOpts = append(copyOpts, model.UpsertFileOpts{WrappedDEK: srcWrappedDEK})
			}
			_ = model.UpsertFile(userID, dstResolved, dstName, false, srcSize, ct, srcContentHash, copyOpts...)
		}
		h.finishTaskOp(userID, taskID, "copy", srcName, "completed")
		h.notifyParentDir(conn.Session.Username, dstResolved)
	}()

	return map[string]any{"task_id": taskID, "op_id": taskID}, nil
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
	userID := conn.Session.UserID
	_ = model.CreateTask(userID, taskID, "move", srcName)

	go func() {
		progress := func(done, total int, current string) {
			h.updateTaskOp(userID, taskID, "move", srcName, done, total, "moving")
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
			h.finishTaskOp(userID, taskID, "move", srcName, "failed")
			return
		}
		if p.IsDir {
			_ = model.MoveFilesByPrefix(userID, srcResolved, dstResolved)
			_ = model.MoveSharesByPrefix(userID, srcResolved, dstResolved)
		} else {
			newName := filepath.Base(dstResolved)
			_ = model.MoveFile(userID, srcResolved, dstResolved, newName)
			_ = model.MoveSharesByPath(userID, srcResolved, dstResolved)
		}
		h.finishTaskOp(userID, taskID, "move", srcName, "completed")
		h.notifyParentDir(conn.Session.Username, srcResolved)
		h.notifyParentDir(conn.Session.Username, dstResolved)
	}()

	return map[string]any{"task_id": taskID, "op_id": taskID}, nil
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
				if job, err := model.FindActiveJobByParam("oss_upload", fileRecord.UploadID); err == nil {
					h.Dispatcher.Cancel(job.JobID)
				}
				tempDir := filepath.Join(config.TempDir, "upload", fileRecord.UploadID)
				_ = os.RemoveAll(tempDir)
			}
			_ = model.DeleteFile(conn.Session.UserID, resolvedPath)
			_ = model.DeleteSharesByPath(conn.Session.UserID, resolvedPath)
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
	userID := conn.Session.UserID
	_ = model.CreateTask(userID, taskID, "delete", deleteName)

	go func() {
		progress := func(done, total int, current string) {
			h.updateTaskOp(userID, taskID, "delete", deleteName, done, total, "deleting")
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
			h.finishTaskOp(userID, taskID, "delete", deleteName, "failed")
			return
		}
		_ = model.CreateTrashRecord(userID, p.Path, trashKey, totalSize, isDir)
		if isDir {
			_ = model.MoveFilesByPrefix(userID, resolvedPath, trashKey)
			_ = model.DeleteSharesByPrefix(userID, resolvedPath)
		} else {
			_ = model.MoveFile(userID, resolvedPath, trashKey, filepath.Base(trashKey))
			_ = model.DeleteSharesByPath(userID, resolvedPath)
		}
		h.finishTaskOp(userID, taskID, "delete", deleteName, "completed")
		h.Audit.Log(userID, conn.Session.Username, "", "file_delete", p.Path, "", "success", 0)
		h.notifyParentDir(conn.Session.Username, resolvedPath)
		h.notifyTrash(userID)
	}()

	return map[string]any{"task_id": taskID, "op_id": taskID}, nil
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

	fileRecord, err := model.GetFile(conn.Session.UserID, resolvedPath)
	if err != nil {
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

	kek, err := h.getFileEncryptionKey(conn.Session)
	if err != nil {
		return nil, &wsError{Code: "internal_error"}
	}
	wrappedDEKBytes, err := hex.DecodeString(fileRecord.WrappedDEK)
	if err != nil {
		return nil, &wsError{Code: "internal_error"}
	}
	dek, err := auth.UnwrapDEK(kek, wrappedDEKBytes)
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
		err := auth.DecryptStream(dek, reader, decW)
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
		pipelineErr = auth.EncryptStream(dek, editR, &encryptedBuf)
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

	// Re-index: for inline edits we only refresh the file name index
	// (full content re-index would require another decrypt pass, not worth it)
	h.indexFileName(conn.Session.UserID, resolvedPath, fileName)

	h.Audit.Log(conn.Session.UserID, conn.Session.Username, "", "file_write", p.Path, "", "success", 0)
	h.notifyParentDir(conn.Session.Username, resolvedPath)
	return map[string]any{"ok": true, "new_size": newSize}, nil
}

// --- Search ---

func (h *Handler) wsFileSearch(conn *ws.Conn, _ string, data json.RawMessage) (any, error) {
	var p struct {
		Query string `json:"query"`
		Limit int    `json:"limit"`
	}
	if err := json.Unmarshal(data, &p); err != nil || p.Query == "" {
		return nil, &wsError{Code: "invalid_request"}
	}
	results, err := model.SearchFiles(conn.Session.UserID, p.Query, p.Limit)
	if err != nil {
		return nil, &wsError{Code: "search_failed"}
	}
	items := make([]map[string]any, 0, len(results))
	for _, r := range results {
		// Convert OSS path back to app path (strip username prefix)
		appPath := toAppPath(r.Path, conn.Session.Username)
		parent := toAppPath(r.Parent, conn.Session.Username)
		items = append(items, map[string]any{
			"path":         appPath,
			"parent":       parent,
			"name":         r.Name,
			"is_dir":       r.IsDir,
			"size":         r.Size,
			"content_type": r.ContentType,
			"rank":         r.Rank,
		})
	}
	return map[string]any{"results": items}, nil
}

// --- Share actions ---

func (h *Handler) wsShareList(conn *ws.Conn, _ string, _ json.RawMessage) (any, error) {
	views, err := model.ListSharesAsFiles(conn.Session.UserID)
	if err != nil {
		return nil, &wsError{Code: "list_failed"}
	}
	if views == nil {
		views = []model.ShareFileView{}
	}
	return views, nil
}

func (h *Handler) wsSharePatchContent(conn *ws.Conn, _ string, data json.RawMessage) (any, error) {
	var p struct {
		ShareID  string `json:"share_id"`
		BaseSize int64  `json:"base_size"`
		Edits    []Edit `json:"edits"`
	}
	if err := json.Unmarshal(data, &p); err != nil {
		return nil, &wsError{Code: "invalid_request"}
	}
	if p.ShareID == "" {
		return nil, &wsError{Code: "share_id_required"}
	}
	if len(p.Edits) == 0 {
		return nil, &wsError{Code: "edits_required"}
	}

	share, err := model.GetShareByID(p.ShareID)
	if err != nil {
		return nil, &wsError{Code: "share_not_found"}
	}
	if conn.Session.UserID != share.TargetUserID {
		return nil, &wsError{Code: "forbidden"}
	}
	if share.Permission != "write" {
		return nil, &wsError{Code: "readonly_share"}
	}
	if share.ExpiresAt != nil && time.Now().After(*share.ExpiresAt) {
		return nil, &wsError{Code: "share_expired"}
	}

	// Load the file record from the owner's files
	ownerUser, err := model.GetUserByID(share.OwnerID)
	if err != nil {
		return nil, &wsError{Code: "internal_error"}
	}
	fileRecord, err := model.GetFile(share.OwnerID, share.FilePath)
	if err != nil {
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

	// Use owner's KEK to decrypt/encrypt the file
	ownerKEK, err := h.loadUserKEK(share.OwnerID)
	if err != nil {
		return nil, &wsError{Code: "internal_error"}
	}
	wrappedDEKBytes, err := hex.DecodeString(fileRecord.WrappedDEK)
	if err != nil {
		return nil, &wsError{Code: "internal_error"}
	}
	dek, err := auth.UnwrapDEK(ownerKEK, wrappedDEKBytes)
	if err != nil {
		return nil, &wsError{Code: "internal_error"}
	}

	reader, err := h.Store.GetObjectContent(share.FilePath)
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
		err := auth.DecryptStream(dek, reader, decW)
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
		pipelineErr = auth.EncryptStream(dek, editR, &encryptedBuf)
	}()

	<-done1
	<-done2
	<-done3

	if pipelineErr != nil {
		return nil, &wsError{Code: "pipeline_failed"}
	}

	if err := h.Store.PutObjectBytes(share.FilePath, encryptedBuf.Bytes()); err != nil {
		return nil, &wsError{Code: "save_file_failed"}
	}

	fileName := filepath.Base(share.FilePath)
	ct := mime.TypeByExtension(filepath.Ext(share.FilePath))
	_ = model.UpsertFile(share.OwnerID, share.FilePath, fileName, false, newSize, ct, "")

	// Update share record with new file size
	share.FileSize = newSize
	_ = model.UpdateShareFileSize(share.ShareID, newSize)

	h.Audit.Log(conn.Session.UserID, conn.Session.Username, "", "file_write", share.FilePath, "shared", "success", 0)

	// Notify the owner that their directory changed
	h.notifyParentDir(ownerUser.Username, share.FilePath)

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
		_ = model.MoveFilesByPrefix(conn.Session.UserID, item.TrashKey, originalResolved)
	} else {
		if err := h.Store.MoveObject(item.TrashKey, originalResolved); err != nil {
			return nil, &wsError{Code: "restore_failed"}
		}
		_ = model.MoveFile(conn.Session.UserID, item.TrashKey, originalResolved, filepath.Base(originalResolved))
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
		_ = model.DeleteFilesByPrefix(conn.Session.UserID, item.TrashKey)
	} else {
		if err := h.Store.DeleteObject(item.TrashKey); err != nil {
			return nil, &wsError{Code: "delete_from_storage_failed"}
		}
		_ = model.DeleteFile(conn.Session.UserID, item.TrashKey)
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
	userID := conn.Session.UserID
	_ = model.CreateTask(userID, taskID, "clear_trash", "")

	go func() {
		total := len(items)
		for i, item := range items {
			var clearErr error
			if item.IsDir {
				clearErr = h.Store.RecursiveDelete(item.TrashKey, nil)
				if clearErr == nil {
					_ = model.DeleteFilesByPrefix(userID, item.TrashKey)
				}
			} else {
				clearErr = h.Store.DeleteObject(item.TrashKey)
				if clearErr == nil {
					_ = model.DeleteFile(userID, item.TrashKey)
				}
			}
			if clearErr != nil {
				h.finishTaskOp(userID, taskID, "clear_trash", "", "failed")
				return
			}
			_ = model.DeleteTrashRecord(item.ID)
			h.updateTaskOp(userID, taskID, "clear_trash", "", i+1, total, "clearing")
		}
		h.finishTaskOp(userID, taskID, "clear_trash", "", "completed")
		h.notifyTrash(userID)
	}()

	return map[string]any{"task_id": taskID, "op_id": taskID}, nil
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
		Username string `json:"username"`
		Password string `json:"password"`
		Role     string `json:"role"`
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
	if !isValidUserRole(p.Role) {
		return nil, &wsError{Code: "invalid_role"}
	}

	wrappedKEKHex, err := h.generateWrappedKEK()
	if err != nil {
		return nil, &wsError{Code: "key_generation_failed"}
	}

	user, err := model.CreateUser(p.Username, p.Password, p.Role, wrappedKEKHex)
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
		ID       string `json:"id"`
		Role     string `json:"role"`
		Password string `json:"password"`
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
		if !isValidUserRole(p.Role) {
			return nil, &wsError{Code: "invalid_role"}
		}
		if code, guardErr := ensureNotDemotingLastRoot(user, p.Role); guardErr != nil {
			return nil, &wsError{Code: "update_user_failed"}
		} else if code != "" {
			return nil, &wsError{Code: code}
		}
		role = p.Role
	}

	if err := model.UpdateUser(user.ID, role); err != nil {
		return nil, &wsError{Code: "update_user_failed"}
	}

	if p.Role != "" && p.Role != user.Role {
		h.revokeUserSessions(user.ID)
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
	if code, guardErr := ensureNotDeletingLastRoot(user); guardErr != nil {
		return nil, &wsError{Code: "delete_user_failed"}
	} else if code != "" {
		return nil, &wsError{Code: code}
	}

	if err := h.deleteUserCompletely(user); err != nil {
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
	h.revokeUserSessions(p.ID)
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
	h.revokeUserSessions(p.ID)
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

// --- Workspace sync ---

// wsWorkspaceEvent relays a workspace event to other connections of the same user.
func (h *Handler) wsWorkspaceEvent(conn *ws.Conn, _ string, data json.RawMessage) (any, error) {
	var payload any
	if err := json.Unmarshal(data, &payload); err != nil {
		return nil, &wsError{Code: "invalid_request"}
	}
	h.Hub.PushWorkspaceEvent(conn.UserID, conn.ID, payload)
	return nil, nil
}

// wsWorkspaceSave persists the full workspace snapshot.
func (h *Handler) wsWorkspaceSave(conn *ws.Conn, _ string, data json.RawMessage) (any, error) {
	var p struct {
		State json.RawMessage `json:"state"`
	}
	if err := json.Unmarshal(data, &p); err != nil {
		return nil, &wsError{Code: "invalid_request"}
	}
	if err := model.SaveWorkspaceState(conn.UserID, string(p.State)); err != nil {
		return nil, &wsError{Code: "save_failed"}
	}
	return nil, nil
}

// wsWorkspaceLoad returns the saved workspace snapshot.
func (h *Handler) wsWorkspaceLoad(conn *ws.Conn, _ string, _ json.RawMessage) (any, error) {
	state, err := model.GetWorkspaceState(conn.UserID)
	if err != nil {
		// No saved state — return empty
		return map[string]any{"state": nil}, nil
	}
	// Return the raw JSON string so the client can parse it
	return map[string]any{"state": json.RawMessage(state)}, nil
}

// wsWorkspaceClear deletes the saved workspace snapshot.
func (h *Handler) wsWorkspaceClear(conn *ws.Conn, _ string, _ json.RawMessage) (any, error) {
	_ = model.DeleteWorkspaceState(conn.UserID)
	return nil, nil
}

// --- Helpers ---

// updateTaskOp updates a task's progress and pushes a WebSocket event.
func (h *Handler) updateTaskOp(userID, taskID, taskType, name string, done, total int, phase string) {
	var progress float64
	if total > 0 {
		progress = float64(done) / float64(total)
	}
	_ = model.UpdateTaskProgress(taskID, progress, phase)
	if h.Hub != nil {
		h.Hub.PushTaskUpdate(userID, taskID, taskType, name, "running", progress, phase)
	}
}

// finishTaskOp marks a task as completed/failed and pushes a WebSocket event.
func (h *Handler) finishTaskOp(userID, taskID, taskType, name, status string) {
	_ = model.UpdateTaskStatus(taskID, status)
	if h.Hub != nil {
		var progress float64
		if status == "completed" {
			progress = 1.0
		}
		h.Hub.PushTaskUpdate(userID, taskID, taskType, name, status, progress, "")
	}
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
