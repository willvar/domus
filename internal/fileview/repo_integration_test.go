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
	privateRoot, err := metadata.Lookup(t.Context(), user.ID, dofs.RootInode, ".domus")
	if err != nil || !privateRoot.IsDir() {
		t.Fatalf("private namespace root = %#v, %v", privateRoot, err)
	}
	for _, name := range []string{"trash", "thumbnails", "user"} {
		entry, lookupErr := metadata.Lookup(t.Context(), user.ID, privateRoot.Inode, name)
		if lookupErr != nil || !entry.IsDir() {
			t.Fatalf("private directory %q = %#v, %v", name, entry, lookupErr)
		}
	}
	if legacyHome, lookupErr := metadata.Lookup(t.Context(), user.ID, dofs.RootInode, "home"); legacyHome.Inode != 0 || !errors.Is(lookupErr, dofs.ErrNotFound) {
		t.Fatalf("legacy home directory still exists: %#v, %v", legacyHome, lookupErr)
	}
	repository, err := Open(database, runtime)
	if err != nil {
		t.Fatal(err)
	}
	repositories.Files = repository
	missing, err := repository.ListDirectChildren(user.ID, "/missing/")
	if err != nil || len(missing) != 0 {
		t.Fatalf("missing directory listing = %#v, %v", missing, err)
	}

	namespacePath := "/note.txt"
	uploadID := uuid.NewString()
	fileKey := bytes.Repeat([]byte{0x73}, dofs.KeySize)
	plaintext := []byte("browser encrypted this before the object-store request")
	direct, err := repository.PrepareDirectUpload(
		t.Context(), user.ID, uploadID, namespacePath, int64(len(plaintext)), fileKey, false, 15*time.Minute,
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
	if err := repository.CreateUpload(user.ID, uploadID, "task", "multipart", namespacePath, "note.txt", int64(len(plaintext)), "text/plain", "browser"); err != nil {
		t.Fatal(err)
	}
	published, err := repository.CommitDirectUpload(t.Context(), user.ID, uploadID)
	if err != nil {
		t.Fatal(err)
	}
	if err := repository.Upsert(
		user.ID, namespacePath, "note.txt", false, int64(len(plaintext)), "text/plain", "hash",
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

	record, err := repository.Get(user.ID, namespacePath)
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

	renamed := "/renamed.txt"
	if err := repository.Move(user.ID, namespacePath, renamed, "renamed.txt"); err != nil {
		t.Fatal(err)
	}
	after, err := repository.Get(user.ID, renamed)
	if err != nil {
		t.Fatal(err)
	}
	if after.ID != record.ID || after.Generation != record.Generation || after.StorageKey() != record.StorageKey() {
		t.Fatalf("rename changed stable identity: before=%#v after=%#v", record, after)
	}
	if ghost, err := repository.Get(user.ID, namespacePath); ghost != nil || !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("renamed path resolved through stale upload projection: %#v, %v", ghost, err)
	}

	if err := database.Model(&metadataRecord{}).
		Where("user_id = ? AND inode = ?", user.ID, published.Inode).
		Update("thumbnail", uint64(777)).Error; err != nil {
		t.Fatal(err)
	}
	destination := published
	destination.Inode += 100_000
	destination.Generation = 3
	if err := repository.copyMetadata(user.ID, published, destination); err != nil {
		t.Fatal(err)
	}
	var copied metadataRecord
	if err := database.Where("user_id = ? AND inode = ?", user.ID, destination.Inode).First(&copied).Error; err != nil {
		t.Fatal(err)
	}
	if copied.Thumbnail != 0 {
		t.Fatalf("copied metadata inherited thumbnail inode %d", copied.Thumbnail)
	}
	if err := repository.Upsert(
		user.ID, renamed, "renamed.txt", false, int64(len(plaintext)), "text/plain", "replacement-hash",
	); err != nil {
		t.Fatal(err)
	}
	var replaced metadataRecord
	if err := database.Where("user_id = ? AND inode = ?", user.ID, published.Inode).First(&replaced).Error; err != nil {
		t.Fatal(err)
	}
	if replaced.Thumbnail != 0 || replaced.MediaWidth != 0 || replaced.MediaHeight != 0 || replaced.MediaDuration != 0 {
		t.Fatalf("new content retained generation-scoped media metadata: %#v", replaced)
	}

	// A terminal upload row exists to make completion retries idempotent. File
	// deletion must remove that transient projection, and the same logical name
	// must immediately be reusable for an unrelated inode.
	if err := repository.Delete(user.ID, renamed); err != nil {
		t.Fatal(err)
	}
	if ghost, err := repository.Get(user.ID, renamed); ghost != nil || !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("deleted path resolved through stale upload projection: %#v, %v", ghost, err)
	}
	var staleUploads int64
	if err := database.Model(&uploadRecord{}).Where("user_id = ? AND inode = ?", user.ID, published.Inode).Count(&staleUploads).Error; err != nil {
		t.Fatal(err)
	}
	if staleUploads != 0 {
		t.Fatalf("deleted inode retained %d upload projections", staleUploads)
	}
	namespaceAfterDelete, err := metadata.GetNamespace(t.Context(), user.ID)
	if err != nil {
		t.Fatal(err)
	}
	if namespaceAfterDelete.UsedBytes != 0 || namespaceAfterDelete.PendingReclaimBytes != int64(len(plaintext)) {
		t.Fatalf("usage after permanent delete = %#v", namespaceAfterDelete)
	}
	reclaimed, err := runtime.ReclaimNamespace(t.Context(), user.ID)
	if err != nil || reclaimed.Purged != 1 {
		t.Fatalf("physical reclaim = %#v, %v", reclaimed, err)
	}
	if _, exists := objects.Bytes(published.ObjectKey); exists {
		t.Fatal("permanently deleted ciphertext remained in object storage")
	}
	namespaceAfterReclaim, err := metadata.GetNamespace(t.Context(), user.ID)
	if err != nil || namespaceAfterReclaim.PendingReclaimBytes != 0 {
		t.Fatalf("usage after physical reclaim = %#v, %v", namespaceAfterReclaim, err)
	}

	reuploadID := uuid.NewString()
	reuploadKey := bytes.Repeat([]byte{0x74}, dofs.KeySize)
	reuploadPlaintext := []byte("same pathname, new inode")
	reupload, err := repository.PrepareDirectUpload(
		t.Context(), user.ID, reuploadID, renamed, int64(len(reuploadPlaintext)), reuploadKey, false, 15*time.Minute,
	)
	if err != nil {
		t.Fatalf("reserve deleted pathname again: %v", err)
	}
	var reuploadCiphertext bytes.Buffer
	if err := dofs.EncryptStream(reuploadKey, bytes.NewReader(reuploadPlaintext), &reuploadCiphertext); err != nil {
		t.Fatal(err)
	}
	if err := objects.Put(t.Context(), reupload.ObjectKey, bytes.NewReader(reuploadCiphertext.Bytes()), int64(reuploadCiphertext.Len())); err != nil {
		t.Fatal(err)
	}
	if err := repository.CreateUpload(
		user.ID, reuploadID, "second-task", "second-multipart", renamed, "renamed.txt",
		int64(len(reuploadPlaintext)), "text/plain", "browser",
	); err != nil {
		t.Fatal(err)
	}
	republished, err := repository.CommitDirectUpload(t.Context(), user.ID, reuploadID)
	if err != nil {
		t.Fatal(err)
	}
	if err := repository.Upsert(
		user.ID, renamed, "renamed.txt", false, int64(len(reuploadPlaintext)), "text/plain", "second-hash",
	); err != nil {
		t.Fatal(err)
	}
	if err := repository.UpdateStatus(reuploadID, "ready"); err != nil {
		t.Fatal(err)
	}
	if err := repository.AcknowledgeDirectUpload(t.Context(), user.ID, reuploadID); err != nil {
		t.Fatal(err)
	}
	reused, err := repository.Get(user.ID, renamed)
	if err != nil {
		t.Fatal(err)
	}
	if reused.ID != int64(republished.Inode) || reused.ID == record.ID || reused.Generation != 1 {
		t.Fatalf("reused pathname projection = %#v", reused)
	}
}

func filepathForTest(t *testing.T, name string) string {
	t.Helper()
	return t.TempDir() + "/" + name
}
