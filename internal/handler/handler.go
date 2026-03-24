package handler

import (
	"mime"
	"path/filepath"
	"strings"

	"github.com/gofiber/fiber/v2"
	"gorm.io/gorm"

	"zephyr/config"
	"zephyr/internal/auth"
	"zephyr/internal/middleware"
	"zephyr/internal/model"
	"zephyr/internal/service"
	"zephyr/internal/store"
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
}

// RegisterRoutes registers all API routes on the Fiber app.
func (h *Handler) RegisterRoutes(app *fiber.App) {
	// Auth routes (public)
	app.Get("/auth/config", h.handleAuthConfig)
	app.Post("/auth/verify", h.handleVerify)
	app.Post("/auth/login", h.handleLogin)

	// Auth routes (authenticated)
	authed := app.Group("", h.Mid.AuthRequired())
	authed.Post("/auth/logout", h.handleLogout)
	authed.Get("/auth/me", h.handleMe)

	// Account security routes
	authed.Get("/account/security", h.handleSecurityStatus)
	authed.Post("/account/email/bind", h.handleBindEmail)
	authed.Post("/account/email/verify", h.handleVerifyBindEmail)
	authed.Delete("/account/email", h.handleUnbindEmail)
	authed.Post("/account/otp/setup", h.handleOTPSetup)
	authed.Post("/account/otp/enable", h.handleOTPEnable)
	authed.Delete("/account/otp", h.handleOTPDisable)
	authed.Put("/account/password", h.handleChangePassword)

	// Admin routes
	admin := authed.Group("/admin", middleware.AdminRequired())
	admin.Get("/users", h.handleListUsers)
	admin.Post("/users", h.handleCreateUser)
	admin.Put("/users/:id", h.handleUpdateUser)
	admin.Delete("/users/:id", h.handleDeleteUser)
	admin.Delete("/users/:id/otp", h.handleResetUserOTP)
	admin.Delete("/users/:id/email", h.handleResetUserEmail)
	admin.Get("/audit", h.handleListAuditLogs)

	// Audit preview
	authed.Post("/audit/preview", h.handleAuditPreview)

	// Bookmarks
	authed.Get("/bookmarks", h.handleListBookmarks)
	authed.Post("/bookmarks", h.handleCreateBookmark)
	authed.Put("/bookmarks/:id", h.handleUpdateBookmark)
	authed.Delete("/bookmarks/:id", h.handleDeleteBookmark)

	// File operations
	authed.Get("/list", middleware.PermissionRequired(model.PermRead), h.handleList)
	authed.Get("/info", middleware.PermissionRequired(model.PermRead), h.handleInfo)
	authed.Get("/download", middleware.PermissionRequired(model.PermRead), h.handleDownload)
	authed.Get("/raw", middleware.PermissionRequired(model.PermRead), h.handleRawFile)
	authed.Get("/content", middleware.PermissionRequired(model.PermRead), h.handleGetContent)
	authed.Get("/trash", middleware.PermissionRequired(model.PermRead), h.handleListTrash)
	authed.Get("/upload/status", middleware.PermissionRequired(model.PermRead), h.handleUploadStatus)

	authed.Post("/upload/check-conflicts", middleware.PermissionRequired(model.PermUpload), h.handleCheckConflicts)
	authed.Post("/upload/init", middleware.PermissionRequired(model.PermUpload), h.handleUploadInit)
	authed.Put("/upload/part", middleware.PermissionRequired(model.PermUpload), h.handleUploadPart)
	authed.Post("/upload/complete", middleware.PermissionRequired(model.PermUpload), h.handleUploadComplete)
	authed.Delete("/upload/abort", middleware.PermissionRequired(model.PermUpload), h.handleUploadAbort)
	authed.Post("/mkdir", middleware.PermissionRequired(model.PermUpload), h.handleMkdir)

	authed.Put("/content", middleware.PermissionRequired(model.PermEdit), h.handlePutContent)
	authed.Put("/content/diff", middleware.PermissionRequired(model.PermEdit), h.handlePatchContent)
	authed.Post("/rename", middleware.PermissionRequired(model.PermEdit), h.handleRename)
	authed.Post("/copy", middleware.PermissionRequired(model.PermEdit), h.handleCopy)
	authed.Post("/move", middleware.PermissionRequired(model.PermEdit), h.handleMove)

	authed.Delete("/delete", middleware.PermissionRequired(model.PermDelete), h.handleDelete)
	authed.Post("/trash/restore", middleware.PermissionRequired(model.PermDelete), h.handleRestoreTrash)
	authed.Delete("/trash/:id", middleware.PermissionRequired(model.PermDelete), h.handleDeleteTrashItem)
	authed.Delete("/trash", middleware.PermissionRequired(model.PermDelete), h.handleClearTrash)

	// Jobs
	authed.Get("/jobs", h.handleListJobs)
	authed.Delete("/jobs", h.handleClearJobs)
	authed.Get("/jobs/:id/status", h.handleJobStatus)
	authed.Delete("/jobs/:id", h.handleCancelJob)

	// Transcode
	authed.Post("/transcode/start", middleware.PermissionRequired(model.PermEdit), h.handleTranscodeStart)
}

// getFileEncryptionKey resolves the correct encryption key for the given file path.
func (h *Handler) getFileEncryptionKey(session *model.Session, resolvedPath string) ([]byte, error) {
	userID := session.UserID
	if session.Role == "admin" {
		var record model.FileRecord
		if err := h.DB.Where("path = ?", resolvedPath).First(&record).Error; err == nil {
			userID = record.UserID
		}
	}
	return auth.DeriveKey(h.Config.Server.EncryptionSecret, userID)
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
