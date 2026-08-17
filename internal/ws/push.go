package ws

import "encoding/base64"

// Push event helpers — all send to specific targets via the Hub.

// PushTaskUpdate sends a task progress/status update to all connections of a user.
func (h *Hub) PushTaskUpdate(userID, taskID, taskType, name, status, clientInstanceID string, progress float64, phase string) {
	h.SendToUser(userID, map[string]any{
		"event": "task.update",
		"data": map[string]any{
			"task_id":            taskID,
			"type":               taskType,
			"name":               name,
			"status":             status,
			"progress":           progress,
			"phase":              phase,
			"client_instance_id": clientInstanceID,
		},
	})
}

// PushDirChanged notifies all subscribers of a directory that it has changed.
// changeType: "created", "deleted", "modified", "refresh"
func (h *Hub) PushDirChanged(resolvedPath, appPath, changeType string) {
	h.NotifyDirectory(resolvedPath, map[string]any{
		"event": "dir.changed",
		"data": map[string]any{
			"path":        appPath,
			"change_type": changeType,
		},
	})
}

// PushSessionOutputBytes preserves arbitrary TTY bytes across the JSON event
// transport. Go strings are UTF-8-normalized by encoding/json, so terminal
// output uses an explicit base64 field and the browser writes a Uint8Array.
func (h *Hub) PushSessionOutputBytes(connID, sessionID string, data []byte) {
	h.SendToConn(connID, map[string]any{
		"event": "session.output",
		"data": map[string]any{
			"session_id":  sessionID,
			"data_base64": base64.StdEncoding.EncodeToString(data),
		},
	})
}

// PushSessionExit signals that a container TTY session has ended.
func (h *Hub) PushSessionExit(connID, sessionID, reason string) {
	h.SendToConn(connID, map[string]any{
		"event": "session.exit",
		"data": map[string]any{
			"session_id": sessionID,
			"reason":     reason,
		},
	})
}

// PushWorkspaceEvent relays a workspace event to all connections of a user
// except the originating connection.
func (h *Hub) PushWorkspaceEvent(userID, excludeConnID string, payload any) {
	h.SendToUserExcept(userID, excludeConnID, map[string]any{
		"event": "workspace.event",
		"data":  payload,
	})
}

// PushSessionExpired notifies all connections of a user that their session has expired.
func (h *Hub) PushSessionExpired(userID string) {
	h.SendToUser(userID, map[string]any{
		"event": "session.expired",
		"data":  map[string]any{},
	})
}
