package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"

	"domus/shared/logger"
)

const (
	UploadChunkSize int64 = 5 * 1024 * 1024
)

type Config struct {
	OSS      OSSConfig      `yaml:"oss"`
	Server   ServerConfig   `yaml:"server"`
	DOFS     DOFSConfig     `yaml:"dofs"`
	Upload   UploadConfig   `yaml:"upload"`
	Database DatabaseConfig `yaml:"database"`
	SMTP     SMTPConfig     `yaml:"smtp"`
	Log      logger.Config  `yaml:"log"`
}

// DOFSConfig controls encrypted namespace metadata and the optional Linux
// FUSE service. FUSE paths are host-local because plaintext writeback state
// must never be placed in object storage.
type DOFSConfig struct {
	Metadata                 DOFSMetadataConfig `yaml:"metadata"`
	MountRoot                string             `yaml:"mount_root"`
	StateRoot                string             `yaml:"state_root"`
	ControlSocket            string             `yaml:"control_socket"`
	SocketGroup              string             `yaml:"socket_group"`
	UID                      uint32             `yaml:"uid"`
	GID                      uint32             `yaml:"gid"`
	AllowOther               bool               `yaml:"allow_other"`
	Writable                 bool               `yaml:"writable"`
	MaxMounts                int                `yaml:"max_mounts"`
	ReconcileIntervalSeconds int                `yaml:"reconcile_interval_seconds"`
	MountTimeoutSeconds      int                `yaml:"mount_timeout_seconds"`
	ShutdownTimeoutSeconds   int                `yaml:"shutdown_timeout_seconds"`
}

// DOFSMetadataConfig mirrors the standalone DOFS deployment contract. SQLite
// is the single-host default; PostgreSQL reuses Domus' configured database and
// is intended for multiple DOFS hosts sharing one namespace catalog.
type DOFSMetadataConfig struct {
	Driver string                   `yaml:"driver"`
	SQLite DOFSSQLiteMetadataConfig `yaml:"sqlite"`
}

type DOFSSQLiteMetadataConfig struct {
	Path               string `yaml:"path"`
	BusyTimeoutSeconds int    `yaml:"busy_timeout_seconds"`
	MaxOpenConnections int    `yaml:"max_open_connections"`
}

type OSSConfig struct {
	ServerEndpoint         string `yaml:"server_endpoint"`          // Server-side read/write (HeadObject, Delete)
	ClientUploadEndpoint   string `yaml:"client_upload_endpoint"`   // Public endpoint for browser direct upload (presigned PUT)
	ClientDownloadEndpoint string `yaml:"client_download_endpoint"` // Browser download/preview (CDN or public endpoint)
	AccessKeyID            string `yaml:"access_key_id"`
	AccessKeySecret        string `yaml:"access_key_secret"`
	Bucket                 string `yaml:"bucket"`
	Region                 string `yaml:"region"`
	MaxPresignBatch        int    `yaml:"max_presign_batch"` // Max presigned URLs per request; default 100
}

type ServerConfig struct {
	Port                      int    `yaml:"port"`
	PidFile                   string `yaml:"pid_file"`
	SessionSecret             string `yaml:"session_secret"`
	EncryptionSecret          string `yaml:"encryption_secret"`
	CORSOrigins               string `yaml:"cors_origins"`
	RootBootstrapPasswordFile string `yaml:"root_bootstrap_password_file"`
}

type UploadConfig struct {
	MaxFileSize int64 `yaml:"max_file_size"`
}

type DatabaseConfig struct {
	Host     string `yaml:"host"`
	Port     int    `yaml:"port"`
	User     string `yaml:"user"`
	Password string `yaml:"password"`
	DBName   string `yaml:"dbname"`
	SSLMode  string `yaml:"sslmode"`
}

func (d DatabaseConfig) DSN() string {
	dsn := fmt.Sprintf("host=%s port=%d user=%s dbname=%s sslmode=%s",
		d.Host, d.Port, d.User, d.DBName, d.SSLMode)
	if d.Password != "" {
		dsn += fmt.Sprintf(" password=%s", d.Password)
	}
	return dsn
}

type SMTPConfig struct {
	Host     string `yaml:"host"`
	Port     int    `yaml:"port"`
	Username string `yaml:"username"`
	Password string `yaml:"password"`
	From     string `yaml:"from"`
}

// Validate checks that required configuration fields are set.
func (c *Config) Validate() error {
	if c.OSS.ClientUploadEndpoint == "" {
		return fmt.Errorf("config: oss.client_upload_endpoint is required")
	}
	if c.OSS.AccessKeyID == "" {
		return fmt.Errorf("config: oss.access_key_id is required")
	}
	if c.OSS.AccessKeySecret == "" {
		return fmt.Errorf("config: oss.access_key_secret is required")
	}
	if c.OSS.Bucket == "" {
		return fmt.Errorf("config: oss.bucket is required")
	}
	if c.OSS.Region == "" {
		return fmt.Errorf("config: oss.region is required")
	}
	if c.Server.Port <= 0 {
		return fmt.Errorf("config: server.port must be greater than 0")
	}
	if c.Database.Host == "" {
		return fmt.Errorf("config: database.host is required")
	}
	if c.Server.SessionSecret == "" {
		return fmt.Errorf("config: server.session_secret is required")
	}
	if c.Server.EncryptionSecret == "" {
		return fmt.Errorf("config: server.encryption_secret is required")
	}
	if err := c.validateDOFSMetadata(); err != nil {
		return err
	}
	return nil
}

// ValidateDOFS validates settings used by `domus dofs serve`. It is separate
// from Validate so each service validates only the configuration in its trust
// domain.
func (c *Config) ValidateDOFS() error {
	if c.Server.EncryptionSecret == "" {
		return fmt.Errorf("config: server.encryption_secret is required")
	}
	if c.Database.Host == "" {
		return fmt.Errorf("config: database.host is required")
	}
	if c.OSS.ServerEndpoint == "" {
		return fmt.Errorf("config: oss.server_endpoint is required")
	}
	if c.OSS.AccessKeyID == "" {
		return fmt.Errorf("config: oss.access_key_id is required")
	}
	if c.OSS.AccessKeySecret == "" {
		return fmt.Errorf("config: oss.access_key_secret is required")
	}
	if c.OSS.Bucket == "" {
		return fmt.Errorf("config: oss.bucket is required")
	}
	if c.OSS.Region == "" {
		return fmt.Errorf("config: oss.region is required")
	}
	if err := c.validateDOFSMetadata(); err != nil {
		return err
	}

	paths := []struct {
		name  string
		value string
	}{
		{name: "dofs.mount_root", value: c.DOFS.MountRoot},
		{name: "dofs.state_root", value: c.DOFS.StateRoot},
		{name: "dofs.control_socket", value: c.DOFS.ControlSocket},
	}
	for _, path := range paths {
		if path.value == "" {
			return fmt.Errorf("config: %s is required", path.name)
		}
		if !filepath.IsAbs(path.value) {
			return fmt.Errorf("config: %s must be an absolute path", path.name)
		}
		if filepath.Clean(path.value) == string(filepath.Separator) {
			return fmt.Errorf("config: %s must not be the filesystem root", path.name)
		}
	}
	mountRoot := filepath.Clean(c.DOFS.MountRoot)
	stateRoot := filepath.Clean(c.DOFS.StateRoot)
	if mountRoot == stateRoot || pathContains(mountRoot, stateRoot) || pathContains(stateRoot, mountRoot) {
		return fmt.Errorf("config: dofs.mount_root and dofs.state_root must not overlap")
	}
	controlSocket := filepath.Clean(c.DOFS.ControlSocket)
	if mountRoot == controlSocket || pathContains(mountRoot, controlSocket) {
		return fmt.Errorf("config: dofs.control_socket must not be inside dofs.mount_root")
	}
	if stateRoot == controlSocket || pathContains(stateRoot, controlSocket) {
		return fmt.Errorf("config: dofs.control_socket must not be inside dofs.state_root")
	}
	if c.DOFS.UID == 0 || c.DOFS.GID == 0 {
		return fmt.Errorf("config: dofs.uid and dofs.gid must be non-root FUSE identities")
	}
	if c.DOFS.MaxMounts <= 0 {
		return fmt.Errorf("config: dofs.max_mounts must be greater than 0")
	}
	if c.DOFS.ReconcileIntervalSeconds <= 0 {
		return fmt.Errorf("config: dofs.reconcile_interval_seconds must be greater than 0")
	}
	if c.DOFS.MountTimeoutSeconds <= 0 {
		return fmt.Errorf("config: dofs.mount_timeout_seconds must be greater than 0")
	}
	if c.DOFS.ShutdownTimeoutSeconds <= 0 {
		return fmt.Errorf("config: dofs.shutdown_timeout_seconds must be greater than 0")
	}
	return nil
}

func (c *Config) validateDOFSMetadata() error {
	switch c.DOFS.Metadata.Driver {
	case "sqlite":
		path := c.DOFS.Metadata.SQLite.Path
		if path != "" {
			if err := validateAbsoluteNonRootPath("dofs.metadata.sqlite.path", path); err != nil {
				return err
			}
		}
		if c.DOFS.Metadata.SQLite.BusyTimeoutSeconds <= 0 {
			return fmt.Errorf("config: dofs.metadata.sqlite.busy_timeout_seconds must be greater than 0")
		}
		if c.DOFS.Metadata.SQLite.MaxOpenConnections <= 0 {
			return fmt.Errorf("config: dofs.metadata.sqlite.max_open_connections must be greater than 0")
		}
	case "postgres":
		// The PostgreSQL adapter deliberately reuses database.* so credentials
		// stay in one Domus secret source.
	default:
		return fmt.Errorf("config: dofs.metadata.driver must be sqlite or postgres")
	}
	return nil
}

func validateAbsoluteNonRootPath(name, value string) error {
	if strings.TrimSpace(value) == "" {
		return fmt.Errorf("config: %s is required", name)
	}
	if !filepath.IsAbs(value) {
		return fmt.Errorf("config: %s must be an absolute path", name)
	}
	if filepath.Clean(value) == string(filepath.Separator) {
		return fmt.Errorf("config: %s must not be the filesystem root", name)
	}
	return nil
}

func pathsOverlapConfig(first, second string) bool {
	return first == second || pathContains(first, second) || pathContains(second, first)
}

func pathContains(parent, child string) bool {
	relative, err := filepath.Rel(parent, child)
	return err == nil && relative != "." && relative != ".." && relative != "" && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}

const defaultConfigPath = "config.yaml"
const defaultDevConfigPath = "config.dev.yaml"

// Load reads a YAML configuration file from path, applies defaults, and
// returns the parsed Config.
func Load(path string) (*Config, error) {
	cfg := &Config{
		// Keep the pre-rename implicit defaults so an existing config that
		// omitted these fields still addresses the same process and database.
		// New deployments use the explicit Domus values in config.example.yaml.
		Server: ServerConfig{Port: 8080, PidFile: "zephyr.pid"},
		DOFS: DOFSConfig{
			Metadata: DOFSMetadataConfig{
				Driver: "sqlite",
				SQLite: DOFSSQLiteMetadataConfig{
					BusyTimeoutSeconds: 5,
					MaxOpenConnections: 8,
				},
			},
			MountRoot:                "/var/lib/domus/dofs/mounts",
			StateRoot:                "/var/lib/domus/dofs/state",
			ControlSocket:            "/run/domus/dofs.sock",
			UID:                      1000,
			GID:                      1000,
			AllowOther:               true,
			Writable:                 true,
			MaxMounts:                32,
			ReconcileIntervalSeconds: 30,
			MountTimeoutSeconds:      60,
			ShutdownTimeoutSeconds:   30,
		},
		Upload: UploadConfig{MaxFileSize: 10 * 1024 * 1024 * 1024},
		Log:    logger.Config{Level: "info"},
	}

	if err := loadInto(path, cfg); err != nil {
		return nil, err
	}
	if path == defaultConfigPath {
		if err := loadOptionalInto(defaultDevConfigPath, cfg); err != nil {
			return nil, err
		}
	}

	// OSS defaults
	if cfg.OSS.ServerEndpoint == "" {
		cfg.OSS.ServerEndpoint = cfg.OSS.ClientUploadEndpoint
	}
	if cfg.OSS.MaxPresignBatch <= 0 {
		cfg.OSS.MaxPresignBatch = 100
	}
	cfg.DOFS.Metadata.Driver = strings.ToLower(strings.TrimSpace(cfg.DOFS.Metadata.Driver))

	// Database defaults
	if cfg.Database.Host == "" {
		cfg.Database.Host = "localhost"
	}
	if cfg.Database.Port == 0 {
		cfg.Database.Port = 5432
	}
	if cfg.Database.User == "" {
		cfg.Database.User = "postgres"
	}
	if cfg.Database.DBName == "" {
		cfg.Database.DBName = "zephyr"
	}
	if cfg.Database.SSLMode == "" {
		cfg.Database.SSLMode = "disable"
	}

	return cfg, nil
}

func loadInto(path string, cfg *Config) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read config: %w", err)
	}
	if err := yaml.Unmarshal(data, cfg); err != nil {
		return fmt.Errorf("parse config: %w", err)
	}
	return nil
}

func loadOptionalInto(path string, cfg *Config) error {
	if err := loadInto(path, cfg); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("load optional config %q: %w", path, err)
	}
	return nil
}

// Save writes the Config as YAML to the given path.
func Save(path string, cfg *Config) error {
	data, err := yaml.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}
	// Config may contain secrets (session/encryption/SMTP credentials).
	return os.WriteFile(path, data, 0600)
}
