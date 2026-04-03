package handler

import (
	"strings"
	"time"

	"github.com/gofiber/fiber/v2"

	"zephyr/internal/model"
)

func (h *Handler) handleListUsers(c *fiber.Ctx) error {
	users, err := model.ListUsers()
	if err != nil {
		return c.Status(500).JSON(fiber.Map{"error": "list_users_failed"})
	}
	if users == nil {
		users = []model.User{}
	}
	return c.JSON(users)
}

func (h *Handler) handleCreateUser(c *fiber.Ctx) error {
	var body struct {
		Username string `json:"username"`
		Password string `json:"password"`
		Role     string `json:"role"`
	}
	if err := c.BodyParser(&body); err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "invalid_request"})
	}

	if body.Username == "" || body.Password == "" {
		return c.Status(400).JSON(fiber.Map{"error": "username_password_required"})
	}
	if body.Role == "" {
		body.Role = "user"
	}
	if !isValidUserRole(body.Role) {
		return c.Status(400).JSON(fiber.Map{"error": "invalid_role"})
	}

	wrappedKEKHex, err := h.generateWrappedKEK()
	if err != nil {
		return c.Status(500).JSON(fiber.Map{"error": "key_generation_failed"})
	}

	user, err := model.CreateUser(body.Username, body.Password, body.Role, wrappedKEKHex)
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE") {
			return c.Status(409).JSON(fiber.Map{"error": "username_exists"})
		}
		return c.Status(500).JSON(fiber.Map{"error": "create_user_failed"})
	}

	// Initialize user's OSS namespace and home directory
	_ = h.Store.CreateDirectory(user.Username + "/")
	_ = h.Store.CreateDirectory(user.Username + "/home/")
	_ = h.Store.CreateDirectory(user.Username + "/home/" + user.Username + "/")
	_ = model.UpsertFile(user.ID, user.Username+"/home/", "home", true, 0, "", "")
	_ = model.UpsertFile(user.ID, user.Username+"/home/"+user.Username+"/", user.Username, true, 0, "", "")

	h.Audit.LogFromCtx(c, "user_create", user.Username, body.Role, "success", 0)
	return c.Status(201).JSON(user)
}

func (h *Handler) handleUpdateUser(c *fiber.Ctx) error {
	id := c.Params("id")
	if id == "" {
		return c.Status(400).JSON(fiber.Map{"error": "invalid_user_id"})
	}

	var body struct {
		Role     string `json:"role"`
		Password string `json:"password"`
	}
	if err := c.BodyParser(&body); err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "invalid_request"})
	}

	user, err := model.GetUserByID(id)
	if err != nil {
		return c.Status(404).JSON(fiber.Map{"error": "user_not_found"})
	}

	if body.Password != "" {
		if err := model.UpdateUserPassword(user.ID, body.Password); err != nil {
			return c.Status(500).JSON(fiber.Map{"error": "update_password_failed"})
		}
	}

	role := user.Role
	if body.Role != "" {
		if !isValidUserRole(body.Role) {
			return c.Status(400).JSON(fiber.Map{"error": "invalid_role"})
		}
		if code, guardErr := ensureNotDemotingLastRoot(user, body.Role); guardErr != nil {
			return c.Status(500).JSON(fiber.Map{"error": "update_user_failed"})
		} else if code != "" {
			return c.Status(400).JSON(fiber.Map{"error": code})
		}
		role = body.Role
	}

	if err := model.UpdateUser(user.ID, role); err != nil {
		return c.Status(500).JSON(fiber.Map{"error": "update_user_failed"})
	}

	// If role changed, invalidate sessions
	if body.Role != "" && body.Role != user.Role {
		h.revokeUserSessions(user.ID)
	}

	h.Audit.LogFromCtx(c, "user_update", user.Username, "", "success", 0)
	return c.JSON(fiber.Map{"ok": true})
}

func (h *Handler) handleDeleteUser(c *fiber.Ctx) error {
	id := c.Params("id")
	if id == "" {
		return c.Status(400).JSON(fiber.Map{"error": "invalid_user_id"})
	}

	session := c.Locals("session").(*model.Session)
	if id == session.UserID {
		return c.Status(400).JSON(fiber.Map{"error": "cannot_delete_self"})
	}

	user, err := model.GetUserByID(id)
	if err != nil {
		return c.Status(404).JSON(fiber.Map{"error": "user_not_found"})
	}
	if code, guardErr := ensureNotDeletingLastRoot(user); guardErr != nil {
		return c.Status(500).JSON(fiber.Map{"error": "delete_user_failed"})
	} else if code != "" {
		return c.Status(400).JSON(fiber.Map{"error": code})
	}

	if err := h.deleteUserCompletely(user); err != nil {
		return c.Status(500).JSON(fiber.Map{"error": "delete_user_failed"})
	}

	h.Audit.LogFromCtx(c, "user_delete", user.Username, "", "success", 0)
	return c.JSON(fiber.Map{"ok": true})
}

func (h *Handler) handleResetUserOTP(c *fiber.Ctx) error {
	id := c.Params("id")
	if _, err := model.GetUserByID(id); err != nil {
		return c.Status(404).JSON(fiber.Map{"error": "user_not_found"})
	}
	if err := model.UpdateUserTOTP(id, "", false); err != nil {
		return c.Status(500).JSON(fiber.Map{"error": "update_failed"})
	}
	h.revokeUserSessions(id)
	h.Audit.LogFromCtx(c, "user_reset_otp", id, "", "success", 0)
	return c.JSON(fiber.Map{"ok": true})
}

func (h *Handler) handleResetUserEmail(c *fiber.Ctx) error {
	id := c.Params("id")
	if _, err := model.GetUserByID(id); err != nil {
		return c.Status(404).JSON(fiber.Map{"error": "user_not_found"})
	}
	if err := model.UpdateUserEmail(id, ""); err != nil {
		return c.Status(500).JSON(fiber.Map{"error": "update_failed"})
	}
	h.revokeUserSessions(id)
	h.Audit.LogFromCtx(c, "user_reset_email", id, "", "success", 0)
	return c.JSON(fiber.Map{"ok": true})
}

// --- Admin audit log query ---

func (h *Handler) handleListAuditLogs(c *fiber.Ctx) error {
	page := c.QueryInt("page", 1)
	size := c.QueryInt("size", 50)
	if page < 1 {
		page = 1
	}
	if size < 1 || size > 200 {
		size = 50
	}

	q := h.DB.Model(&model.AuditLog{})

	if user := c.Query("user"); user != "" {
		q = q.Where("username = ?", user)
	}
	if action := c.Query("action"); action != "" {
		q = q.Where("action = ?", action)
	}
	if from := c.Query("from"); from != "" {
		if t, err := time.Parse(time.RFC3339, from); err == nil {
			q = q.Where("created_at >= ?", t)
		}
	}
	if to := c.Query("to"); to != "" {
		if t, err := time.Parse(time.RFC3339, to); err == nil {
			q = q.Where("created_at <= ?", t)
		}
	}

	var total int64
	q.Count(&total)

	var logs []model.AuditLog
	q.Order("id DESC").Offset((page - 1) * size).Limit(size).Find(&logs)

	return c.JSON(fiber.Map{
		"total": total,
		"page":  page,
		"size":  size,
		"items": logs,
	})
}

// --- Preview duration (from sendBeacon) ---

func (h *Handler) handleAuditPreview(c *fiber.Ctx) error {
	var body struct {
		Path       string `json:"path"`
		DurationMs int64  `json:"duration_ms"`
		Type       string `json:"type"`
	}
	if err := c.BodyParser(&body); err != nil || body.Path == "" {
		return c.SendStatus(204)
	}
	h.Audit.LogFromCtx(c, "file_preview", body.Path, body.Type, "success", body.DurationMs)
	return c.SendStatus(204)
}
