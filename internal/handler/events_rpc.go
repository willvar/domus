package handler

import (
	"context"
	"log"

	"domus/internal/middleware"
	"domus/internal/notify"
)

// StartEventRelay accepts local hints and forwards current state through the
// existing WebSocket hub. It never executes jobs or polls the product tables.
func (h *Handler) StartEventRelay(ctx context.Context) {
	if _, err := notify.Listen(ctx, notify.SocketPath(h.Config.DOFS.ControlSocket), h.publishMediaEvent); err != nil {
		log.Print("[events] local notification listener unavailable; view snapshots remain available")
	}
}

func (h *Handler) publishMediaEvent(event notify.Event) {
	if event.Kind == "dofs" {
		if h.relayDOFSChange != nil {
			h.relayDOFSChange(event.UserID)
		}
		return
	}
	if event.Kind == "task" {
		task, err := h.Repos.Tasks.Get(event.TaskID)
		if err == nil && task.UserID == event.UserID {
			h.Hub.SendToUser(task.UserID, map[string]any{"event": "task.update", "data": task})
		}
		return
	}
	if h.FileSystem == nil {
		return
	}
	record, err := h.FileSystem.GetByID(event.UserID, event.Inode)
	if err != nil || record == nil || record.IsDir || record.Status != "ready" || middleware.IsInternalStoragePath(record.Path) {
		return
	}
	if event.Generation > 0 && event.Generation != record.Generation {
		return
	}
	if event.Kind == "rendition" {
		h.Hub.SendToUser(event.UserID, map[string]any{
			"event": "rendition.changed",
			"data":  map[string]any{"path": record.Path, "inode": record.ID, "generation": record.Generation},
		})
		return
	}
	// Send to this user's devices, not just parent-directory subscribers:
	// a filename search can display a file outside its subscribed directory.
	h.Hub.SendToUser(event.UserID, map[string]any{
		"event": "dir.changed",
		"data":  map[string]any{"path": parentDirOf(record.Path), "change_type": "refresh"},
	})
}
