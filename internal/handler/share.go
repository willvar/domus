package handler

import (
	"encoding/hex"
	"mime"
	"path/filepath"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"

	"zephyr/internal/auth"
	"zephyr/internal/middleware"
	"zephyr/internal/model"
)

const maxShareExpirySeconds int64 = 365 * 24 * 60 * 60

// handleCreateShare creates a user-to-user share.
// POST /file/share { path, target_username, permission: "read"|"write", expires_in? }
func (h *Handler) handleCreateShare(c *fiber.Ctx) error {
	var body struct {
		Path           string `json:"path"`
		Permission     string `json:"permission"`
		TargetUsername string `json:"target_username"`
		ExpiresIn      int64  `json:"expires_in"` // seconds, 0 = no expiry
	}
	if err := c.BodyParser(&body); err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "invalid_request"})
	}
	if body.Path == "" || body.TargetUsername == "" {
		return c.Status(400).JSON(fiber.Map{"error": "invalid_request"})
	}
	if body.Permission == "" {
		body.Permission = "read"
	}
	if body.Permission != "read" && body.Permission != "write" {
		return c.Status(400).JSON(fiber.Map{"error": "invalid_permission"})
	}
	if body.ExpiresIn < 0 || body.ExpiresIn > maxShareExpirySeconds {
		return c.Status(400).JSON(fiber.Map{"error": "invalid_expiry"})
	}

	resolvedPath, err := middleware.ResolvePath(c, body.Path)
	if err != nil {
		return err
	}

	session := c.Locals("session").(*model.Session)
	fileRecord, err := h.Repos.Files.Get(session.UserID, resolvedPath)
	if err != nil {
		return c.Status(404).JSON(fiber.Map{"error": "file_not_found"})
	}
	if fileRecord.IsDir {
		return c.Status(400).JSON(fiber.Map{"error": "directory_not_shareable"})
	}
	if fileRecord.Status != "ready" {
		return c.Status(409).JSON(fiber.Map{"error": "file_not_ready"})
	}
	if fileRecord.WrappedDEK == "" {
		return c.Status(409).JSON(fiber.Map{"error": "file_not_encrypted"})
	}

	// Get the file's DEK
	kek, err := h.getFileEncryptionKey(session)
	if err != nil {
		return c.Status(500).JSON(fiber.Map{"error": "internal_error"})
	}
	wrappedBytes, err := hex.DecodeString(fileRecord.WrappedDEK)
	if err != nil {
		return c.Status(500).JSON(fiber.Map{"error": "internal_error"})
	}
	dek, err := auth.UnwrapDEK(kek, wrappedBytes)
	if err != nil {
		return c.Status(500).JSON(fiber.Map{"error": "unwrap_failed"})
	}

	// Resolve target user
	targetUser, err := h.Repos.Users.GetByUsername(body.TargetUsername)
	if err != nil {
		return c.Status(404).JSON(fiber.Map{"error": "target_user_not_found"})
	}
	if targetUser.ID == session.UserID {
		return c.Status(400).JSON(fiber.Map{"error": "cannot_share_with_self"})
	}

	// Wrap DEK with target user's KEK
	targetKEK, err := h.loadUserKEK(targetUser.ID)
	if err != nil {
		return c.Status(500).JSON(fiber.Map{"error": "internal_error"})
	}
	wrappedForTarget, err := auth.WrapDEK(targetKEK, dek)
	if err != nil {
		return c.Status(500).JSON(fiber.Map{"error": "wrap_failed"})
	}

	shareID := uuid.New().String()
	fileName := filepath.Base(resolvedPath)
	ct := mime.TypeByExtension(filepath.Ext(fileName))

	share := &model.Share{
		ShareID:      shareID,
		OwnerID:      session.UserID,
		FilePath:     resolvedPath,
		FileName:     fileName,
		FileSize:     fileRecord.Size,
		ContentType:  ct,
		TargetUserID: targetUser.ID,
		WrappedDEK:   hex.EncodeToString(wrappedForTarget),
		Permission:   body.Permission,
	}

	if body.ExpiresIn > 0 {
		exp := time.Now().Add(time.Duration(body.ExpiresIn) * time.Second)
		share.ExpiresAt = &exp
	}

	if err := h.Repos.Shares.Create(share); err != nil {
		return c.Status(500).JSON(fiber.Map{"error": "create_share_failed"})
	}

	// Notify target user to refresh their shared directory
	if h.Hub != nil {
		h.Hub.SendToUser(targetUser.ID, map[string]any{
			"event": "dir.changed",
			"data":  map[string]any{"path": "__shared__/", "change_type": "refresh"},
		})
	}

	h.Audit.LogFromCtx(c, "file_share", body.Path, body.TargetUsername, "success", 0)
	return c.JSON(fiber.Map{"share_id": shareID})
}

// handleListShares lists all shares for a file.
// GET /file/shares?path=...
func (h *Handler) handleListShares(c *fiber.Ctx) error {
	path := c.Query("path", "")
	if path == "" {
		return c.Status(400).JSON(fiber.Map{"error": "path_required"})
	}
	resolvedPath, err := middleware.ResolvePath(c, path)
	if err != nil {
		return err
	}
	session := c.Locals("session").(*model.Session)
	shares, err := h.Repos.Shares.ListForFile(session.UserID, resolvedPath)
	if err != nil {
		return c.Status(500).JSON(fiber.Map{"error": "list_failed"})
	}
	if shares == nil {
		shares = []model.Share{}
	}
	return c.JSON(shares)
}

// handleDeleteShare revokes a share. Both owner and target user can delete.
// DELETE /file/share/:id
func (h *Handler) handleDeleteShare(c *fiber.Ctx) error {
	id, err := c.ParamsInt("id")
	if err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "invalid_id"})
	}
	session := c.Locals("session").(*model.Session)
	deleted, err := h.Repos.Shares.Delete(int64(id), session.UserID)
	if err != nil {
		return c.Status(500).JSON(fiber.Map{"error": "delete_failed"})
	}
	if !deleted {
		return c.Status(404).JSON(fiber.Map{"error": "share_not_found"})
	}

	// Notify the current user to refresh shared directory
	if h.Hub != nil {
		h.Hub.SendToUser(session.UserID, map[string]any{
			"event": "dir.changed",
			"data":  map[string]any{"path": "__shared__/", "change_type": "refresh"},
		})
	}

	return c.JSON(fiber.Map{"ok": true})
}

// handleListSharedWithMe lists files shared with the current user.
// GET /file/shared
func (h *Handler) handleListSharedWithMe(c *fiber.Ctx) error {
	session := c.Locals("session").(*model.Session)
	shares, err := h.Repos.Shares.ListForUser(session.UserID)
	if err != nil {
		return c.Status(500).JSON(fiber.Map{"error": "list_failed"})
	}
	if shares == nil {
		shares = []model.Share{}
	}
	return c.JSON(shares)
}

// handleShareInfo returns metadata and decryption key for a shared file.
// Requires authentication as the target user.
// GET /file/shared/:share_id
func (h *Handler) handleShareInfo(c *fiber.Ctx) error {
	shareID := c.Params("share_id")
	if shareID == "" {
		return c.Status(400).JSON(fiber.Map{"error": "missing_share_id"})
	}

	share, err := h.Repos.Shares.GetByID(shareID)
	if err != nil {
		return c.Status(404).JSON(fiber.Map{"error": "share_not_found"})
	}

	session := c.Locals("session").(*model.Session)
	if session.UserID != share.TargetUserID {
		return c.Status(403).JSON(fiber.Map{"error": "forbidden"})
	}

	// Check expiration
	if share.ExpiresAt != nil && time.Now().After(*share.ExpiresAt) {
		return c.Status(410).JSON(fiber.Map{"error": "share_expired"})
	}

	// Generate presigned URL for the encrypted file
	presignedURL, err := h.Store.GeneratePresignedURL(share.FilePath, 4*time.Hour)
	if err != nil {
		return c.Status(500).JSON(fiber.Map{"error": "presign_failed"})
	}

	// Unwrap DEK server-side
	kek, err := h.getFileEncryptionKey(session)
	if err != nil {
		return c.Status(500).JSON(fiber.Map{"error": "internal_error"})
	}
	wrappedBytes, err := hex.DecodeString(share.WrappedDEK)
	if err != nil {
		return c.Status(500).JSON(fiber.Map{"error": "internal_error"})
	}
	dek, err := auth.UnwrapDEK(kek, wrappedBytes)
	if err != nil {
		return c.Status(500).JSON(fiber.Map{"error": "unwrap_failed"})
	}

	return c.JSON(fiber.Map{
		"url":          presignedURL,
		"size":         share.FileSize,
		"name":         share.FileName,
		"content_type": share.ContentType,
		"chunk_size":   auth.DefaultChunkSize,
		"permission":   share.Permission,
		"dek":          hex.EncodeToString(dek),
	})
}
