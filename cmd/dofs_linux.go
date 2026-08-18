//go:build linux

package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"math"
	"net"
	"os"
	"os/signal"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	dofscore "github.com/willvar/dofs"
	"gorm.io/gorm"

	"domus/config"
	"domus/internal/auth"
	"domus/internal/dofs"
	"domus/internal/dofsbridge"
	"domus/internal/model"
	"domus/shared/logger"
)

type dofsMountOptions struct {
	username   string
	mountpoint string
	debug      bool
	allowOther bool
	writable   bool
	stateDir   string
	uid        uint32
	gid        uint32
}

type dofsServeOptions struct {
	mountRoot string
	stateRoot string
	socket    string
	debug     bool
}

type dofsControlOptions struct {
	socket   string
	userID   string
	username string
	json     bool
	live     bool
}

func dofsCommand(defaultConfigPath string, args []string) {
	if len(args) == 0 {
		printDOFSHelp()
		return
	}
	switch args[0] {
	case "mount":
		dofsMountCommand(defaultConfigPath, args[1:])
	case "serve":
		dofsServeCommand(defaultConfigPath, args[1:])
	case "ensure", "status", "unmount", "health", "reconcile":
		dofsControlCommand(defaultConfigPath, args[0], args[1:])
	case "help", "-h", "--help":
		printDOFSHelp()
	default:
		printDOFSHelp()
		logger.Fatal("Unknown DOFS command: %s", args[0])
	}
}

func dofsMountCommand(defaultConfigPath string, args []string) {
	flags := flag.NewFlagSet("dofs mount", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	flags.Usage = printDOFSHelp
	configPath := defaultConfigPath
	options := dofsMountOptions{uid: uint32(os.Getuid()), gid: uint32(os.Getgid())}
	var uid, gid uint
	uid = uint(options.uid)
	gid = uint(options.gid)
	flags.StringVar(&configPath, "c", configPath, "配置文件路径")
	flags.StringVar(&options.username, "user", "", "要挂载的用户名")
	flags.StringVar(&options.username, "u", "", "要挂载的用户名（简写）")
	flags.StringVar(&options.mountpoint, "mountpoint", "", "空挂载目录")
	flags.StringVar(&options.mountpoint, "m", "", "空挂载目录（简写）")
	flags.BoolVar(&options.debug, "debug", false, "输出 FUSE 调试日志")
	flags.BoolVar(&options.allowOther, "allow-other", false, "允许其他宿主机 UID 访问挂载")
	flags.BoolVar(&options.writable, "writable", false, "启用 generation + WAL 写回")
	flags.StringVar(&options.stateDir, "state-dir", "", "写回缓存和 WAL 目录")
	flags.UintVar(&uid, "uid", uid, "挂载中文件的宿主机 UID")
	flags.UintVar(&gid, "gid", gid, "挂载中文件的宿主机 GID")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return
		}
		logger.Fatal("Invalid DOFS options: %v", err)
	}
	if uid > math.MaxUint32 || gid > math.MaxUint32 {
		logger.Fatal("DOFS uid/gid must fit in uint32")
	}
	options.uid = uint32(uid)
	options.gid = uint32(gid)

	if err := runDOFSMount(configPath, options); err != nil {
		logger.Fatal("DOFS mount failed: %v", err)
	}
}

func runDOFSMount(configPath string, options dofsMountOptions) error {
	options.username = strings.TrimSpace(options.username)
	if options.username == "" {
		return errors.New("--user is required")
	}
	mountpoint, err := validateDOFSMountpoint(options.mountpoint)
	if err != nil {
		return err
	}
	if err := dofscore.ValidateHostRequirements(options.allowOther); err != nil {
		return err
	}

	cfg, err := config.Load(configPath)
	if err != nil {
		return err
	}
	serverKey, err := auth.ServerKeyFromSecret(cfg.Server.EncryptionSecret)
	if err != nil {
		return fmt.Errorf("load server encryption key: %w", err)
	}
	defer zeroSecret(serverKey)

	db, err := model.InitDB(cfg.Database)
	if err != nil {
		return fmt.Errorf("initialize database: %w", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		return fmt.Errorf("get database handle: %w", err)
	}
	defer func() { _ = sqlDB.Close() }()
	repos := model.NewRepos(db, nil)

	user, err := repos.Users.GetByUsername(options.username)
	if err != nil {
		return fmt.Errorf("find user %q: %w", options.username, err)
	}
	runtime, err := dofsbridge.Open(context.Background(), db, cfg, serverKey, repos.Users)
	if err != nil {
		return err
	}
	defer runtime.Close()
	if err := runtime.EnsureUser(context.Background(), user); err != nil {
		return err
	}
	backend, err := dofscore.NewBackend(context.Background(), user.ID, runtime.Metadata, runtime.Objects, runtime.Keys,
		dofscore.BackendOptions{Writable: options.writable})
	if err != nil {
		return err
	}
	defer func() { _ = backend.Close() }()
	if options.writable {
		stateDir := strings.TrimSpace(options.stateDir)
		if stateDir == "" {
			cacheRoot, err := os.UserCacheDir()
			if err != nil {
				return fmt.Errorf("resolve default DOFS state directory: %w", err)
			}
			stateDir = filepath.Join(cacheRoot, "domus", "dofs", user.ID)
		}
		if err := backend.EnableWriteback(context.Background(), stateDir); err != nil {
			return err
		}
		logger.Info("DOFS writeback state: %s", stateDir)
	}

	server, err := dofscore.Mount(mountpoint, backend, dofscore.MountOptions{
		Debug:      options.debug,
		AllowOther: options.allowOther,
		UID:        options.uid,
		GID:        options.gid,
		Writable:   options.writable,
	})
	if err != nil {
		return err
	}

	mode := "read-only"
	if options.writable {
		mode = "writable"
	}
	logger.Info("DOFS mounted user %s at %s (%s)", user.Username, mountpoint, mode)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	done := make(chan struct{})
	leaseLost := backend.LeaseLost()
	go func() {
		select {
		case <-ctx.Done():
			_ = server.Unmount()
		case <-leaseLost:
			logger.Error("DOFS writable lease lost: %v", backend.LeaseError())
			if err := server.Unmount(); err != nil {
				logger.Error("Failed to unmount DOFS after lease loss: %v", err)
			}
		case <-done:
		}
	}()
	server.Wait()
	close(done)
	stop()
	logger.Info("DOFS unmounted from %s", mountpoint)
	return nil
}

func dofsServeCommand(defaultConfigPath string, args []string) {
	flags := flag.NewFlagSet("dofs serve", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	flags.Usage = printDOFSHelp
	configPath := defaultConfigPath
	var options dofsServeOptions
	flags.StringVar(&configPath, "c", configPath, "配置文件路径")
	flags.StringVar(&options.mountRoot, "mount-root", "", "覆盖配置中的挂载根目录")
	flags.StringVar(&options.stateRoot, "state-root", "", "覆盖配置中的状态根目录")
	flags.StringVar(&options.socket, "socket", "", "覆盖配置中的控制 Socket")
	flags.BoolVar(&options.debug, "debug", false, "输出 FUSE 调试日志")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return
		}
		logger.Fatal("Invalid DOFS serve options: %v", err)
	}
	if flags.NArg() != 0 {
		logger.Fatal("Unexpected DOFS serve argument: %s", flags.Arg(0))
	}
	if err := runDOFSServe(configPath, options); err != nil {
		logger.Fatal("DOFS service failed: %v", err)
	}
}

func runDOFSServe(configPath string, overrides dofsServeOptions) error {
	cfg, err := config.Load(configPath)
	if err != nil {
		return err
	}
	if strings.TrimSpace(overrides.mountRoot) != "" {
		cfg.DOFS.MountRoot = overrides.mountRoot
	}
	if strings.TrimSpace(overrides.stateRoot) != "" {
		cfg.DOFS.StateRoot = overrides.stateRoot
	}
	if strings.TrimSpace(overrides.socket) != "" {
		cfg.DOFS.ControlSocket = overrides.socket
	}
	if err := cfg.ValidateDOFS(); err != nil {
		return err
	}
	if err := dofscore.ValidateHostRequirements(cfg.DOFS.AllowOther); err != nil {
		return fmt.Errorf("validate DOFS host: %w", err)
	}
	if err := logger.InitFromConfig(&cfg.Log); err != nil {
		return fmt.Errorf("initialize DOFS logging: %w", err)
	}

	serverKey, err := auth.ServerKeyFromSecret(cfg.Server.EncryptionSecret)
	if err != nil {
		return fmt.Errorf("load server encryption key: %w", err)
	}
	defer zeroSecret(serverKey)

	db, err := model.InitDB(cfg.Database)
	if err != nil {
		return fmt.Errorf("initialize database: %w", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		return fmt.Errorf("get database handle: %w", err)
	}
	sqlDB.SetMaxOpenConns(cfg.DOFS.MaxMounts + 8)
	sqlDB.SetMaxIdleConns(8)
	sqlDB.SetConnMaxIdleTime(5 * time.Minute)
	defer func() { _ = sqlDB.Close() }()
	repos := model.NewRepos(db, nil)

	runtime, err := dofsbridge.Open(context.Background(), db, cfg, serverKey, repos.Users)
	if err != nil {
		return err
	}
	defer runtime.Close()
	if err := runtime.Check(context.Background()); err != nil {
		return fmt.Errorf("check independent DOFS runtime: %w", err)
	}
	if err := runtime.EnsureAllUsers(context.Background()); err != nil {
		return err
	}

	socketGID, socketMode, err := resolveDOFSSocketAccess(cfg.DOFS.SocketGroup)
	if err != nil {
		return err
	}
	provider := &productionDOFSMountProvider{
		users: repos.Users, runtime: runtime,
	}
	manager, err := dofs.NewManager(dofs.ManagerOptions{
		MountRoot:     cfg.DOFS.MountRoot,
		StateRoot:     cfg.DOFS.StateRoot,
		ControlSocket: cfg.DOFS.ControlSocket,
		SocketMode:    socketMode,
		SocketGID:     socketGID,
		Mount: dofs.MountOptions{
			Debug: overrides.debug, AllowOther: cfg.DOFS.AllowOther,
			UID: cfg.DOFS.UID, GID: cfg.DOFS.GID, Writable: cfg.DOFS.Writable,
		},
		ReconcileInterval: time.Duration(cfg.DOFS.ReconcileIntervalSeconds) * time.Second,
		MountTimeout:      time.Duration(cfg.DOFS.MountTimeoutSeconds) * time.Second,
		MaxMounts:         cfg.DOFS.MaxMounts,
		Logf:              logger.Info,
		OnReady:           notifySystemdDOFSReady,
	}, provider)
	if err != nil {
		return err
	}
	defer func() { _ = manager.Close() }()

	serviceContext, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	logger.Info("DOFS manager starting (mount root %s, state root %s)", cfg.DOFS.MountRoot, cfg.DOFS.StateRoot)
	serveErr := manager.ServeControl(serviceContext)
	stop()
	shutdownContext, cancelShutdown := context.WithTimeout(
		context.Background(), time.Duration(cfg.DOFS.ShutdownTimeoutSeconds)*time.Second,
	)
	shutdownErr := manager.Shutdown(shutdownContext)
	cancelShutdown()
	if shutdownErr != nil {
		shutdownErr = fmt.Errorf("shutdown DOFS mounts: %w", shutdownErr)
	}
	if serveErr != nil {
		serveErr = fmt.Errorf("serve DOFS control socket: %w", serveErr)
	}
	if err := errors.Join(serveErr, shutdownErr); err != nil {
		return err
	}
	logger.Info("DOFS manager stopped")
	return nil
}

func notifySystemdDOFSReady() error {
	socket := strings.TrimSpace(os.Getenv("NOTIFY_SOCKET"))
	if socket == "" {
		return nil
	}
	// systemd represents an abstract Linux Unix socket as "@name" in the
	// environment, while Go's net package expects the leading byte to be NUL.
	if strings.HasPrefix(socket, "@") {
		socket = "\x00" + strings.TrimPrefix(socket, "@")
	} else if !filepath.IsAbs(socket) {
		return errors.New("systemd NOTIFY_SOCKET must be absolute or abstract")
	}
	connection, err := net.DialUnix("unixgram", nil, &net.UnixAddr{Name: socket, Net: "unixgram"})
	if err != nil {
		return fmt.Errorf("connect systemd notification socket: %w", err)
	}
	defer func() { _ = connection.Close() }()
	if _, err := connection.Write([]byte("READY=1\nSTATUS=DOFS control socket is ready")); err != nil {
		return fmt.Errorf("send systemd readiness notification: %w", err)
	}
	return nil
}

func resolveDOFSSocketAccess(group string) (int, os.FileMode, error) {
	group = strings.TrimSpace(group)
	if group == "" {
		return -1, 0600, nil
	}
	if numeric, err := strconv.Atoi(group); err == nil {
		if numeric < 0 {
			return 0, 0, errors.New("DOFS socket group ID must not be negative")
		}
		return numeric, 0660, nil
	}
	resolved, err := user.LookupGroup(group)
	if err != nil {
		return 0, 0, fmt.Errorf("resolve DOFS socket group %q: %w", group, err)
	}
	gid, err := strconv.Atoi(resolved.Gid)
	if err != nil || gid < 0 {
		return 0, 0, fmt.Errorf("invalid GID for DOFS socket group %q", group)
	}
	return gid, 0660, nil
}

type productionDOFSMountProvider struct {
	users   model.UserRepo
	runtime *dofsbridge.Runtime
}

func (p *productionDOFSMountProvider) ResolveUser(ctx context.Context, selector dofs.MountUserSelector) (dofs.MountIdentity, error) {
	if err := selector.Validate(); err != nil {
		return dofs.MountIdentity{}, err
	}
	if err := ctx.Err(); err != nil {
		return dofs.MountIdentity{}, err
	}
	var resolved *model.User
	var err error
	if strings.TrimSpace(selector.UserID) != "" {
		resolved, err = p.users.GetByID(strings.TrimSpace(selector.UserID))
	} else {
		resolved, err = p.users.GetByUsername(strings.TrimSpace(selector.Username))
	}
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return dofs.MountIdentity{}, fmt.Errorf("%w: %s", dofs.ErrUserNotFound, firstNonempty(selector.UserID, selector.Username))
		}
		return dofs.MountIdentity{}, err
	}
	return dofs.MountIdentity{UserID: resolved.ID, Username: resolved.Username}, nil
}

func firstNonempty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return "unknown"
}

func (p *productionDOFSMountProvider) Mount(
	ctx context.Context,
	identity dofs.MountIdentity,
	mountpoint string,
	stateDirectory string,
	options dofs.MountOptions,
) (dofs.ManagedMount, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	resolved, err := p.users.GetByID(identity.UserID)
	if err != nil {
		return nil, err
	}
	if resolved.Username != identity.Username {
		return nil, errors.New("DOFS user identity changed during mount")
	}
	if p.runtime == nil {
		return nil, errors.New("independent DOFS runtime is unavailable")
	}
	if err := p.runtime.EnsureUser(ctx, resolved); err != nil {
		return nil, err
	}
	backend, err := dofscore.NewBackend(ctx, identity.UserID, p.runtime.Metadata, p.runtime.Objects, p.runtime.Keys,
		dofscore.BackendOptions{Writable: options.Writable})
	if err != nil {
		return nil, err
	}
	if options.Writable {
		if err := backend.EnableWriteback(ctx, stateDirectory); err != nil {
			_ = backend.Close()
			return nil, err
		}
		if backend.LeaseLost() == nil {
			_ = backend.Close()
			return nil, errors.New("production writable DOFS lease does not support health checks")
		}
	}
	if err := ctx.Err(); err != nil {
		_ = backend.Close()
		return nil, err
	}
	server, err := dofscore.Mount(mountpoint, backend, dofscore.MountOptions{
		Debug: options.Debug, AllowOther: options.AllowOther,
		UID: options.UID, GID: options.GID, Writable: options.Writable,
	})
	if err != nil {
		_ = backend.Close()
		return nil, err
	}
	return newProductionDOFSMount(server, backend), nil
}

type productionDOFSMount struct {
	server    dofscore.MountedFilesystem
	done      chan struct{}
	failures  chan error
	unmountMu sync.Mutex
}

func newProductionDOFSMount(server dofscore.MountedFilesystem, backend *dofscore.Backend) *productionDOFSMount {
	mount := &productionDOFSMount{server: server, done: make(chan struct{}), failures: make(chan error, 2)}
	go func() {
		server.Wait()
		_ = backend.Close()
		close(mount.done)
	}()
	if leaseLost := backend.LeaseLost(); leaseLost != nil {
		go func() {
			select {
			case <-mount.done:
				return
			case <-leaseLost:
				leaseErr := backend.LeaseError()
				if leaseErr == nil {
					leaseErr = dofscore.ErrLeaseLost
				}
				mount.reportFailure(leaseErr)
				if err := mount.Unmount(); err != nil {
					mount.reportFailure(fmt.Errorf("%w (automatic unmount failed: %v)", leaseErr, err))
				}
			}
		}()
	}
	return mount
}

func (m *productionDOFSMount) Unmount() error {
	m.unmountMu.Lock()
	defer m.unmountMu.Unlock()
	return m.server.Unmount()
}
func (m *productionDOFSMount) Done() <-chan struct{}  { return m.done }
func (m *productionDOFSMount) Failures() <-chan error { return m.failures }

func (m *productionDOFSMount) reportFailure(err error) {
	select {
	case m.failures <- err:
	default:
	}
}

func dofsControlCommand(defaultConfigPath, action string, args []string) {
	flags := flag.NewFlagSet("dofs "+action, flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	flags.Usage = printDOFSHelp
	configPath := defaultConfigPath
	var options dofsControlOptions
	flags.StringVar(&configPath, "c", configPath, "配置文件路径")
	flags.StringVar(&options.socket, "socket", "", "覆盖 DOFS 控制 Socket")
	flags.StringVar(&options.userID, "user-id", "", "Domus 用户 ID")
	flags.StringVar(&options.username, "user", "", "Domus 用户名（仅 ensure）")
	flags.BoolVar(&options.json, "json", false, "输出 JSON")
	flags.BoolVar(&options.live, "live", false, "检查存活状态而不是就绪状态")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return
		}
		logger.Fatal("Invalid DOFS %s options: %v", action, err)
	}
	if flags.NArg() != 0 {
		logger.Fatal("Unexpected DOFS %s argument: %s", action, flags.Arg(0))
	}
	if err := runDOFSControl(configPath, action, options); err != nil {
		logger.Fatal("DOFS %s failed: %v", action, err)
	}
}

func runDOFSControl(configPath, action string, options dofsControlOptions) error {
	socket := strings.TrimSpace(options.socket)
	timeout := 70 * time.Second
	if socket == "" {
		cfg, err := config.Load(configPath)
		if err != nil {
			return err
		}
		socket = cfg.DOFS.ControlSocket
		timeout = time.Duration(cfg.DOFS.MountTimeoutSeconds+10) * time.Second
	}
	client := dofs.ControlClient{
		SocketPath: socket,
		Timeout:    timeout,
	}
	ctx := context.Background()
	var output any
	switch action {
	case "ensure":
		selector := dofs.MountUserSelector{
			UserID: strings.TrimSpace(options.userID), Username: strings.TrimSpace(options.username),
		}
		status, err := client.Ensure(ctx, selector)
		if err != nil {
			return err
		}
		output = status
	case "status":
		if strings.TrimSpace(options.username) != "" {
			return errors.New("--user is supported only by `dofs ensure`; use --user-id or list all mounts")
		}
		if strings.TrimSpace(options.userID) == "" {
			statuses, err := client.List(ctx)
			if err != nil {
				return err
			}
			output = statuses
		} else {
			status, err := client.Status(ctx, strings.TrimSpace(options.userID))
			if err != nil {
				return err
			}
			output = status
		}
	case "unmount":
		if strings.TrimSpace(options.userID) == "" || strings.TrimSpace(options.username) != "" {
			return errors.New("dofs unmount requires --user-id")
		}
		status, err := client.Unmount(ctx, strings.TrimSpace(options.userID))
		if err != nil {
			return err
		}
		output = status
	case "health":
		health, err := client.Health(ctx, !options.live)
		if err != nil {
			return err
		}
		output = health
	case "reconcile":
		health, err := client.Reconcile(ctx)
		if err != nil {
			return err
		}
		output = health
	default:
		return fmt.Errorf("unsupported DOFS control action %q", action)
	}
	return printDOFSControlOutput(output, options.json)
}

func printDOFSControlOutput(output any, forceJSON bool) error {
	if forceJSON {
		encoder := json.NewEncoder(os.Stdout)
		encoder.SetIndent("", "  ")
		return encoder.Encode(output)
	}
	switch value := output.(type) {
	case dofs.MountStatus:
		fmt.Printf("%-12s %s\n", "STATE", value.State)
		fmt.Printf("%-12s %s\n", "USER_ID", value.UserID)
		if value.Username != "" {
			fmt.Printf("%-12s %s\n", "USERNAME", value.Username)
		}
		fmt.Printf("%-12s %s\n", "MOUNTPOINT", value.Mountpoint)
		if value.MountID != "" {
			fmt.Printf("%-12s %s\n", "MOUNT_ID", value.MountID)
		}
		fmt.Printf("%-12s %t\n", "DESIRED", value.Desired)
		fmt.Printf("%-12s %t\n", "WRITABLE", value.Writable)
		fmt.Printf("%-12s %t\n", "ALLOW_OTHER", value.AllowOther)
		fmt.Printf("%-12s %d:%d\n", "UID:GID", value.UID, value.GID)
		if value.LastError != "" {
			fmt.Printf("%-12s %s\n", "ERROR", value.LastError)
		}
	case []dofs.MountStatus:
		fmt.Printf("%-38s %-12s %-7s %s\n", "USER_ID", "STATE", "DESIRED", "MOUNTPOINT")
		for _, status := range value {
			fmt.Printf("%-38s %-12s %-7t %s\n", status.UserID, status.State, status.Desired, status.Mountpoint)
		}
	case dofs.ManagerHealth:
		fmt.Printf("%-18s %s\n", "STATUS", value.Status)
		fmt.Printf("%-18s %d\n", "DESIRED_MOUNTS", value.DesiredMounts)
		fmt.Printf("%-18s %d\n", "MOUNTED", value.Mounted)
		fmt.Printf("%-18s %d\n", "MAX_MOUNTS", value.MaxMounts)
		fmt.Printf("%-18s %d\n", "DEGRADED", value.Degraded)
		if value.LastError != "" {
			fmt.Printf("%-18s %s\n", "ERROR", value.LastError)
		}
	default:
		return json.NewEncoder(os.Stdout).Encode(output)
	}
	return nil
}

func validateDOFSMountpoint(input string) (string, error) {
	if strings.TrimSpace(input) == "" {
		return "", errors.New("--mountpoint is required")
	}
	absolute, err := filepath.Abs(input)
	if err != nil {
		return "", fmt.Errorf("resolve mountpoint: %w", err)
	}
	info, err := os.Lstat(absolute)
	if err != nil {
		return "", fmt.Errorf("inspect mountpoint: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return "", errors.New("mountpoint must not be a symbolic link")
	}
	resolved, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		return "", fmt.Errorf("resolve mountpoint components: %w", err)
	}
	if filepath.Clean(resolved) != filepath.Clean(absolute) {
		return "", errors.New("mountpoint must not contain symbolic-link components")
	}
	if !info.IsDir() {
		return "", errors.New("mountpoint must be a directory")
	}
	entries, err := os.ReadDir(absolute)
	if err != nil {
		return "", fmt.Errorf("read mountpoint: %w", err)
	}
	if len(entries) != 0 {
		return "", errors.New("mountpoint must be empty")
	}
	return absolute, nil
}

func zeroSecret(secret []byte) {
	for i := range secret {
		secret[i] = 0
	}
}

func printDOFSHelp() {
	fmt.Println("用法:")
	fmt.Println("  domus dofs mount --user <username> --mountpoint <empty-directory> [--writable] [options]")
	fmt.Println("  domus dofs serve [-c config.yaml] [--debug]")
	fmt.Println("  domus dofs ensure (--user-id <id> | --user <username>) [--json]")
	fmt.Println("  domus dofs status [--user-id <id>] [--json]")
	fmt.Println("  domus dofs unmount --user-id <id> [--json]")
	fmt.Println("  domus dofs health [--live] [--json]")
	fmt.Println("  domus dofs reconcile [--json]")
	fmt.Println()
	fmt.Println("mount 选项:")
	fmt.Println("  -c string          配置文件路径（默认 config.yaml）")
	fmt.Println("  -u, --user string  要挂载的 Domus 用户")
	fmt.Println("  -m, --mountpoint   已存在的空目录")
	fmt.Println("  --uid uint         文件所有者 UID（默认当前 UID）")
	fmt.Println("  --gid uint         文件所有者 GID（默认当前 GID）")
	fmt.Println("  --allow-other      允许其他宿主机 UID 访问（需要配置 fuse.conf）")
	fmt.Println("  --writable         启用本地 writeback、WAL 和对象 generation")
	fmt.Println("  --state-dir string writeback 明文缓存和 WAL 目录")
	fmt.Println("  --debug            输出 FUSE 协议日志")
	fmt.Println()
	fmt.Println("serve 从配置文件的 dofs 段读取生产路径、UID/GID、权限和协调超时。")
	fmt.Println("控制命令默认连接 dofs.control_socket；可用 --socket 临时覆盖。")
}
