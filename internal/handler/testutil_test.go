package handler

import (
	"encoding/hex"
	"fmt"
	"testing"
	"time"

	"github.com/gofiber/fiber/v2"

	"domus/config"
	"domus/internal/auth"
	"domus/internal/middleware"
	"domus/internal/model"
	"domus/internal/service"
	"domus/internal/store"
	"domus/internal/terminal"
	"domus/internal/ws"
)

func newTestTerminalManager() *terminal.Manager {
	return terminal.NewManager(cleanupWorkspaceService{}, 4)
}

func setupTestApp(t *testing.T) (*fiber.App, *model.Repos, func(username, password string) string) {
	t.Helper()

	repos := model.NewMemRepos(nil)

	cfg := &config.Config{
		Server: config.ServerConfig{
			Port:             8080,
			SessionSecret:    "test-secret-key",
			EncryptionSecret: "0000000000000000000000000000000000000000000000000000000000000000",
		},
		Upload: config.UploadConfig{
			MaxFileSize: 10 * 1024 * 1024 * 1024,
		},
	}

	serverKey, _ := auth.ServerKeyFromSecret(cfg.Server.EncryptionSecret)
	repos.Sessions.SetPopulateKEK(func(s *model.Session) {
		wrappedHex, err := repos.Users.GetWrappedKEK(s.UserID)
		if err != nil || wrappedHex == "" {
			return
		}
		wrappedBytes, _ := hex.DecodeString(wrappedHex)
		s.KEK, _ = auth.UnwrapKEK(serverKey, wrappedBytes)
	})
	challenges := auth.NewChallengeManager()
	audit := model.NewAuditWorker(nil) // don't call Start — entries buffer but never flush
	mid := middleware.New(repos.Sessions, cfg.Server.SessionSecret)

	mockStore := &MockFileStore{}

	hub := ws.NewHub()

	h := &Handler{
		Config:     cfg,
		Repos:      repos,
		Store:      mockStore,
		Email:      &service.MockEmailSender{},
		Audit:      audit,
		Challenges: challenges,
		Mid:        mid,
		Hub:        hub,
		Workspace:  cleanupWorkspaceService{},
		Terminal:   newTestTerminalManager(),
	}

	app := fiber.New()
	h.RegisterRoutes(app)

	loginAs := func(username, password string) string {
		t.Helper()
		user, err := repos.Users.GetByUsername(username)
		if err != nil {
			kek, _ := auth.GenerateKEK()
			wrapped, _ := auth.WrapKEK(serverKey, kek)
			user, _ = repos.Users.Create(username, password, "root", hex.EncodeToString(wrapped))
		}
		sessionID, err := repos.Sessions.Create(user.ID, username, "root")
		if err != nil {
			t.Fatalf("failed to create session: %v", err)
		}
		return auth.SignCookie(sessionID, cfg.Server.SessionSecret)
	}

	mockStore.HeadObjectFn = func(key string) (*store.HeadResult, error) {
		return &store.HeadResult{Size: 43, ETag: "\"mock-etag\""}, nil
	}

	return app, repos, loginAs
}

// MockFileStore implements the HTTP-facing store.ControlStore for testing.
type MockFileStore struct {
	GeneratePresignedURLFn func(key string, expires time.Duration) (string, error)
	HeadObjectFn           func(key string) (*store.HeadResult, error)
	AbortMultipartUploadFn func(key, uploadID string) error
}

func (m *MockFileStore) GeneratePresignedURL(key string, expires time.Duration) (string, error) {
	if m.GeneratePresignedURLFn != nil {
		return m.GeneratePresignedURLFn(key, expires)
	}
	return fmt.Sprintf("https://mock-oss.example.com/%s?signed=true", key), nil
}
func (m *MockFileStore) PresignedPutObject(key string, expires time.Duration) (string, error) {
	return fmt.Sprintf("https://mock-oss.example.com/%s?method=PUT&signed=true", key), nil
}
func (m *MockFileStore) PresignedDeleteObject(key string, expires time.Duration) (string, error) {
	return fmt.Sprintf("https://mock-oss.example.com/%s?method=DELETE&signed=true", key), nil
}
func (m *MockFileStore) CreateMultipartUpload(key string) (string, error) {
	return "mock-upload-id", nil
}
func (m *MockFileStore) PresignedUploadPart(key, uploadID string, partNumber int, expires time.Duration) (string, error) {
	return fmt.Sprintf("https://mock-oss.example.com/%s?partNumber=%d&uploadId=%s&signed=true", key, partNumber, uploadID), nil
}
func (m *MockFileStore) CompleteMultipartUpload(key, uploadID string, parts []store.CompletePart) error {
	return nil
}
func (m *MockFileStore) AbortMultipartUpload(key, uploadID string) error {
	if m.AbortMultipartUploadFn != nil {
		return m.AbortMultipartUploadFn(key, uploadID)
	}
	return nil
}
func (m *MockFileStore) ListParts(key, uploadID string) ([]store.PartInfo, error) {
	return nil, nil
}
func (m *MockFileStore) HeadObject(key string) (*store.HeadResult, error) {
	if m.HeadObjectFn != nil {
		return m.HeadObjectFn(key)
	}
	return &store.HeadResult{Size: 100, ETag: "\"mock-etag\""}, nil
}
