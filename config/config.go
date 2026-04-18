package config

import (
	"errors"
	"fmt"
	"os"

	"gopkg.in/yaml.v3"

	"zephyr/shared/logger"
)

const (
	UploadChunkSize int64 = 5 * 1024 * 1024
)

type Config struct {
	OSS      OSSConfig      `yaml:"oss"`
	Server   ServerConfig   `yaml:"server"`
	Upload   UploadConfig   `yaml:"upload"`
	Database DatabaseConfig `yaml:"database"`
	SMTP     SMTPConfig     `yaml:"smtp"`
	Log      logger.Config  `yaml:"log"`
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
	return nil
}

const defaultConfigPath = "config.yaml"
const defaultDevConfigPath = "config.dev.yaml"

// Load reads a YAML configuration file from path, applies defaults, and
// returns the parsed Config.
func Load(path string) (*Config, error) {
	cfg := &Config{
		Server: ServerConfig{Port: 8080, PidFile: "zephyr.pid"},
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
