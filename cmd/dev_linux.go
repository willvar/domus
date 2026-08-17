//go:build linux

package cmd

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"domus/config"
	"domus/internal/dofs"
	"domus/internal/workspace"
	"domus/shared/logger"
)

const defaultDevWorkspaceImage = "domus-workspace:0.1.0"

type devOptions struct {
	runtimeRoot string
	image       string
}

type devChild struct {
	name string
	cmd  *exec.Cmd
	done chan struct{}

	mu  sync.Mutex
	err error
}

func devCommand(defaultConfigPath string, args []string) {
	flags := flag.NewFlagSet("dev", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	configPath := defaultConfigPath
	options := devOptions{runtimeRoot: "tmp/dev", image: defaultDevWorkspaceImage}
	flags.StringVar(&configPath, "c", configPath, "配置文件路径")
	flags.StringVar(&options.runtimeRoot, "runtime-root", options.runtimeRoot, "本地运行状态目录")
	flags.StringVar(&options.image, "image", options.image, "Workspace 容器镜像")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return
		}
		logger.Fatal("Invalid dev options: %v", err)
	}
	if flags.NArg() != 0 {
		logger.Fatal("Unexpected dev argument: %s", flags.Arg(0))
	}
	if err := runDev(configPath, options); err != nil {
		logger.Fatal("Development stack failed: %v", err)
	}
}

func runDev(configPath string, options devOptions) error {
	if os.Getuid() == 0 || os.Getgid() == 0 {
		return errors.New("run the development stack as a non-root user with Docker access")
	}
	runtimeRoot, err := filepath.Abs(strings.TrimSpace(options.runtimeRoot))
	if err != nil {
		return fmt.Errorf("resolve development runtime root: %w", err)
	}
	runtimeRoot = filepath.Clean(runtimeRoot)
	if runtimeRoot == string(filepath.Separator) {
		return errors.New("development runtime root must not be the filesystem root")
	}
	if err := ensurePrivateDevRoot(runtimeRoot); err != nil {
		return err
	}

	cfg, err := config.Load(configPath)
	if err != nil {
		return err
	}
	// Preserve the existing `domus start` first-run contract: generated secrets
	// belong to the instance config, not the disposable local runtime overlay.
	// Otherwise deleting tmp/dev would make existing wrapped user keys unusable.
	configChanged := false
	if cfg.Server.SessionSecret == "" || cfg.Server.SessionSecret == "change-me-to-random-string" {
		cfg.Server.SessionSecret = generateRandomSecret(32)
		configChanged = true
	}
	if cfg.Server.EncryptionSecret == "" {
		cfg.Server.EncryptionSecret = generateRandomSecret(64)
		configChanged = true
	}
	if configChanged {
		if err := config.Save(configPath, cfg); err != nil {
			return fmt.Errorf("persist generated instance secrets: %w", err)
		}
		logger.Info("Generated instance secrets and saved them to %s", configPath)
	}
	uid := uint32(os.Getuid())
	gid := uint32(os.Getgid())
	applyDevRuntimeConfig(cfg, runtimeRoot, strings.TrimSpace(options.image), uid, gid)

	if err := cfg.Validate(); err != nil {
		return err
	}
	if err := cfg.ValidateDOFS(); err != nil {
		return err
	}
	if err := cfg.ValidateWorkspace(); err != nil {
		return err
	}
	if err := dofs.ValidateHostRequirements(true); err != nil {
		return fmt.Errorf("development DOFS host prerequisite: %w", err)
	}
	if err := os.MkdirAll(filepath.Join(runtimeRoot, "run"), 0700); err != nil {
		return fmt.Errorf("create development runtime directory: %w", err)
	}
	generatedConfig := filepath.Join(runtimeRoot, "config.yaml")
	if err := rejectDevConfigSymlink(generatedConfig); err != nil {
		return err
	}
	if err := config.Save(generatedConfig, cfg); err != nil {
		return fmt.Errorf("write generated development config: %w", err)
	}
	if err := os.Chmod(generatedConfig, 0600); err != nil {
		return fmt.Errorf("secure generated development config: %w", err)
	}

	executable, err := os.Executable()
	if err != nil {
		return fmt.Errorf("resolve Domus executable: %w", err)
	}
	children := make([]*devChild, 0, 3)
	shutdown := func() {
		for index := len(children) - 1; index >= 0; index-- {
			stopDevChild(children[index], 2*time.Minute)
		}
	}
	defer shutdown()

	dofsChild, err := startDevChild("DOFS", executable, "dofs", "serve", "-c", generatedConfig)
	if err != nil {
		return err
	}
	children = append(children, dofsChild)
	if err := waitForDevService(dofsChild, 45*time.Second, func(ctx context.Context) error {
		_, healthErr := (dofs.ControlClient{SocketPath: cfg.DOFS.ControlSocket, Timeout: time.Second}).Health(ctx, true)
		return healthErr
	}); err != nil {
		return err
	}

	workspaceChild, err := startDevChild("Workspace Manager", executable, "workspace", "serve", "-c", generatedConfig)
	if err != nil {
		return err
	}
	children = append(children, workspaceChild)
	if err := waitForDevService(workspaceChild, 90*time.Second, func(ctx context.Context) error {
		health, healthErr := (workspace.ControlClient{SocketPath: cfg.Workspace.ControlSocket, Timeout: time.Second}).Health(ctx, true)
		if healthErr == nil && health.ProtocolVersion != workspace.ControlProtocolVersion {
			return fmt.Errorf("workspace protocol mismatch: %d", health.ProtocolVersion)
		}
		return healthErr
	}); err != nil {
		return err
	}

	webChild, err := startDevChild("Web", executable, "start", "-c", generatedConfig)
	if err != nil {
		return err
	}
	children = append(children, webChild)
	if err := waitForDevService(webChild, 30*time.Second, func(ctx context.Context) error {
		dialer := net.Dialer{Timeout: 250 * time.Millisecond}
		connection, dialErr := dialer.DialContext(ctx, "tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(cfg.Server.Port)))
		if dialErr == nil {
			_ = connection.Close()
		}
		return dialErr
	}); err != nil {
		return err
	}

	logger.Info("Development stack ready: web http://127.0.0.1:%d, runtime %s", cfg.Server.Port, runtimeRoot)
	serviceContext, stopSignals := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stopSignals()
	select {
	case <-serviceContext.Done():
		logger.Info("Stopping development stack...")
		return nil
	case <-dofsChild.done:
		return devChildExitError(dofsChild)
	case <-workspaceChild.done:
		return devChildExitError(workspaceChild)
	case <-webChild.done:
		return devChildExitError(webChild)
	}
}

func applyDevRuntimeConfig(cfg *config.Config, runtimeRoot, image string, uid, gid uint32) {
	runRoot := filepath.Join(runtimeRoot, "run")
	cfg.Server.PidFile = filepath.Join(runtimeRoot, "domus.pid")
	cfg.DOFS.MountRoot = filepath.Join(runtimeRoot, "dofs", "mounts")
	cfg.DOFS.StateRoot = filepath.Join(runtimeRoot, "dofs", "state")
	cfg.DOFS.ControlSocket = filepath.Join(runRoot, "dofs.sock")
	cfg.DOFS.SocketGroup = ""
	cfg.DOFS.UID = uid
	cfg.DOFS.GID = gid
	cfg.DOFS.AllowOther = true
	cfg.Workspace.ControlSocket = filepath.Join(runRoot, "workspace.sock")
	cfg.Workspace.SocketGroup = ""
	cfg.Workspace.StateRoot = filepath.Join(runtimeRoot, "workspace")
	cfg.Workspace.DOFSControlSocket = cfg.DOFS.ControlSocket
	cfg.Workspace.DOFSMountRoot = cfg.DOFS.MountRoot
	cfg.Workspace.Image = image
	cfg.Workspace.PullPolicy = "never"
	cfg.Workspace.ContainerPrefix = "domus-dev-" + strconv.FormatUint(uint64(uid), 10)
	cfg.Workspace.UID = uid
	cfg.Workspace.GID = gid
	cfg.Log.File = ""
}

func ensurePrivateDevRoot(root string) error {
	info, err := os.Lstat(root)
	if errors.Is(err, os.ErrNotExist) {
		if err := os.MkdirAll(root, 0700); err != nil {
			return fmt.Errorf("create development runtime root: %w", err)
		}
		return nil
	}
	if err != nil {
		return fmt.Errorf("inspect development runtime root: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return errors.New("development runtime root must be a real directory")
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || stat.Uid != uint32(os.Geteuid()) {
		return errors.New("development runtime root must be owned by the current user")
	}
	if info.Mode().Perm()&0077 != 0 {
		return errors.New("existing development runtime root must not be accessible by group or other users")
	}
	return nil
}

func rejectDevConfigSymlink(path string) error {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("inspect generated development config: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return errors.New("generated development config path must be a regular file")
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || stat.Uid != uint32(os.Geteuid()) || stat.Nlink != 1 {
		return errors.New("generated development config must be owned by the current user and not hard-linked")
	}
	return nil
}

func startDevChild(name, executable string, args ...string) (*devChild, error) {
	command := exec.Command(executable, args...)
	command.Stdin = os.Stdin
	command.Stdout = os.Stdout
	command.Stderr = os.Stderr
	// Put each service in its own process group so terminal Ctrl+C reaches this
	// supervisor first; it can then preserve Web -> Workspace -> DOFS shutdown
	// ordering. Pdeathsig prevents orphan services if the supervisor crashes.
	command.SysProcAttr = &syscall.SysProcAttr{Pdeathsig: syscall.SIGTERM, Setpgid: true}
	if err := command.Start(); err != nil {
		return nil, fmt.Errorf("start %s: %w", name, err)
	}
	child := &devChild{name: name, cmd: command, done: make(chan struct{})}
	go func() {
		err := command.Wait()
		child.mu.Lock()
		child.err = err
		child.mu.Unlock()
		close(child.done)
	}()
	return child, nil
}

func waitForDevService(child *devChild, timeout time.Duration, check func(context.Context) error) error {
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	var lastErr error
	for {
		select {
		case <-child.done:
			return devChildExitError(child)
		case <-deadline.C:
			return fmt.Errorf("%s readiness timed out: %w", child.name, lastErr)
		case <-ticker.C:
			checkContext, cancel := context.WithTimeout(context.Background(), time.Second)
			lastErr = check(checkContext)
			cancel()
			if lastErr == nil {
				return nil
			}
		}
	}
}

func devChildExitError(child *devChild) error {
	child.mu.Lock()
	err := child.err
	child.mu.Unlock()
	if err == nil {
		return fmt.Errorf("%s exited unexpectedly", child.name)
	}
	return fmt.Errorf("%s exited: %w", child.name, err)
}

func stopDevChild(child *devChild, timeout time.Duration) {
	select {
	case <-child.done:
		return
	default:
	}
	if child.cmd.Process != nil {
		_ = child.cmd.Process.Signal(syscall.SIGTERM)
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case <-child.done:
	case <-timer.C:
		if child.cmd.Process != nil {
			_ = child.cmd.Process.Kill()
		}
		<-child.done
	}
}
