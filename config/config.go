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
	OSS       OSSConfig       `yaml:"oss"`
	Server    ServerConfig    `yaml:"server"`
	DOFS      DOFSConfig      `yaml:"dofs"`
	Workspace WorkspaceConfig `yaml:"workspace"`
	Upload    UploadConfig    `yaml:"upload"`
	Database  DatabaseConfig  `yaml:"database"`
	SMTP      SMTPConfig      `yaml:"smtp"`
	Log       logger.Config   `yaml:"log"`
}

// DOFSConfig controls the Linux DOFS data-plane service. Paths are explicit
// and host-local because FUSE mounts and their plaintext writeback state must
// never be placed in object storage or a container writable layer.
type DOFSConfig struct {
	MountRoot                string `yaml:"mount_root"`
	StateRoot                string `yaml:"state_root"`
	ControlSocket            string `yaml:"control_socket"`
	SocketGroup              string `yaml:"socket_group"`
	UID                      uint32 `yaml:"uid"`
	GID                      uint32 `yaml:"gid"`
	AllowOther               bool   `yaml:"allow_other"`
	Writable                 bool   `yaml:"writable"`
	MaxMounts                int    `yaml:"max_mounts"`
	ReconcileIntervalSeconds int    `yaml:"reconcile_interval_seconds"`
	MountTimeoutSeconds      int    `yaml:"mount_timeout_seconds"`
	ShutdownTimeoutSeconds   int    `yaml:"shutdown_timeout_seconds"`
}

// WorkspaceConfig controls the Linux workspace execution plane. The main
// Domus process uses the client/admission fields (ControlSocket,
// MaxSessionsPerUser, and operation/exec/shutdown timeouts); the remaining
// settings are consumed by the separately-accounted `domus workspace serve`
// daemon. Keeping the Docker socket out of the web process and user
// containers is a deliberate privilege boundary.
type WorkspaceConfig struct {
	ControlSocket            string   `yaml:"control_socket"`
	SocketGroup              string   `yaml:"socket_group"`
	StateRoot                string   `yaml:"state_root"`
	DOFSControlSocket        string   `yaml:"dofs_control_socket"`
	DOFSMountRoot            string   `yaml:"dofs_mount_root"`
	DockerHost               string   `yaml:"docker_host"`
	Image                    string   `yaml:"image"`
	PullPolicy               string   `yaml:"pull_policy"`
	ContainerPrefix          string   `yaml:"container_prefix"`
	UID                      uint32   `yaml:"uid"`
	GID                      uint32   `yaml:"gid"`
	NetworkMode              string   `yaml:"network_mode"`
	ReadOnlyRootFS           bool     `yaml:"read_only_rootfs"`
	MemoryBytes              int64    `yaml:"memory_bytes"`
	MemorySwapBytes          int64    `yaml:"memory_swap_bytes"`
	NanoCPUs                 int64    `yaml:"nano_cpus"`
	PIDsLimit                int64    `yaml:"pids_limit"`
	TmpfsSizeBytes           int64    `yaml:"tmpfs_size_bytes"`
	ShmSizeBytes             int64    `yaml:"shm_size_bytes"`
	MaxRunning               int      `yaml:"max_running"`
	MaxSessionsPerUser       int      `yaml:"max_sessions_per_user"`
	IdleTimeoutSeconds       int      `yaml:"idle_timeout_seconds"`
	ReconcileIntervalSeconds int      `yaml:"reconcile_interval_seconds"`
	OperationTimeoutSeconds  int      `yaml:"operation_timeout_seconds"`
	ExecTimeoutSeconds       int      `yaml:"exec_timeout_seconds"`
	ExecOutputLimitBytes     int64    `yaml:"exec_output_limit_bytes"`
	StopTimeoutSeconds       int      `yaml:"stop_timeout_seconds"`
	ShutdownTimeoutSeconds   int      `yaml:"shutdown_timeout_seconds"`
	Shell                    []string `yaml:"shell"`
	Keepalive                []string `yaml:"keepalive"`
}

// UnmarshalYAML rejects the former rollout switch instead of silently
// ignoring it. Workspace is now the only execution architecture, so accepting
// enabled: false would give operators a dangerously false sense of fallback.
func (c *WorkspaceConfig) UnmarshalYAML(node *yaml.Node) error {
	if node.Kind != yaml.MappingNode {
		return errors.New("workspace must be a mapping")
	}
	for index := 0; index+1 < len(node.Content); index += 2 {
		if node.Content[index].Value == "enabled" {
			return errors.New("workspace.enabled has been removed; Workspace Manager is always required")
		}
	}
	type plainWorkspaceConfig WorkspaceConfig
	return node.Decode((*plainWorkspaceConfig)(c))
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
	if err := validateAbsoluteNonRootPath("workspace.control_socket", c.Workspace.ControlSocket); err != nil {
		return err
	}
	if c.Workspace.MaxSessionsPerUser <= 0 {
		return fmt.Errorf("config: workspace.max_sessions_per_user must be greater than zero")
	}
	if c.Workspace.OperationTimeoutSeconds <= 0 || c.Workspace.ExecTimeoutSeconds <= 0 ||
		c.Workspace.ShutdownTimeoutSeconds <= 0 {
		return fmt.Errorf("config: workspace client timeout settings must be greater than zero")
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
		return fmt.Errorf("config: dofs.uid and dofs.gid must be non-root container identities")
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

// ValidateWorkspace validates settings consumed by `domus workspace serve`.
// It intentionally does not require database, OSS, or encryption credentials:
// the workspace daemon receives decrypted files only through DOFS and must not
// be given the secrets needed to decrypt object storage itself.
func (c *Config) ValidateWorkspace() error {
	paths := []struct {
		name  string
		value string
	}{
		{name: "workspace.control_socket", value: c.Workspace.ControlSocket},
		{name: "workspace.state_root", value: c.Workspace.StateRoot},
		{name: "workspace.dofs_control_socket", value: c.Workspace.DOFSControlSocket},
		{name: "workspace.dofs_mount_root", value: c.Workspace.DOFSMountRoot},
	}
	for _, candidate := range paths {
		if err := validateAbsoluteNonRootPath(candidate.name, candidate.value); err != nil {
			return err
		}
	}

	workspaceSocket := filepath.Clean(c.Workspace.ControlSocket)
	stateRoot := filepath.Clean(c.Workspace.StateRoot)
	dofsSocket := filepath.Clean(c.Workspace.DOFSControlSocket)
	dofsMountRoot := filepath.Clean(c.Workspace.DOFSMountRoot)
	if workspaceSocket == dofsSocket {
		return fmt.Errorf("config: workspace.control_socket and workspace.dofs_control_socket must differ")
	}
	if pathsOverlapConfig(stateRoot, dofsMountRoot) {
		return fmt.Errorf("config: workspace.state_root and workspace.dofs_mount_root must not overlap")
	}
	if workspaceSocket == stateRoot || pathContains(stateRoot, workspaceSocket) {
		return fmt.Errorf("config: workspace.control_socket must not be inside workspace.state_root")
	}
	if workspaceSocket == dofsMountRoot || pathContains(dofsMountRoot, workspaceSocket) {
		return fmt.Errorf("config: workspace.control_socket must not be inside workspace.dofs_mount_root")
	}

	dockerSocket := strings.TrimPrefix(strings.TrimSpace(c.Workspace.DockerHost), "unix://")
	if dockerSocket == c.Workspace.DockerHost || filepath.Clean(dockerSocket) == string(filepath.Separator) || !filepath.IsAbs(dockerSocket) {
		return fmt.Errorf("config: workspace.docker_host must be an absolute unix:// socket")
	}
	if strings.TrimSpace(c.Workspace.Image) == "" {
		return fmt.Errorf("config: workspace.image is required")
	}
	switch c.Workspace.PullPolicy {
	case "never", "if_not_present", "always":
	default:
		return fmt.Errorf("config: workspace.pull_policy must be never, if_not_present, or always")
	}
	if !safeRuntimeName(c.Workspace.ContainerPrefix) {
		return fmt.Errorf("config: workspace.container_prefix is invalid")
	}
	if c.Workspace.UID == 0 || c.Workspace.GID == 0 {
		return fmt.Errorf("config: workspace.uid and workspace.gid must be non-root container identities")
	}
	if !safeNetworkMode(c.Workspace.NetworkMode) {
		return fmt.Errorf("config: workspace.network_mode must be none, bridge, or a named non-host network")
	}
	if !c.Workspace.ReadOnlyRootFS {
		return fmt.Errorf("config: workspace.read_only_rootfs must be true")
	}
	if c.Workspace.MemoryBytes <= 0 || c.Workspace.MemorySwapBytes < c.Workspace.MemoryBytes {
		return fmt.Errorf("config: workspace memory limits must be positive and memory_swap_bytes must be at least memory_bytes")
	}
	if c.Workspace.NanoCPUs <= 0 || c.Workspace.PIDsLimit <= 0 || c.Workspace.TmpfsSizeBytes <= 0 || c.Workspace.ShmSizeBytes <= 0 {
		return fmt.Errorf("config: workspace CPU, PID, tmpfs, and shm limits must be greater than zero")
	}
	if c.Workspace.MaxRunning <= 0 || c.Workspace.MaxSessionsPerUser <= 0 {
		return fmt.Errorf("config: workspace capacity limits must be greater than zero")
	}
	if c.Workspace.IdleTimeoutSeconds <= 0 || c.Workspace.ReconcileIntervalSeconds <= 0 ||
		c.Workspace.OperationTimeoutSeconds <= 0 || c.Workspace.ExecTimeoutSeconds <= 0 ||
		c.Workspace.StopTimeoutSeconds <= 0 || c.Workspace.ShutdownTimeoutSeconds <= 0 {
		return fmt.Errorf("config: workspace timeout and reconcile settings must be greater than zero")
	}
	if c.Workspace.ExecOutputLimitBytes <= 0 || c.Workspace.ExecOutputLimitBytes > 16*1024*1024 {
		return fmt.Errorf("config: workspace.exec_output_limit_bytes must be between 1 and 16777216")
	}
	if err := validateCommand("workspace.shell", c.Workspace.Shell); err != nil {
		return err
	}
	if err := validateCommand("workspace.keepalive", c.Workspace.Keepalive); err != nil {
		return err
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

func safeRuntimeName(value string) bool {
	if value == "" || len(value) > 63 || value[0] == '.' || value[0] == '-' {
		return false
	}
	for _, character := range value {
		if character >= 'a' && character <= 'z' || character >= 'A' && character <= 'Z' ||
			character >= '0' && character <= '9' || character == '_' || character == '.' || character == '-' {
			continue
		}
		return false
	}
	return true
}

func safeNetworkMode(value string) bool {
	value = strings.TrimSpace(value)
	if value == "none" || value == "bridge" {
		return true
	}
	if value == "" || value == "host" || strings.HasPrefix(value, "container:") || strings.HasPrefix(value, "service:") {
		return false
	}
	return safeRuntimeName(value)
}

func validateCommand(name string, command []string) error {
	if len(command) == 0 || len(command) > 64 {
		return fmt.Errorf("config: %s must contain between 1 and 64 arguments", name)
	}
	for _, argument := range command {
		if argument == "" || len(argument) > 4096 || strings.IndexByte(argument, 0) >= 0 {
			return fmt.Errorf("config: %s contains an invalid argument", name)
		}
	}
	return nil
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
		Workspace: WorkspaceConfig{
			ControlSocket:            "/run/domus-workspace/control.sock",
			StateRoot:                "/var/lib/domus-workspace",
			DOFSControlSocket:        "/run/domus/dofs.sock",
			DOFSMountRoot:            "/var/lib/domus/dofs/mounts",
			DockerHost:               "unix:///var/run/docker.sock",
			Image:                    "domus-workspace:latest",
			PullPolicy:               "never",
			ContainerPrefix:          "domus-workspace",
			UID:                      1000,
			GID:                      1000,
			NetworkMode:              "bridge",
			ReadOnlyRootFS:           true,
			MemoryBytes:              1024 * 1024 * 1024,
			MemorySwapBytes:          1024 * 1024 * 1024,
			NanoCPUs:                 1_000_000_000,
			PIDsLimit:                256,
			TmpfsSizeBytes:           64 * 1024 * 1024,
			ShmSizeBytes:             64 * 1024 * 1024,
			MaxRunning:               32,
			MaxSessionsPerUser:       4,
			IdleTimeoutSeconds:       1800,
			ReconcileIntervalSeconds: 30,
			OperationTimeoutSeconds:  120,
			ExecTimeoutSeconds:       900,
			ExecOutputLimitBytes:     16 * 1024 * 1024,
			StopTimeoutSeconds:       10,
			ShutdownTimeoutSeconds:   30,
			Shell:                    []string{"/bin/bash"},
			Keepalive: []string{
				"/bin/sh", "-c", "trap 'exit 0' TERM INT; while :; do sleep 3600 & wait $!; done",
			},
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
