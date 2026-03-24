package handler

import (
	"fmt"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/gofiber/fiber/v2"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"

	"zephyr/config"
	"zephyr/internal/auth"
	"zephyr/internal/middleware"
	"zephyr/internal/model"
	"zephyr/internal/store"
)

func setupTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_DSN")
	if dsn == "" {
		dsn = "host=localhost port=5432 user=postgres password= dbname=zephyr_test sslmode=disable"
	}
	testDB, err := gorm.Open(postgres.Open(dsn), &gorm.Config{
		Logger: gormlogger.Default.LogMode(gormlogger.Silent),
	})
	if err != nil {
		t.Skipf("skipping test: could not connect to PostgreSQL: %v", err)
	}
	if err := testDB.AutoMigrate(&model.User{}, &model.TrashItem{}, &model.UploadRecord{}, &model.Bookmark{}, &model.FileRecord{}, &model.DBSession{}, &model.Job{}, &model.AuditLog{}); err != nil {
		t.Fatalf("failed to migrate: %v", err)
	}
	testDB.Exec("DELETE FROM users")
	testDB.Exec("DELETE FROM trash")
	testDB.Exec("DELETE FROM uploads")
	testDB.Exec("DELETE FROM bookmarks")
	testDB.Exec("DELETE FROM files")
	testDB.Exec("DELETE FROM sessions")
	testDB.Exec("DELETE FROM jobs")
	return testDB
}

func setupTestApp(t *testing.T) (*fiber.App, func(username, password string) string) {
	t.Helper()
	testDB := setupTestDB(t)

	// Initialize model package's internal db
	model.SetDB(testDB)

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

	sessions := model.NewSessionStore(testDB)
	challenges := auth.NewChallengeManager()
	audit := model.NewAuditWorker(testDB)
	audit.Start()
	mid := middleware.New(sessions, cfg.Server.SessionSecret)

	mockStore := &MockFileStore{}

	h := &Handler{
		Config:     cfg,
		DB:         testDB,
		Store:      mockStore,
		Sessions:   sessions,
		Audit:      audit,
		Challenges: challenges,
		Mid:        mid,
	}

	app := fiber.New()
	h.RegisterRoutes(app)

	loginAs := func(username, password string) string {
		t.Helper()
		user, err := model.GetUserByUsername(username)
		if err != nil {
			user, _ = model.CreateUser(username, password, "admin", model.PermAll)
		}
		sessionID, err := sessions.Create(user.ID, username, "admin", model.PermAll)
		if err != nil {
			t.Fatalf("failed to create session: %v", err)
		}
		return auth.SignCookie(sessionID, cfg.Server.SessionSecret)
	}

	return app, loginAs
}

// MockFileStore implements store.FileStore for testing
type MockFileStore struct {
	ListObjectsFn           func(prefix, marker string, limit int) (*store.ListResult, error)
	GetObjectInfoFn         func(key string) (*store.FileInfo, error)
	CreateDirectoryFn       func(key string) error
	DeleteObjectFn          func(key string) error
	DeleteObjectsFn         func(keys []string) error
	CopyObjectFn            func(srcKey, dstKey string) error
	MoveObjectFn            func(srcKey, dstKey string) error
	ListAllObjectsFn        func(prefix string) ([]store.ObjectInfo, error)
	RecursiveCopyFn         func(srcPrefix, dstPrefix string, progress func(done, total int, current string)) error
	RecursiveMoveFn         func(srcPrefix, dstPrefix string, progress func(done, total int, current string)) error
	RecursiveDeleteFn       func(prefix string, progress func(done, total int, current string)) error
	GetTotalSizeFn          func(prefix string) (int64, int, error)
	GeneratePresignedURLFn  func(key string, expires time.Duration) (string, error)
	GetObjectContentFn      func(key string) (io.ReadCloser, error)
	PutObjectContentFn      func(key, content string) error
	RenameObjectFn          func(oldKey, newKey string, isDir bool) error
	DownloadToFileFn        func(key, localPath string) error
	UploadFromFileFn        func(key, localPath string) error
	PutObjectBytesFn        func(key string, data []byte) error
	GetObjectContentRangeFn func(key string, start, end int64) (io.ReadCloser, error)
}

func (m *MockFileStore) ListObjects(prefix, marker string, limit int) (*store.ListResult, error) {
	if m.ListObjectsFn != nil {
		return m.ListObjectsFn(prefix, marker, limit)
	}
	return &store.ListResult{Files: []store.FileInfo{}}, nil
}
func (m *MockFileStore) GetObjectInfo(key string) (*store.FileInfo, error) {
	if m.GetObjectInfoFn != nil {
		return m.GetObjectInfoFn(key)
	}
	return &store.FileInfo{Name: key, Path: key, Size: 100}, nil
}
func (m *MockFileStore) CreateDirectory(key string) error {
	if m.CreateDirectoryFn != nil {
		return m.CreateDirectoryFn(key)
	}
	return nil
}
func (m *MockFileStore) DeleteObject(key string) error {
	if m.DeleteObjectFn != nil {
		return m.DeleteObjectFn(key)
	}
	return nil
}
func (m *MockFileStore) DeleteObjects(keys []string) error {
	if m.DeleteObjectsFn != nil {
		return m.DeleteObjectsFn(keys)
	}
	return nil
}
func (m *MockFileStore) CopyObject(srcKey, dstKey string) error {
	if m.CopyObjectFn != nil {
		return m.CopyObjectFn(srcKey, dstKey)
	}
	return nil
}
func (m *MockFileStore) MoveObject(srcKey, dstKey string) error {
	if m.MoveObjectFn != nil {
		return m.MoveObjectFn(srcKey, dstKey)
	}
	return nil
}
func (m *MockFileStore) ListAllObjects(prefix string) ([]store.ObjectInfo, error) {
	if m.ListAllObjectsFn != nil {
		return m.ListAllObjectsFn(prefix)
	}
	return []store.ObjectInfo{}, nil
}
func (m *MockFileStore) RecursiveCopy(srcPrefix, dstPrefix string, progress func(done, total int, current string)) error {
	if m.RecursiveCopyFn != nil {
		return m.RecursiveCopyFn(srcPrefix, dstPrefix, progress)
	}
	return nil
}
func (m *MockFileStore) RecursiveMove(srcPrefix, dstPrefix string, progress func(done, total int, current string)) error {
	if m.RecursiveMoveFn != nil {
		return m.RecursiveMoveFn(srcPrefix, dstPrefix, progress)
	}
	return nil
}
func (m *MockFileStore) RecursiveDelete(prefix string, progress func(done, total int, current string)) error {
	if m.RecursiveDeleteFn != nil {
		return m.RecursiveDeleteFn(prefix, progress)
	}
	return nil
}
func (m *MockFileStore) GetTotalSize(prefix string) (int64, int, error) {
	if m.GetTotalSizeFn != nil {
		return m.GetTotalSizeFn(prefix)
	}
	return 0, 0, nil
}
func (m *MockFileStore) GeneratePresignedURL(key string, expires time.Duration) (string, error) {
	if m.GeneratePresignedURLFn != nil {
		return m.GeneratePresignedURLFn(key, expires)
	}
	return fmt.Sprintf("https://mock-oss.example.com/%s?signed=true", key), nil
}
func (m *MockFileStore) GetObjectContent(key string) (io.ReadCloser, error) {
	if m.GetObjectContentFn != nil {
		return m.GetObjectContentFn(key)
	}
	return io.NopCloser(strings.NewReader("mock content")), nil
}
func (m *MockFileStore) PutObjectContent(key, content string) error {
	if m.PutObjectContentFn != nil {
		return m.PutObjectContentFn(key, content)
	}
	return nil
}
func (m *MockFileStore) RenameObject(oldKey, newKey string, isDir bool) error {
	if m.RenameObjectFn != nil {
		return m.RenameObjectFn(oldKey, newKey, isDir)
	}
	return nil
}
func (m *MockFileStore) DownloadToFile(key, localPath string) error {
	if m.DownloadToFileFn != nil {
		return m.DownloadToFileFn(key, localPath)
	}
	return nil
}
func (m *MockFileStore) UploadFromFile(key, localPath string) error {
	if m.UploadFromFileFn != nil {
		return m.UploadFromFileFn(key, localPath)
	}
	return nil
}
func (m *MockFileStore) PutObjectBytes(key string, data []byte) error {
	if m.PutObjectBytesFn != nil {
		return m.PutObjectBytesFn(key, data)
	}
	return nil
}
func (m *MockFileStore) GetObjectContentRange(key string, start, end int64) (io.ReadCloser, error) {
	if m.GetObjectContentRangeFn != nil {
		return m.GetObjectContentRangeFn(key, start, end)
	}
	return io.NopCloser(strings.NewReader("")), nil
}
