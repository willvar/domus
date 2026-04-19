package cmd

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"zephyr/config"
	"zephyr/internal/store"
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
}

func (s *backupTestStore) ListObjects(prefix, marker string, limit int) (*store.ListResult, error) {
	return nil, nil
}
func (s *backupTestStore) GetObjectInfo(key string) (*store.FileInfo, error) { return nil, nil }
func (s *backupTestStore) CreateDirectory(key string) error {
	if s.createDirErr != nil {
		return s.createDirErr
	}
	s.createdDirs = append(s.createdDirs, key)
	return nil
}
func (s *backupTestStore) DeleteObject(key string) error          { return nil }
func (s *backupTestStore) DeleteObjects(keys []string) error      { return nil }
func (s *backupTestStore) CopyObject(srcKey, dstKey string) error { return nil }
func (s *backupTestStore) MoveObject(srcKey, dstKey string) error { return nil }
func (s *backupTestStore) ListAllObjects(prefix string) ([]store.ObjectInfo, error) {
	if s.listAllErr != nil {
		return nil, s.listAllErr
	}
	return s.objects, nil
}
func (s *backupTestStore) RecursiveCopy(srcPrefix, dstPrefix string, progress func(done, total int, current string)) error {
	return nil
}
func (s *backupTestStore) RecursiveMove(srcPrefix, dstPrefix string, progress func(done, total int, current string)) error {
	return nil
}
func (s *backupTestStore) RecursiveDelete(prefix string, progress func(done, total int, current string)) error {
	return nil
}
func (s *backupTestStore) DeleteAllObjects(progress func(done, total int, current string)) error {
	s.deleteAllCalled = true
	return s.deleteAllErr
}
func (s *backupTestStore) GetTotalSize(prefix string) (int64, int, error) { return 0, 0, nil }
func (s *backupTestStore) GeneratePresignedURL(key string, expiresDuration time.Duration) (string, error) {
	return "", nil
}
func (s *backupTestStore) GetObjectContent(key string) (io.ReadCloser, error) {
	if s.getObjectErr != nil {
		return nil, s.getObjectErr
	}
	return io.NopCloser(bytes.NewReader(s.contents[key])), nil
}
func (s *backupTestStore) PutObjectBytes(key string, data []byte) error {
	if s.putObjectErr != nil {
		return s.putObjectErr
	}
	if s.putObjects == nil {
		s.putObjects = map[string][]byte{}
	}
	buf := make([]byte, len(data))
	copy(buf, data)
	s.putObjects[key] = buf
	return nil
}
func (s *backupTestStore) RenameObject(oldKey, newKey string, isDir bool) error { return nil }
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
	data, err := os.ReadFile(filepath.Join(backupDir, "objects", "root", "home", "hello.txt"))
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
	if manifest.DatabaseName != "zephyr_test" {
		t.Fatalf("unexpected database name: %s", manifest.DatabaseName)
	}
	if manifest.Bucket != "zephyr-bucket" {
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
	manifest := backupManifest{FormatVersion: backupManifestVersion, EncryptionSecretFingerprint: "mismatch"}
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
	if err := os.MkdirAll(filepath.Join(backupDir, "objects", "root", "home"), 0o755); err != nil {
		t.Fatalf("mkdir objects: %v", err)
	}
	if err := os.WriteFile(filepath.Join(backupDir, "objects", "root", "home", "hello.txt"), []byte("hello"), 0o644); err != nil {
		t.Fatalf("write object file: %v", err)
	}
	if err := os.WriteFile(filepath.Join(backupDir, "database.sql"), []byte("dump"), 0o644); err != nil {
		t.Fatalf("write database dump: %v", err)
	}
	manifest := backupManifest{
		FormatVersion:               backupManifestVersion,
		EncryptionSecretFingerprint: encryptionSecretFingerprint(testResetConfig().Server.EncryptionSecret),
		ObjectCount:                 2,
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
	err := restoreInstance(testResetConfig(), "config.yaml", backupDir, true, restoreDeps{
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
	manifest := backupManifest{
		FormatVersion:               backupManifestVersion,
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
