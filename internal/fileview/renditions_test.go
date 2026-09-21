package fileview

import (
	"bytes"
	"encoding/hex"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/willvar/dofs"
	dofssqlite "github.com/willvar/dofs/metadata/sqlite"
	objectmemory "github.com/willvar/dofs/object/memory"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"domus/internal/dofsbridge"
	"domus/internal/model"
)

// renditionTestEnv is a minimal DOFS runtime + projection for rendition tests.
type renditionTestEnv struct {
	database   *gorm.DB
	repository *Repo
	objects    *objectmemory.Store
	userID     string
}

func newRenditionTestEnv(t *testing.T) *renditionTestEnv {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_DSN")
	if dsn == "" {
		dsn = "host=localhost port=5432 user=postgres dbname=postgres sslmode=disable"
	}
	database, err := gorm.Open(postgres.Open(dsn), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Skipf("PostgreSQL test database is unavailable: %v", err)
	}
	sqlDB, err := database.DB()
	if err != nil {
		t.Skipf("PostgreSQL test pool is unavailable: %v", err)
	}
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = sqlDB.Close() })
	schema := "domus_test_renditions_" + uuid.NewString()[:8]
	if err := database.Exec("CREATE SCHEMA IF NOT EXISTS " + schema).Error; err != nil {
		t.Skipf("cannot create isolated PostgreSQL test schema: %v", err)
	}
	if err := database.Exec("SET search_path TO " + schema + ", public").Error; err != nil {
		t.Fatalf("select isolated PostgreSQL test schema: %v", err)
	}
	if err := database.AutoMigrate(&model.User{}); err != nil {
		t.Skipf("cannot migrate PostgreSQL test database: %v", err)
	}

	master := bytes.Repeat([]byte{0x31}, dofs.KeySize)
	keys, err := dofs.NewEnvelopeKeyProvider(master)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { keys.Close() })
	namespaceKey := bytes.Repeat([]byte{0x52}, dofs.KeySize)
	wrappedNamespaceKey, err := dofs.WrapKey(master, namespaceKey)
	if err != nil {
		t.Fatal(err)
	}
	repositories := model.NewRepos(database, nil)
	username := "rendition-" + uuid.NewString()[:8]
	user, err := repositories.Users.Create(username, "password", "user", hex.EncodeToString(wrappedNamespaceKey))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = database.Exec("DELETE FROM domus_file_uploads WHERE user_id = ?", user.ID).Error
		_ = database.Exec("DELETE FROM domus_file_metadata WHERE user_id = ?", user.ID).Error
		_ = database.Exec("DELETE FROM domus_file_renditions WHERE user_id = ?", user.ID).Error
		_ = database.Where("id = ?", user.ID).Delete(&model.User{}).Error
	})

	metadata, err := dofssqlite.Open(t.Context(), dofssqlite.Config{
		Path: t.TempDir() + "/metadata.sqlite", BusyTimeout: time.Second, MaxOpenConnections: 2,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { metadata.Close() })
	if err := metadata.Migrate(t.Context()); err != nil {
		t.Fatal(err)
	}
	objects := objectmemory.New()
	runtime := &dofsbridge.Runtime{Metadata: metadata, Objects: objects, Keys: keys, Users: repositories.Users}
	if err := runtime.EnsureUser(t.Context(), user); err != nil {
		t.Fatal(err)
	}
	repository, err := Open(database, runtime)
	if err != nil {
		t.Fatal(err)
	}
	return &renditionTestEnv{database: database, repository: repository, objects: objects, userID: user.ID}
}

// publishTestVideo publishes a small file as a video source through the
// browser direct-upload path and returns its inode and object key.
func (env *renditionTestEnv) publishTestVideo(t *testing.T, name string) (int64, string) {
	t.Helper()
	namespacePath := "/" + name
	uploadID := uuid.NewString()
	fileKey := bytes.Repeat([]byte{0x61}, dofs.KeySize)
	plaintext := []byte("transcoded source bytes")
	direct, err := env.repository.PrepareDirectUpload(
		t.Context(), env.userID, uploadID, namespacePath, int64(len(plaintext)), fileKey, false, 15*time.Minute,
	)
	if err != nil {
		t.Fatal(err)
	}
	var ciphertext bytes.Buffer
	if err := dofs.EncryptStream(fileKey, bytes.NewReader(plaintext), &ciphertext); err != nil {
		t.Fatal(err)
	}
	if err := env.objects.Put(t.Context(), direct.ObjectKey, bytes.NewReader(ciphertext.Bytes()), int64(ciphertext.Len())); err != nil {
		t.Fatal(err)
	}
	if err := env.repository.CreateUpload(env.userID, uploadID, "task", "multipart", namespacePath, name, int64(len(plaintext)), "video/mp4", "browser"); err != nil {
		t.Fatal(err)
	}
	published, err := env.repository.CommitDirectUpload(t.Context(), env.userID, uploadID)
	if err != nil {
		t.Fatal(err)
	}
	if err := env.repository.Upsert(
		env.userID, namespacePath, name, false, int64(len(plaintext)), "video/mp4", "hash",
		model.UpsertFileOpts{WrappedDEK: hex.EncodeToString(published.WrappedDEK), ObjectKey: published.ObjectKey},
	); err != nil {
		t.Fatal(err)
	}
	if err := env.repository.UpdateStatus(uploadID, "ready"); err != nil {
		t.Fatal(err)
	}
	if err := env.repository.AcknowledgeDirectUpload(t.Context(), env.userID, uploadID); err != nil {
		t.Fatal(err)
	}
	return int64(published.Inode), published.ObjectKey
}

func TestRenditionWorkflow(t *testing.T) {
	env := newRenditionTestEnv(t)
	sourceInode, sourceKey := env.publishTestVideo(t, "video.mp4")

	rendition, err := env.repository.CreateRendition(env.userID, &model.FileRecord{
		ID: sourceInode, Path: "/" + "video.mp4", Name: "video.mp4", Status: "ready", Generation: 1,
	}, "720p")
	if err != nil {
		t.Fatalf("CreateRendition: %v", err)
	}
	if rendition.Status != "queued" || rendition.SourceGeneration != 1 {
		t.Fatalf("rendition = %#v", rendition)
	}

	if err := env.repository.SetRenditionTask(env.userID, uint64(sourceInode), "720p", "task-1"); err != nil {
		t.Fatalf("SetRenditionTask: %v", err)
	}
	if err := env.repository.SetRenditionInit(env.userID, uint64(sourceInode), "720p", "avc1.640028,mp4a.40.2", 1280, 720, uint64(sourceInode)); err != nil {
		t.Fatalf("SetRenditionInit: %v", err)
	}
	if err := env.repository.UpdateRenditionStatus(env.userID, uint64(sourceInode), "720p", "running", ""); err != nil {
		t.Fatalf("UpdateRenditionStatus: %v", err)
	}

	for _, duration := range []float64{4, 3.5} {
		if err := env.repository.AppendRenditionSegment(env.userID, uint64(sourceInode), "720p", uint64(sourceInode), duration); err != nil {
			t.Fatalf("AppendRenditionSegment: %v", err)
		}
	}

	fetched, err := env.repository.GetRendition(env.userID, uint64(sourceInode), "720p")
	if err != nil {
		t.Fatalf("GetRendition: %v", err)
	}
	if fetched.Status != "running" || fetched.TaskID != "task-1" || fetched.Codecs != "avc1.640028,mp4a.40.2" {
		t.Fatalf("fetched rendition = %#v", fetched)
	}
	if len(fetched.Segments) != 2 || fetched.Segments[0].Duration != 4 || fetched.Segments[1].Duration != 3.5 {
		t.Fatalf("segments = %#v", fetched.Segments)
	}
	for _, segment := range fetched.Segments {
		if segment.ObjectKey != sourceKey {
			t.Fatalf("segment artifact resolved to %q, want source key %q", segment.ObjectKey, sourceKey)
		}
		if segment.WrappedDEK == "" || segment.Size <= 0 {
			t.Fatalf("segment artifact not resolved: %#v", segment)
		}
	}

	listed, err := env.repository.ListRenditions(env.userID, uint64(sourceInode))
	if err != nil || len(listed) != 1 || listed[0].Profile != "720p" {
		t.Fatalf("ListRenditions = %#v, %v", listed, err)
	}

	if err := env.repository.FailRendition(env.userID, uint64(sourceInode), "720p", "probe failed"); err != nil {
		t.Fatalf("FailRendition: %v", err)
	}
	failed, err := env.repository.GetRendition(env.userID, uint64(sourceInode), "720p")
	if err != nil {
		t.Fatalf("GetRendition after fail: %v", err)
	}
	if failed.Status != "failed" || failed.Error != "probe failed" || failed.Init != nil || len(failed.Segments) != 0 {
		t.Fatalf("failed rendition = %#v", failed)
	}

	if _, err := env.repository.CreateRendition(env.userID, &model.FileRecord{
		ID: sourceInode, Path: "/video.mp4", Name: "video.mp4", Status: "ready", Generation: 1,
	}, "720p"); err != nil {
		t.Fatalf("recreate rendition after failure: %v", err)
	}
	reset, err := env.repository.GetRendition(env.userID, uint64(sourceInode), "720p")
	if err != nil || reset.Status != "queued" || reset.Init != nil || len(reset.Segments) != 0 {
		t.Fatalf("reset rendition = %#v, %v", reset, err)
	}

	if err := env.repository.DeleteRendition(env.userID, uint64(sourceInode), "720p"); err != nil {
		t.Fatalf("DeleteRendition: %v", err)
	}
	if missing, err := env.repository.GetRendition(env.userID, uint64(sourceInode), "720p"); missing != nil || err == nil {
		t.Fatalf("rendition survived deletion: %#v, %v", missing, err)
	}
	if listed, err := env.repository.ListRenditions(env.userID, uint64(sourceInode)); err != nil || len(listed) != 0 {
		t.Fatalf("ListRenditions after delete = %#v, %v", listed, err)
	}
}

func TestResetRenditionsForTasks(t *testing.T) {
	env := newRenditionTestEnv(t)
	sourceInode, _ := env.publishTestVideo(t, "video.mp4")

	if _, err := env.repository.CreateRendition(env.userID, &model.FileRecord{
		ID: sourceInode, Path: "/video.mp4", Name: "video.mp4", Status: "ready", Generation: 1,
	}, "480p"); err != nil {
		t.Fatal(err)
	}
	if err := env.repository.SetRenditionTask(env.userID, uint64(sourceInode), "480p", "stale-task"); err != nil {
		t.Fatal(err)
	}
	if err := env.repository.SetRenditionInit(env.userID, uint64(sourceInode), "480p", "avc1.640028,mp4a.40.2", 640, 480, uint64(sourceInode)); err != nil {
		t.Fatal(err)
	}
	if err := env.repository.AppendRenditionSegment(env.userID, uint64(sourceInode), "480p", uint64(sourceInode), 4); err != nil {
		t.Fatal(err)
	}
	if err := env.repository.UpdateRenditionStatus(env.userID, uint64(sourceInode), "480p", "running", ""); err != nil {
		t.Fatal(err)
	}

	if err := env.repository.ResetRenditionsForTasks(env.userID, []string{"stale-task"}); err != nil {
		t.Fatalf("ResetRenditionsForTasks: %v", err)
	}
	reset, err := env.repository.GetRendition(env.userID, uint64(sourceInode), "480p")
	if err != nil {
		t.Fatalf("GetRendition after reset: %v", err)
	}
	if reset.Status != "queued" || reset.Init != nil || len(reset.Segments) != 0 {
		t.Fatalf("reset rendition = %#v", reset)
	}
}
