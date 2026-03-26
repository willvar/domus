// Package cmd 提供 CLI 命令实现
package cmd

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/compress"
	"github.com/gofiber/fiber/v2/middleware/cors"
	"github.com/gofiber/fiber/v2/middleware/recover"

	"zephyr/config"
	"zephyr/internal/auth"
	"zephyr/internal/handler"
	"zephyr/internal/middleware"
	"zephyr/internal/model"
	"zephyr/internal/service"
	"zephyr/internal/store"
	"zephyr/internal/ws"
	"zephyr/shared/bootstrap"
	"zephyr/shared/logger"
	"zephyr/shared/stats"
	"zephyr/shared/version"
)

const daemonEnvKey = "ZEPHYR_DAEMON"

// Execute 执行 CLI
func Execute() {
	version.Check("zephyr")

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
	case "help", "-h", "--help":
		printHelp()
	default:
		logger.Info("Unknown command: %s", action)
		printHelp()
		os.Exit(1)
	}
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
		if !bootstrap.IsDaemonChild(daemonEnvKey) {
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
	db, err := model.InitDB(cfg.Database)
	if err != nil {
		logger.Fatal("Failed to init database: %v", err)
	}

	// Initialize audit worker
	audit := model.NewAuditWorker(db)
	audit.Start()

	// Clean expired sessions and challenge stores periodically
	challenges := auth.NewChallengeManager()
	go func() {
		ticker := time.NewTicker(1 * time.Minute)
		defer ticker.Stop()
		for range ticker.C {
			model.CleanExpiredSessions()
			challenges.CleanAllExpiredEntries()
		}
	}()

	// Initialize OSS
	fileStore, err := store.NewOSSClient(cfg.OSS)
	if err != nil {
		logger.Fatal("Failed to init OSS: %v", err)
	}

	// Ensure temp dir exists
	if err := os.MkdirAll(config.TempDir, 0755); err != nil {
		logger.Fatal("Failed to create temp dir: %v", err)
	}

	// Initialize services
	sessions := model.NewSessionStore(db)
	transcoder := service.NewTranscoder(cfg.Transcode)
	mid := middleware.New(sessions, cfg.Server.SessionSecret)

	// Initialize WebSocket hub
	hub := ws.NewHub()

	// Wire task update notifications to WebSocket push
	model.OnTaskUpdate = func(userID, taskID, taskType, name, status string, progress float64, phase string) {
		hub.PushTaskUpdate(userID, taskID, taskType, name, status, progress, phase)
	}

	// Initialize handler
	h := &handler.Handler{
		Config:     cfg,
		DB:         db,
		Store:      fileStore,
		Sessions:   sessions,
		Dispatcher: nil,
		Transcoder: transcoder,
		Audit:      audit,
		Challenges: challenges,
		Mid:        mid,
		Hub:        hub,
	}

	// Initialize and start job dispatcher
	dispatcher := service.NewDispatcher()
	dispatcher.Register("transcode", cfg.Jobs.TranscodeConcurrency, h.RunTranscodeJob)
	dispatcher.Register("thumbnail", cfg.Jobs.ThumbnailConcurrency, h.RunThumbnailJob)
	dispatcher.Register("oss_upload", cfg.Jobs.SystemConcurrency, h.RunOSSUploadJob)
	dispatcher.Start()
	defer dispatcher.Stop()
	h.Dispatcher = dispatcher

	// Clean orphan uploads at startup, then periodically
	go func() {
		cleanOrphanUploads(fileStore)
		ticker := time.NewTicker(1 * time.Hour)
		defer ticker.Stop()
		for range ticker.C {
			cleanOrphanUploads(fileStore)
		}
	}()

	// Auto-create root user if no users exist
	if count, err := model.UserCount(); err == nil && count == 0 {
		password := generateRandomPassword()
		user, err := model.CreateUser("root", password, "root", model.PermAll)
		if err != nil {
			logger.Fatal("Failed to create root user: %v", err)
		}
		// Initialize home directory
		_ = fileStore.CreateDirectory("root/")
		_ = fileStore.CreateDirectory("root/home/")
		_ = fileStore.CreateDirectory("root/home/root/")
		_ = model.UpsertFile(user.ID, "root/home/", "home", true, 0, "", "")
		_ = model.UpsertFile(user.ID, "root/home/root/", "root", true, 0, "", "")
		fmt.Println("========================================")
		fmt.Println("  Root user created automatically")
		fmt.Printf("  Username: root\n")
		fmt.Printf("  Password: %s\n", password)
		fmt.Println("  Please change the password after login!")
		fmt.Println("========================================")
	}

	app := fiber.New(fiber.Config{
		BodyLimit:             int(config.UploadChunkSize) + 1024*1024,
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

	// Production: serve static files
	app.Static("/", "./frontend/dist")
	app.Get("/*", func(c *fiber.Ctx) error {
		return c.SendFile("./frontend/dist/index.html")
	})

	// 优雅关闭
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		<-quit
		logger.Info("Shutting down service...")

		hub.CloseAll()

		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		done := make(chan error, 1)
		go func() {
			done <- app.Shutdown()
		}()

		select {
		case err := <-done:
			if err != nil {
				logger.Info("Failed to shutdown service: %v", err)
			}
		case <-ctx.Done():
			logger.Info("Shutdown timeout")
		}

		os.Exit(0)
	}()

	addr := fmt.Sprintf(":%d", cfg.Server.Port)
	logger.Info("Zephyr v%s started on %s", version.Version, addr)
	if err := app.Listen(addr); err != nil {
		logger.Fatal("Failed to start service: %v", err)
	}
}

// ServerStats 实现 stats.Collector 接口
type ServerStats struct {
	BaseStats *stats.BaseStats
}

func (s *ServerStats) GetSnapshot() stats.Snapshot {
	return s.BaseStats.GetBaseSnapshot()
}

func generateRandomPassword() string {
	b := make([]byte, 12)
	rand.Read(b)
	return hex.EncodeToString(b)[:16]
}

func generateRandomSecret(length int) string {
	b := make([]byte, length)
	rand.Read(b)
	return hex.EncodeToString(b)[:length]
}

func cleanOrphanUploads(_ store.FileStore) {
	const staleThreshold = 24 * time.Hour

	stale, err := model.GetStaleUploadFiles(staleThreshold)
	if err != nil {
		logger.Error("[cleanup] Failed to query stale uploads: %v", err)
		return
	}
	for _, r := range stale {
		tempDir := filepath.Join(config.TempDir, "upload", r.UploadID)
		_ = os.RemoveAll(tempDir)
		_ = model.DeleteFile(r.UserID, r.Path)
		logger.Info("[cleanup] Cleaned stale upload: %s (file: %s)", r.UploadID, r.Name)
	}
}

func printHelp() {
	bootstrap.PrintHelp("zephyr", "文件管理服务")
}
