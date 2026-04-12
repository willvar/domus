package config

import (
	"os"
	"path/filepath"
	"testing"
)

func validConfig() *Config {
	return &Config{
		OSS: OSSConfig{
			ClientUploadEndpoint: "https://upload.example.com",
			AccessKeyID:          "ak",
			AccessKeySecret:      "sk",
			Bucket:               "bucket",
			Region:               "cn-test-1",
		},
		Server: ServerConfig{
			Port:             8080,
			SessionSecret:    "session-secret",
			EncryptionSecret: "00112233445566778899aabbccddeeff00112233445566778899aabbccddeeff",
		},
		Database: DatabaseConfig{
			Host: "localhost",
		},
	}
}

func TestConfigLoadPreservesRootBootstrapPasswordFile(t *testing.T) {
	cfg := &Config{
		Server: ServerConfig{RootBootstrapPasswordFile: "/run/secrets/zephyr-root-password"},
	}

	if cfg.Server.RootBootstrapPasswordFile != "/run/secrets/zephyr-root-password" {
		t.Fatalf("unexpected root bootstrap password file: %q", cfg.Server.RootBootstrapPasswordFile)
	}
}

func TestLoadAppliesConfigDevOverlayForDefaultConfigPath(t *testing.T) {
	dir := t.TempDir()
	oldWd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd() error = %v", err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("Chdir(%q) error = %v", dir, err)
	}
	defer func() {
		if err := os.Chdir(oldWd); err != nil {
			t.Fatalf("restore cwd error = %v", err)
		}
	}()

	base := []byte("server:\n  port: 8080\n  session_secret: base-secret\n  encryption_secret: 00112233445566778899aabbccddeeff00112233445566778899aabbccddeeff\ndatabase:\n  host: db-base\noss:\n  client_upload_endpoint: https://upload-base.example.com\n  access_key_id: ak-base\n  access_key_secret: sk-base\n  bucket: bucket-base\n  region: cn-base-1\n")
	if err := os.WriteFile(filepath.Join(dir, defaultConfigPath), base, 0600); err != nil {
		t.Fatalf("WriteFile(config.yaml) error = %v", err)
	}

	dev := []byte("server:\n  port: 9090\ndatabase:\n  dbname: zephyr_dev\n")
	if err := os.WriteFile(filepath.Join(dir, defaultDevConfigPath), dev, 0600); err != nil {
		t.Fatalf("WriteFile(config.dev.yaml) error = %v", err)
	}

	cfg, err := Load(defaultConfigPath)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.Server.Port != 9090 {
		t.Fatalf("Server.Port = %d, want 9090", cfg.Server.Port)
	}
	if cfg.Database.Host != "db-base" {
		t.Fatalf("Database.Host = %q, want %q", cfg.Database.Host, "db-base")
	}
	if cfg.Database.DBName != "zephyr_dev" {
		t.Fatalf("Database.DBName = %q, want %q", cfg.Database.DBName, "zephyr_dev")
	}
	if cfg.OSS.ClientUploadEndpoint != "https://upload-base.example.com" {
		t.Fatalf("OSS.ClientUploadEndpoint = %q", cfg.OSS.ClientUploadEndpoint)
	}
}

func TestLoadSkipsConfigDevOverlayForExplicitConfigPath(t *testing.T) {
	dir := t.TempDir()
	oldWd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd() error = %v", err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("Chdir(%q) error = %v", dir, err)
	}
	defer func() {
		if err := os.Chdir(oldWd); err != nil {
			t.Fatalf("restore cwd error = %v", err)
		}
	}()

	custom := []byte("server:\n  port: 8081\n  session_secret: custom-secret\n  encryption_secret: 00112233445566778899aabbccddeeff00112233445566778899aabbccddeeff\ndatabase:\n  host: db-custom\noss:\n  client_upload_endpoint: https://upload-custom.example.com\n  access_key_id: ak-custom\n  access_key_secret: sk-custom\n  bucket: bucket-custom\n  region: cn-custom-1\n")
	if err := os.WriteFile(filepath.Join(dir, "custom.yaml"), custom, 0600); err != nil {
		t.Fatalf("WriteFile(custom.yaml) error = %v", err)
	}

	dev := []byte("server:\n  port: 9090\n")
	if err := os.WriteFile(filepath.Join(dir, defaultDevConfigPath), dev, 0600); err != nil {
		t.Fatalf("WriteFile(config.dev.yaml) error = %v", err)
	}

	cfg, err := Load("custom.yaml")
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.Server.Port != 8081 {
		t.Fatalf("Server.Port = %d, want 8081", cfg.Server.Port)
	}
}

func TestConfigValidate(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(*Config)
		wantErr string
	}{
		{
			name:    "valid config",
			mutate:  func(*Config) {},
			wantErr: "",
		},
		{
			name: "missing upload endpoint",
			mutate: func(cfg *Config) {
				cfg.OSS.ClientUploadEndpoint = ""
			},
			wantErr: "config: oss.client_upload_endpoint is required",
		},
		{
			name: "missing access key",
			mutate: func(cfg *Config) {
				cfg.OSS.AccessKeyID = ""
			},
			wantErr: "config: oss.access_key_id is required",
		},
		{
			name: "missing access key secret",
			mutate: func(cfg *Config) {
				cfg.OSS.AccessKeySecret = ""
			},
			wantErr: "config: oss.access_key_secret is required",
		},
		{
			name: "missing bucket",
			mutate: func(cfg *Config) {
				cfg.OSS.Bucket = ""
			},
			wantErr: "config: oss.bucket is required",
		},
		{
			name: "missing region",
			mutate: func(cfg *Config) {
				cfg.OSS.Region = ""
			},
			wantErr: "config: oss.region is required",
		},
		{
			name: "invalid port",
			mutate: func(cfg *Config) {
				cfg.Server.Port = 0
			},
			wantErr: "config: server.port must be greater than 0",
		},
		{
			name: "missing database host",
			mutate: func(cfg *Config) {
				cfg.Database.Host = ""
			},
			wantErr: "config: database.host is required",
		},
		{
			name: "missing session secret",
			mutate: func(cfg *Config) {
				cfg.Server.SessionSecret = ""
			},
			wantErr: "config: server.session_secret is required",
		},
		{
			name: "missing encryption secret",
			mutate: func(cfg *Config) {
				cfg.Server.EncryptionSecret = ""
			},
			wantErr: "config: server.encryption_secret is required",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := validConfig()
			tt.mutate(cfg)

			err := cfg.Validate()
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("Validate() unexpected error: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("Validate() expected error %q, got nil", tt.wantErr)
			}
			if err.Error() != tt.wantErr {
				t.Fatalf("Validate() error = %q, want %q", err.Error(), tt.wantErr)
			}
		})
	}
}
