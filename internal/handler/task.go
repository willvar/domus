package handler

import (
	"context"
	"net/http"
	"strings"

	"domus/internal/httpx"
	"domus/internal/middleware"
	"domus/internal/model"
)

func (h *Handler) handleListTasks(w http.ResponseWriter, r *http.Request) error {
	session := middleware.Session(r)
	tasks, err := h.Repos.Tasks.ListRecent(session.UserID)
	if err != nil {
		return httpx.JSON(w, 500, map[string]any{"error": "list_tasks_failed"})
	}
	if tasks == nil {
		tasks = []model.Task{}
	}
	visibleTasks := tasks[:0]
	for _, task := range tasks {
		if task.Type == "upload" || task.Type == "transcode" {
			visibleTasks = append(visibleTasks, task)
		}
	}
	tasks = visibleTasks
	activeUploads, _ := h.Repos.Files.ListActiveUploads(session.UserID)
	byTaskID := make(map[string]string, len(activeUploads))
	for _, rec := range activeUploads {
		if rec.TaskID != "" {
			byTaskID[rec.TaskID] = rec.ClientInstanceID
		}
	}
	for i := range tasks {
		if tasks[i].Type == "upload" {
			tasks[i].ClientInstanceID = byTaskID[tasks[i].TaskID]
		}
	}
	return httpx.JSON(w, http.StatusOK, tasks)
}

func (h *Handler) cancelTask(session *model.Session, taskID string) error {
	task, err := h.Repos.Tasks.Get(taskID)
	if err != nil {
		return httpx.NewError(http.StatusNotFound, "task_not_found")
	}
	if task.UserID != session.UserID && session.Role != "root" {
		return httpx.NewError(http.StatusForbidden, "access_denied")
	}
	if task.Type == "transcode" {
		switch task.Status {
		case "queued":
			// Nothing published yet: drop the reservation row outright.
			if h.FileSystem != nil {
				_ = h.FileSystem.DeleteRendition(task.UserID, uint64(task.SourceInode), task.Profile)
			}
			return h.Repos.Tasks.UpdateStatus(taskID, "cancelled")
		case "running":
			// The worker observes the flag between segment publishes.
			_ = h.FileSystem.SetRenditionCancelling(task.UserID, task.TaskID)
			return h.Repos.Tasks.UpdateStatus(taskID, "cancelling")
		default:
			return nil // already terminal
		}
	}
	if task.Type == "upload" {
		files, _ := h.Repos.Files.ListActiveUploads(task.UserID)
		for _, f := range files {
			if f.TaskID == taskID && f.Status == "uploading" {
				if f.OSSUploadID != "" {
					_ = h.Store.AbortMultipartUpload(f.StorageKey(), f.OSSUploadID)
				}
				if h.FileSystem != nil {
					_ = h.FileSystem.AbortDirectUpload(context.Background(), task.UserID, f.UploadID)
				} else {
					_ = h.Repos.Files.Delete(task.UserID, f.Path)
				}
				h.notifyParentDir(task.UserID, f.Path)
				break
			}
		}
	}
	return h.Repos.Tasks.UpdateStatus(taskID, "cancelled")
}

func (h *Handler) handleCancelTask(w http.ResponseWriter, r *http.Request) error {
	taskID := strings.TrimSpace(r.PathValue("id"))
	if taskID == "" {
		return httpx.JSON(w, 400, map[string]any{"error": "invalid_task_id"})
	}

	session := middleware.Session(r)
	if err := h.cancelTask(session, taskID); err != nil {
		if ferr, ok := err.(*httpx.Error); ok {
			return httpx.JSON(w, ferr.Code, map[string]any{"error": ferr.Message})
		}
		return httpx.JSON(w, 500, map[string]any{"error": "cancel_task_failed"})
	}

	return httpx.JSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (h *Handler) handleClearDoneTasks(w http.ResponseWriter, r *http.Request) error {
	session := middleware.Session(r)
	if err := h.Repos.Tasks.DeleteCompleted(session.UserID); err != nil {
		return httpx.JSON(w, 500, map[string]any{"error": "clear_tasks_failed"})
	}
	return httpx.JSON(w, http.StatusOK, map[string]any{"ok": true})
}
