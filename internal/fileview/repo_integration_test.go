package fileview

import (
	"bytes"
	"encoding/hex"
	"errors"
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

func TestSQLiteDOFSDirectUploadProjectionAndStableRename(t *testing.T) {
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
	sqlDB.SetMaxIdleConns(1)
	t.Cleanup(func() { _ = sqlDB.Close() })
	if err := database.Exec("CREATE SCHEMA IF NOT EXISTS domus_test_fileview").Error; err != nil {
		t.Skipf("cannot create isolated PostgreSQL test schema: %v", err)
	}
	if err := database.Exec("SET search_path TO domus_test_fileview, public").Error; err != nil {
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
	defer keys.Close()
	namespaceKey := bytes.Repeat([]byte{0x52}, dofs.KeySize)
	wrappedNamespaceKey, err := dofs.WrapKey(master, namespaceKey)
	if err != nil {
		t.Fatal(err)
	}
	repositories := model.NewRepos(database, nil)
	username := "fileview-" + uuid.NewString()[:8]
	user, err := repositories.Users.Create(username, "password", "user", hex.EncodeToString(wrappedNamespaceKey))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = database.Exec("DELETE FROM domus_file_uploads WHERE user_id = ?", user.ID).Error
		_ = database.Exec("DELETE FROM domus_file_metadata WHERE user_id = ?", user.ID).Error
		_ = database.Where("id = ?", user.ID).Delete(&model.User{}).Error
	})

	metadata, err := dofssqlite.Open(t.Context(), dofssqlite.Config{
		Path: filepathForTest(t, "metadata.sqlite"), BusyTimeout: time.Second, MaxOpenConnections: 2,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer metadata.Close()
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
	repositories.Files = repository
	missing, err := repository.ListDirectChildren(user.ID, username+"/missing/")
	if err != nil || len(missing) != 0 {
		t.Fatalf("missing directory listing = %#v, %v", missing, err)
	}

	physical := username + "/home/" + username + "/note.txt"
	uploadID := uuid.NewString()
	fileKey := bytes.Repeat([]byte{0x73}, dofs.KeySize)
	plaintext := []byte("browser encrypted this before the object-store request")
	direct, err := repository.PrepareDirectUpload(
		t.Context(), user.ID, uploadID, physical, int64(len(plaintext)), fileKey, false, 15*time.Minute,
	)
	if err != nil {
		t.Fatal(err)
	}
	if pending, err := repository.GetByID(user.ID, int64(direct.Inode)); pending != nil || !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("reserved inode projection = %#v, %v; want nil, record not found", pending, err)
	}
	var ciphertext bytes.Buffer
	if err := dofs.EncryptStream(fileKey, bytes.NewReader(plaintext), &ciphertext); err != nil {
		t.Fatal(err)
	}
	if err := objects.Put(t.Context(), direct.ObjectKey, bytes.NewReader(ciphertext.Bytes()), int64(ciphertext.Len())); err != nil {
		t.Fatal(err)
	}
	if err := repository.CreateUpload(user.ID, uploadID, "task", "multipart", physical, "note.txt", int64(len(plaintext)), "text/plain", "browser"); err != nil {
		t.Fatal(err)
	}
	published, err := repository.CommitDirectUpload(t.Context(), user.ID, uploadID)
	if err != nil {
		t.Fatal(err)
	}
	if err := repository.Upsert(
		user.ID, physical, "note.txt", false, int64(len(plaintext)), "text/plain", "hash",
		model.UpsertFileOpts{WrappedDEK: hex.EncodeToString(published.WrappedDEK), ObjectKey: published.ObjectKey},
	); err != nil {
		t.Fatal(err)
	}
	if err := repository.UpdateStatus(uploadID, "ready"); err != nil {
		t.Fatal(err)
	}
	if err := repository.AcknowledgeDirectUpload(t.Context(), user.ID, uploadID); err != nil {
		t.Fatal(err)
	}
	publishedNode, err := metadata.GetNode(t.Context(), user.ID, published.Inode)
	if err != nil {
		t.Fatalf("published DOFS inode disappeared after acknowledgement: %v", err)
	}
	if publishedNode.State != dofs.NodeStateReady || publishedNode.Name != "note.txt" {
		t.Fatalf("unexpected published DOFS inode: %#v", publishedNode)
	}

	record, err := repository.Get(user.ID, physical)
	if err != nil {
		t.Fatal(err)
	}
	if record.ID != int64(published.Inode) || record.Generation != 1 || record.StorageKey() != published.ObjectKey {
		t.Fatalf("published projection = %#v", record)
	}
	control, err := runtime.OpenControl(t.Context(), user.ID)
	if err != nil {
		t.Fatal(err)
	}
	read, err := control.ReadAt(t.Context(), published, 0, len(plaintext))
	_ = control.Close()
	if err != nil || !bytes.Equal(read, plaintext) {
		t.Fatalf("DOFS read = %q, %v", read, err)
	}

	renamed := username + "/home/" + username + "/renamed.txt"
	if err := repository.Move(user.ID, physical, renamed, "renamed.txt"); err != nil {
		t.Fatal(err)
	}
	after, err := repository.Get(user.ID, renamed)
	if err != nil {
		t.Fatal(err)
	}
	if after.ID != record.ID || after.Generation != record.Generation || after.StorageKey() != record.StorageKey() {
		t.Fatalf("rename changed stable identity: before=%#v after=%#v", record, after)
	}
}

func filepathForTest(t *testing.T, name string) string {
	t.Helper()
	return t.TempDir() + "/" + name
}
