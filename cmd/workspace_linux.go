//go:build linux

package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net"
	"os"
	"os/signal"
	"os/user"
	"strconv"
	"strings"
	"syscall"
	"time"

	"domus/config"
	"domus/internal/dofs"
	"domus/internal/workspace"
	"domus/shared/logger"
)

func workspaceCommand(defaultConfigPath string, args []string) {
	if len(args) == 0 {
		printWorkspaceHelp()
		return
	}
	switch args[0] {
	case "serve":
		workspaceServeCommand(defaultConfigPath, args[1:])
	case "ensure", "status", "stop", "remove", "exec", "health", "reconcile", "list":
		workspaceControlCommand(defaultConfigPath, args[0], args[1:])
	case "help", "-h", "--help":
		printWorkspaceHelp()
	default:
		printWorkspaceHelp()
		logger.Fatal("Unknown workspace command: %s", args[0])
	}
}

func workspaceServeCommand(defaultConfigPath string, args []string) {
	flags := flag.NewFlagSet("workspace serve", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	flags.Usage = printWorkspaceHelp
	configPath := defaultConfigPath
	flags.StringVar(&configPath, "c", configPath, "配置文件路径")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return
		}
		logger.Fatal("Invalid workspace serve options: %v", err)
	}
	if flags.NArg() != 0 {
		logger.Fatal("Unexpected workspace serve argument: %s", flags.Arg(0))
	}
	if err := runWorkspaceServe(configPath); err != nil {
		logger.Fatal("Workspace service failed: %v", err)
	}
}

func runWorkspaceServe(configPath string) error {
	cfg, err := config.Load(configPath)
	if err != nil {
		return err
	}
	if err := cfg.ValidateWorkspace(); err != nil {
		return err
	}
	if err := logger.InitFromConfig(&cfg.Log); err != nil {
		return fmt.Errorf("initialize workspace logging: %w", err)
	}
	socketGID, socketMode, err := resolveWorkspaceSocketAccess(cfg.Workspace.SocketGroup)
	if err != nil {
		return err
	}
	runtime, err := workspace.NewDockerRuntime(cfg.Workspace.DockerHost)
	if err != nil {
		return err
	}
	filesystem := dofs.ControlClient{
		SocketPath: cfg.Workspace.DOFSControlSocket,
		Timeout:    time.Duration(cfg.Workspace.OperationTimeoutSeconds) * time.Second,
	}
	manager, err := workspace.NewManager(workspace.ManagerOptions{
		StateRoot: cfg.Workspace.StateRoot, ControlSocket: cfg.Workspace.ControlSocket,
		SocketMode: socketMode, SocketGID: socketGID, DOFSMountRoot: cfg.Workspace.DOFSMountRoot,
		Image: cfg.Workspace.Image, PullPolicy: cfg.Workspace.PullPolicy, ContainerPrefix: cfg.Workspace.ContainerPrefix,
		UID: cfg.Workspace.UID, GID: cfg.Workspace.GID, NetworkMode: cfg.Workspace.NetworkMode,
		ReadOnlyRootFS: cfg.Workspace.ReadOnlyRootFS, MemoryBytes: cfg.Workspace.MemoryBytes,
		MemorySwapBytes: cfg.Workspace.MemorySwapBytes, NanoCPUs: cfg.Workspace.NanoCPUs,
		PIDsLimit: cfg.Workspace.PIDsLimit, TmpfsSizeBytes: cfg.Workspace.TmpfsSizeBytes,
		ShmSizeBytes: cfg.Workspace.ShmSizeBytes, MaxRunning: cfg.Workspace.MaxRunning,
		MaxSessionsPerUser: cfg.Workspace.MaxSessionsPerUser,
		IdleTimeout:        time.Duration(cfg.Workspace.IdleTimeoutSeconds) * time.Second,
		ReconcileInterval:  time.Duration(cfg.Workspace.ReconcileIntervalSeconds) * time.Second,
		OperationTimeout:   time.Duration(cfg.Workspace.OperationTimeoutSeconds) * time.Second,
		ExecTimeout:        time.Duration(cfg.Workspace.ExecTimeoutSeconds) * time.Second,
		ExecOutputLimit:    cfg.Workspace.ExecOutputLimitBytes,
		StopTimeout:        time.Duration(cfg.Workspace.StopTimeoutSeconds) * time.Second,
		Shell:              append([]string(nil), cfg.Workspace.Shell...), Keepalive: append([]string(nil), cfg.Workspace.Keepalive...),
		Logf: logger.Info, OnReady: notifySystemdWorkspaceReady,
	}, filesystem, runtime)
	if err != nil {
		_ = runtime.Close()
		return err
	}
	defer func() { _ = manager.Close() }()
	preflightContext, cancelPreflight := context.WithTimeout(
		context.Background(), time.Duration(cfg.Workspace.OperationTimeoutSeconds)*time.Second,
	)
	preflightErr := manager.Preflight(preflightContext)
	cancelPreflight()
	if preflightErr != nil {
		return fmt.Errorf("workspace preflight: %w", preflightErr)
	}

	serviceContext, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	logger.Info("Workspace manager starting (state root %s, image %s)", cfg.Workspace.StateRoot, cfg.Workspace.Image)
	serveErr := manager.ServeControl(serviceContext)
	stop()
	shutdownContext, cancelShutdown := context.WithTimeout(context.Background(), time.Duration(cfg.Workspace.ShutdownTimeoutSeconds)*time.Second)
	shutdownErr := manager.Shutdown(shutdownContext)
	cancelShutdown()
	if serveErr != nil {
		serveErr = fmt.Errorf("serve workspace control socket: %w", serveErr)
	}
	if shutdownErr != nil {
		shutdownErr = fmt.Errorf("shutdown workspaces: %w", shutdownErr)
	}
	if err := errors.Join(serveErr, shutdownErr); err != nil {
		return err
	}
	logger.Info("Workspace manager stopped")
	return nil
}

func resolveWorkspaceSocketAccess(group string) (int, os.FileMode, error) {
	group = strings.TrimSpace(group)
	if group == "" {
		return -1, 0600, nil
	}
	if numeric, err := strconv.Atoi(group); err == nil {
		if numeric < 0 {
			return 0, 0, errors.New("workspace socket group ID must not be negative")
		}
		return numeric, 0660, nil
	}
	resolved, err := user.LookupGroup(group)
	if err != nil {
		return 0, 0, fmt.Errorf("resolve workspace socket group %q: %w", group, err)
	}
	gid, err := strconv.Atoi(resolved.Gid)
	if err != nil || gid < 0 {
		return 0, 0, fmt.Errorf("invalid GID for workspace socket group %q", group)
	}
	return gid, 0660, nil
}

func notifySystemdWorkspaceReady() error {
	socket := strings.TrimSpace(os.Getenv("NOTIFY_SOCKET"))
	if socket == "" {
		return nil
	}
	if strings.HasPrefix(socket, "@") {
		socket = "\x00" + strings.TrimPrefix(socket, "@")
	} else if !strings.HasPrefix(socket, "/") {
		return errors.New("systemd NOTIFY_SOCKET must be absolute or abstract")
	}
	connection, err := net.DialUnix("unixgram", nil, &net.UnixAddr{Name: socket, Net: "unixgram"})
	if err != nil {
		return err
	}
	defer func() { _ = connection.Close() }()
	_, err = connection.Write([]byte("READY=1\nSTATUS=Workspace control socket is ready"))
	return err
}

func workspaceControlCommand(defaultConfigPath, action string, args []string) {
	flags := flag.NewFlagSet("workspace "+action, flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	flags.Usage = printWorkspaceHelp
	configPath := defaultConfigPath
	var socket, userID, username, workingDirectory string
	var outputJSON, ready bool
	var timeout int
	flags.StringVar(&configPath, "c", configPath, "配置文件路径")
	flags.StringVar(&socket, "socket", "", "覆盖 workspace 控制 Socket")
	flags.StringVar(&userID, "user-id", "", "用户 UUID")
	flags.StringVar(&username, "user", "", "用户名")
	flags.StringVar(&workingDirectory, "workdir", "", "容器内工作目录")
	flags.IntVar(&timeout, "timeout", 0, "命令超时秒数")
	flags.BoolVar(&outputJSON, "json", false, "输出 JSON")
	flags.BoolVar(&ready, "ready", false, "检查 readiness")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return
		}
		logger.Fatal("Invalid workspace options: %v", err)
	}
	cfg, err := config.Load(configPath)
	if err != nil {
		logger.Fatal("Load config failed: %v", err)
	}
	if socket == "" {
		socket = cfg.Workspace.ControlSocket
	}
	clientTimeout := max(cfg.Workspace.OperationTimeoutSeconds, cfg.Workspace.ExecTimeoutSeconds+30)
	client := workspace.ControlClient{SocketPath: socket, Timeout: time.Duration(clientTimeout) * time.Second}
	requestTimeout := cfg.Workspace.OperationTimeoutSeconds
	if action == "exec" {
		requestTimeout = clientTimeout
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(requestTimeout)*time.Second)
	defer cancel()
	var output any
	switch action {
	case "ensure":
		requireWorkspaceIdentity(userID, username)
		output, err = client.Ensure(ctx, workspace.Identity{UserID: userID, Username: username})
	case "status":
		requireWorkspaceUserID(userID)
		output, err = client.Status(ctx, userID)
	case "list":
		output, err = client.List(ctx)
	case "stop":
		requireWorkspaceUserID(userID)
		output, err = client.Stop(ctx, userID)
	case "remove":
		requireWorkspaceUserID(userID)
		output, err = client.Remove(ctx, userID)
	case "health":
		output, err = client.Health(ctx, ready)
	case "reconcile":
		output, err = client.Reconcile(ctx)
	case "exec":
		requireWorkspaceIdentity(userID, username)
		if flags.NArg() == 0 {
			logger.Fatal("workspace exec requires a command after --")
		}
		result, execErr := client.Exec(ctx, workspace.ExecRequest{
			Identity: workspace.Identity{UserID: userID, Username: username}, Command: flags.Args(),
			WorkingDir: workingDirectory, TimeoutSeconds: timeout,
		})
		if outputJSON {
			output, err = result, execErr
		} else {
			_, _ = os.Stdout.Write(result.Stdout)
			_, _ = os.Stderr.Write(result.Stderr)
			if execErr != nil {
				logger.Fatal("Workspace exec failed: %v", execErr)
			}
			if result.ExitCode != 0 {
				os.Exit(result.ExitCode)
			}
			return
		}
	}
	if err != nil {
		logger.Fatal("Workspace %s failed: %v", action, err)
	}
	if err := json.NewEncoder(os.Stdout).Encode(output); err != nil {
		logger.Fatal("Encode workspace response failed: %v", err)
	}
}

func requireWorkspaceUserID(userID string) {
	if strings.TrimSpace(userID) == "" {
		logger.Fatal("--user-id is required")
	}
}

func requireWorkspaceIdentity(userID, username string) {
	requireWorkspaceUserID(userID)
	if strings.TrimSpace(username) == "" {
		logger.Fatal("--user is required")
	}
}

func printWorkspaceHelp() {
	fmt.Println("Domus workspace（Linux）")
	fmt.Println()
	fmt.Println("用法:")
	fmt.Println("  domus workspace serve -c /etc/domus/workspace.yaml")
	fmt.Println("  domus workspace ensure --user-id UUID --user USER")
	fmt.Println("  domus workspace status|stop|remove --user-id UUID")
	fmt.Println("  domus workspace list|health|reconcile [--json]")
	fmt.Println("  domus workspace exec --user-id UUID --user USER [--workdir PATH] -- COMMAND [ARG...]")
}
