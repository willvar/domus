package dofsbridge_test

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/willvar/dofs"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"domus/config"
	"domus/internal/auth"
	"domus/internal/dofsbridge"
	"domus/internal/model"
	"domus/internal/notify"
)

func TestMetadataDriversEmitCommittedChangesThroughRPC(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_DSN")
	if dsn == "" {
		t.Skip("set TEST_DATABASE_DSN for both metadata-driver checks")
	}
	for _, driver := range []string{"sqlite", "postgres"} {
		t.Run(driver, func(t *testing.T) {
			database, err := gorm.Open(postgres.Open(dsn), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
			if err != nil {
				t.Fatal(err)
			}
			sqlDB, err := database.DB()
			if err != nil {
				t.Fatal(err)
			}
			sqlDB.SetMaxOpenConns(1)
			t.Cleanup(func() { sqlDB.Close() })
			schema := "domus_test_wake_" + uuid.NewString()[:8]
			if err := database.Exec("CREATE SCHEMA " + schema).Error; err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { database.Exec("DROP SCHEMA " + schema + " CASCADE") })
			if err := database.Exec("SET search_path TO " + schema + ", public").Error; err != nil {
				t.Fatal(err)
			}
			directory, err := os.MkdirTemp(os.TempDir(), "rpc-store-")
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { os.RemoveAll(directory) })
			ctx, cancel := context.WithCancel(t.Context())
			t.Cleanup(cancel)
			controlSocket := filepath.Join(directory, "dofs.sock")
			events := make(chan notify.Event, 64)
			listener, err := notify.Listen(ctx, notify.SocketPath(controlSocket), func(event notify.Event) { events <- event })
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { listener.Close(); notify.Configure("") })
			master := bytes.Repeat([]byte{0x31}, dofs.KeySize)
			wrapped, err := dofs.WrapKey(master, bytes.Repeat([]byte{0x52}, dofs.KeySize))
			if err != nil {
				t.Fatal(err)
			}
			repos := model.NewMemRepos(nil)
			user, err := repos.Users.Create("event-fixture", "password", "user", hex.EncodeToString(wrapped))
			if err != nil {
				t.Fatal(err)
			}
			cfg := &config.Config{}
			cfg.DOFS.Metadata.Driver = driver
			cfg.DOFS.Metadata.SQLite.Path = filepath.Join(directory, "metadata.sqlite")
			cfg.DOFS.Metadata.SQLite.BusyTimeoutSeconds = 1
			cfg.DOFS.Metadata.SQLite.MaxOpenConnections = 2
			cfg.DOFS.ControlSocket = controlSocket
			cfg.OSS.ServerEndpoint, cfg.OSS.Bucket, cfg.OSS.Region = "127.0.0.1:1", "fixture", "us-east-1"
			cfg.OSS.AccessKeyID, cfg.OSS.AccessKeySecret = "fixture-access", "fixture-secret"
			runtime, err := dofsbridge.Open(ctx, database, cfg, master, repos.Users)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(runtime.Close)
			if err := runtime.EnsureUser(ctx, user); err != nil {
				t.Fatal(err)
			}
			for len(events) > 0 {
				<-events
			}
			sequence, err := runtime.Metadata.LatestEventSequence(ctx, user.ID)
			if err != nil {
				t.Fatal(err)
			}
			node, err := runtime.Metadata.CreateNode(ctx, dofs.CreateNodeRequest{NamespaceID: user.ID, Parent: dofs.RootInode, Name: "folder", Kind: dofs.NodeKindDirectory, Mode: 0700})
			if err != nil {
				t.Fatal(err)
			}
			if _, _, err := runtime.Metadata.Rename(ctx, dofs.RenameRequest{NamespaceID: user.ID, Inode: node.Inode, NewParent: dofs.RootInode, NewName: "renamed"}); err != nil {
				t.Fatal(err)
			}
			if _, err := runtime.Metadata.Chmod(ctx, user.ID, node.Inode, 0750); err != nil {
				t.Fatal(err)
			}
			if _, err := runtime.Metadata.Remove(ctx, dofs.RemoveRequest{NamespaceID: user.ID, Parent: dofs.RootInode, Name: "renamed", Directory: true}); err != nil {
				t.Fatal(err)
			}
			for i := 0; i < 4; i++ {
				select {
				case event := <-events:
					if event.Kind != "dofs" || event.UserID != user.ID {
						t.Fatalf("wrong change identity: %#v", event)
					}
				case <-time.After(3 * time.Second):
					t.Fatal("committed metadata change emitted no RPC hint")
				}
			}
			journal, err := runtime.Metadata.ListEvents(ctx, user.ID, sequence, 20)
			if err != nil || len(journal) != 4 {
				t.Fatalf("stored changes = %d, error = %v", len(journal), err)
			}
			if _, _, err := runtime.Metadata.Rename(ctx, dofs.RenameRequest{NamespaceID: user.ID, Inode: node.Inode, NewParent: dofs.RootInode, NewName: "gone"}); err == nil {
				t.Fatal("removed inode was renamed")
			}
			if len(events) != 0 {
				t.Fatal("failed write emitted a change hint")
			}
		})
	}
}

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
