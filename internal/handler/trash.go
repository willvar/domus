package handler

import (
	"bufio"
	"encoding/json"
	"fmt"
	"mime"
	"path/filepath"

	"github.com/gofiber/fiber/v2"

	"zephyr/internal/middleware"
	"zephyr/internal/model"
)

func (h *Handler) handleListTrash(c *fiber.Ctx) error {
	session := c.Locals("session").(*model.Session)
	items, err := model.ListTrash(session.UserID)
	if err != nil {
		return c.Status(500).JSON(fiber.Map{"error": "list_trash_failed"})
	}
	if items == nil {
		items = []model.TrashItem{}
	}
	return c.JSON(items)
}

func (h *Handler) handleRestoreTrash(c *fiber.Ctx) error {
	var body struct {
		ID int64 `json:"id"`
	}
	if err := c.BodyParser(&body); err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "invalid_request"})
	}

	session := c.Locals("session").(*model.Session)
	item, err := model.GetTrashItem(body.ID, session.UserID)
	if err != nil {
		return c.Status(404).JSON(fiber.Map{"error": "trash_not_found"})
	}

	// Restore: move from trash back to original path
	originalResolved, resolveErr := middleware.ResolvePath(c, item.OriginalPath)
	if resolveErr != nil {
		return resolveErr
	}

	if item.IsDir {
		if err := h.Store.RecursiveMove(item.TrashKey, originalResolved, nil); err != nil {
			return c.Status(500).JSON(fiber.Map{"error": "restore_failed"})
		}
		// Rebuild file records from OSS
		h.syncDirFiles(session.UserID, originalResolved)
	} else {
		if err := h.Store.MoveObject(item.TrashKey, originalResolved); err != nil {
			return c.Status(500).JSON(fiber.Map{"error": "restore_failed"})
		}
		fileName := filepath.Base(originalResolved)
		ct := mime.TypeByExtension(filepath.Ext(originalResolved))
		_ = model.UpsertFile(session.UserID, originalResolved, fileName, false, item.Size, ct, "")
	}

	_ = model.DeleteTrashRecord(item.ID)
	return c.JSON(fiber.Map{"ok": true})
}

func (h *Handler) handleDeleteTrashItem(c *fiber.Ctx) error {
	id, err := c.ParamsInt("id")
	if err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "invalid_id"})
	}

	session := c.Locals("session").(*model.Session)
	item, err := model.GetTrashItem(int64(id), session.UserID)
	if err != nil {
		return c.Status(404).JSON(fiber.Map{"error": "trash_not_found"})
	}

	// Permanently delete from OSS
	if item.IsDir {
		if err := h.Store.RecursiveDelete(item.TrashKey, nil); err != nil {
			return c.Status(500).JSON(fiber.Map{"error": "delete_from_storage_failed"})
		}
	} else {
		if err := h.Store.DeleteObject(item.TrashKey); err != nil {
			return c.Status(500).JSON(fiber.Map{"error": "delete_from_storage_failed"})
		}
	}

	if err := model.DeleteTrashRecord(item.ID); err != nil {
		return c.Status(500).JSON(fiber.Map{"error": "delete_record_failed"})
	}

	return c.JSON(fiber.Map{"ok": true})
}

func (h *Handler) handleClearTrash(c *fiber.Ctx) error {
	session := c.Locals("session").(*model.Session)
	items, err := model.ListTrash(session.UserID)
	if err != nil {
		return c.Status(500).JSON(fiber.Map{"error": "clear_trash_failed"})
	}

	clearItem := func(item model.TrashItem) error {
		if item.IsDir {
			if err := h.Store.RecursiveDelete(item.TrashKey, nil); err != nil {
				return err
			}
		} else {
			if err := h.Store.DeleteObject(item.TrashKey); err != nil {
				return err
			}
		}
		return model.DeleteTrashRecord(item.ID)
	}

	if c.Get("Accept") == "text/event-stream" {
		c.Set("Content-Type", "text/event-stream")
		c.Set("Cache-Control", "no-cache")
		c.Set("Connection", "keep-alive")

		c.Context().SetBodyStreamWriter(func(w *bufio.Writer) {
			total := len(items)
			for i, item := range items {
				if err := clearItem(item); err != nil {
					data, _ := json.Marshal(fiber.Map{"error": "clear_item_failed"})
					_, _ = fmt.Fprintf(w, "data: %s\n\n", data)
					_ = w.Flush()
					return
				}
				data, _ := json.Marshal(fiber.Map{
					"done":    i + 1,
					"total":   total,
					"current": item.OriginalPath,
				})
				_, _ = fmt.Fprintf(w, "data: %s\n\n", data)
				_ = w.Flush()
			}

			data, _ := json.Marshal(fiber.Map{"done": true})
			_, _ = fmt.Fprintf(w, "data: %s\n\n", data)
			_ = w.Flush()
		})
		return nil
	}

	for _, item := range items {
		if err := clearItem(item); err != nil {
			return c.Status(500).JSON(fiber.Map{"error": "clear_trash_failed"})
		}
	}

	return c.JSON(fiber.Map{"ok": true})
}
