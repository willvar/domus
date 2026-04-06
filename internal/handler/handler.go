package handler

import (
	"encoding/hex"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/websocket/v2"
	"gorm.io/gorm"

	"zephyr/config"
	"zephyr/internal/auth"
	"zephyr/internal/middleware"
	"zephyr/internal/model"
	"zephyr/internal/service"
	"zephyr/internal/store"
	"zephyr/internal/vsh"
	"zephyr/internal/ws"
)

// Handler holds all dependencies for HTTP handlers.
type Handler struct {
	Config     *config.Config
	DB         *gorm.DB
	Store      store.FileStore
	Sessions   *model.SessionStore
	Dispatcher *service.Dispatcher
	Transcoder *service.Transcoder
	Audit      *model.AuditWorker
	Challenges *auth.ChallengeManager
	Mid        *middleware.Middleware
	Hub        *ws.Hub
	Vsh        *vsh.ShellManager
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
	user.Post("/avatar", h.handleUploadAvatar)
	user.Get("/store/*", h.handleUserStoreGet)
	user.Put("/store/*", h.handleUserStorePut)
	user.Get("/security", h.handleSecurityStatus)
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

	// /file
	file := authed.Group("/file")
	file.Get("/", h.handleList)
	file.Get("/access", h.handleFileAccess)
	file.Get("/preview", h.handlePreview)
	file.Get("/thumbnail", h.handleThumbnail)
	file.Put("/content/diff", h.handlePatchContent)
	file.Post("/mkdir", h.handleMkdir)
	file.Post("/rename", h.handleRename)
	file.Post("/copy", h.handleCopy)
	file.Post("/move", h.handleMove)
	file.Delete("/delete", h.handleDelete)

	fileUpload := file.Group("/upload")
	fileUpload.Get("/", h.handleUploadStatus)
	fileUpload.Post("/", h.handleUploadDispatch)
	fileUpload.Put("/part", h.handleUploadPart)
	fileUpload.Delete("/", h.handleUploadAbort)

	// /file/share (authenticated)
	file.Post("/share", h.handleCreateShare)
	file.Get("/shares", h.handleListShares)
	file.Delete("/share/:id", h.handleDeleteShare)
	file.Get("/shared", h.handleListSharedWithMe)
	file.Get("/shared/:share_id", h.handleShareInfo)

	fileTrash := file.Group("/trash")
	fileTrash.Get("/", h.handleListTrash)
	fileTrash.Post("/restore", h.handleRestoreTrash)
	fileTrash.Delete("/:id", h.handleDeleteTrashItem)
	fileTrash.Delete("/", h.handleClearTrash)

	// /job
	job := authed.Group("/job")
	job.Get("/", h.handleListJobs)
	job.Post("/", h.handleJobDispatch)
	job.Delete("/done", h.handleClearJobs)
	job.Get("/:id/status", h.handleJobStatus)
	job.Delete("/:id", h.handleCancelJob)

	// WebSocket — auth via cookie on HTTP upgrade
	app.Use("/ws", h.Mid.WebSocketUpgrade())
	app.Get("/ws", websocket.New(func(c *websocket.Conn) {
		session := c.Locals("session").(*model.Session)
		sessionID := c.Locals("sessionID").(string)
		h.Hub.HandleConnection(c.Conn, session, sessionID)
	}))

	// Register all WS actions
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
	wrappedHex, err := model.GetUserWrappedKEK(userID)
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
	serverKey, err := auth.ServerKeyFromSecret(h.Config.Server.EncryptionSecret)
	if err != nil {
		return "", err
	}
	wrapped, err := auth.WrapKEK(serverKey, kek)
	if err != nil {
		return "", fmt.Errorf("wrap KEK: %w", err)
	}
	return hex.EncodeToString(wrapped), nil
}

// cloneDirFiles copies file records from srcPrefix to dstPrefix,
// preserving WrappedDEK, plaintext size, and other metadata.
func (h *Handler) cloneDirFiles(userID, srcPrefix, dstPrefix string) {
	records, err := model.ListFilesByPrefix(userID, srcPrefix)
	if err != nil {
		return
	}
	for _, r := range records {
		newPath := dstPrefix + strings.TrimPrefix(r.Path, srcPrefix)
		newName := filepath.Base(strings.TrimSuffix(newPath, "/"))
		var opts []model.UpsertFileOpts
		if r.WrappedDEK != "" {
			opts = append(opts, model.UpsertFileOpts{WrappedDEK: r.WrappedDEK})
		}
		_ = model.UpsertFile(userID, newPath, newName, r.IsDir, r.Size, r.ContentType, r.ContentHash, opts...)
		if r.ThumbnailKey != "" {
			_ = model.UpdateFileThumbnail(userID, newPath, r.ThumbnailKey, r.ThumbnailWrappedDEK, r.MediaWidth, r.MediaHeight, r.MediaDuration)
		}
	}
}
