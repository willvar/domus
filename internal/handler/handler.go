package handler

import (
	"encoding/hex"
	"fmt"

	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/websocket/v2"
	"github.com/willvar/dofs"

	"domus/config"
	"domus/internal/auth"
	"domus/internal/dofsbridge"
	"domus/internal/fileview"
	"domus/internal/middleware"
	"domus/internal/model"
	"domus/internal/service"
	"domus/internal/store"
	"domus/internal/ws"
)

// Handler holds all dependencies for HTTP handlers.
type Handler struct {
	Config     *config.Config
	Repos      *model.Repos
	Store      store.ControlStore
	Email      service.EmailSender
	Audit      *model.AuditWorker
	Challenges *auth.ChallengeManager
	Mid        *middleware.Middleware
	Hub        *ws.Hub
	DOFS       *dofsbridge.Runtime
	FileSystem *fileview.Repo
}

// RegisterRoutes registers all API routes on the Fiber app.
func (h *Handler) RegisterRoutes(app *fiber.App) {
	if h.Hub == nil {
		h.Hub = ws.NewHub()
	}

	// /auth (public)
	app.Get("/auth", h.handleAuthConfig)
	app.Post("/auth", h.handleLogin)
	app.Post("/auth/verify", h.handleVerify)
	app.Get("/user/avatar/:username", h.handlePublicAvatar)

	authed := app.Group("", h.Mid.AuthRequired())
	authed.Delete("/auth", h.handleLogout)

	// /user
	user := authed.Group("/user")
	user.Get("/", h.handleMe)
	user.Get("/storage", h.handleStorageUsage)
	user.Get("/security", h.handleSecurityStatus)
	user.Put("/display-name", h.handleUpdateDisplayName)
	user.Put("/security/password", h.handleChangePassword)
	user.Post("/security/email/bind", h.handleBindEmail)
	user.Post("/security/email/verify", h.handleVerifyBindEmail)
	user.Delete("/security/email", h.handleUnbindEmail)
	user.Post("/security/otp/setup", h.handleOTPSetup)
	user.Post("/security/otp/enable", h.handleOTPEnable)
	user.Delete("/security/otp", h.handleOTPDisable)

	// /audit
	audit := authed.Group("/audit")
	audit.Get("/", middleware.RootRequired(), h.handleListAuditLogs)
	audit.Post("/", h.handleAuditPreview)
	auditUser := audit.Group("/user", middleware.RootRequired())
	auditUser.Get("/", h.handleListUsers)
	auditUser.Post("/", h.handleCreateUser)
	auditUser.Put("/:id", h.handleUpdateUser)
	auditUser.Delete("/:id", h.handleDeleteUser)
	auditUser.Delete("/:id/otp", h.handleResetUserOTP)
	auditUser.Delete("/:id/email", h.handleResetUserEmail)

	// /admin (root only)
	admin := authed.Group("/admin", middleware.RootRequired())
	admin.Get("/oss/cors-check", h.handleCORSCheck)

	// /file
	file := authed.Group("/file")
	file.Use(rejectFileContentPayload)
	file.Get("/", h.handleList)
	file.Get("/search", h.handleSearch)
	file.Get("/access", h.handleFileAccess)
	file.Post("/mkdir", h.handleMkdir)
	file.Post("/rename", h.handleRename)
	file.Post("/copy", h.handleCopy)
	file.Post("/move", h.handleMove)
	file.Delete("/delete", h.handleDelete)

	fileUpload := file.Group("/upload")
	fileUpload.Get("/", h.handleUploadStatus)
	fileUpload.Post("/", h.handleUploadDispatch)
	fileUpload.Get("/presign", h.handleUploadPresign)
	fileUpload.Post("/heartbeat", h.handleUploadHeartbeat)
	fileUpload.Post("/cancel", h.handleUploadCancel)
	fileUpload.Post("/cleanup", h.handleUploadCleanup)

	// Server-side transcode (playback renditions).
	file.Post("/transcode", h.handleTranscodeRequest)
	file.Get("/renditions", h.handleListRenditions)

	// /trash — deletion events are addressed by opaque IDs, never by a
	// reserved path in the user's visible namespace.
	trash := authed.Group("/trash")
	trash.Use(rejectFileContentPayload)
	trash.Post("/", h.handleCreateTrashEntry)
	trash.Get("/", h.handleListTrash)
	trash.Delete("/", h.handleEmptyTrash)
	trash.Get("/:id/list", h.handleListTrashDirectory)
	trash.Get("/:id/access", h.handleTrashAccess)
	trash.Post("/:id/restore", h.handleRestoreTrash)
	trash.Delete("/:id", h.handleDeleteTrashItem)

	// /task
	task := authed.Group("/task")
	task.Get("/", h.handleListTasks)
	task.Delete("/done", h.handleClearDoneTasks)
	task.Delete("/:id", h.handleCancelTask)

	// WebSocket — auth via cookie on HTTP upgrade
	app.Use("/ws", h.Mid.WebSocketUpgrade())
	app.Get("/ws", websocket.New(func(c *websocket.Conn) {
		session := c.Locals("session").(*model.Session)
		sessionID := c.Locals("sessionID").(string)
		h.Hub.HandleConnection(c.Conn, session, sessionID)
	}))

	// Register realtime/session WS actions only.
	h.registerWSActions()
}

// getFileEncryptionKey returns the cached KEK for the current user's files.
func (h *Handler) getFileEncryptionKey(session *model.Session) ([]byte, error) {
	if session.KEK != nil {
		return session.KEK, nil
	}
	return h.loadUserKEK(session.UserID)
}

// loadUserKEK loads and unwraps a user's KEK from the database.
// Used by background jobs (no session) and for target user KEK in sharing.
func (h *Handler) loadUserKEK(userID string) ([]byte, error) {
	wrappedHex, err := h.Repos.Users.GetWrappedKEK(userID)
	if err != nil {
		return nil, fmt.Errorf("load wrapped KEK: %w", err)
	}
	if wrappedHex == "" {
		return nil, fmt.Errorf("user %s has no wrapped KEK", userID)
	}
	serverKey, err := auth.ServerKeyFromSecret(h.Config.Server.EncryptionSecret)
	if err != nil {
		return nil, err
	}
	defer dofs.Clear(serverKey)
	wrappedBytes, err := hex.DecodeString(wrappedHex)
	if err != nil {
		return nil, fmt.Errorf("decode wrapped KEK: %w", err)
	}
	return auth.UnwrapKEK(serverKey, wrappedBytes)
}

// generateWrappedKEK creates a new random KEK and wraps it with the server key.
// Returns the hex-encoded wrapped KEK for storage in the users table.
func (h *Handler) generateWrappedKEK() (string, error) {
	kek, err := auth.GenerateKEK()
	if err != nil {
		return "", fmt.Errorf("generate KEK: %w", err)
	}
	defer dofs.Clear(kek)
	serverKey, err := auth.ServerKeyFromSecret(h.Config.Server.EncryptionSecret)
	if err != nil {
		return "", err
	}
	defer dofs.Clear(serverKey)
	wrapped, err := auth.WrapKEK(serverKey, kek)
	if err != nil {
		return "", fmt.Errorf("wrap KEK: %w", err)
	}
	return hex.EncodeToString(wrapped), nil
}
