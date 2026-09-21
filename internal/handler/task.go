package handler

import (
	"context"
	"strings"

	"github.com/gofiber/fiber/v2"

	"domus/internal/model"
)

func (h *Handler) handleListTasks(c *fiber.Ctx) error {
	session := c.Locals("session").(*model.Session)
	tasks, err := h.Repos.Tasks.ListRecent(session.UserID)
	if err != nil {
		return c.Status(500).JSON(fiber.Map{"error": "list_tasks_failed"})
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
	return c.JSON(tasks)
}

func (h *Handler) cancelTask(session *model.Session, taskID string) error {
	task, err := h.Repos.Tasks.Get(taskID)
	if err != nil {
		return fiber.NewError(fiber.StatusNotFound, "task_not_found")
	}
	if task.UserID != session.UserID && session.Role != "root" {
		return fiber.NewError(fiber.StatusForbidden, "access_denied")
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

func (h *Handler) handleCancelTask(c *fiber.Ctx) error {
	taskID := strings.TrimSpace(c.Params("id"))
	if taskID == "" {
		return c.Status(400).JSON(fiber.Map{"error": "invalid_task_id"})
	}

	session := c.Locals("session").(*model.Session)
	if err := h.cancelTask(session, taskID); err != nil {
		if ferr, ok := err.(*fiber.Error); ok {
			return c.Status(ferr.Code).JSON(fiber.Map{"error": ferr.Message})
		}
		return c.Status(500).JSON(fiber.Map{"error": "cancel_task_failed"})
	}

	return c.JSON(fiber.Map{"ok": true})
}

func (h *Handler) handleClearDoneTasks(c *fiber.Ctx) error {
	session := c.Locals("session").(*model.Session)
	if err := h.Repos.Tasks.DeleteCompleted(session.UserID); err != nil {
		return c.Status(500).JSON(fiber.Map{"error": "clear_tasks_failed"})
	}
	return c.JSON(fiber.Map{"ok": true})
}
