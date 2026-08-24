package cmd

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"math"
	"net"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"

	dofssqlite "github.com/willvar/dofs/metadata/sqlite"

	"domus/config"
	"domus/internal/model"
	"domus/internal/store"
	"domus/shared/bootstrap"
	"domus/shared/logger"
	"domus/shared/version"
)

const backupManifestVersion = 2

const dofsSQLiteBackupName = "dofs-metadata.sqlite"

type backupManifest struct {
	FormatVersion               int                    `json:"format_version"`
	DomusVersion                string                 `json:"domus_version"`
	LegacyZephyrVersion         string                 `json:"zephyr_version,omitempty"`
	CreatedAt                   time.Time              `json:"created_at"`
	DatabaseName                string                 `json:"database_name"`
	DOFSMetadataDriver          string                 `json:"dofs_metadata_driver"`
	Bucket                      string                 `json:"bucket"`
	ObjectPrefix                string                 `json:"object_prefix,omitempty"`
	EncryptionSecretFingerprint string                 `json:"encryption_secret_fingerprint"`
	ObjectCount                 int                    `json:"object_count"`
	TotalSize                   int64                  `json:"total_size"`
	Objects                     []backupManifestObject `json:"objects"`
}

type backupManifestObject struct {
	Key   string `json:"key"`
	Size  int64  `json:"size"`
	IsDir bool   `json:"is_dir"`
}

type backupDeps struct {
	getStatus func(pidFile string) (running bool, pid int, err error)
	newStore  func(cfg config.OSSConfig) (store.FileStore, error)
	dumpDB    func(cfg config.DatabaseConfig, outputPath string) error
	now       func() time.Time
}

type restoreDeps struct {
	getStatus func(pidFile string) (running bool, pid int, err error)
	resetDB   func(cfg config.DatabaseConfig) error
	newStore  func(cfg config.OSSConfig) (store.FileStore, error)
	importDB  func(cfg config.DatabaseConfig, inputPath string) error
}

func backup(configPath, outputPath string) {
	cfg := loadConfig(configPath)
	if outputPath == "" {
		outputPath = defaultBackupPath()
	}

	if err := runBackup(cfg, configPath, outputPath, backupDeps{
		getStatus: bootstrap.GetStatus,
		newStore:  store.NewOSSClientFromConfig,
		dumpDB:    dumpDatabase,
		now:       time.Now,
	}); err != nil {
		logger.Fatal("%v", err)
	}
	logger.Info("Backup finished: %s", outputPath)
}

func restore(configPath, inputPath string, confirmed bool) {
	if strings.TrimSpace(inputPath) == "" {
		logger.Fatal("restore requires -i <backup path>")
	}

	cfg := loadConfig(configPath)
	if err := runRestore(cfg, configPath, inputPath, confirmed, restoreDeps{
		getStatus: bootstrap.GetStatus,
		resetDB:   model.ResetDatabase,
		newStore:  store.NewOSSClientFromConfig,
		importDB:  importDatabase,
	}); err != nil {
		logger.Fatal("%v", err)
	}
	logger.Info("Restore finished. Run `domus start -c %s` to bring the instance back online.", configPath)
}

func getFlagValue(args []string, flag string) string {
	for i := 0; i < len(args); i++ {
		if args[i] == flag && i+1 < len(args) {
			return args[i+1]
		}
	}
	return ""
}

func defaultBackupPath() string {
	return fmt.Sprintf("domus-backup-%s.tar.gz", time.Now().Format("20060102-150405"))
}

func runBackup(cfg *config.Config, configPath, outputPath string, deps backupDeps) error {
	stageDir, cleanup, finalize, err := prepareBackupOutput(outputPath)
	if err != nil {
		return err
	}
	defer cleanup()

	if err := backupInstance(cfg, configPath, stageDir, deps); err != nil {
		return err
	}
	return finalize()
}

func runRestore(cfg *config.Config, configPath, inputPath string, confirmed bool, deps restoreDeps) error {
	stageDir, cleanup, err := prepareRestoreInput(inputPath)
	if err != nil {
		return err
	}
	defer cleanup()

	return restoreInstance(cfg, configPath, stageDir, confirmed, deps)
}

func backupInstance(cfg *config.Config, configPath, backupDir string, deps backupDeps) error {
	running, pid, err := deps.getStatus(cfg.Server.PidFile)
	if err != nil {
		return fmt.Errorf("failed to check process status: %w", err)
	}
	if running {
		return fmt.Errorf("service is running (PID: %d). Stop it before backup", pid)
	}
	if err := requireDOFSStopped(cfg.DOFS.ControlSocket); err != nil {
		return err
	}

	logger.Info("Backing up Domus instance")
	logger.Info("  Config file: %s", configPath)
	logger.Info("  Database: %s", cfg.Database.DBName)
	logger.Info("  Bucket: %s", cfg.OSS.Bucket)
	logger.Info("  Object prefix: %s", displayObjectPrefix(cfg.OSS.Prefix))
	logger.Info("  Output: %s", backupDir)

	if err := os.MkdirAll(backupDir, 0o700); err != nil {
		return fmt.Errorf("create backup directory: %w", err)
	}

	fileStore, err := deps.newStore(cfg.OSS)
	if err != nil {
		return fmt.Errorf("init OSS client: %w", err)
	}
	objects, err := fileStore.ListAllObjects("")
	if err != nil {
		return fmt.Errorf("list configured object prefix: %w", err)
	}

	manifest := backupManifest{
		FormatVersion:               backupManifestVersion,
		DomusVersion:                version.Version,
		CreatedAt:                   deps.now().UTC(),
		DatabaseName:                cfg.Database.DBName,
		DOFSMetadataDriver:          cfg.DOFS.Metadata.Driver,
		Bucket:                      cfg.OSS.Bucket,
		ObjectPrefix:                cfg.OSS.Prefix,
		EncryptionSecretFingerprint: encryptionSecretFingerprint(cfg.Server.EncryptionSecret),
		ObjectCount:                 len(objects),
	}
	totalFileObjects := 0
	for _, obj := range objects {
		if !strings.HasSuffix(obj.Key, "/") {
			totalFileObjects++
		}
	}
	completedFileObjects := 0

	objectsDir := filepath.Join(backupDir, "objects")
	if err := os.MkdirAll(objectsDir, 0o700); err != nil {
		return fmt.Errorf("create objects directory: %w", err)
	}

	for _, obj := range objects {
		isDir := strings.HasSuffix(obj.Key, "/")
		manifest.Objects = append(manifest.Objects, backupManifestObject{Key: obj.Key, Size: obj.Size, IsDir: isDir})
		manifest.TotalSize += obj.Size
		if isDir {
			continue
		}

		relPath, err := objectRelativePath(obj.Key)
		if err != nil {
			return fmt.Errorf("invalid object key %q: %w", obj.Key, err)
		}
		localPath := filepath.Join(objectsDir, relPath)
		if err := os.MkdirAll(filepath.Dir(localPath), 0o700); err != nil {
			return fmt.Errorf("create object parent directory for %q: %w", obj.Key, err)
		}
		reader, err := fileStore.GetObjectContent(obj.Key)
		if err != nil {
			return fmt.Errorf("read object %q: %w", obj.Key, err)
		}
		if err := writeStreamToFile(localPath, reader, obj.Size); err != nil {
			_ = reader.Close()
			return fmt.Errorf("write object %q: %w", obj.Key, err)
		}
		if err := reader.Close(); err != nil {
			return fmt.Errorf("close object %q: %w", obj.Key, err)
		}
		completedFileObjects++
		logObjectProgress("backup", completedFileObjects, totalFileObjects, obj.Key)
	}

	sort.Slice(manifest.Objects, func(i, j int) bool {
		return manifest.Objects[i].Key < manifest.Objects[j].Key
	})

	if err := deps.dumpDB(cfg.Database, filepath.Join(backupDir, "database.sql")); err != nil {
		return fmt.Errorf("dump database: %w", err)
	}
	logger.Info("Database dump complete")
	if err := backupDOFSMetadata(cfg, filepath.Join(backupDir, dofsSQLiteBackupName)); err != nil {
		return fmt.Errorf("backup DOFS metadata: %w", err)
	}

	if err := writeManifest(filepath.Join(backupDir, "manifest.json"), manifest); err != nil {
		return fmt.Errorf("write manifest: %w", err)
	}
	logger.Info("Object prefix export complete (%d objects, %d bytes)", manifest.ObjectCount, manifest.TotalSize)
	return nil
}

func restoreInstance(cfg *config.Config, configPath, backupDir string, confirmed bool, deps restoreDeps) error {
	running, pid, err := deps.getStatus(cfg.Server.PidFile)
	if err != nil {
		return fmt.Errorf("failed to check process status: %w", err)
	}
	if running {
		return fmt.Errorf("service is running (PID: %d). Stop it before restore", pid)
	}
	if err := requireDOFSStopped(cfg.DOFS.ControlSocket); err != nil {
		return err
	}
	if !confirmed {
		return fmt.Errorf("restore is destructive. Re-run with --yes to replace database %q and %s", cfg.Database.DBName, objectStoreScope(cfg.OSS))
	}

	manifest, err := readManifest(filepath.Join(backupDir, "manifest.json"))
	if err != nil {
		return fmt.Errorf("read manifest: %w", err)
	}
	if manifest.FormatVersion != backupManifestVersion {
		return fmt.Errorf("unsupported backup format version %d", manifest.FormatVersion)
	}
	if manifest.DOFSMetadataDriver != cfg.DOFS.Metadata.Driver {
		return fmt.Errorf("backup DOFS metadata driver %q does not match configured driver %q", manifest.DOFSMetadataDriver, cfg.DOFS.Metadata.Driver)
	}
	if manifest.EncryptionSecretFingerprint != encryptionSecretFingerprint(cfg.Server.EncryptionSecret) {
		return fmt.Errorf("backup encryption fingerprint does not match current config")
	}
	if err := validateRestoreBundle(cfg, backupDir, manifest); err != nil {
		return fmt.Errorf("validate backup before destructive restore: %w", err)
	}
	if err := validateDOFSMetadataReset(cfg); err != nil {
		return fmt.Errorf("validate DOFS metadata target before destructive restore: %w", err)
	}
	if err := validateDOFSRuntimeStateReset(cfg); err != nil {
		return fmt.Errorf("validate DOFS runtime state before destructive restore: %w", err)
	}

	logger.Info("Restoring Domus instance")
	logger.Info("  Config file: %s", configPath)
	logger.Info("  Database: %s", cfg.Database.DBName)
	logger.Info("  Bucket: %s", cfg.OSS.Bucket)
	logger.Info("  Object prefix: %s", displayObjectPrefix(cfg.OSS.Prefix))
	logger.Info("  Input: %s", backupDir)

	// Construct and validate the object-store client before the first
	// destructive action. A malformed endpoint or credential must not leave a
	// freshly reset database paired with an untouched bucket.
	fileStore, err := deps.newStore(cfg.OSS)
	if err != nil {
		return fmt.Errorf("init OSS client: %w", err)
	}
	checkContext, cancelCheck := context.WithTimeout(context.Background(), 30*time.Second)
	checkErr := fileStore.Check(checkContext)
	cancelCheck()
	if checkErr != nil {
		return fmt.Errorf("validate OSS bucket before destructive restore: %w", checkErr)
	}

	if err := deps.resetDB(cfg.Database); err != nil {
		return fmt.Errorf("reset database: %w", err)
	}
	logger.Info("Database reset complete")
	if err := clearDOFSRuntimeState(cfg); err != nil {
		return fmt.Errorf("database reset completed, but failed to clear DOFS runtime state: %w", err)
	}
	logger.Info("DOFS runtime state reset complete")
	if err := fileStore.DeleteAllObjects(nil); err != nil {
		return fmt.Errorf("clear %s: %w", objectStoreScope(cfg.OSS), err)
	}
	logger.Info("Object prefix cleanup complete")

	dirs := sortManifestObjects(manifest.Objects, true)
	for i, obj := range dirs {
		if !obj.IsDir {
			continue
		}
		if err := fileStore.CreateDirectory(obj.Key); err != nil {
			return fmt.Errorf("restore directory %q: %w", obj.Key, err)
		}
		logObjectProgress("restore dirs", i+1, len(dirs), obj.Key)
	}
	files := sortManifestObjects(manifest.Objects, false)
	for i, obj := range files {
		if obj.IsDir {
			continue
		}
		relPath, err := objectRelativePath(obj.Key)
		if err != nil {
			return fmt.Errorf("invalid object key %q: %w", obj.Key, err)
		}
		objectPath := filepath.Join(backupDir, "objects", relPath)
		file, err := os.Open(objectPath)
		if err != nil {
			return fmt.Errorf("open backup object %q: %w", obj.Key, err)
		}
		info, err := file.Stat()
		if err != nil {
			_ = file.Close()
			return fmt.Errorf("inspect backup object %q: %w", obj.Key, err)
		}
		if !info.Mode().IsRegular() || info.Size() != obj.Size {
			_ = file.Close()
			return fmt.Errorf("backup object %q size differs from manifest", obj.Key)
		}
		if err := fileStore.PutObject(obj.Key, file, obj.Size); err != nil {
			_ = file.Close()
			return fmt.Errorf("restore object %q: %w", obj.Key, err)
		}
		if err := file.Close(); err != nil {
			return fmt.Errorf("close backup object %q: %w", obj.Key, err)
		}
		logObjectProgress("restore files", i+1, len(files), obj.Key)
	}
	logger.Info("Object prefix restore complete (%d objects)", manifest.ObjectCount)

	if err := deps.importDB(cfg.Database, filepath.Join(backupDir, "database.sql")); err != nil {
		return fmt.Errorf("import database: %w", err)
	}
	logger.Info("Database import complete")
	if err := restoreDOFSMetadata(cfg, filepath.Join(backupDir, dofsSQLiteBackupName)); err != nil {
		return fmt.Errorf("restore DOFS metadata: %w", err)
	}
	return nil
}

func prepareBackupOutput(outputPath string) (string, func(), func() error, error) {
	if strings.TrimSpace(outputPath) == "" {
		return "", func() {}, nil, fmt.Errorf("backup requires -o <output path>")
	}
	if _, err := os.Stat(outputPath); err == nil {
		return "", func() {}, nil, fmt.Errorf("output path already exists: %s", outputPath)
	} else if !os.IsNotExist(err) {
		return "", func() {}, nil, fmt.Errorf("stat output path %s: %w", outputPath, err)
	}

	if isTarGzPath(outputPath) {
		stageDir, err := os.MkdirTemp("", "domus-backup-*")
		if err != nil {
			return "", func() {}, nil, fmt.Errorf("create temporary backup directory: %w", err)
		}
		cleanup := func() { _ = os.RemoveAll(stageDir) }
		finalize := func() error { return createTarGz(stageDir, outputPath) }
		return stageDir, cleanup, finalize, nil
	}

	cleanup := func() {}
	finalize := func() error { return nil }
	return outputPath, cleanup, finalize, nil
}

func prepareRestoreInput(inputPath string) (string, func(), error) {
	info, err := os.Stat(inputPath)
	if err != nil {
		return "", func() {}, fmt.Errorf("stat input path %s: %w", inputPath, err)
	}
	if info.IsDir() {
		return inputPath, func() {}, nil
	}
	if !isTarGzPath(inputPath) {
		return "", func() {}, fmt.Errorf("restore input must be a directory or .tar.gz archive: %s", inputPath)
	}
	stageDir, err := os.MkdirTemp("", "domus-restore-*")
	if err != nil {
		return "", func() {}, fmt.Errorf("create temporary restore directory: %w", err)
	}
	if err := extractTarGz(inputPath, stageDir); err != nil {
		_ = os.RemoveAll(stageDir)
		return "", func() {}, err
	}
	return stageDir, func() { _ = os.RemoveAll(stageDir) }, nil
}

func encryptionSecretFingerprint(secret string) string {
	sum := sha256.Sum256([]byte(secret))
	return hex.EncodeToString(sum[:])
}

func objectRelativePath(key string) (string, error) {
	if key == "" || strings.HasSuffix(key, "/") || strings.IndexByte(key, 0) >= 0 {
		return "", fmt.Errorf("empty object path")
	}
	// Object keys are opaque S3 bytes, not filesystem paths. Hashing the exact
	// key avoids traversal, normalization collisions (a//b vs a/b), platform
	// filename restrictions and deeply nested attacker-controlled paths. The
	// manifest remains the reversible key-to-object mapping.
	digest := sha256.Sum256([]byte(key))
	encoded := hex.EncodeToString(digest[:])
	return filepath.Join(encoded[:2], encoded), nil
}

func validateRestoreBundle(cfg *config.Config, backupDir string, manifest backupManifest) error {
	if manifest.ObjectCount != len(manifest.Objects) {
		return fmt.Errorf("manifest object_count is %d, but contains %d entries", manifest.ObjectCount, len(manifest.Objects))
	}
	seen := make(map[string]struct{}, len(manifest.Objects))
	var totalSize int64
	for _, object := range manifest.Objects {
		if object.Key == "" || strings.IndexByte(object.Key, 0) >= 0 {
			return errors.New("manifest contains an invalid empty object key")
		}
		if _, exists := seen[object.Key]; exists {
			return fmt.Errorf("manifest contains duplicate object key %q", object.Key)
		}
		seen[object.Key] = struct{}{}
		if object.Size < 0 || totalSize > math.MaxInt64-object.Size {
			return fmt.Errorf("manifest object %q has an invalid size", object.Key)
		}
		totalSize += object.Size
		if object.IsDir {
			if !strings.HasSuffix(object.Key, "/") || object.Size != 0 {
				return fmt.Errorf("manifest directory marker %q is invalid", object.Key)
			}
			continue
		}
		if strings.HasSuffix(object.Key, "/") {
			return fmt.Errorf("manifest file object %q ends with a slash", object.Key)
		}
		relative, err := objectRelativePath(object.Key)
		if err != nil {
			return fmt.Errorf("manifest object %q: %w", object.Key, err)
		}
		if err := requireRegularFile(filepath.Join(backupDir, "objects", relative), &object.Size); err != nil {
			return fmt.Errorf("backup object %q: %w", object.Key, err)
		}
	}
	if totalSize != manifest.TotalSize {
		return fmt.Errorf("manifest total_size is %d, computed %d", manifest.TotalSize, totalSize)
	}
	if err := requireRegularFile(filepath.Join(backupDir, "database.sql"), nil); err != nil {
		return fmt.Errorf("database.sql: %w", err)
	}
	return validateDOFSBackupSnapshot(cfg, filepath.Join(backupDir, dofsSQLiteBackupName))
}

func requireRegularFile(filePath string, expectedSize *int64) error {
	info, err := os.Lstat(filePath)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return errors.New("must be a regular file, not a symlink")
	}
	if expectedSize != nil && info.Size() != *expectedSize {
		return fmt.Errorf("size is %d, expected %d", info.Size(), *expectedSize)
	}
	return nil
}

func validateDOFSBackupSnapshot(cfg *config.Config, source string) error {
	switch cfg.DOFS.Metadata.Driver {
	case "postgres":
		return nil
	case "sqlite":
		if err := requireRegularFile(source, nil); err != nil {
			return fmt.Errorf("DOFS SQLite snapshot: %w", err)
		}
		temporaryDirectory, err := os.MkdirTemp("", "domus-dofs-validate-*")
		if err != nil {
			return err
		}
		defer func() { _ = os.RemoveAll(temporaryDirectory) }()
		copyPath := filepath.Join(temporaryDirectory, "metadata.sqlite")
		if err := copyRegularFile(source, copyPath, 0600); err != nil {
			return err
		}
		metadata, err := dofssqlite.Open(context.Background(), dofssqlite.Config{
			Path: copyPath, BusyTimeout: time.Duration(cfg.DOFS.Metadata.SQLite.BusyTimeoutSeconds) * time.Second,
			MaxOpenConnections: cfg.DOFS.Metadata.SQLite.MaxOpenConnections,
		})
		if err != nil {
			return err
		}
		if err := metadata.Migrate(context.Background()); err != nil {
			_ = metadata.Close()
			return err
		}
		if _, err := metadata.ListNamespaces(context.Background()); err != nil {
			_ = metadata.Close()
			return err
		}
		return metadata.Close()
	default:
		return fmt.Errorf("unsupported DOFS metadata driver %q", cfg.DOFS.Metadata.Driver)
	}
}

func writeStreamToFile(path string, reader io.Reader, expectedSize int64) error {
	if expectedSize < 0 {
		return errors.New("negative object size")
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	closed := false
	defer func() {
		if !closed {
			_ = file.Close()
		}
	}()
	written, err := io.Copy(file, reader)
	if err != nil {
		return err
	}
	if written != expectedSize {
		return fmt.Errorf("streamed object size is %d, expected %d", written, expectedSize)
	}
	if err := file.Sync(); err != nil {
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	closed = true
	return nil
}

func requireDOFSStopped(socketPath string) error {
	return requireLocalManagerStopped("DOFS", socketPath)
}

func requireLocalManagerStopped(service, socketPath string) error {
	socketPath = strings.TrimSpace(socketPath)
	if socketPath == "" {
		return nil
	}
	info, err := os.Lstat(socketPath)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("cannot inspect %s control socket %s: %w", service, socketPath, err)
	}
	if info.Mode()&os.ModeSymlink != 0 || info.Mode()&os.ModeSocket == 0 {
		return fmt.Errorf("refusing backup, restore, or reset: %s control path %s is not a real Unix socket", service, socketPath)
	}
	connection, err := net.DialTimeout("unix", socketPath, 300*time.Millisecond)
	if errors.Is(err, syscall.ECONNREFUSED) || errors.Is(err, os.ErrNotExist) {
		// A real but stale socket cannot have a live manager behind it.
		return nil
	}
	if err != nil {
		return fmt.Errorf("cannot prove %s is stopped at %s: %w", service, socketPath, err)
	}
	_ = connection.Close()
	return fmt.Errorf("%s manager is still running at %s; stop it before backup, restore, or reset", service, socketPath)
}

func dofsSQLitePath(cfg *config.Config) string {
	if cfg.DOFS.Metadata.SQLite.Path != "" {
		return filepath.Clean(cfg.DOFS.Metadata.SQLite.Path)
	}
	return filepath.Join(cfg.DOFS.StateRoot, "metadata.sqlite")
}

func backupDOFSMetadata(cfg *config.Config, destination string) error {
	switch cfg.DOFS.Metadata.Driver {
	case "postgres":
		// The DOFS tables are part of database.sql.
		return nil
	case "sqlite":
		source := dofsSQLitePath(cfg)
		if err := requireRegularFile(source, nil); err != nil {
			return fmt.Errorf("DOFS SQLite database: %w", err)
		}
		metadata, err := dofssqlite.Open(context.Background(), dofssqlite.Config{
			Path:               source,
			BusyTimeout:        time.Duration(cfg.DOFS.Metadata.SQLite.BusyTimeoutSeconds) * time.Second,
			MaxOpenConnections: cfg.DOFS.Metadata.SQLite.MaxOpenConnections,
		})
		if err != nil {
			return err
		}
		if err := metadata.Migrate(context.Background()); err != nil {
			_ = metadata.Close()
			return err
		}
		if err := metadata.Close(); err != nil {
			return fmt.Errorf("checkpoint DOFS SQLite database (is another DOFS process still running?): %w", err)
		}
		if info, err := os.Stat(source + "-wal"); err == nil && info.Size() != 0 {
			return errors.New("DOFS SQLite WAL is not empty; stop every DOFS process before backup")
		} else if err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		return copyRegularFile(source, destination, 0600)
	default:
		return fmt.Errorf("unsupported DOFS metadata driver %q", cfg.DOFS.Metadata.Driver)
	}
}

func restoreDOFSMetadata(cfg *config.Config, source string) error {
	switch cfg.DOFS.Metadata.Driver {
	case "postgres":
		// database.sql already restored the DOFS tables.
		return nil
	case "sqlite":
		target := dofsSQLitePath(cfg)
		parent := filepath.Dir(target)
		if err := ensurePrivateBackupDirectory(parent); err != nil {
			return err
		}
		if info, err := os.Lstat(target); err == nil {
			if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
				return errors.New("configured DOFS SQLite target must be a regular file")
			}
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		temporary, err := os.CreateTemp(parent, ".dofs-restore-*.sqlite")
		if err != nil {
			return err
		}
		temporaryPath := temporary.Name()
		if err := temporary.Close(); err != nil {
			return err
		}
		if err := os.Remove(temporaryPath); err != nil {
			return err
		}
		defer func() { _ = os.Remove(temporaryPath) }()
		if err := copyRegularFile(source, temporaryPath, 0600); err != nil {
			return err
		}
		metadata, err := dofssqlite.Open(context.Background(), dofssqlite.Config{
			Path:               temporaryPath,
			BusyTimeout:        time.Duration(cfg.DOFS.Metadata.SQLite.BusyTimeoutSeconds) * time.Second,
			MaxOpenConnections: cfg.DOFS.Metadata.SQLite.MaxOpenConnections,
		})
		if err != nil {
			return fmt.Errorf("validate DOFS SQLite backup: %w", err)
		}
		if err := metadata.Migrate(context.Background()); err != nil {
			_ = metadata.Close()
			return fmt.Errorf("validate DOFS SQLite schema: %w", err)
		}
		if err := metadata.Close(); err != nil {
			return err
		}
		for _, suffix := range []string{"-wal", "-shm"} {
			if err := os.Remove(target + suffix); err != nil && !errors.Is(err, os.ErrNotExist) {
				return err
			}
		}
		if err := os.Rename(temporaryPath, target); err != nil {
			return err
		}
		return syncBackupDirectory(parent)
	default:
		return fmt.Errorf("unsupported DOFS metadata driver %q", cfg.DOFS.Metadata.Driver)
	}
}

func copyRegularFile(source, destination string, mode os.FileMode) error {
	sourceInfo, err := os.Lstat(source)
	if err != nil {
		return err
	}
	if sourceInfo.Mode()&os.ModeSymlink != 0 || !sourceInfo.Mode().IsRegular() {
		return fmt.Errorf("source %s must be a regular file, not a symlink", source)
	}
	input, err := os.Open(source)
	if err != nil {
		return err
	}
	defer input.Close()
	info, err := input.Stat()
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || !os.SameFile(sourceInfo, info) {
		return fmt.Errorf("source %s must be a regular file", source)
	}
	output, err := os.OpenFile(destination, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
	if err != nil {
		return err
	}
	closed := false
	defer func() {
		if !closed {
			_ = output.Close()
		}
	}()
	if _, err := io.Copy(output, input); err != nil {
		return err
	}
	if err := output.Sync(); err != nil {
		return err
	}
	if err := output.Close(); err != nil {
		return err
	}
	closed = true
	return nil
}

func syncBackupDirectory(directory string) error {
	handle, err := os.Open(directory)
	if err != nil {
		return err
	}
	defer handle.Close()
	return handle.Sync()
}

func ensurePrivateBackupDirectory(directory string) error {
	directory = filepath.Clean(directory)
	if !filepath.IsAbs(directory) || directory == string(filepath.Separator) {
		return errors.New("backup state directory must be an absolute non-root path")
	}
	volume := filepath.VolumeName(directory)
	current := volume + string(filepath.Separator)
	relative := strings.TrimPrefix(directory, current)
	for _, component := range strings.Split(relative, string(filepath.Separator)) {
		if component == "" {
			continue
		}
		current = filepath.Join(current, component)
		info, err := os.Lstat(current)
		if errors.Is(err, os.ErrNotExist) {
			if err := os.Mkdir(current, 0700); err != nil {
				return err
			}
			info, err = os.Lstat(current)
		}
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return fmt.Errorf("backup state path component %s must be a real directory", current)
		}
	}
	return os.Chmod(directory, 0700)
}

func writeManifest(path string, manifest backupManifest) error {
	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return os.WriteFile(path, data, 0o600)
}

func readManifest(path string) (backupManifest, error) {
	var manifest backupManifest
	data, err := os.ReadFile(path)
	if err != nil {
		return manifest, err
	}
	err = json.Unmarshal(data, &manifest)
	if manifest.DomusVersion == "" {
		manifest.DomusVersion = manifest.LegacyZephyrVersion
	}
	return manifest, err
}

func sortManifestObjects(objects []backupManifestObject, dirs bool) []backupManifestObject {
	filtered := make([]backupManifestObject, 0, len(objects))
	for _, obj := range objects {
		if obj.IsDir == dirs {
			filtered = append(filtered, obj)
		}
	}
	sort.Slice(filtered, func(i, j int) bool {
		if dirs {
			if len(filtered[i].Key) != len(filtered[j].Key) {
				return len(filtered[i].Key) < len(filtered[j].Key)
			}
		}
		return filtered[i].Key < filtered[j].Key
	})
	return filtered
}

func logObjectProgress(label string, current, total int, key string) {
	if total == 0 {
		logger.Info("[%s] %s", label, key)
		return
	}
	logger.Info("[%s] %d/%d %s", label, current, total, key)
}

func dumpDatabase(cfg config.DatabaseConfig, outputPath string) error {
	if _, err := exec.LookPath("pg_dump"); err != nil {
		return fmt.Errorf("pg_dump not found in PATH")
	}
	args := []string{
		"--host", cfg.Host,
		"--port", fmt.Sprintf("%d", cfg.Port),
		"--username", cfg.User,
		"--dbname", cfg.DBName,
		"--file", outputPath,
		"--clean",
		"--if-exists",
		"--no-owner",
		"--no-privileges",
	}
	cmd := exec.Command("pg_dump", args...)
	cmd.Env = append(os.Environ(), pgPasswordEnv(cfg.Password))
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("pg_dump failed: %w: %s", err, strings.TrimSpace(string(output)))
	}
	return nil
}

func importDatabase(cfg config.DatabaseConfig, inputPath string) error {
	if _, err := exec.LookPath("psql"); err != nil {
		return fmt.Errorf("psql not found in PATH")
	}
	args := []string{
		"--host", cfg.Host,
		"--port", fmt.Sprintf("%d", cfg.Port),
		"--username", cfg.User,
		"--dbname", cfg.DBName,
		"--set", "ON_ERROR_STOP=1",
		"--file", inputPath,
	}
	cmd := exec.Command("psql", args...)
	cmd.Env = append(os.Environ(), pgPasswordEnv(cfg.Password))
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("psql failed: %w: %s", err, strings.TrimSpace(string(output)))
	}
	return nil
}

func pgPasswordEnv(password string) string {
	return "PGPASSWORD=" + password
}

func isTarGzPath(path string) bool {
	return strings.HasSuffix(path, ".tar.gz") || strings.HasSuffix(path, ".tgz")
}

func createTarGz(srcDir, outputPath string) error {
	absoluteOutput, err := filepath.Abs(outputPath)
	if err != nil {
		return fmt.Errorf("resolve archive path %s: %w", outputPath, err)
	}
	parent := filepath.Dir(absoluteOutput)
	file, err := os.CreateTemp(parent, ".domus-backup-*.tar.gz")
	if err != nil {
		return fmt.Errorf("create temporary archive for %s: %w", outputPath, err)
	}
	temporaryPath := file.Name()
	published := false
	defer func() {
		_ = file.Close()
		if !published {
			_ = os.Remove(temporaryPath)
		}
	}()

	gzWriter := gzip.NewWriter(file)
	tarWriter := tar.NewWriter(gzWriter)

	if err := filepath.WalkDir(srcDir, func(current string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if current == srcDir {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 || (!d.IsDir() && !info.Mode().IsRegular()) {
			return fmt.Errorf("backup source contains unsupported entry %s", current)
		}
		relPath, err := filepath.Rel(srcDir, current)
		if err != nil {
			return err
		}
		archivePath := filepath.ToSlash(relPath)
		if d.IsDir() {
			archivePath += "/"
		}
		header, err := tar.FileInfoHeader(info, "")
		if err != nil {
			return err
		}
		header.Name = archivePath
		if err := tarWriter.WriteHeader(header); err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		reader, err := os.Open(current)
		if err != nil {
			return err
		}
		_, copyErr := io.Copy(tarWriter, reader)
		closeErr := reader.Close()
		return errors.Join(copyErr, closeErr)
	}); err != nil {
		return err
	}
	if err := tarWriter.Close(); err != nil {
		return err
	}
	if err := gzWriter.Close(); err != nil {
		return err
	}
	if err := file.Sync(); err != nil {
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	if err := os.Link(temporaryPath, absoluteOutput); err != nil {
		return fmt.Errorf("publish archive %s without overwrite: %w", outputPath, err)
	}
	if err := os.Remove(temporaryPath); err != nil {
		return fmt.Errorf("remove temporary archive %s: %w", temporaryPath, err)
	}
	published = true
	return syncBackupDirectory(parent)
}

func extractTarGz(inputPath, destDir string) error {
	file, err := os.Open(inputPath)
	if err != nil {
		return fmt.Errorf("open archive %s: %w", inputPath, err)
	}
	defer file.Close()

	gzReader, err := gzip.NewReader(file)
	if err != nil {
		return fmt.Errorf("open gzip stream %s: %w", inputPath, err)
	}
	defer gzReader.Close()

	tarReader := tar.NewReader(gzReader)
	seen := make(map[string]struct{})
	for {
		header, err := tarReader.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return fmt.Errorf("read archive %s: %w", inputPath, err)
		}
		targetPath, err := archiveTargetPath(destDir, header.Name)
		if err != nil {
			return err
		}
		if _, duplicate := seen[targetPath]; duplicate {
			return fmt.Errorf("duplicate archive entry %q", header.Name)
		}
		seen[targetPath] = struct{}{}
		switch header.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(targetPath, 0o700); err != nil {
				return fmt.Errorf("create directory %s: %w", targetPath, err)
			}
		// A NUL type flag is the historical alternate regular-file marker.
		case tar.TypeReg, byte(0):
			if header.Size < 0 {
				return fmt.Errorf("archive file %q has a negative size", header.Name)
			}
			if err := os.MkdirAll(filepath.Dir(targetPath), 0o700); err != nil {
				return fmt.Errorf("create parent directory for %s: %w", targetPath, err)
			}
			file, err := os.OpenFile(targetPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
			if err != nil {
				return fmt.Errorf("create file %s: %w", targetPath, err)
			}
			written, copyErr := io.Copy(file, tarReader)
			if copyErr != nil {
				file.Close()
				return fmt.Errorf("extract file %s: %w", targetPath, copyErr)
			}
			if written != header.Size {
				file.Close()
				return fmt.Errorf("archive file %q size is %d, expected %d", header.Name, written, header.Size)
			}
			if err := file.Close(); err != nil {
				return fmt.Errorf("close file %s: %w", targetPath, err)
			}
		default:
			return fmt.Errorf("unsupported archive entry %q", header.Name)
		}
	}
}

func archiveTargetPath(destDir, name string) (string, error) {
	cleaned := path.Clean("/" + name)
	rel := strings.TrimPrefix(cleaned, "/")
	if rel == "" || rel == "." {
		return "", fmt.Errorf("invalid archive entry %q", name)
	}
	targetPath := filepath.Join(destDir, filepath.FromSlash(rel))
	if !strings.HasPrefix(targetPath, filepath.Clean(destDir)+string(os.PathSeparator)) && filepath.Clean(targetPath) != filepath.Clean(destDir) {
		return "", fmt.Errorf("archive entry escapes destination: %q", name)
	}
	return targetPath, nil
}
