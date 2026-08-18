package cmd

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/willvar/dofs"
	dofssqlite "github.com/willvar/dofs/metadata/sqlite"

	"domus/config"
	"domus/internal/store"
)

type backupTestStore struct {
	objects         []store.ObjectInfo
	contents        map[string][]byte
	createdDirs     []string
	putObjects      map[string][]byte
	deleteAllCalled bool
	deleteAllErr    error
	listAllErr      error
	getObjectErr    error
	createDirErr    error
	putObjectErr    error
	checkCalled     bool
	checkErr        error
}

func (s *backupTestStore) Check(context.Context) error {
	s.checkCalled = true
	return s.checkErr
}
func (s *backupTestStore) CreateDirectory(key string) error {
	if s.createDirErr != nil {
		return s.createDirErr
	}
	s.createdDirs = append(s.createdDirs, key)
	return nil
}
func (s *backupTestStore) ListAllObjects(prefix string) ([]store.ObjectInfo, error) {
	if s.listAllErr != nil {
		return nil, s.listAllErr
	}
	return s.objects, nil
}
func (s *backupTestStore) DeleteAllObjects(progress func(done, total int, current string)) error {
	s.deleteAllCalled = true
	return s.deleteAllErr
}
func (s *backupTestStore) GeneratePresignedURL(key string, expiresDuration time.Duration) (string, error) {
	return "", nil
}
func (s *backupTestStore) GetObjectContent(key string) (io.ReadCloser, error) {
	if s.getObjectErr != nil {
		return nil, s.getObjectErr
	}
	return io.NopCloser(bytes.NewReader(s.contents[key])), nil
}
func (s *backupTestStore) PutObject(key string, reader io.Reader, size int64) error {
	if s.putObjectErr != nil {
		return s.putObjectErr
	}
	data, err := io.ReadAll(io.LimitReader(reader, size+1))
	if err != nil {
		return err
	}
	if int64(len(data)) != size {
		return errors.New("unexpected streamed object size")
	}
	if s.putObjects == nil {
		s.putObjects = map[string][]byte{}
	}
	buf := make([]byte, len(data))
	copy(buf, data)
	s.putObjects[key] = buf
	return nil
}
func (s *backupTestStore) PresignedPutObject(key string, expiresDuration time.Duration) (string, error) {
	return "", nil
}
func (s *backupTestStore) PresignedDeleteObject(key string, expiresDuration time.Duration) (string, error) {
	return "", nil
}
func (s *backupTestStore) CreateMultipartUpload(key string) (string, error) { return "", nil }
func (s *backupTestStore) PresignedUploadPart(key, uploadID string, partNumber int, expiresDuration time.Duration) (string, error) {
	return "", nil
}
func (s *backupTestStore) CompleteMultipartUpload(key, uploadID string, parts []store.CompletePart) error {
	return nil
}
func (s *backupTestStore) AbortMultipartUpload(key, uploadID string) error { return nil }
func (s *backupTestStore) ListParts(key, uploadID string) ([]store.PartInfo, error) {
	return nil, nil
}
func (s *backupTestStore) HeadObject(key string) (*store.HeadResult, error) { return nil, nil }

func TestBackupInstanceRejectsRunningService(t *testing.T) {
	err := backupInstance(testResetConfig(), "config.yaml", t.TempDir(), backupDeps{
		getStatus: func(pidFile string) (bool, int, error) { return true, 42, nil },
		newStore:  func(cfg config.OSSConfig) (store.FileStore, error) { return &backupTestStore{}, nil },
		dumpDB:    func(cfg config.DatabaseConfig, outputPath string) error { return nil },
		now:       func() time.Time { return time.Unix(0, 0) },
	})
	if err == nil {
		t.Fatal("expected running service error")
	}
}

func TestBackupInstanceWritesManifestDatabaseAndObjects(t *testing.T) {
	backupDir := t.TempDir()
	st := &backupTestStore{
		objects: []store.ObjectInfo{
			{Key: "root/home/", Size: 0},
			{Key: "root/home/hello.txt", Size: 5},
		},
		contents: map[string][]byte{"root/home/hello.txt": []byte("hello")},
	}
	dumped := false
	now := time.Date(2026, 4, 19, 12, 30, 0, 0, time.UTC)
	err := backupInstance(testResetConfig(), "config.yaml", backupDir, backupDeps{
		getStatus: func(pidFile string) (bool, int, error) { return false, 0, nil },
		newStore:  func(cfg config.OSSConfig) (store.FileStore, error) { return st, nil },
		dumpDB: func(cfg config.DatabaseConfig, outputPath string) error {
			dumped = true
			return os.WriteFile(outputPath, []byte("dump"), 0o644)
		},
		now: func() time.Time { return now },
	})
	if err != nil {
		t.Fatalf("backupInstance returned error: %v", err)
	}
	if !dumped {
		t.Fatal("expected database dump to run")
	}
	objectPath, err := objectRelativePath("root/home/hello.txt")
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(backupDir, "objects", objectPath))
	if err != nil {
		t.Fatalf("read backed up object: %v", err)
	}
	if string(data) != "hello" {
		t.Fatalf("unexpected object data: %q", string(data))
	}
	manifest, err := readManifest(filepath.Join(backupDir, "manifest.json"))
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	if manifest.DatabaseName != "domus_test" {
		t.Fatalf("unexpected database name: %s", manifest.DatabaseName)
	}
	if manifest.DOFSMetadataDriver != "postgres" {
		t.Fatalf("unexpected DOFS metadata driver: %s", manifest.DOFSMetadataDriver)
	}
	if manifest.Bucket != "domus-bucket" {
		t.Fatalf("unexpected bucket: %s", manifest.Bucket)
	}
	if manifest.ObjectCount != 2 {
		t.Fatalf("unexpected object count: %d", manifest.ObjectCount)
	}
	if manifest.TotalSize != 5 {
		t.Fatalf("unexpected total size: %d", manifest.TotalSize)
	}
	if manifest.CreatedAt != now {
		t.Fatalf("unexpected timestamp: %v", manifest.CreatedAt)
	}
	if manifest.EncryptionSecretFingerprint != encryptionSecretFingerprint(testResetConfig().Server.EncryptionSecret) {
		t.Fatal("expected encryption fingerprint in manifest")
	}
	if len(manifest.Objects) != 2 || manifest.Objects[0].Key != "root/home/" || manifest.Objects[1].Key != "root/home/hello.txt" {
		t.Fatalf("unexpected manifest objects: %+v", manifest.Objects)
	}
	if _, err := os.Stat(filepath.Join(backupDir, "database.sql")); err != nil {
		t.Fatalf("expected database dump file: %v", err)
	}
}

func TestSQLiteDOFSMetadataBackupAndRestore(t *testing.T) {
	root := t.TempDir()
	cfg := testResetConfig()
	cfg.DOFS.StateRoot = filepath.Join(root, "state")
	cfg.DOFS.Metadata = config.DOFSMetadataConfig{
		Driver: "sqlite",
		SQLite: config.DOFSSQLiteMetadataConfig{
			Path:               filepath.Join(root, "state", "metadata.sqlite"),
			BusyTimeoutSeconds: 5, MaxOpenConnections: 2,
		},
	}
	open := func() *dofssqlite.Store {
		t.Helper()
		metadata, err := dofssqlite.Open(t.Context(), dofssqlite.Config{
			Path: cfg.DOFS.Metadata.SQLite.Path, BusyTimeout: 5 * time.Second, MaxOpenConnections: 2,
		})
		if err != nil {
			t.Fatal(err)
		}
		if err := metadata.Migrate(t.Context()); err != nil {
			_ = metadata.Close()
			t.Fatal(err)
		}
		return metadata
	}
	metadata := open()
	if _, err := metadata.CreateNamespace(t.Context(), dofs.Namespace{
		ID: "backup-tenant", WrappedKEK: bytes.Repeat([]byte{7}, dofs.KeySize),
	}); err != nil {
		t.Fatal(err)
	}
	if err := metadata.Close(); err != nil {
		t.Fatal(err)
	}
	snapshot := filepath.Join(root, "backup", dofsSQLiteBackupName)
	if err := os.MkdirAll(filepath.Dir(snapshot), 0700); err != nil {
		t.Fatal(err)
	}
	if err := backupDOFSMetadata(cfg, snapshot); err != nil {
		t.Fatal(err)
	}
	metadata = open()
	if err := metadata.DeleteNamespace(t.Context(), "backup-tenant"); err != nil {
		t.Fatal(err)
	}
	if err := metadata.Close(); err != nil {
		t.Fatal(err)
	}
	if err := restoreDOFSMetadata(cfg, snapshot); err != nil {
		t.Fatal(err)
	}
	metadata = open()
	defer metadata.Close()
	if _, err := metadata.GetNamespace(t.Context(), "backup-tenant"); err != nil {
		t.Fatalf("restored namespace: %v", err)
	}
}

func TestRequireDOFSStoppedFailsClosed(t *testing.T) {
	socketPath := filepath.Join(t.TempDir(), "dofs.sock")
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatal(err)
	}
	unixListener := listener.(*net.UnixListener)
	unixListener.SetUnlinkOnClose(false)
	if err := requireDOFSStopped(socketPath); err == nil {
		t.Fatal("live DOFS socket was accepted")
	}
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	if err := requireDOFSStopped(socketPath); err != nil {
		t.Fatalf("stale DOFS socket should be accepted: %v", err)
	}
	if err := os.Remove(socketPath); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(socketPath, []byte("not a socket"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := requireDOFSStopped(socketPath); err == nil {
		t.Fatal("non-socket DOFS control path was accepted")
	}
}

func TestRequireWorkspaceStoppedFailsClosed(t *testing.T) {
	socketPath := filepath.Join(t.TempDir(), "workspace.sock")
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatal(err)
	}
	unixListener := listener.(*net.UnixListener)
	unixListener.SetUnlinkOnClose(false)
	if err := requireWorkspaceStopped(socketPath); err == nil {
		t.Fatal("live Workspace socket was accepted")
	}
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	if err := requireWorkspaceStopped(socketPath); err != nil {
		t.Fatalf("stale Workspace socket should be accepted: %v", err)
	}
	if err := os.Remove(socketPath); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(socketPath, []byte("not a socket"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := requireWorkspaceStopped(socketPath); err == nil {
		t.Fatal("non-socket Workspace control path was accepted")
	}
}

func TestObjectRelativePathTreatsObjectKeysAsOpaque(t *testing.T) {
	first, err := objectRelativePath("a//b")
	if err != nil {
		t.Fatal(err)
	}
	second, err := objectRelativePath("a/b")
	if err != nil {
		t.Fatal(err)
	}
	if first == second {
		t.Fatal("distinct object keys collided after filesystem mapping")
	}
	if filepath.IsAbs(first) || filepath.Clean(first) == ".." || strings.HasPrefix(filepath.Clean(first), ".."+string(filepath.Separator)) {
		t.Fatalf("unsafe object archive path: %q", first)
	}
}

func TestReadManifestAcceptsLegacyZephyrVersionField(t *testing.T) {
	manifestPath := filepath.Join(t.TempDir(), "manifest.json")
	data := []byte(`{"format_version":1,"zephyr_version":"0.9.0"}`)
	if err := os.WriteFile(manifestPath, data, 0600); err != nil {
		t.Fatal(err)
	}
	manifest, err := readManifest(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	if manifest.DomusVersion != "0.9.0" {
		t.Fatalf("migrated version = %q", manifest.DomusVersion)
	}
}

func TestRestoreInstanceRequiresYes(t *testing.T) {
	err := restoreInstance(testResetConfig(), "config.yaml", t.TempDir(), false, restoreDeps{
		getStatus: func(pidFile string) (bool, int, error) { return false, 0, nil },
		resetDB:   func(cfg config.DatabaseConfig) error { return nil },
		newStore:  func(cfg config.OSSConfig) (store.FileStore, error) { return &backupTestStore{}, nil },
		importDB:  func(cfg config.DatabaseConfig, inputPath string) error { return nil },
	})
	if err == nil {
		t.Fatal("expected error when --yes is missing")
	}
}

func TestRestoreInstanceRejectsFingerprintMismatch(t *testing.T) {
	backupDir := t.TempDir()
	manifest := backupManifest{
		FormatVersion: backupManifestVersion, DOFSMetadataDriver: "postgres",
		EncryptionSecretFingerprint: "mismatch",
	}
	if err := writeManifest(filepath.Join(backupDir, "manifest.json"), manifest); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	err := restoreInstance(testResetConfig(), "config.yaml", backupDir, true, restoreDeps{
		getStatus: func(pidFile string) (bool, int, error) { return false, 0, nil },
		resetDB:   func(cfg config.DatabaseConfig) error { return nil },
		newStore:  func(cfg config.OSSConfig) (store.FileStore, error) { return &backupTestStore{}, nil },
		importDB:  func(cfg config.DatabaseConfig, inputPath string) error { return nil },
	})
	if err == nil {
		t.Fatal("expected fingerprint mismatch error")
	}
}

func TestRestoreInstanceRestoresBucketAndDatabase(t *testing.T) {
	backupDir := t.TempDir()
	objectPath, err := objectRelativePath("root/home/hello.txt")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(backupDir, "objects", filepath.Dir(objectPath)), 0o755); err != nil {
		t.Fatalf("mkdir objects: %v", err)
	}
	if err := os.WriteFile(filepath.Join(backupDir, "objects", objectPath), []byte("hello"), 0o644); err != nil {
		t.Fatalf("write object file: %v", err)
	}
	if err := os.WriteFile(filepath.Join(backupDir, "database.sql"), []byte("dump"), 0o644); err != nil {
		t.Fatalf("write database dump: %v", err)
	}
	manifest := backupManifest{
		FormatVersion:               backupManifestVersion,
		DOFSMetadataDriver:          "postgres",
		EncryptionSecretFingerprint: encryptionSecretFingerprint(testResetConfig().Server.EncryptionSecret),
		ObjectCount:                 2,
		TotalSize:                   5,
		Objects: []backupManifestObject{
			{Key: "root/home/", IsDir: true},
			{Key: "root/home/hello.txt", Size: 5},
		},
	}
	if err := writeManifest(filepath.Join(backupDir, "manifest.json"), manifest); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	st := &backupTestStore{}
	resetCalled := false
	importCalled := false
	err = restoreInstance(testResetConfig(), "config.yaml", backupDir, true, restoreDeps{
		getStatus: func(pidFile string) (bool, int, error) { return false, 0, nil },
		resetDB: func(cfg config.DatabaseConfig) error {
			resetCalled = true
			return nil
		},
		newStore: func(cfg config.OSSConfig) (store.FileStore, error) { return st, nil },
		importDB: func(cfg config.DatabaseConfig, inputPath string) error {
			importCalled = true
			if !resetCalled {
				t.Fatal("expected database reset before import")
			}
			if !st.deleteAllCalled {
				t.Fatal("expected bucket cleanup before import")
			}
			if string(st.putObjects["root/home/hello.txt"]) != "hello" {
				t.Fatalf("unexpected restored object data: %q", string(st.putObjects["root/home/hello.txt"]))
			}
			return nil
		},
	})
	if err != nil {
		t.Fatalf("restoreInstance returned error: %v", err)
	}
	if !resetCalled {
		t.Fatal("expected database reset")
	}
	if !st.deleteAllCalled {
		t.Fatal("expected bucket cleanup")
	}
	if len(st.createdDirs) != 1 || st.createdDirs[0] != "root/home/" {
		t.Fatalf("unexpected restored dirs: %+v", st.createdDirs)
	}
	if !importCalled {
		t.Fatal("expected database import")
	}
	if string(st.putObjects["root/home/hello.txt"]) != "hello" {
		t.Fatalf("unexpected restored object data: %q", string(st.putObjects["root/home/hello.txt"]))
	}
	if _, err := os.Stat(filepath.Join(backupDir, "database.sql")); err != nil {
		t.Fatalf("expected database dump to remain present: %v", err)
	}
}

func TestRestoreInstanceStopsOnBucketCleanupError(t *testing.T) {
	backupDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(backupDir, "database.sql"), []byte("dump"), 0600); err != nil {
		t.Fatal(err)
	}
	manifest := backupManifest{
		FormatVersion:               backupManifestVersion,
		DOFSMetadataDriver:          "postgres",
		EncryptionSecretFingerprint: encryptionSecretFingerprint(testResetConfig().Server.EncryptionSecret),
	}
	if err := writeManifest(filepath.Join(backupDir, "manifest.json"), manifest); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	st := &backupTestStore{deleteAllErr: errors.New("bucket failed")}
	importCalled := false
	err := restoreInstance(testResetConfig(), "config.yaml", backupDir, true, restoreDeps{
		getStatus: func(pidFile string) (bool, int, error) { return false, 0, nil },
		resetDB:   func(cfg config.DatabaseConfig) error { return nil },
		newStore:  func(cfg config.OSSConfig) (store.FileStore, error) { return st, nil },
		importDB: func(cfg config.DatabaseConfig, inputPath string) error {
			importCalled = true
			return nil
		},
	})
	if err == nil {
		t.Fatal("expected bucket cleanup error")
	}
	if importCalled {
		t.Fatal("database import should not run after bucket cleanup failure")
	}
}

func TestRestorePreflightRejectsMissingObjectBeforeReset(t *testing.T) {
	backupDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(backupDir, "database.sql"), []byte("dump"), 0600); err != nil {
		t.Fatal(err)
	}
	manifest := backupManifest{
		FormatVersion: backupManifestVersion, DOFSMetadataDriver: "postgres",
		EncryptionSecretFingerprint: encryptionSecretFingerprint(testResetConfig().Server.EncryptionSecret),
		ObjectCount:                 1, TotalSize: 5,
		Objects: []backupManifestObject{{Key: "missing-object", Size: 5}},
	}
	if err := writeManifest(filepath.Join(backupDir, "manifest.json"), manifest); err != nil {
		t.Fatal(err)
	}
	resetCalled := false
	storeCalled := false
	err := restoreInstance(testResetConfig(), "config.yaml", backupDir, true, restoreDeps{
		getStatus: func(pidFile string) (bool, int, error) { return false, 0, nil },
		resetDB: func(cfg config.DatabaseConfig) error {
			resetCalled = true
			return nil
		},
		newStore: func(cfg config.OSSConfig) (store.FileStore, error) {
			storeCalled = true
			return &backupTestStore{}, nil
		},
		importDB: func(cfg config.DatabaseConfig, inputPath string) error { return nil },
	})
	if err == nil {
		t.Fatal("corrupt backup was accepted")
	}
	if resetCalled || storeCalled {
		t.Fatal("restore mutated external state before bundle preflight completed")
	}
}

func TestRestoreInitializesObjectStoreBeforeReset(t *testing.T) {
	backupDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(backupDir, "database.sql"), []byte("dump"), 0600); err != nil {
		t.Fatal(err)
	}
	manifest := backupManifest{
		FormatVersion: backupManifestVersion, DOFSMetadataDriver: "postgres",
		EncryptionSecretFingerprint: encryptionSecretFingerprint(testResetConfig().Server.EncryptionSecret),
	}
	if err := writeManifest(filepath.Join(backupDir, "manifest.json"), manifest); err != nil {
		t.Fatal(err)
	}
	resetCalled := false
	err := restoreInstance(testResetConfig(), "config.yaml", backupDir, true, restoreDeps{
		getStatus: func(pidFile string) (bool, int, error) { return false, 0, nil },
		resetDB: func(cfg config.DatabaseConfig) error {
			resetCalled = true
			return nil
		},
		newStore: func(cfg config.OSSConfig) (store.FileStore, error) {
			return nil, errors.New("invalid credentials")
		},
		importDB: func(cfg config.DatabaseConfig, inputPath string) error { return nil },
	})
	if err == nil || resetCalled {
		t.Fatalf("restore error = %v, reset called = %t", err, resetCalled)
	}
}

func TestRestoreChecksObjectStoreBeforeReset(t *testing.T) {
	backupDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(backupDir, "database.sql"), []byte("dump"), 0600); err != nil {
		t.Fatal(err)
	}
	manifest := backupManifest{
		FormatVersion: backupManifestVersion, DOFSMetadataDriver: "postgres",
		EncryptionSecretFingerprint: encryptionSecretFingerprint(testResetConfig().Server.EncryptionSecret),
	}
	if err := writeManifest(filepath.Join(backupDir, "manifest.json"), manifest); err != nil {
		t.Fatal(err)
	}
	objectStore := &backupTestStore{checkErr: errors.New("bucket unavailable")}
	resetCalled := false
	err := restoreInstance(testResetConfig(), "config.yaml", backupDir, true, restoreDeps{
		getStatus: func(pidFile string) (bool, int, error) { return false, 0, nil },
		resetDB: func(cfg config.DatabaseConfig) error {
			resetCalled = true
			return nil
		},
		newStore: func(cfg config.OSSConfig) (store.FileStore, error) { return objectStore, nil },
		importDB: func(cfg config.DatabaseConfig, inputPath string) error { return nil },
	})
	if err == nil || resetCalled || !objectStore.checkCalled {
		t.Fatalf("restore error = %v, reset called = %t, bucket checked = %t", err, resetCalled, objectStore.checkCalled)
	}
}

func TestRestoreRejectsSymlinkedSQLiteTargetBeforeExternalAccess(t *testing.T) {
	backupDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(backupDir, "database.sql"), []byte("dump"), 0600); err != nil {
		t.Fatal(err)
	}
	snapshotPath := filepath.Join(backupDir, dofsSQLiteBackupName)
	metadata, err := dofssqlite.Open(t.Context(), dofssqlite.Config{
		Path: snapshotPath, BusyTimeout: 5 * time.Second, MaxOpenConnections: 2,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := metadata.Migrate(t.Context()); err != nil {
		_ = metadata.Close()
		t.Fatal(err)
	}
	if err := metadata.Close(); err != nil {
		t.Fatal(err)
	}

	targetRoot := t.TempDir()
	realParent := filepath.Join(targetRoot, "real")
	if err := os.Mkdir(realParent, 0700); err != nil {
		t.Fatal(err)
	}
	linkedParent := filepath.Join(targetRoot, "linked")
	if err := os.Symlink(realParent, linkedParent); err != nil {
		t.Fatal(err)
	}
	cfg := testResetConfig()
	cfg.DOFS.Metadata = config.DOFSMetadataConfig{
		Driver: "sqlite",
		SQLite: config.DOFSSQLiteMetadataConfig{
			Path: filepath.Join(linkedParent, "metadata.sqlite"), BusyTimeoutSeconds: 5, MaxOpenConnections: 2,
		},
	}
	manifest := backupManifest{
		FormatVersion: backupManifestVersion, DOFSMetadataDriver: "sqlite",
		EncryptionSecretFingerprint: encryptionSecretFingerprint(cfg.Server.EncryptionSecret),
	}
	if err := writeManifest(filepath.Join(backupDir, "manifest.json"), manifest); err != nil {
		t.Fatal(err)
	}
	resetCalled := false
	storeCalled := false
	err = restoreInstance(cfg, "config.yaml", backupDir, true, restoreDeps{
		getStatus: func(pidFile string) (bool, int, error) { return false, 0, nil },
		resetDB: func(cfg config.DatabaseConfig) error {
			resetCalled = true
			return nil
		},
		newStore: func(cfg config.OSSConfig) (store.FileStore, error) {
			storeCalled = true
			return &backupTestStore{}, nil
		},
		importDB: func(cfg config.DatabaseConfig, inputPath string) error { return nil },
	})
	if err == nil || resetCalled || storeCalled {
		t.Fatalf("restore error = %v, reset called = %t, object store accessed = %t", err, resetCalled, storeCalled)
	}
}

func TestCreateTarGzPublishesCompleteArchiveWithoutOverwrite(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "source")
	if err := os.Mkdir(source, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "manifest.json"), []byte("complete"), 0600); err != nil {
		t.Fatal(err)
	}
	archive := filepath.Join(root, "backup.tar.gz")
	if err := createTarGz(source, archive); err != nil {
		t.Fatal(err)
	}
	extracted := filepath.Join(root, "extracted")
	if err := os.Mkdir(extracted, 0700); err != nil {
		t.Fatal(err)
	}
	if err := extractTarGz(archive, extracted); err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(filepath.Join(extracted, "manifest.json")); err != nil || string(data) != "complete" {
		t.Fatalf("extracted archive = %q, %v", data, err)
	}
	if err := createTarGz(source, archive); err == nil {
		t.Fatal("existing archive was overwritten")
	}
	if data, err := os.ReadFile(filepath.Join(extracted, "manifest.json")); err != nil || string(data) != "complete" {
		t.Fatalf("published archive changed = %q, %v", data, err)
	}
}

func TestExtractTarGzRejectsDuplicateEntries(t *testing.T) {
	archive := filepath.Join(t.TempDir(), "duplicate.tar.gz")
	file, err := os.Create(archive)
	if err != nil {
		t.Fatal(err)
	}
	gzipWriter := gzip.NewWriter(file)
	tarWriter := tar.NewWriter(gzipWriter)
	for range 2 {
		if err := tarWriter.WriteHeader(&tar.Header{Name: "same", Mode: 0600, Size: 1, Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
		if _, err := tarWriter.Write([]byte("x")); err != nil {
			t.Fatal(err)
		}
	}
	if err := tarWriter.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gzipWriter.Close(); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	if err := extractTarGz(archive, t.TempDir()); err == nil || !strings.Contains(err.Error(), "duplicate archive entry") {
		t.Fatalf("duplicate archive error = %v", err)
	}
}
