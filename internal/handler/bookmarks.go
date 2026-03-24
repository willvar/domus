package handler

import (
	"github.com/gofiber/fiber/v2"

	"zephyr/internal/model"
)

func (h *Handler) handleListBookmarks(c *fiber.Ctx) error {
	session := c.Locals("session").(*model.Session)
	bookmarks, err := model.ListBookmarks(session.UserID)
	if err != nil {
		return c.Status(500).JSON(fiber.Map{"error": "list_bookmarks_failed"})
	}
	if bookmarks == nil {
		bookmarks = []model.Bookmark{}
	}
	return c.JSON(bookmarks)
}

func (h *Handler) handleCreateBookmark(c *fiber.Ctx) error {
	var body struct {
		Name      string `json:"name"`
		Path      string `json:"path"`
		Icon      string `json:"icon"`
		SortOrder int    `json:"sort_order"`
	}
	if err := c.BodyParser(&body); err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "invalid_request"})
	}

	session := c.Locals("session").(*model.Session)
	if body.Icon == "" {
		body.Icon = "folder"
	}

	bookmark, err := model.CreateBookmark(session.UserID, body.Name, body.Path, body.Icon, body.SortOrder)
	if err != nil {
		return c.Status(500).JSON(fiber.Map{"error": "create_bookmark_failed"})
	}

	return c.Status(201).JSON(bookmark)
}

func (h *Handler) handleUpdateBookmark(c *fiber.Ctx) error {
	id, err := c.ParamsInt("id")
	if err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "invalid_id"})
	}

	var body struct {
		Name      string `json:"name"`
		Path      string `json:"path"`
		Icon      string `json:"icon"`
		SortOrder int    `json:"sort_order"`
	}
	if err := c.BodyParser(&body); err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "invalid_request"})
	}

	session := c.Locals("session").(*model.Session)
	if err := model.UpdateBookmark(int64(id), session.UserID, body.Name, body.Path, body.Icon, body.SortOrder); err != nil {
		return c.Status(500).JSON(fiber.Map{"error": "update_bookmark_failed"})
	}

	return c.JSON(fiber.Map{"ok": true})
}

func (h *Handler) handleDeleteBookmark(c *fiber.Ctx) error {
	id, err := c.ParamsInt("id")
	if err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "invalid_id"})
	}

	session := c.Locals("session").(*model.Session)
	if err := model.DeleteBookmark(int64(id), session.UserID); err != nil {
		return c.Status(500).JSON(fiber.Map{"error": "delete_bookmark_failed"})
	}

	return c.JSON(fiber.Map{"ok": true})
}
