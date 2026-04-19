package cmd

import (
	"archive/tar"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"zephyr/config"
	"zephyr/internal/model"
	"zephyr/internal/store"
	"zephyr/shared/bootstrap"
	"zephyr/shared/logger"
	"zephyr/shared/version"
)

const backupManifestVersion = 1

type backupManifest struct {
	FormatVersion               int                    `json:"format_version"`
	ZephyrVersion               string                 `json:"zephyr_version"`
	CreatedAt                   time.Time              `json:"created_at"`
	DatabaseName                string                 `json:"database_name"`
	Bucket                      string                 `json:"bucket"`
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
		newStore:  store.NewOSSClient,
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
		newStore:  store.NewOSSClient,
		importDB:  importDatabase,
	}); err != nil {
		logger.Fatal("%v", err)
	}
	logger.Info("Restore finished. Run `zephyr start -c %s` to bring the instance back online.", configPath)
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
	return fmt.Sprintf("zephyr-backup-%s.tar.gz", time.Now().Format("20060102-150405"))
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

	logger.Info("Backing up Zephyr instance")
	logger.Info("  Config file: %s", configPath)
	logger.Info("  Database: %s", cfg.Database.DBName)
	logger.Info("  Bucket: %s", cfg.OSS.Bucket)
	logger.Info("  Output: %s", backupDir)

	if err := os.MkdirAll(backupDir, 0o755); err != nil {
		return fmt.Errorf("create backup directory: %w", err)
	}

	fileStore, err := deps.newStore(cfg.OSS)
	if err != nil {
		return fmt.Errorf("init OSS client: %w", err)
	}
	objects, err := fileStore.ListAllObjects("")
	if err != nil {
		return fmt.Errorf("list bucket objects: %w", err)
	}

	manifest := backupManifest{
		FormatVersion:               backupManifestVersion,
		ZephyrVersion:               version.Version,
		CreatedAt:                   deps.now().UTC(),
		DatabaseName:                cfg.Database.DBName,
		Bucket:                      cfg.OSS.Bucket,
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
	if err := os.MkdirAll(objectsDir, 0o755); err != nil {
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
		if err := os.MkdirAll(filepath.Dir(localPath), 0o755); err != nil {
			return fmt.Errorf("create object parent directory for %q: %w", obj.Key, err)
		}
		reader, err := fileStore.GetObjectContent(obj.Key)
		if err != nil {
			return fmt.Errorf("read object %q: %w", obj.Key, err)
		}
		if err := writeStreamToFile(localPath, reader); err != nil {
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

	if err := writeManifest(filepath.Join(backupDir, "manifest.json"), manifest); err != nil {
		return fmt.Errorf("write manifest: %w", err)
	}
	logger.Info("Bucket export complete (%d objects, %d bytes)", manifest.ObjectCount, manifest.TotalSize)
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
	if !confirmed {
		return fmt.Errorf("restore is destructive. Re-run with --yes to replace database %q and bucket %q", cfg.Database.DBName, cfg.OSS.Bucket)
	}

	manifest, err := readManifest(filepath.Join(backupDir, "manifest.json"))
	if err != nil {
		return fmt.Errorf("read manifest: %w", err)
	}
	if manifest.FormatVersion != backupManifestVersion {
		return fmt.Errorf("unsupported backup format version %d", manifest.FormatVersion)
	}
	if manifest.EncryptionSecretFingerprint != encryptionSecretFingerprint(cfg.Server.EncryptionSecret) {
		return fmt.Errorf("backup encryption fingerprint does not match current config")
	}

	logger.Info("Restoring Zephyr instance")
	logger.Info("  Config file: %s", configPath)
	logger.Info("  Database: %s", cfg.Database.DBName)
	logger.Info("  Bucket: %s", cfg.OSS.Bucket)
	logger.Info("  Input: %s", backupDir)

	if err := deps.resetDB(cfg.Database); err != nil {
		return fmt.Errorf("reset database: %w", err)
	}
	logger.Info("Database reset complete")

	fileStore, err := deps.newStore(cfg.OSS)
	if err != nil {
		return fmt.Errorf("init OSS client: %w", err)
	}
	if err := fileStore.DeleteAllObjects(nil); err != nil {
		return fmt.Errorf("clear bucket %q: %w", cfg.OSS.Bucket, err)
	}
	logger.Info("Bucket cleanup complete")

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
		data, err := os.ReadFile(filepath.Join(backupDir, "objects", relPath))
		if err != nil {
			return fmt.Errorf("read backup object %q: %w", obj.Key, err)
		}
		if err := fileStore.PutObjectBytes(obj.Key, data); err != nil {
			return fmt.Errorf("restore object %q: %w", obj.Key, err)
		}
		logObjectProgress("restore files", i+1, len(files), obj.Key)
	}
	logger.Info("Bucket restore complete (%d objects)", manifest.ObjectCount)

	if err := deps.importDB(cfg.Database, filepath.Join(backupDir, "database.sql")); err != nil {
		return fmt.Errorf("import database: %w", err)
	}
	logger.Info("Database import complete")
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
		stageDir, err := os.MkdirTemp("", "zephyr-backup-*")
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
	stageDir, err := os.MkdirTemp("", "zephyr-restore-*")
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
	trimmed := strings.TrimSuffix(key, "/")
	cleaned := path.Clean("/" + trimmed)
	rel := strings.TrimPrefix(cleaned, "/")
	if rel == "" || rel == "." {
		return "", fmt.Errorf("empty object path")
	}
	if strings.Contains(rel, "../") || rel == ".." {
		return "", fmt.Errorf("path traversal is not allowed")
	}
	return filepath.FromSlash(rel), nil
}

func writeStreamToFile(path string, reader io.Reader) error {
	file, err := os.Create(path)
	if err != nil {
		return err
	}
	defer file.Close()
	if _, err := io.Copy(file, reader); err != nil {
		return err
	}
	return file.Close()
}

func writeManifest(path string, manifest backupManifest) error {
	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return os.WriteFile(path, data, 0o644)
}

func readManifest(path string) (backupManifest, error) {
	var manifest backupManifest
	data, err := os.ReadFile(path)
	if err != nil {
		return manifest, err
	}
	err = json.Unmarshal(data, &manifest)
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
	file, err := os.Create(outputPath)
	if err != nil {
		return fmt.Errorf("create archive %s: %w", outputPath, err)
	}
	defer file.Close()

	gzWriter := gzip.NewWriter(file)
	defer gzWriter.Close()

	tarWriter := tar.NewWriter(gzWriter)
	defer tarWriter.Close()

	return filepath.WalkDir(srcDir, func(current string, d fs.DirEntry, walkErr error) error {
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
		defer reader.Close()
		_, err = io.Copy(tarWriter, reader)
		return err
	})
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
		switch header.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(targetPath, 0o755); err != nil {
				return fmt.Errorf("create directory %s: %w", targetPath, err)
			}
		case tar.TypeReg, tar.TypeRegA:
			if err := os.MkdirAll(filepath.Dir(targetPath), 0o755); err != nil {
				return fmt.Errorf("create parent directory for %s: %w", targetPath, err)
			}
			file, err := os.OpenFile(targetPath, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, os.FileMode(header.Mode))
			if err != nil {
				return fmt.Errorf("create file %s: %w", targetPath, err)
			}
			if _, err := io.Copy(file, tarReader); err != nil {
				file.Close()
				return fmt.Errorf("extract file %s: %w", targetPath, err)
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
