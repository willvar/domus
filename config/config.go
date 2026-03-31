package config

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"

	"zephyr/shared/logger"
)

const (
	UploadChunkSize int64 = 5 * 1024 * 1024
	TempDir               = "tmp"
)

type Config struct {
	OSS       OSSConfig       `yaml:"oss"`
	Server    ServerConfig    `yaml:"server"`
	Upload    UploadConfig    `yaml:"upload"`
	Database  DatabaseConfig  `yaml:"database"`
	Transcode TranscodeConfig `yaml:"transcode"`
	Jobs      JobsConfig      `yaml:"jobs"`
	SMTP      SMTPConfig      `yaml:"smtp"`
	Log       logger.Config   `yaml:"log"`
}

type OSSConfig struct {
	Endpoint        string `yaml:"endpoint"`
	AccessKeyID     string `yaml:"access_key_id"`
	AccessKeySecret string `yaml:"access_key_secret"`
	Bucket          string `yaml:"bucket"`
	Region          string `yaml:"region"`
	CNAME           bool   `yaml:"cname"` // true when endpoint is a custom domain
}

type ServerConfig struct {
	Port             int    `yaml:"port"`
	PidFile          string `yaml:"pid_file"`
	SessionSecret    string `yaml:"session_secret"`
	EncryptionSecret string `yaml:"encryption_secret"`
	CORSOrigins      string `yaml:"cors_origins"`
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

type TranscodeConfig struct {
	FFmpegPath  string `yaml:"ffmpeg_path"`
	FFprobePath string `yaml:"ffprobe_path"`
}

type JobsConfig struct {
	TranscodeConcurrency int `yaml:"transcode_concurrency"`
	ThumbnailConcurrency int `yaml:"thumbnail_concurrency"`
	SystemConcurrency    int `yaml:"system_concurrency"`
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
	if c.OSS.Endpoint == "" {
		return fmt.Errorf("config: oss.endpoint is required")
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

// Load reads a YAML configuration file from path, applies defaults, and
// returns the parsed Config.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}

	cfg := &Config{
		Server: ServerConfig{Port: 8080, PidFile: "zephyr.pid"},
		Upload: UploadConfig{MaxFileSize: 10 * 1024 * 1024 * 1024},
		Log:    logger.Config{Level: "info"},
	}

	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
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

	// Transcode defaults
	if cfg.Transcode.FFmpegPath == "" {
		cfg.Transcode.FFmpegPath = "ffmpeg"
	}
	if cfg.Transcode.FFprobePath == "" {
		cfg.Transcode.FFprobePath = "ffprobe"
	}

	// Jobs defaults
	if cfg.Jobs.TranscodeConcurrency <= 0 {
		cfg.Jobs.TranscodeConcurrency = 2
	}
	if cfg.Jobs.ThumbnailConcurrency <= 0 {
		cfg.Jobs.ThumbnailConcurrency = 4
	}
	if cfg.Jobs.SystemConcurrency <= 0 {
		cfg.Jobs.SystemConcurrency = 4
	}

	return cfg, nil
}

// Save writes the Config as YAML to the given path.
func Save(path string, cfg *Config) error {
	data, err := yaml.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}
	return os.WriteFile(path, data, 0644)
}
