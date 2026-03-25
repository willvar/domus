package ws

// Push event helpers — all send to specific targets via the Hub.

// PushTaskUpdate sends a task progress/status update to all connections of a user.
func (h *Hub) PushTaskUpdate(userID, taskID, taskType, name, status string, progress float64, phase string) {
	h.SendToUser(userID, map[string]any{
		"event": "task.update",
		"data": map[string]any{
			"task_id":  taskID,
			"type":     taskType,
			"name":     name,
			"status":   status,
			"progress": progress,
			"phase":    phase,
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

// PushSessionExpired notifies all connections of a user that their session has expired.
func (h *Hub) PushSessionExpired(userID string) {
	h.SendToUser(userID, map[string]any{
		"event": "session.expired",
		"data":  map[string]any{},
	})
}
