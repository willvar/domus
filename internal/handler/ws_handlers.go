package handler

import (
	"encoding/json"
	"path"
	"strings"

	"zephyr/internal/ws"
)

// registerWSActions registers only realtime/session WebSocket handlers.
// Query/command style APIs are intentionally served over HTTP.
func (h *Handler) registerWSActions() {
	r := h.Hub.Router()

	// --- Client-driven task reporting ---
	r.Handle("task.report", h.wsTaskReport)

	// --- Terminal Session ---
	r.Handle("session.open", h.wsSessionOpen)
	r.Handle("session.input", h.wsSessionInput)
	r.Handle("session.resize", h.wsSessionResize)
	r.Handle("session.close", h.wsSessionClose)
	r.Handle("session.complete", h.wsSessionComplete)

	// --- Workspace sync ---
	r.Handle("workspace.event", h.wsWorkspaceEvent)

	// --- Subscriptions ---
	r.Handle("subscribe.directory", h.wsSubscribeDirectory)
	r.Handle("unsubscribe.directory", h.wsUnsubscribeDirectory)
}

// --- Shared path resolution (no *fiber.Ctx dependency) ---

func resolvePath(username, p string) (string, error) {
	if len(p) == 0 || p[0] != '/' {
		p = "/" + p
	}

	// Preserve trailing slash (directory marker) since path.Clean strips it.
	trailingSlash := strings.HasSuffix(p, "/") && p != "/"

	cleaned := path.Clean(p)
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

// --- Client-driven task reporting ---

func (h *Handler) wsTaskReport(conn *ws.Conn, _ string, data json.RawMessage) (any, error) {
	var p struct {
		TaskID   string   `json:"task_id"`
		Progress *float64 `json:"progress"`
		Phase    *string  `json:"phase"`
		Status   *string  `json:"status"`
	}
	if err := json.Unmarshal(data, &p); err != nil || p.TaskID == "" {
		return nil, &wsError{Code: "invalid_request"}
	}

	task, err := h.Repos.Tasks.Get(p.TaskID)
	if err != nil {
		return nil, &wsError{Code: "task_not_found"}
	}
	if task.UserID != conn.Session.UserID {
		return nil, &wsError{Code: "access_denied"}
	}
	if task.Type != "upload" {
		return nil, &wsError{Code: "task_not_reportable"}
	}

	if p.Progress != nil || p.Phase != nil {
		progress := task.Progress
		if p.Progress != nil {
			progress = *p.Progress
		}
		phase := task.Phase
		if p.Phase != nil {
			phase = *p.Phase
		}
		_ = h.Repos.Tasks.UpdateProgress(p.TaskID, progress, phase)
	}
	if p.Status != nil && *p.Status != "" && *p.Status != "running" {
		_ = h.Repos.Tasks.UpdateStatus(p.TaskID, *p.Status)
	}
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

// --- Helpers ---

// updateTaskOp updates a task's progress and pushes a WebSocket event.
func (h *Handler) updateTaskOp(userID, taskID, taskType, name string, done, total int, phase string) {
	var progress float64
	if total > 0 {
		progress = float64(done) / float64(total)
	}
	_ = h.Repos.Tasks.UpdateProgress(taskID, progress, phase)
	if h.Hub != nil {
		h.Hub.PushTaskUpdate(userID, taskID, taskType, name, "running", "", progress, phase)
	}
}

// finishTaskOp marks a task as completed/failed and pushes a WebSocket event.
func (h *Handler) finishTaskOp(userID, taskID, taskType, name, status string) {
	_ = h.Repos.Tasks.UpdateStatus(taskID, status)
	if h.Hub != nil {
		var progress float64
		if status == "completed" {
			progress = 1.0
		}
		h.Hub.PushTaskUpdate(userID, taskID, taskType, name, status, "", progress, "")
	}
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
