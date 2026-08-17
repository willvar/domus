package config

import (
	"os"
	"path/filepath"
	"strings"
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
		Workspace: WorkspaceConfig{
			ControlSocket:      "/run/domus-workspace/control.sock",
			MaxSessionsPerUser: 4, OperationTimeoutSeconds: 120,
			ExecTimeoutSeconds: 900, ShutdownTimeoutSeconds: 30,
		},
	}
}

func TestConfigLoadPreservesRootBootstrapPasswordFile(t *testing.T) {
	cfg := &Config{
		Server: ServerConfig{RootBootstrapPasswordFile: "/run/secrets/domus-root-password"},
	}

	if cfg.Server.RootBootstrapPasswordFile != "/run/secrets/domus-root-password" {
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

	dev := []byte("server:\n  port: 9090\ndatabase:\n  dbname: domus_dev\n")
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
	if cfg.Database.DBName != "domus_dev" {
		t.Fatalf("Database.DBName = %q, want %q", cfg.Database.DBName, "domus_dev")
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

func TestLoadKeepsLegacyImplicitProcessAndDatabaseDefaults(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "legacy-implicit.yaml")
	if err := os.WriteFile(configPath, []byte("server:\n  port: 8080\n"), 0600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Server.PidFile != "zephyr.pid" || cfg.Database.DBName != "zephyr" {
		t.Fatalf("legacy defaults not preserved: pid=%q db=%q", cfg.Server.PidFile, cfg.Database.DBName)
	}
}

func TestLoadUsesExplicitDomusProcessAndDatabaseNames(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "domus.yaml")
	data := []byte("server:\n  port: 8080\n  pid_file: domus.pid\ndatabase:\n  dbname: domus\n")
	if err := os.WriteFile(configPath, data, 0600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Server.PidFile != "domus.pid" || cfg.Database.DBName != "domus" {
		t.Fatalf("explicit Domus names changed: pid=%q db=%q", cfg.Server.PidFile, cfg.Database.DBName)
	}
}

func TestLoadAppliesProductionDOFSDefaults(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "domus.yaml")
	if err := os.WriteFile(configPath, []byte("server:\n  port: 8080\n"), 0600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.DOFS.MountRoot != "/var/lib/domus/dofs/mounts" ||
		cfg.DOFS.StateRoot != "/var/lib/domus/dofs/state" ||
		cfg.DOFS.ControlSocket != "/run/domus/dofs.sock" {
		t.Fatalf("unexpected DOFS paths: %+v", cfg.DOFS)
	}
	if cfg.DOFS.UID != 1000 || cfg.DOFS.GID != 1000 || !cfg.DOFS.AllowOther || !cfg.DOFS.Writable || cfg.DOFS.MaxMounts != 32 {
		t.Fatalf("unexpected DOFS identity/default mode: %+v", cfg.DOFS)
	}
}

func TestLoadAppliesProductionWorkspaceDefaults(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "domus.yaml")
	if err := os.WriteFile(configPath, []byte("server:\n  port: 8080\n"), 0600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Workspace.ControlSocket != "/run/domus-workspace/control.sock" ||
		cfg.Workspace.DOFSMountRoot != "/var/lib/domus/dofs/mounts" ||
		cfg.Workspace.DockerHost != "unix:///var/run/docker.sock" {
		t.Fatalf("unexpected workspace paths: %+v", cfg.Workspace)
	}
	if cfg.Workspace.UID != 1000 || cfg.Workspace.GID != 1000 || !cfg.Workspace.ReadOnlyRootFS ||
		cfg.Workspace.MemoryBytes <= 0 || cfg.Workspace.PIDsLimit <= 0 || cfg.Workspace.MaxRunning != 32 {
		t.Fatalf("unexpected workspace security defaults: %+v", cfg.Workspace)
	}
}

func TestLoadRejectsRemovedWorkspaceEnabledSwitch(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "domus.yaml")
	if err := os.WriteFile(configPath, []byte("workspace:\n  enabled: false\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(configPath); err == nil || !strings.Contains(err.Error(), "workspace.enabled has been removed") {
		t.Fatalf("Load() error = %v", err)
	}
}

func TestValidateWorkspaceRejectsUnsafeRuntimeSettings(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "domus.yaml")
	if err := os.WriteFile(configPath, []byte("server:\n  port: 8080\n"), 0600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := cfg.ValidateWorkspace(); err != nil {
		t.Fatalf("ValidateWorkspace() unexpected error = %v", err)
	}
	tests := []struct {
		name    string
		mutate  func(*Config)
		wantErr string
	}{
		{
			name: "tcp Docker endpoint",
			mutate: func(copy *Config) {
				copy.Workspace.DockerHost = "tcp://127.0.0.1:2375"
			},
			wantErr: "config: workspace.docker_host must be an absolute unix:// socket",
		},
		{
			name: "host network",
			mutate: func(copy *Config) {
				copy.Workspace.NetworkMode = "host"
			},
			wantErr: "config: workspace.network_mode must be none, bridge, or a named non-host network",
		},
		{
			name: "writable container root",
			mutate: func(copy *Config) {
				copy.Workspace.ReadOnlyRootFS = false
			},
			wantErr: "config: workspace.read_only_rootfs must be true",
		},
		{
			name: "root identity",
			mutate: func(copy *Config) {
				copy.Workspace.UID = 0
			},
			wantErr: "config: workspace.uid and workspace.gid must be non-root container identities",
		},
		{
			name: "unlimited memory",
			mutate: func(copy *Config) {
				copy.Workspace.MemoryBytes = 0
			},
			wantErr: "config: workspace memory limits must be positive and memory_swap_bytes must be at least memory_bytes",
		},
		{
			name: "overlapping plaintext state",
			mutate: func(copy *Config) {
				copy.Workspace.StateRoot = "/var/lib/domus/dofs/mounts/state"
			},
			wantErr: "config: workspace.state_root and workspace.dofs_mount_root must not overlap",
		},
		{
			name: "oversized exec output",
			mutate: func(copy *Config) {
				copy.Workspace.ExecOutputLimitBytes = 16*1024*1024 + 1
			},
			wantErr: "config: workspace.exec_output_limit_bytes must be between 1 and 16777216",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			copy := *cfg
			test.mutate(&copy)
			if err := copy.ValidateWorkspace(); err == nil || err.Error() != test.wantErr {
				t.Fatalf("ValidateWorkspace() error = %v, want %q", err, test.wantErr)
			}
		})
	}
}

func TestValidateRequiresWorkspaceSocket(t *testing.T) {
	cfg := validConfig()
	cfg.Workspace.ControlSocket = ""
	if err := cfg.Validate(); err == nil || err.Error() != "config: workspace.control_socket is required" {
		t.Fatalf("Validate() error = %v", err)
	}
}

func TestValidateChecksWorkspaceClientSettings(t *testing.T) {
	cfg := validConfig()
	cfg.Workspace = WorkspaceConfig{
		ControlSocket:      "/run/domus-workspace/control.sock",
		MaxSessionsPerUser: 4, OperationTimeoutSeconds: 120,
		ExecTimeoutSeconds: 900, ShutdownTimeoutSeconds: 30,
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate() unexpected error = %v", err)
	}

	cfg.Workspace.MaxSessionsPerUser = 0
	if err := cfg.Validate(); err == nil || err.Error() != "config: workspace.max_sessions_per_user must be greater than zero" {
		t.Fatalf("Validate() session error = %v", err)
	}
	cfg.Workspace.MaxSessionsPerUser = 4
	cfg.Workspace.ExecTimeoutSeconds = 0
	if err := cfg.Validate(); err == nil || err.Error() != "config: workspace client timeout settings must be greater than zero" {
		t.Fatalf("Validate() timeout error = %v", err)
	}
}

func TestValidateDOFSRejectsUnsafeProductionLayout(t *testing.T) {
	cfg := validConfig()
	cfg.OSS.ServerEndpoint = cfg.OSS.ClientUploadEndpoint
	cfg.DOFS = DOFSConfig{
		MountRoot: "/var/lib/domus/dofs/mounts", StateRoot: "/var/lib/domus/dofs/state",
		ControlSocket: "/run/domus/dofs.sock", UID: 1000, GID: 1000,
		Writable: true, AllowOther: true,
		MaxMounts:                32,
		ReconcileIntervalSeconds: 30, MountTimeoutSeconds: 60, ShutdownTimeoutSeconds: 30,
	}
	if err := cfg.ValidateDOFS(); err != nil {
		t.Fatalf("ValidateDOFS() unexpected error = %v", err)
	}

	tests := []struct {
		name    string
		mutate  func(*Config)
		wantErr string
	}{
		{
			name: "relative mount root",
			mutate: func(cfg *Config) {
				cfg.DOFS.MountRoot = "dofs/mounts"
			},
			wantErr: "config: dofs.mount_root must be an absolute path",
		},
		{
			name: "overlapping state root",
			mutate: func(cfg *Config) {
				cfg.DOFS.StateRoot = "/var/lib/domus/dofs/mounts/state"
			},
			wantErr: "config: dofs.mount_root and dofs.state_root must not overlap",
		},
		{
			name: "socket below mount tree",
			mutate: func(cfg *Config) {
				cfg.DOFS.ControlSocket = "/var/lib/domus/dofs/mounts/control.sock"
			},
			wantErr: "config: dofs.control_socket must not be inside dofs.mount_root",
		},
		{
			name: "root container identity",
			mutate: func(cfg *Config) {
				cfg.DOFS.UID = 0
			},
			wantErr: "config: dofs.uid and dofs.gid must be non-root container identities",
		},
		{
			name: "zero mount capacity",
			mutate: func(cfg *Config) {
				cfg.DOFS.MaxMounts = 0
			},
			wantErr: "config: dofs.max_mounts must be greater than 0",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			copy := *cfg
			test.mutate(&copy)
			if err := copy.ValidateDOFS(); err == nil || err.Error() != test.wantErr {
				t.Fatalf("ValidateDOFS() error = %v, want %q", err, test.wantErr)
			}
		})
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
