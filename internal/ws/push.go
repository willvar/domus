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
func (h *Hub) PushDirChanged(userID, resolvedPath, appPath, changeType string) {
	h.NotifyDirectory(userID, resolvedPath, map[string]any{
		"event": "dir.changed",
		"data": map[string]any{
			"path":        appPath,
			"change_type": changeType,
		},
	})
}

// PushTrashChanged invalidates the ID-addressed recycle-bin view on every
// active device for the user. It is intentionally not a directory
// subscription because trash is no longer part of the visible namespace.
func (h *Hub) PushTrashChanged(userID string) {
	h.SendToUser(userID, map[string]any{
		"event": "trash.changed",
		"data":  map[string]any{},
	})
}

// PushSessionExpired notifies all connections of a user that their session has expired.
func (h *Hub) PushSessionExpired(userID string) {
	h.SendToUser(userID, map[string]any{
		"event": "session.expired",
		"data":  map[string]any{},
	})
}
