package cmd

import (
	"errors"
	"io"
	"testing"
	"time"

	"domus/config"
	"domus/internal/store"
)

type resetTestStore struct {
	deleteAllCalled bool
	deleteAllErr    error
}

func (s *resetTestStore) ListObjects(prefix, marker string, limit int) (*store.ListResult, error) {
	return nil, nil
}
func (s *resetTestStore) GetObjectInfo(key string) (*store.FileInfo, error) { return nil, nil }
func (s *resetTestStore) CreateDirectory(key string) error                  { return nil }
func (s *resetTestStore) DeleteObject(key string) error                     { return nil }
func (s *resetTestStore) DeleteObjects(keys []string) error                 { return nil }
func (s *resetTestStore) CopyObject(srcKey, dstKey string) error            { return nil }
func (s *resetTestStore) MoveObject(srcKey, dstKey string) error            { return nil }
func (s *resetTestStore) ListAllObjects(prefix string) ([]store.ObjectInfo, error) {
	return nil, nil
}
func (s *resetTestStore) RecursiveCopy(srcPrefix, dstPrefix string, progress func(done, total int, current string)) error {
	return nil
}
func (s *resetTestStore) RecursiveMove(srcPrefix, dstPrefix string, progress func(done, total int, current string)) error {
	return nil
}
func (s *resetTestStore) RecursiveDelete(prefix string, progress func(done, total int, current string)) error {
	return nil
}
func (s *resetTestStore) DeleteAllObjects(progress func(done, total int, current string)) error {
	s.deleteAllCalled = true
	return s.deleteAllErr
}
func (s *resetTestStore) GetTotalSize(prefix string) (int64, int, error) { return 0, 0, nil }
func (s *resetTestStore) GeneratePresignedURL(key string, expiresDuration time.Duration) (string, error) {
	return "", nil
}
func (s *resetTestStore) GetObjectContent(key string) (io.ReadCloser, error) {
	return nil, nil
}
func (s *resetTestStore) PutObjectBytes(key string, data []byte) error         { return nil }
func (s *resetTestStore) RenameObject(oldKey, newKey string, isDir bool) error { return nil }
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
	return &config.Config{
		Server:   config.ServerConfig{PidFile: "domus.pid"},
		Database: config.DatabaseConfig{DBName: "domus_test"},
		OSS:      config.OSSConfig{Bucket: "domus-bucket"},
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
	if !st.deleteAllCalled {
		t.Fatal("expected bucket cleanup to be called")
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
	if calledStore {
		t.Fatal("bucket cleanup should not be called when database reset fails")
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
