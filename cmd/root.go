// Package cmd 提供 CLI 命令实现
package cmd

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/compress"
	"github.com/gofiber/fiber/v2/middleware/cors"
	"github.com/gofiber/fiber/v2/middleware/recover"

	"domus/config"
	"domus/internal/auth"
	"domus/internal/handler"
	"domus/internal/middleware"
	"domus/internal/model"
	"domus/internal/service"
	"domus/internal/store"
	"domus/internal/terminal"
	workspaceRuntime "domus/internal/workspace"
	"domus/internal/ws"
	"domus/shared/bootstrap"
	"domus/shared/logger"
	"domus/shared/stats"
	"domus/shared/version"
)

const (
	daemonEnvKey                = "DOMUS_DAEMON"
	legacyDaemonEnvKey          = "ZEPHYR_DAEMON"
	rootBootstrapPasswordEnvKey = "DOMUS_ROOT_BOOTSTRAP_PASSWORD"
	legacyRootPasswordEnvKey    = "ZEPHYR_ROOT_BOOTSTRAP_PASSWORD"
)

// Execute 执行 CLI
func Execute() {
	version.Check("domus")

	// 初始化日志（默认 info 级别，输出到控制台）
	_ = logger.Init("info", "")

	if len(os.Args) < 2 {
		printHelp()
		os.Exit(1)
	}

	action := os.Args[1]
	configPath := "config.yaml"

	// 解析 -c 参数
	for i := 2; i < len(os.Args); i++ {
		if os.Args[i] == "-c" && i+1 < len(os.Args) {
			configPath = os.Args[i+1]
			break
		}
	}

	// 检查 -d 参数
	daemonMode := false
	for _, arg := range os.Args[2:] {
		if arg == "-d" {
			daemonMode = true
			break
		}
	}

	switch action {
	case "start":
		start(configPath, daemonMode)
	case "stop":
		stop(configPath)
	case "restart":
		restart(configPath, daemonMode)
	case "status":
		status(configPath)
	case "backup":
		backup(configPath, getFlagValue(os.Args[2:], "-o"))
	case "reset":
		reset(configPath, hasFlag(os.Args[2:], "--yes"))
	case "restore":
		restore(configPath, getFlagValue(os.Args[2:], "-i"), hasFlag(os.Args[2:], "--yes"))
	case "dofs":
		dofsCommand(configPath, os.Args[2:])
	case "workspace":
		workspaceCommand(configPath, os.Args[2:])
	case "dev":
		devCommand(configPath, os.Args[2:])
	case "help", "-h", "--help":
		printHelp()
	default:
		logger.Info("Unknown command: %s", action)
		printHelp()
		os.Exit(1)
	}
}

func hasFlag(args []string, flag string) bool {
	for _, arg := range args {
		if arg == flag {
			return true
		}
	}
	return false
}

func start(configPath string, daemonMode bool) {
	cfg := loadConfig(configPath)

	// 检查是否已在运行
	running, pid, err := bootstrap.GetStatus(cfg.Server.PidFile)
	if err != nil {
		logger.Fatal("Failed to check process status: %v", err)
	}
	if running {
		logger.Info("Service already running (PID: %d)", pid)
		os.Exit(0)
	}

	// 守护进程模式
	if daemonMode {
		if !bootstrap.IsDaemonChild(daemonEnvKey) && !bootstrap.IsDaemonChild(legacyDaemonEnvKey) {
			pid, err := bootstrap.StartDaemon([]string{"start", "-c", configPath, "-d"}, daemonEnvKey)
			if err != nil {
				if running, p, _ := bootstrap.GetStatus(cfg.Server.PidFile); running {
					logger.Info("Service already running (PID: %d)", p)
					return
				}
				logger.Fatal("Failed to start daemon: %v", err)
			}
			logger.Info("Service started (PID: %d)", pid)
			return
		}
	}

	// 运行服务器
	runServer(cfg, configPath)
}

func stop(configPath string) {
	cfg := loadConfig(configPath)

	running, pid, err := bootstrap.GetStatus(cfg.Server.PidFile)
	if err != nil {
		logger.Fatal("Failed to check process status: %v", err)
	}
	if !running {
		logger.Info("Service not running")
		os.Exit(0)
	}

	logger.Info("Stopping service (PID: %d)...", pid)
	if err := bootstrap.StopProcess(pid); err != nil {
		logger.Fatal("Failed to stop process: %v", err)
	}

	if bootstrap.WaitForStop(pid, 5*time.Second) {
		bootstrap.CleanupFiles(cfg.Server.PidFile)
		logger.Info("Service stopped")
	} else {
		logger.Info("Service stop timeout")
		os.Exit(1)
	}
}

func restart(configPath string, daemonMode bool) {
	cfg := loadConfig(configPath)

	running, pid, err := bootstrap.GetStatus(cfg.Server.PidFile)
	if err != nil {
		logger.Info("Failed to get service status: %v", err)
	}
	if running {
		logger.Info("Stopping service (PID: %d)...", pid)
		if err := bootstrap.StopProcess(pid); err != nil {
			logger.Info("Failed to stop process: %v", err)
		}
		bootstrap.WaitForStop(pid, 5*time.Second)
		bootstrap.CleanupFiles(cfg.Server.PidFile)
	}

	logger.Info("Starting service...")
	start(configPath, daemonMode)
}

func status(configPath string) {
	cfg := loadConfig(configPath)

	running, pid, err := bootstrap.GetStatus(cfg.Server.PidFile)
	if err != nil {
		logger.Fatal("Failed to check process status: %v", err)
	}

	logger.Info("Service status:")
	logger.Info("  Config file: %s", configPath)
	logger.Info("  PID file: %s", cfg.Server.PidFile)
	logger.Info("  Listen port: %d", cfg.Server.Port)
	if !running {
		logger.Info("  Status: not running")
		os.Exit(1)
	}

	logger.Info("  Status: running")
	logger.Info("  PID: %d", pid)

	data, err := bootstrap.FetchStats(cfg.Server.PidFile, 3*time.Second)
	if err != nil {
		logger.Info("  Failed to get stats: %v", err)
		os.Exit(0)
	}

	logger.Info("")
	logger.Info("Stats:")
	logger.Info("  Start time: %v", data["start_time"])
	logger.Info("  Uptime: %v", data["uptime"])
	logger.Info("  Total requests: %v", data["total_requests"])
	logger.Info("  Avg QPS: %v", data["avg_qps"])
	logger.Info("  Current QPS: %v", data["current_qps"])
}

func reset(configPath string, confirmed bool) {
	cfg := loadConfig(configPath)
	if err := resetInstance(cfg, configPath, confirmed, resetDeps{
		getStatus: bootstrap.GetStatus,
		resetDB:   model.ResetDatabase,
		newStore:  store.NewOSSClient,
	}); err != nil {
		logger.Fatal("%v", err)
	}
	logger.Info("Reset finished. Run `domus start -c %s` to reinitialize the instance.", configPath)
}

type resetDeps struct {
	getStatus func(pidFile string) (running bool, pid int, err error)
	resetDB   func(cfg config.DatabaseConfig) error
	newStore  func(cfg config.OSSConfig) (store.FileStore, error)
}

func resetInstance(cfg *config.Config, configPath string, confirmed bool, deps resetDeps) error {
	running, pid, err := deps.getStatus(cfg.Server.PidFile)
	if err != nil {
		return fmt.Errorf("failed to check process status: %w", err)
	}
	if running {
		return fmt.Errorf("service is running (PID: %d). Stop it before reset", pid)
	}
	if !confirmed {
		return fmt.Errorf("reset is destructive. Re-run with --yes to clear database %q and bucket %q", cfg.Database.DBName, cfg.OSS.Bucket)
	}

	logger.Info("Resetting Domus instance")
	logger.Info("  Config file: %s", configPath)
	logger.Info("  Database: %s", cfg.Database.DBName)
	logger.Info("  Bucket: %s", cfg.OSS.Bucket)

	if err := deps.resetDB(cfg.Database); err != nil {
		return fmt.Errorf("failed to reset database: %w", err)
	}
	logger.Info("Database reset complete")

	fileStore, err := deps.newStore(cfg.OSS)
	if err != nil {
		return fmt.Errorf("database reset completed, but failed to init OSS client for bucket cleanup: %w", err)
	}
	if err := fileStore.DeleteAllObjects(nil); err != nil {
		return fmt.Errorf("database reset completed, but failed to clear bucket %q: %w", cfg.OSS.Bucket, err)
	}
	logger.Info("Bucket cleanup complete")
	return nil
}

func loadConfig(path string) *config.Config {
	cfg, err := config.Load(path)
	if err != nil {
		logger.Fatal("Failed to load config: %v", err)
	}
	return cfg
}

func runServer(cfg *config.Config, configPath string) {
	// 从配置初始化日志
	if err := logger.InitFromConfig(&cfg.Log); err != nil {
		logger.Fatal("Failed to init logger: %v", err)
	}

	// Auto-generate session secret if missing
	if cfg.Server.SessionSecret == "" || cfg.Server.SessionSecret == "change-me-to-random-string" {
		cfg.Server.SessionSecret = generateRandomSecret(32)
		if saveErr := config.Save(configPath, cfg); saveErr != nil {
			logger.Fatal("Failed to save config: %v", saveErr)
		}
		logger.Info("Generated new session secret and saved to config.")
	}

	// Auto-generate encryption secret if missing
	if cfg.Server.EncryptionSecret == "" {
		cfg.Server.EncryptionSecret = generateRandomSecret(64)
		if saveErr := config.Save(configPath, cfg); saveErr != nil {
			logger.Fatal("Failed to save config: %v", saveErr)
		}
		logger.Info("Generated new encryption secret and saved to config.")
	}

	if err := cfg.Validate(); err != nil {
		logger.Fatal("Invalid config: %v", err)
	}

	serverKey, err := auth.ServerKeyFromSecret(cfg.Server.EncryptionSecret)
	if err != nil {
		logger.Fatal("Invalid encryption secret: %v", err)
	}

	// 初始化统计
	serverStats := &ServerStats{BaseStats: stats.NewBaseStats()}

	// 启动服务（PID + stats socket）
	svc := bootstrap.New(cfg.Server.PidFile, serverStats)
	if err := svc.Start(); err != nil {
		if bootstrap.IsAlreadyRunning(err) {
			logger.Info("Service already running")
			os.Exit(0)
		}
		logger.Fatal("Failed to start service: %v", err)
	}
	defer svc.Stop()

	// 启动 QPS 重置定时器
	stopQPS := stats.StartQPSResetTimer(serverStats.BaseStats, time.Second)
	defer close(stopQPS)

	// Initialize database
	db, hasFTS, err := model.InitDB(cfg.Database)
	if err != nil {
		logger.Fatal("Failed to init database: %v", err)
	}

	// Initialize audit worker
	audit := model.NewAuditWorker(db)
	audit.Start()

	// Initialize WebSocket hub (needed for task update callback)
	hub := ws.NewHub()

	// Build repository layer
	onTaskUpdate := model.TaskUpdateFunc(func(userID, taskID, taskType, name, status, clientInstanceID string, progress float64, phase string) {
		hub.PushTaskUpdate(userID, taskID, taskType, name, status, clientInstanceID, progress, phase)
	})
	repos := model.NewRepos(db, hasFTS, onTaskUpdate)

	// Wire session KEK population
	repos.Sessions.SetPopulateKEK(func(s *model.Session) {
		wrappedHex, err := repos.Users.GetWrappedKEK(s.UserID)
		if err != nil || wrappedHex == "" {
			return
		}
		wrappedBytes, err := hex.DecodeString(wrappedHex)
		if err != nil {
			return
		}
		s.KEK, _ = auth.UnwrapKEK(serverKey, wrappedBytes)
	})

	// Clean expired sessions and challenge stores periodically
	challenges := auth.NewChallengeManager()
	go func() {
		ticker := time.NewTicker(1 * time.Minute)
		defer ticker.Stop()
		for range ticker.C {
			repos.Sessions.CleanExpired()
			challenges.CleanAllExpiredEntries()
		}
	}()

	// Initialize OSS
	fileStore, err := store.NewOSSClient(cfg.OSS)
	if err != nil {
		logger.Fatal("Failed to init OSS: %v", err)
	}

	// Initialize services
	mid := middleware.New(repos.Sessions, cfg.Server.SessionSecret)
	workspaceClient := workspaceRuntime.ControlClient{
		SocketPath: cfg.Workspace.ControlSocket,
		Timeout: time.Duration(max(
			cfg.Workspace.OperationTimeoutSeconds,
			cfg.Workspace.ExecTimeoutSeconds+30,
		)) * time.Second,
	}
	workspaceContext, cancelWorkspace := context.WithTimeout(
		context.Background(), time.Duration(cfg.Workspace.OperationTimeoutSeconds)*time.Second,
	)
	health, healthErr := workspaceClient.Health(workspaceContext, true)
	cancelWorkspace()
	if healthErr != nil && health.Status != "degraded" {
		logger.Fatal("Workspace manager is unavailable: %v", healthErr)
	}
	if health.ProtocolVersion != workspaceRuntime.ControlProtocolVersion {
		logger.Fatal("Workspace control protocol mismatch: daemon=%d server=%d", health.ProtocolVersion, workspaceRuntime.ControlProtocolVersion)
	}
	if healthErr != nil {
		logger.Info("Workspace manager starts degraded; reconciliation remains active: %v", healthErr)
	}
	logger.Info("Workspace manager ready via %s", cfg.Workspace.ControlSocket)

	terminalMgr := terminal.NewManager(workspaceClient, cfg.Workspace.MaxSessionsPerUser)
	terminalMgr.OnPushBytes = func(connID, sessionID string, data []byte) {
		hub.PushSessionOutputBytes(connID, sessionID, data)
	}
	terminalMgr.OnPushExit = func(connID, sessionID, reason string) {
		hub.PushSessionExit(connID, sessionID, reason)
	}
	hub.OnConnClose = func(connID string) {
		terminalMgr.CloseByConn(connID)
	}

	// Initialize handler
	h := &handler.Handler{
		Config:     cfg,
		Repos:      repos,
		Store:      fileStore,
		Email:      service.NewSMTPEmailSender(cfg.SMTP),
		Audit:      audit,
		Challenges: challenges,
		Mid:        mid,
		Hub:        hub,
		Terminal:   terminalMgr,
		Workspace:  workspaceClient,
	}

	// Clean orphan uploads at startup, then periodically
	go func() {
		cleanOrphanUploads(fileStore, repos)
		ticker := time.NewTicker(1 * time.Hour)
		defer ticker.Stop()
		for range ticker.C {
			cleanOrphanUploads(fileStore, repos)
		}
	}()

	// Auto-create root user if no users exist
	if count, err := repos.Users.Count(); err == nil && count == 0 {
		password, err := loadRootBootstrapPassword(cfg)
		if err != nil {
			logger.Fatal("Failed to load root bootstrap password: %v", err)
		}
		wrappedKEKHex, err := generateWrappedKEKForUser(cfg.Server.EncryptionSecret)
		if err != nil {
			logger.Fatal("Failed to generate KEK for root user: %v", err)
		}
		user, err := repos.Users.Create("root", password, "root", wrappedKEKHex)
		if err != nil {
			logger.Fatal("Failed to create root user: %v", err)
		}
		// Initialize home directory
		_ = fileStore.CreateDirectory("root/")
		_ = fileStore.CreateDirectory("root/home/")
		_ = fileStore.CreateDirectory("root/home/root/")
		_ = repos.Files.Upsert(user.ID, "root/home/", "home", true, 0, "", "")
		_ = repos.Files.Upsert(user.ID, "root/home/root/", "root", true, 0, "", "")
		logger.Info("Root user initialized from bootstrap password source")
	}

	app := fiber.New(fiber.Config{
		BodyLimit:             1024 * 1024 * 1024,
		DisableStartupMessage: true,
	})

	app.Use(recover.New())
	if cfg.Server.CORSOrigins != "" {
		app.Use(cors.New(cors.Config{
			AllowOrigins:     cfg.Server.CORSOrigins,
			AllowCredentials: true,
		}))
	}
	app.Use(func(c *fiber.Ctx) error {
		start := time.Now()
		err := c.Next()
		logger.Info("%s %s %d %v", c.Method(), c.Path(), c.Response().StatusCode(), time.Since(start))
		serverStats.BaseStats.RecordRequest()
		return err
	})
	app.Use(compress.New())

	// Register all API routes
	h.RegisterRoutes(app)

	// Start background stale upload sweeper
	h.StartUploadSweeper()

	// 优雅关闭
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(quit)
	shutdownStarted := make(chan struct{})
	shutdownComplete := make(chan struct{})

	go func() {
		<-quit
		close(shutdownStarted)
		defer close(shutdownComplete)
		logger.Info("Shutting down service...")

		// Stop admitting requests first. The listener shutdown is independent of
		// media cleanup so one slow derived-file removal cannot consume every
		// remaining shutdown stage's deadline.
		if err := app.ShutdownWithTimeout(10 * time.Second); err != nil {
			logger.Info("Failed to stop HTTP service cleanly: %v", err)
		}
		hub.CloseAll()

		mediaTimeout := time.Duration(max(45, cfg.Workspace.ShutdownTimeoutSeconds)) * time.Second
		mediaContext, cancelMedia := context.WithTimeout(context.Background(), mediaTimeout)
		if err := h.ShutdownMediaJobs(mediaContext); err != nil {
			logger.Info("Failed to stop media jobs cleanly: %v", err)
		}
		cancelMedia()
	}()

	addr := fmt.Sprintf(":%d", cfg.Server.Port)
	logger.Info("Domus v%s started on %s", version.Version, addr)
	listenErr := app.Listen(addr)
	select {
	case <-shutdownStarted:
		<-shutdownComplete
		if listenErr != nil {
			logger.Info("HTTP listener stopped during shutdown: %v", listenErr)
		}
		return
	default:
	}
	if listenErr != nil {
		logger.Fatal("Failed to start service: %v", listenErr)
	}
}

// ServerStats 实现 stats.Collector 接口
type ServerStats struct {
	BaseStats *stats.BaseStats
}

func (s *ServerStats) GetSnapshot() stats.Snapshot {
	return s.BaseStats.GetBaseSnapshot()
}

func loadRootBootstrapPassword(cfg *config.Config) (string, error) {
	if path := strings.TrimSpace(cfg.Server.RootBootstrapPasswordFile); path != "" {
		data, err := os.ReadFile(path)
		if err != nil {
			return "", fmt.Errorf("read %s: %w", path, err)
		}
		password := strings.TrimSpace(string(data))
		if password == "" {
			return "", fmt.Errorf("root bootstrap password file %s is empty", path)
		}
		return password, nil
	}

	password := strings.TrimSpace(os.Getenv(rootBootstrapPasswordEnvKey))
	if password == "" {
		password = strings.TrimSpace(os.Getenv(legacyRootPasswordEnvKey))
	}
	if password == "" {
		return "", fmt.Errorf("set server.root_bootstrap_password_file or %s before first startup (legacy %s is also accepted)", rootBootstrapPasswordEnvKey, legacyRootPasswordEnvKey)
	}
	return password, nil
}

func generateWrappedKEKForUser(encryptionSecret string) (string, error) {
	kek, err := auth.GenerateKEK()
	if err != nil {
		return "", err
	}
	serverKey, err := auth.ServerKeyFromSecret(encryptionSecret)
	if err != nil {
		return "", err
	}
	wrapped, err := auth.WrapKEK(serverKey, kek)
	if err != nil {
		return "", err
	}
	return hex.EncodeToString(wrapped), nil
}

func generateRandomSecret(length int) string {
	b := make([]byte, length)
	rand.Read(b)
	return hex.EncodeToString(b)[:length]
}

func cleanOrphanUploads(s store.FileStore, repos *model.Repos) {
	const staleThreshold = 24 * time.Hour

	stale, err := repos.Files.GetStaleUploads(staleThreshold)
	if err != nil {
		logger.Error("[cleanup] Failed to query stale uploads: %v", err)
		return
	}
	for _, r := range stale {
		if r.OSSUploadID != "" {
			_ = s.AbortMultipartUpload(r.Path, r.OSSUploadID)
		}
		if _, err := s.HeadObject(r.Path); err == nil {
			_ = s.DeleteObject(r.Path)
		}
		_ = repos.Files.Delete(r.UserID, r.Path)
		logger.Info("[cleanup] Cleaned stale upload: %s (file: %s)", r.UploadID, r.Name)
	}
}

func printHelp() {
	fmt.Printf("%s %s v%s\n", "domus", "文件管理服务", version.Version)
	fmt.Println()
	fmt.Println("用法:")
	fmt.Println("  domus <command> [options]")
	fmt.Println()
	fmt.Println("命令:")
	fmt.Println("  start     启动服务")
	fmt.Println("  stop      停止服务")
	fmt.Println("  restart   重启服务")
	fmt.Println("  status    查看服务状态")
	fmt.Println("  backup    备份数据库和整个 bucket（需先停服务）")
	fmt.Println("  reset     清空数据库并清空整个 bucket（危险）")
	fmt.Println("  restore   从备份恢复数据库和整个 bucket（危险，需先停服务）")
	fmt.Println("  dofs      管理 DOFS 挂载服务或执行单用户挂载（Linux）")
	fmt.Println("  workspace 管理按用户隔离的容器执行服务（Linux）")
	fmt.Println("  dev       启动完整本地开发栈（Linux）")
	fmt.Println()
	fmt.Println("选项:")
	fmt.Println("  -c string  配置文件路径 (默认: config.yaml)")
	fmt.Println("  -d         守护进程模式")
	fmt.Println("  -o string  备份输出路径（仅 backup 使用）")
	fmt.Println("  -i string  备份输入路径（仅 restore 使用）")
	fmt.Println("  --yes      确认执行危险操作（reset / restore 使用）")
	fmt.Println()
	fmt.Println("示例:")
	fmt.Println("  domus start")
	fmt.Println("  domus start -d")
	fmt.Println("  domus start -c app.yaml")
	fmt.Println("  domus stop")
	fmt.Println("  domus status")
	fmt.Println("  domus backup -c config.yaml -o backup-20260419.tar.gz")
	fmt.Println("  domus reset -c config.yaml --yes")
	fmt.Println("  domus restore -c config.yaml -i backup-20260419.tar.gz --yes")
	fmt.Println("  domus dofs mount -c config.yaml --user root --mountpoint /mnt/dofs-root")
	fmt.Println("  domus dofs serve -c /etc/domus/config.yaml")
	fmt.Println("  domus dofs status -c /etc/domus/config.yaml")
	fmt.Println("  domus workspace serve -c /etc/domus/workspace.yaml")
	fmt.Println("  domus workspace health -c /etc/domus/workspace.yaml --ready")
	fmt.Println("  domus dev -c config.yaml --runtime-root ./tmp/dev")
}
