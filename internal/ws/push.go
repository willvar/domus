package ws

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

// PushSessionOutput sends terminal output to a specific connection.
func (h *Hub) PushSessionOutput(connID, sessionID, data string) {
	h.SendToConn(connID, map[string]any{
		"event": "session.output",
		"data": map[string]any{
			"session_id": sessionID,
			"data":       data,
		},
	})
}

// PushSessionDone signals that a vsh command has finished executing.
func (h *Hub) PushSessionDone(connID, sessionID, cwd string) {
	h.SendToConn(connID, map[string]any{
		"event": "session.done",
		"data": map[string]any{
			"session_id": sessionID,
			"cwd":        cwd,
		},
	})
}

// PushSessionExit signals that a session has ended (SSH disconnect or vsh exit).
func (h *Hub) PushSessionExit(connID, sessionID, reason string) {
	h.SendToConn(connID, map[string]any{
		"event": "session.exit",
		"data": map[string]any{
			"session_id": sessionID,
			"reason":     reason,
		},
	})
}

// PushSessionSSH signals SSH mode changes.
// status: "connecting" | "connected" | "disconnected"
func (h *Hub) PushSessionSSH(connID, sessionID, status string) {
	h.SendToConn(connID, map[string]any{
		"event": "session.ssh",
		"data": map[string]any{
			"session_id": sessionID,
			"status":     status,
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
