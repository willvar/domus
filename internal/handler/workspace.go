package handler

import (
	"encoding/json"

	"github.com/gofiber/fiber/v2"
	"zephyr/internal/model"
)

func (h *Handler) handleWorkspaceSave(c *fiber.Ctx) error {
	var body struct {
		State json.RawMessage `json:"state"`
	}
	if err := c.BodyParser(&body); err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "invalid_request"})
	}

	session := c.Locals("session").(*model.Session)
	if err := h.Repos.Workspace.Save(session.UserID, string(body.State)); err != nil {
		return c.Status(500).JSON(fiber.Map{"error": "save_failed"})
	}
	return c.JSON(fiber.Map{"ok": true})
}

func (h *Handler) handleWorkspaceLoad(c *fiber.Ctx) error {
	session := c.Locals("session").(*model.Session)
	state, err := h.Repos.Workspace.Get(session.UserID)
	if err != nil {
		return c.JSON(fiber.Map{"state": nil})
	}
	return c.JSON(fiber.Map{"state": json.RawMessage(state)})
}

func (h *Handler) handleWorkspaceClear(c *fiber.Ctx) error {
	session := c.Locals("session").(*model.Session)
	_ = h.Repos.Workspace.Delete(session.UserID)
	return c.JSON(fiber.Map{"ok": true})
}
