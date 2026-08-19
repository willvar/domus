package dofsbridge_test

import (
	"bytes"
	"context"
	"crypto/rand"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/willvar/dofs"

	"domus/config"
	"domus/internal/auth"
	"domus/internal/dofsbridge"
	"domus/internal/model"
)

// TestLiveObjectReclamation is opt-in because it writes one short-lived,
// encrypted fixture to the configured production-compatible object store.
// It proves that permanent deletion drains both metadata accounting and the
// physical S3/OSS object, rather than merely hiding the pathname.
func TestLiveObjectReclamation(t *testing.T) {
	configPath := strings.TrimSpace(os.Getenv("DOMUS_DOFS_LIVE_CONFIG"))
	if configPath == "" {
		t.Skip("set DOMUS_DOFS_LIVE_CONFIG to a generated Domus config")
	}
	cfg, err := config.Load(configPath)
	if err != nil {
		t.Fatal(err)
	}
	database, err := model.InitDB(cfg.Database)
	if err != nil {
		t.Fatal(err)
	}
	sqlDatabase, err := database.DB()
	if err != nil {
		t.Fatal(err)
	}
	defer sqlDatabase.Close()
	repositories := model.NewRepos(database, nil)
	users, err := repositories.Users.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(users) == 0 {
		t.Fatal("live Domus instance has no namespace owner")
	}
	user := &users[0]
	if root, rootErr := repositories.Users.GetByUsername("root"); rootErr == nil {
		user = root
	}

	serverKey, err := auth.ServerKeyFromSecret(cfg.Server.EncryptionSecret)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		for index := range serverKey {
			serverKey[index] = 0
		}
	}()
	runtime, err := dofsbridge.Open(t.Context(), database, cfg, serverKey, repositories.Users)
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()
	if err := runtime.EnsureUser(t.Context(), user); err != nil {
		t.Fatal(err)
	}
	if inspector, ok := runtime.Objects.(interface {
		VersioningStatus(context.Context) (string, error)
	}); ok {
		status, err := inspector.VersioningStatus(t.Context())
		if err != nil {
			t.Fatalf("inspect object-store bucket versioning: %v", err)
		}
		if status != "" {
			t.Fatalf("object-store bucket versioning is %q; noncurrent objects require lifecycle cleanup", status)
		}
	}
	controller, err := runtime.OpenControl(t.Context(), user.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer controller.Close()

	baseline, err := runtime.Metadata.GetNamespace(t.Context(), user.ID)
	if err != nil {
		t.Fatal(err)
	}
	name := ".dofs-live-reclaim-" + uuid.NewString() + ".bin"
	uploadID := "live-reclaim-" + uuid.NewString()
	plaintext := []byte("verify permanent deletion against the configured object store")
	fileKey := make([]byte, dofs.KeySize)
	if _, err := rand.Read(fileKey); err != nil {
		t.Fatal(err)
	}
	defer dofs.Clear(fileKey)
	upload, err := controller.PrepareDirectUpload(t.Context(), dofs.DirectUploadRequest{
		ID: uploadID, Parent: dofs.RootInode, Name: name, Size: int64(len(plaintext)),
		FileKey: fileKey, TTL: 5 * time.Minute,
	})
	if err != nil {
		t.Fatal(err)
	}
	published := false
	defer func() {
		cleanupCtx, cancel := contextWithTimeout(t, 30*time.Second)
		defer cancel()
		if published {
			_ = controller.Remove(cleanupCtx, dofs.RootInode, name, false)
		} else {
			_ = controller.AbortDirectUpload(cleanupCtx, upload.ID)
			_ = runtime.Objects.Delete(cleanupCtx, upload.ObjectKey)
		}
		_, _ = runtime.ReclaimNamespace(cleanupCtx, user.ID)
	}()

	var ciphertext bytes.Buffer
	if err := dofs.EncryptStream(fileKey, bytes.NewReader(plaintext), &ciphertext); err != nil {
		t.Fatal(err)
	}
	if err := runtime.Objects.Put(t.Context(), upload.ObjectKey, bytes.NewReader(ciphertext.Bytes()), int64(ciphertext.Len())); err != nil {
		t.Fatal(err)
	}
	node, err := controller.CommitDirectUpload(t.Context(), upload.ID)
	if err != nil {
		t.Fatal(err)
	}
	published = true
	if err := controller.AcknowledgeDirectUpload(t.Context(), upload.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.Objects.Stat(t.Context(), node.ObjectKey); err != nil {
		t.Fatalf("published ciphertext is unavailable: %v", err)
	}
	afterUpload, err := runtime.Metadata.GetNamespace(t.Context(), user.ID)
	if err != nil {
		t.Fatal(err)
	}
	if afterUpload.UsedBytes != baseline.UsedBytes+int64(len(plaintext)) {
		t.Fatalf("used bytes after upload = %d, want %d", afterUpload.UsedBytes, baseline.UsedBytes+int64(len(plaintext)))
	}
	if err := controller.Remove(t.Context(), dofs.RootInode, name, false); err != nil {
		t.Fatal(err)
	}
	published = false
	afterDelete, err := runtime.Metadata.GetNamespace(t.Context(), user.ID)
	if err != nil {
		t.Fatal(err)
	}
	if afterDelete.UsedBytes != baseline.UsedBytes ||
		afterDelete.PendingReclaimBytes > baseline.PendingReclaimBytes+int64(len(plaintext)) {
		t.Fatalf("usage after unlink = used %d, pending %d", afterDelete.UsedBytes, afterDelete.PendingReclaimBytes)
	}
	deadline := time.Now().Add(30 * time.Second)
	for {
		result, reclaimErr := runtime.ReclaimNamespace(t.Context(), user.ID)
		if reclaimErr != nil {
			t.Fatalf("live reclamation = %#v, %v", result, reclaimErr)
		}
		_, objectErr := runtime.Objects.Stat(t.Context(), node.ObjectKey)
		_, inodeErr := runtime.Metadata.GetNode(t.Context(), user.ID, node.Inode)
		afterReclaim, usageErr := runtime.Metadata.GetNamespace(t.Context(), user.ID)
		if errors.Is(objectErr, dofs.ErrObjectNotFound) && errors.Is(inodeErr, dofs.ErrNotFound) &&
			usageErr == nil && afterReclaim.UsedBytes == baseline.UsedBytes &&
			afterReclaim.PendingReclaimBytes <= baseline.PendingReclaimBytes {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf(
				"reclaim deadline: result=%#v object=%v inode=%v usage=%#v usage_error=%v",
				result, objectErr, inodeErr, afterReclaim, usageErr,
			)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func contextWithTimeout(t *testing.T, timeout time.Duration) (context.Context, context.CancelFunc) {
	t.Helper()
	return context.WithTimeout(context.Background(), timeout)
}
