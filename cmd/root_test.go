package cmd

import (
	"context"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"

	"domus/config"
	"domus/internal/store"
)

type resetTestStore struct {
	checkCalled     bool
	checkErr        error
	deleteAllCalled bool
	deleteAllErr    error
}

func (s *resetTestStore) Check(context.Context) error {
	s.checkCalled = true
	return s.checkErr
}
func (s *resetTestStore) CreateDirectory(key string) error { return nil }
func (s *resetTestStore) ListAllObjects(prefix string) ([]store.ObjectInfo, error) {
	return nil, nil
}
func (s *resetTestStore) DeleteAllObjects(progress func(done, total int, current string)) error {
	s.deleteAllCalled = true
	return s.deleteAllErr
}
func (s *resetTestStore) GeneratePresignedURL(key string, expiresDuration time.Duration) (string, error) {
	return "", nil
}
func (s *resetTestStore) GetObjectContent(key string) (io.ReadCloser, error) {
	return nil, nil
}
func (s *resetTestStore) PutObject(key string, reader io.Reader, size int64) error { return nil }
func (s *resetTestStore) PresignedPutObject(key string, expiresDuration time.Duration) (string, error) {
	return "", nil
}
func (s *resetTestStore) PresignedDeleteObject(key string, expiresDuration time.Duration) (string, error) {
	return "", nil
}
func (s *resetTestStore) CreateMultipartUpload(key string) (string, error) { return "", nil }
func (s *resetTestStore) PresignedUploadPart(key, uploadID string, partNumber int, expiresDuration time.Duration) (string, error) {
	return "", nil
}
func (s *resetTestStore) CompleteMultipartUpload(key, uploadID string, parts []store.CompletePart) error {
	return nil
}
func (s *resetTestStore) AbortMultipartUpload(key, uploadID string) error { return nil }
func (s *resetTestStore) ListParts(key, uploadID string) ([]store.PartInfo, error) {
	return nil, nil
}
func (s *resetTestStore) HeadObject(key string) (*store.HeadResult, error) { return nil, nil }

func testResetConfig() *config.Config {
	runtimeRoot := filepath.Join(os.TempDir(), "domus-reset-test-"+uuid.NewString())
	return &config.Config{
		Server:   config.ServerConfig{PidFile: "domus.pid"},
		Database: config.DatabaseConfig{DBName: "domus_test"},
		OSS:      config.OSSConfig{Bucket: "domus-bucket"},
		DOFS: config.DOFSConfig{Metadata: config.DOFSMetadataConfig{
			Driver: "postgres",
		}, StateRoot: filepath.Join(runtimeRoot, "dofs"), ControlSocket: filepath.Join(runtimeRoot, "run", "dofs.sock")},
		Workspace: config.WorkspaceConfig{
			StateRoot: filepath.Join(runtimeRoot, "workspace"), ControlSocket: filepath.Join(runtimeRoot, "run", "workspace.sock"),
		},
	}
}

func TestGenerateRandomSecretLengthAndAlphabet(t *testing.T) {
	secret, err := generateRandomSecret(33)
	if err != nil {
		t.Fatal(err)
	}
	if len(secret) != 33 {
		t.Fatalf("secret length = %d", len(secret))
	}
	if _, err := hex.DecodeString(secret + "0"); err != nil {
		t.Fatalf("secret is not hexadecimal: %v", err)
	}
	if _, err := generateRandomSecret(0); err == nil {
		t.Fatal("expected non-positive secret length to be rejected")
	}
}

func TestResetInstanceRequiresYes(t *testing.T) {
	calledDB := false
	calledStore := false
	err := resetInstance(testResetConfig(), "config.yaml", false, resetDeps{
		getStatus: func(pidFile string) (bool, int, error) { return false, 0, nil },
		resetDB: func(cfg config.DatabaseConfig) error {
			calledDB = true
			return nil
		},
		newStore: func(cfg config.OSSConfig) (store.FileStore, error) {
			calledStore = true
			return &resetTestStore{}, nil
		},
	})
	if err == nil {
		t.Fatal("expected error when --yes is missing")
	}
	if calledDB || calledStore {
		t.Fatal("reset should stop before touching database or bucket")
	}
}

func TestResetInstanceRejectsRunningService(t *testing.T) {
	err := resetInstance(testResetConfig(), "config.yaml", true, resetDeps{
		getStatus: func(pidFile string) (bool, int, error) { return true, 1234, nil },
		resetDB:   func(cfg config.DatabaseConfig) error { return nil },
		newStore:  func(cfg config.OSSConfig) (store.FileStore, error) { return &resetTestStore{}, nil },
	})
	if err == nil {
		t.Fatal("expected error when service is running")
	}
}

func TestResetInstanceSuccess(t *testing.T) {
	st := &resetTestStore{}
	calledDB := false
	err := resetInstance(testResetConfig(), "config.yaml", true, resetDeps{
		getStatus: func(pidFile string) (bool, int, error) { return false, 0, nil },
		resetDB: func(cfg config.DatabaseConfig) error {
			calledDB = true
			if cfg.DBName != "domus_test" {
				t.Fatalf("unexpected db name: %s", cfg.DBName)
			}
			return nil
		},
		newStore: func(cfg config.OSSConfig) (store.FileStore, error) {
			if cfg.Bucket != "domus-bucket" {
				t.Fatalf("unexpected bucket: %s", cfg.Bucket)
			}
			return st, nil
		},
	})
	if err != nil {
		t.Fatalf("resetInstance returned error: %v", err)
	}
	if !calledDB {
		t.Fatal("expected database reset to be called")
	}
	if !st.checkCalled {
		t.Fatal("expected bucket preflight before reset")
	}
	if !st.deleteAllCalled {
		t.Fatal("expected bucket cleanup to be called")
	}
}

func TestClearDOFSMetadataRemovesOnlySQLiteSidecars(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "dofs", "metadata.sqlite")
	if err := os.MkdirAll(filepath.Dir(target), 0700); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{target, target + "-wal", target + "-shm", filepath.Join(root, "keep")} {
		if err := os.WriteFile(path, []byte("fixture"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	cfg := testResetConfig()
	cfg.DOFS.Metadata = config.DOFSMetadataConfig{
		Driver: "sqlite", SQLite: config.DOFSSQLiteMetadataConfig{Path: target},
	}
	if err := clearDOFSMetadata(cfg); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{target, target + "-wal", target + "-shm"} {
		if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("metadata path remained: %s (%v)", path, err)
		}
	}
	if _, err := os.Stat(filepath.Join(root, "keep")); err != nil {
		t.Fatalf("neighbor file was removed: %v", err)
	}
}

func TestClearDOFSMetadataRejectsSymlinkedParent(t *testing.T) {
	root := t.TempDir()
	realParent := filepath.Join(root, "real")
	if err := os.Mkdir(realParent, 0700); err != nil {
		t.Fatal(err)
	}
	linkedParent := filepath.Join(root, "linked")
	if err := os.Symlink(realParent, linkedParent); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(linkedParent, "metadata.sqlite")
	if err := os.WriteFile(filepath.Join(realParent, "metadata.sqlite"), []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	cfg := testResetConfig()
	cfg.DOFS.Metadata = config.DOFSMetadataConfig{
		Driver: "sqlite", SQLite: config.DOFSSQLiteMetadataConfig{Path: target},
	}
	if err := clearDOFSMetadata(cfg); err == nil {
		t.Fatal("expected symlinked metadata parent to be rejected")
	}
	if _, err := os.Stat(filepath.Join(realParent, "metadata.sqlite")); err != nil {
		t.Fatalf("metadata behind rejected symlink was changed: %v", err)
	}
}

func TestClearDOFSRuntimeStateRemovesOnlyTransientTrees(t *testing.T) {
	stateRoot := filepath.Join(t.TempDir(), "state")
	for _, directory := range []string{
		filepath.Join(stateRoot, "desired"),
		filepath.Join(stateRoot, "users", "user-id", "wal"),
	} {
		if err := os.MkdirAll(directory, 0700); err != nil {
			t.Fatal(err)
		}
	}
	for _, path := range []string{
		filepath.Join(stateRoot, "desired", "user-id.json"),
		filepath.Join(stateRoot, "users", "user-id", "wal", "transaction.json"),
		filepath.Join(stateRoot, "metadata.sqlite"),
		filepath.Join(stateRoot, "operator.keep"),
	} {
		if err := os.WriteFile(path, []byte("fixture"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	cfg := testResetConfig()
	cfg.DOFS.StateRoot = stateRoot
	if err := clearDOFSRuntimeState(cfg); err != nil {
		t.Fatal(err)
	}
	for _, removed := range []string{filepath.Join(stateRoot, "desired"), filepath.Join(stateRoot, "users")} {
		if _, err := os.Stat(removed); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("transient runtime path remained: %s (%v)", removed, err)
		}
	}
	for _, preserved := range []string{filepath.Join(stateRoot, "metadata.sqlite"), filepath.Join(stateRoot, "operator.keep")} {
		if _, err := os.Stat(preserved); err != nil {
			t.Fatalf("non-transient state was removed: %s (%v)", preserved, err)
		}
	}
}

func TestClearDOFSRuntimeStateRejectsSymlinkBeforeRemovingAnything(t *testing.T) {
	root := t.TempDir()
	stateRoot := filepath.Join(root, "state")
	desired := filepath.Join(stateRoot, "desired")
	if err := os.MkdirAll(desired, 0700); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(desired, "user-id.json")
	if err := os.WriteFile(marker, []byte("fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	external := filepath.Join(root, "external")
	if err := os.Mkdir(external, 0700); err != nil {
		t.Fatal(err)
	}
	externalFile := filepath.Join(external, "keep")
	if err := os.WriteFile(externalFile, []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(external, filepath.Join(stateRoot, "users")); err != nil {
		t.Fatal(err)
	}
	cfg := testResetConfig()
	cfg.DOFS.StateRoot = stateRoot
	if err := clearDOFSRuntimeState(cfg); err == nil {
		t.Fatal("expected symlinked runtime state to be rejected")
	}
	for _, preserved := range []string{marker, externalFile} {
		if _, err := os.Stat(preserved); err != nil {
			t.Fatalf("preflight failure changed %s: %v", preserved, err)
		}
	}
}

func TestClearWorkspaceRuntimeStateRemovesOnlyTransientTrees(t *testing.T) {
	stateRoot := filepath.Join(t.TempDir(), "workspace")
	for _, directory := range []string{
		filepath.Join(stateRoot, "desired"),
		filepath.Join(stateRoot, "identities", "user-id"),
	} {
		if err := os.MkdirAll(directory, 0700); err != nil {
			t.Fatal(err)
		}
	}
	for _, path := range []string{
		filepath.Join(stateRoot, "desired", "user-id.json"),
		filepath.Join(stateRoot, "identities", "user-id", "passwd"),
		filepath.Join(stateRoot, "manager-id"),
		filepath.Join(stateRoot, "operator.keep"),
	} {
		if err := os.WriteFile(path, []byte("fixture"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	cfg := testResetConfig()
	cfg.Workspace.StateRoot = stateRoot
	if err := clearWorkspaceRuntimeState(cfg); err != nil {
		t.Fatal(err)
	}
	for _, removed := range []string{filepath.Join(stateRoot, "desired"), filepath.Join(stateRoot, "identities")} {
		if _, err := os.Stat(removed); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("transient Workspace path remained: %s (%v)", removed, err)
		}
	}
	for _, preserved := range []string{filepath.Join(stateRoot, "manager-id"), filepath.Join(stateRoot, "operator.keep")} {
		if _, err := os.Stat(preserved); err != nil {
			t.Fatalf("persistent Workspace state was removed: %s (%v)", preserved, err)
		}
	}
}

func TestClearWorkspaceRuntimeStateRejectsSymlinkBeforeRemovingAnything(t *testing.T) {
	root := t.TempDir()
	stateRoot := filepath.Join(root, "workspace")
	desired := filepath.Join(stateRoot, "desired")
	if err := os.MkdirAll(desired, 0700); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(desired, "user-id.json")
	if err := os.WriteFile(marker, []byte("fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	external := filepath.Join(root, "external")
	if err := os.Mkdir(external, 0700); err != nil {
		t.Fatal(err)
	}
	externalFile := filepath.Join(external, "keep")
	if err := os.WriteFile(externalFile, []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(external, filepath.Join(stateRoot, "identities")); err != nil {
		t.Fatal(err)
	}
	cfg := testResetConfig()
	cfg.Workspace.StateRoot = stateRoot
	if err := clearWorkspaceRuntimeState(cfg); err == nil {
		t.Fatal("expected symlinked Workspace state to be rejected")
	}
	for _, preserved := range []string{marker, externalFile} {
		if _, err := os.Stat(preserved); err != nil {
			t.Fatalf("preflight failure changed %s: %v", preserved, err)
		}
	}
}

func TestResetInstancePreflightsObjectStoreBeforeDatabase(t *testing.T) {
	calledDB := false
	err := resetInstance(testResetConfig(), "config.yaml", true, resetDeps{
		getStatus: func(pidFile string) (bool, int, error) { return false, 0, nil },
		resetDB: func(cfg config.DatabaseConfig) error {
			calledDB = true
			return nil
		},
		newStore: func(cfg config.OSSConfig) (store.FileStore, error) {
			return nil, errors.New("invalid object-store configuration")
		},
	})
	if err == nil || calledDB {
		t.Fatalf("reset error = %v, database reset = %t", err, calledDB)
	}
}

func TestResetInstanceStopsOnObjectStoreCheckFailure(t *testing.T) {
	st := &resetTestStore{checkErr: errors.New("bucket unavailable")}
	calledDB := false
	err := resetInstance(testResetConfig(), "config.yaml", true, resetDeps{
		getStatus: func(pidFile string) (bool, int, error) { return false, 0, nil },
		resetDB: func(cfg config.DatabaseConfig) error {
			calledDB = true
			return nil
		},
		newStore: func(cfg config.OSSConfig) (store.FileStore, error) { return st, nil },
	})
	if err == nil || calledDB || st.deleteAllCalled {
		t.Fatalf("reset error = %v, database reset = %t, bucket delete = %t", err, calledDB, st.deleteAllCalled)
	}
}

func TestResetInstanceReturnsBucketErrorAfterDBReset(t *testing.T) {
	st := &resetTestStore{deleteAllErr: errors.New("bucket failed")}
	calledDB := false
	err := resetInstance(testResetConfig(), "config.yaml", true, resetDeps{
		getStatus: func(pidFile string) (bool, int, error) { return false, 0, nil },
		resetDB: func(cfg config.DatabaseConfig) error {
			calledDB = true
			return nil
		},
		newStore: func(cfg config.OSSConfig) (store.FileStore, error) {
			return st, nil
		},
	})
	if err == nil {
		t.Fatal("expected bucket cleanup error")
	}
	if !calledDB {
		t.Fatal("expected database reset to run before bucket cleanup")
	}
	if !st.deleteAllCalled {
		t.Fatal("expected bucket cleanup attempt")
	}
}

func TestResetInstanceStopsOnDBFailure(t *testing.T) {
	st := &resetTestStore{}
	calledStore := false
	err := resetInstance(testResetConfig(), "config.yaml", true, resetDeps{
		getStatus: func(pidFile string) (bool, int, error) { return false, 0, nil },
		resetDB: func(cfg config.DatabaseConfig) error {
			return errors.New("database connection failed")
		},
		newStore: func(cfg config.OSSConfig) (store.FileStore, error) {
			calledStore = true
			return st, nil
		},
	})
	if err == nil {
		t.Fatal("expected database error")
	}
	if !calledStore || !st.checkCalled {
		t.Fatal("object store must be initialized and checked before database reset")
	}
	if st.deleteAllCalled {
		t.Fatal("bucket cleanup should not run when database reset fails")
	}
}

func TestLoadRootBootstrapPasswordLegacyEnvironmentFallback(t *testing.T) {
	t.Setenv(rootBootstrapPasswordEnvKey, "")
	t.Setenv(legacyRootPasswordEnvKey, "legacy-secret")
	password, err := loadRootBootstrapPassword(&config.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if password != "legacy-secret" {
		t.Fatalf("password = %q", password)
	}
}

func TestLoadRootBootstrapPasswordPrefersDomusEnvironment(t *testing.T) {
	t.Setenv(rootBootstrapPasswordEnvKey, "domus-secret")
	t.Setenv(legacyRootPasswordEnvKey, "legacy-secret")
	password, err := loadRootBootstrapPassword(&config.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if password != "domus-secret" {
		t.Fatalf("password = %q", password)
	}
}
