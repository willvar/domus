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

// handleCreateShare creates a link share or user-to-user share.
// POST /file/share { path, type: "link"|"user", permission: "read"|"write", target_username?, expires_in? }
func (h *Handler) handleCreateShare(c *fiber.Ctx) error {
	var body struct {
		Path           string `json:"path"`
		ShareType      string `json:"type"`
		Permission     string `json:"permission"`
		TargetUsername string `json:"target_username"`
		ExpiresIn      int64  `json:"expires_in"` // seconds, for link shares
	}
	if err := c.BodyParser(&body); err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "invalid_request"})
	}
	if body.Path == "" || (body.ShareType != "link" && body.ShareType != "user") {
		return c.Status(400).JSON(fiber.Map{"error": "invalid_request"})
	}
	if body.Permission == "" {
		body.Permission = "read"
	}
	if body.Permission != "read" && body.Permission != "write" {
		return c.Status(400).JSON(fiber.Map{"error": "invalid_permission"})
	}

	resolvedPath, err := middleware.ResolvePath(c, body.Path)
	if err != nil {
		return err
	}

	session := c.Locals("session").(*model.Session)
	fileRecord, err := model.GetFile(session.UserID, resolvedPath)
	if err != nil {
		return c.Status(404).JSON(fiber.Map{"error": "file_not_found"})
	}
	if fileRecord.Status != "ready" {
		return c.Status(409).JSON(fiber.Map{"error": "file_not_ready"})
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

	shareID := uuid.New().String()
	fileName := filepath.Base(resolvedPath)
	ct := mime.TypeByExtension(filepath.Ext(fileName))

	share := &model.Share{
		ShareID:     shareID,
		OwnerID:     session.UserID,
		FilePath:    resolvedPath,
		FileName:    fileName,
		FileSize:    fileRecord.Size,
		ContentType: ct,
		ShareType:   body.ShareType,
		Permission:  body.Permission,
	}

	if body.ExpiresIn > 0 {
		exp := time.Now().Add(time.Duration(body.ExpiresIn) * time.Second)
		share.ExpiresAt = &exp
	}

	response := fiber.Map{"share_id": shareID}

	switch body.ShareType {
	case "link":
		// Generate a random share key and re-wrap DEK
		shareKey, err := auth.GenerateDEK() // reuse DEK generator for 32-byte random key
		if err != nil {
			return c.Status(500).JSON(fiber.Map{"error": "internal_error"})
		}
		wrappedForShare, err := auth.WrapDEK(shareKey, dek)
		if err != nil {
			return c.Status(500).JSON(fiber.Map{"error": "wrap_failed"})
		}
		share.WrappedDEK = hex.EncodeToString(wrappedForShare)
		response["share_key"] = hex.EncodeToString(shareKey)

	case "user":
		if body.TargetUsername == "" {
			return c.Status(400).JSON(fiber.Map{"error": "target_username_required"})
		}
		targetUser, err := model.GetUserByUsername(body.TargetUsername)
		if err != nil {
			return c.Status(404).JSON(fiber.Map{"error": "target_user_not_found"})
		}
		share.TargetUserID = targetUser.ID

		// Wrap DEK with target user's KEK
		targetKEK, err := h.loadUserKEK(targetUser.ID)
		if err != nil {
			return c.Status(500).JSON(fiber.Map{"error": "internal_error"})
		}
		wrappedForTarget, err := auth.WrapDEK(targetKEK, dek)
		if err != nil {
			return c.Status(500).JSON(fiber.Map{"error": "wrap_failed"})
		}
		share.WrappedDEK = hex.EncodeToString(wrappedForTarget)
	}

	if err := model.CreateShare(share); err != nil {
		return c.Status(500).JSON(fiber.Map{"error": "create_share_failed"})
	}

	h.Audit.LogFromCtx(c, "file_share", body.Path, body.ShareType, "success", 0)
	return c.JSON(response)
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
	shares, err := model.ListSharesForFile(session.UserID, resolvedPath)
	if err != nil {
		return c.Status(500).JSON(fiber.Map{"error": "list_failed"})
	}
	if shares == nil {
		shares = []model.Share{}
	}
	return c.JSON(shares)
}

// handleDeleteShare revokes a share.
// DELETE /file/share/:id
func (h *Handler) handleDeleteShare(c *fiber.Ctx) error {
	id, err := c.ParamsInt("id")
	if err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "invalid_id"})
	}
	session := c.Locals("session").(*model.Session)
	if err := model.DeleteShare(int64(id), session.UserID); err != nil {
		return c.Status(500).JSON(fiber.Map{"error": "delete_failed"})
	}
	return c.JSON(fiber.Map{"ok": true})
}

// handleListSharedWithMe lists files shared with the current user.
// GET /file/shared
func (h *Handler) handleListSharedWithMe(c *fiber.Ctx) error {
	session := c.Locals("session").(*model.Session)
	shares, err := model.ListSharesForUser(session.UserID)
	if err != nil {
		return c.Status(500).JSON(fiber.Map{"error": "list_failed"})
	}
	if shares == nil {
		shares = []model.Share{}
	}
	return c.JSON(shares)
}

// handleShareInfo returns metadata for a shared file.
// Link shares are public (no auth). User shares require authentication as the target user.
// GET /share/:share_id/info
func (h *Handler) handleShareInfo(c *fiber.Ctx) error {
	shareID := c.Params("share_id")
	if shareID == "" {
		return c.Status(400).JSON(fiber.Map{"error": "missing_share_id"})
	}

	share, err := model.GetShareByID(shareID)
	if err != nil {
		return c.Status(404).JSON(fiber.Map{"error": "share_not_found"})
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

	resp := fiber.Map{
		"url":          presignedURL,
		"size":         share.FileSize,
		"name":         share.FileName,
		"content_type": share.ContentType,
		"chunk_size":   auth.DefaultChunkSize,
		"permission":   share.Permission,
	}

	switch share.ShareType {
	case "link":
		// Link share: return wrapped_dek for client-side unwrap with share_key
		resp["wrapped_dek"] = share.WrappedDEK

	case "user":
		// User share: require authentication as the target user
		cookieValue := c.Cookies(middleware.SessionCookieName)
		if cookieValue == "" {
			return c.Status(401).JSON(fiber.Map{"error": "unauthorized"})
		}
		sessionID, err := auth.VerifyCookie(cookieValue, h.Config.Server.SessionSecret)
		if err != nil {
			return c.Status(401).JSON(fiber.Map{"error": "invalid_session"})
		}
		session := h.Sessions.Get(sessionID)
		if session == nil {
			return c.Status(401).JSON(fiber.Map{"error": "session_expired"})
		}
		if session.UserID != share.TargetUserID {
			return c.Status(403).JSON(fiber.Map{"error": "forbidden"})
		}

		// Unwrap DEK server-side with target user's KEK
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
		resp["dek"] = hex.EncodeToString(dek)
	}

	return c.JSON(resp)
}
