package handler

import (
	"encoding/json"
	"strings"

	"domus/internal/middleware"
	"domus/internal/ws"
)

// registerWSActions registers only realtime/session WebSocket handlers.
// Query/command style APIs are intentionally served over HTTP.
func (h *Handler) registerWSActions() {
	r := h.Hub.Router()

	// --- Client-driven task reporting ---
	r.Handle("task.report", h.wsTaskReport)

	// --- Subscriptions ---
	r.Handle("subscribe.directory", h.wsSubscribeDirectory)
	r.Handle("unsubscribe.directory", h.wsUnsubscribeDirectory)
}

// --- Shared path resolution (no *fiber.Ctx dependency) ---

func resolvePath(p string) (string, error) {
	resolved, err := middleware.ResolveApplicationPath(p)
	if err != nil {
		return "", errInvalidPath
	}
	return resolved, nil
}

func toAppPath(ossPath, username string) string {
	return middleware.ToAppPath(ossPath, username)
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

	resolvedPath, err := resolvePath(p.Path)
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

	resolvedPath, err := resolvePath(p.Path)
	if err != nil {
		return nil, err
	}

	conn.Unsubscribe(resolvedPath)
	return map[string]any{"ok": true}, nil
}

// --- Helpers ---

// notifyParentDir notifies subscribers of the parent directory that it changed.
func (h *Handler) notifyParentDir(userID, resolvedPath string) {
	parent := parentDirOf(resolvedPath)
	if parent != "" {
		appPath := middleware.ToAppPath(parent, "")
		h.Hub.PushDirChanged(userID, parent, appPath, "refresh")
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
