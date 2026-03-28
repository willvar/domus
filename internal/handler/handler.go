package handler

import (
	"mime"
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
}

// RegisterRoutes registers all API routes on the Fiber app.
func (h *Handler) RegisterRoutes(app *fiber.App) {
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
	user.Get("/bookmark", h.handleListBookmarks)
	user.Post("/bookmark", h.handleCreateBookmark)
	user.Put("/bookmark/:id", h.handleUpdateBookmark)
	user.Delete("/bookmark/:id", h.handleDeleteBookmark)

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
	file.Get("/", middleware.PermissionRequired(model.PermRead), h.handleList)
	file.Get("/download", middleware.PermissionRequired(model.PermRead), h.handleDownload)
	file.Get("/content/raw", middleware.PermissionRequired(model.PermRead), h.handleRawFile)
	file.Put("/content/diff", middleware.PermissionRequired(model.PermEdit), h.handlePatchContent)
	file.Post("/mkdir", middleware.PermissionRequired(model.PermUpload), h.handleMkdir)
	file.Post("/rename", middleware.PermissionRequired(model.PermEdit), h.handleRename)
	file.Post("/copy", middleware.PermissionRequired(model.PermEdit), h.handleCopy)
	file.Post("/move", middleware.PermissionRequired(model.PermEdit), h.handleMove)
	file.Delete("/delete", middleware.PermissionRequired(model.PermDelete), h.handleDelete)

	fileUpload := file.Group("/upload")
	fileUpload.Get("/", middleware.PermissionRequired(model.PermRead), h.handleUploadStatus)
	fileUpload.Post("/", middleware.PermissionRequired(model.PermUpload), h.handleUploadDispatch)
	fileUpload.Put("/part", middleware.PermissionRequired(model.PermUpload), h.handleUploadPart)
	fileUpload.Delete("/", middleware.PermissionRequired(model.PermUpload), h.handleUploadAbort)

	fileTrash := file.Group("/trash")
	fileTrash.Get("/", middleware.PermissionRequired(model.PermRead), h.handleListTrash)
	fileTrash.Post("/restore", middleware.PermissionRequired(model.PermDelete), h.handleRestoreTrash)
	fileTrash.Delete("/:id", middleware.PermissionRequired(model.PermDelete), h.handleDeleteTrashItem)
	fileTrash.Delete("/", middleware.PermissionRequired(model.PermDelete), h.handleClearTrash)

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

// getFileEncryptionKey derives the encryption key for the current user's files.
func (h *Handler) getFileEncryptionKey(session *model.Session) ([]byte, error) {
	return auth.DeriveKey(h.Config.Server.EncryptionSecret, session.UserID)
}

// syncDirFiles scans OSS objects under a prefix and upserts them all into the files table.
func (h *Handler) syncDirFiles(userID string, prefix string) {
	objects, err := h.Store.ListAllObjects(prefix)
	if err != nil {
		return
	}
	for _, obj := range objects {
		name := filepath.Base(strings.TrimSuffix(obj.Key, "/"))
		isDir := strings.HasSuffix(obj.Key, "/")
		ct := ""
		if !isDir {
			ct = mime.TypeByExtension(filepath.Ext(obj.Key))
		}
		_ = model.UpsertFile(userID, obj.Key, name, isDir, obj.Size, ct, "")
	}
}
