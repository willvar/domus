// Package bootstrap 提供应用启动时的共享依赖和服务运行时基础设施。
package bootstrap

import (
	"errors"
	"fmt"
	"os"
	"time"

	"domus/shared/daemon"
	"domus/shared/logger"
	"domus/shared/stats"
	"domus/shared/version"
)

// Service 服务运行时管理
type Service struct {
	pidFile      string
	pidLock      *daemon.PIDLock
	socketServer *stats.SocketServer
}

// New 创建服务实例
func New(pidFile string, collector stats.Collector) *Service {
	svc := &Service{
		pidFile: pidFile,
	}

	if collector != nil {
		socketPath := daemon.GetSocketPath(pidFile)
		svc.socketServer = stats.NewSocketServer(socketPath, collector)
	}

	return svc
}

// Start 启动服务（获取 PID 锁，启动 stats socket）
func (s *Service) Start() error {
	lock, err := daemon.AcquirePID(s.pidFile)
	if err != nil {
		return err
	}
	s.pidLock = lock

	if s.socketServer != nil {
		if err := s.socketServer.Start(); err != nil {
			logger.Warn("Failed to start stats socket: %v", err)
		} else {
			logger.Info("Stats socket started: %s", s.socketServer.Path())
		}
	}

	return nil
}

// Stop 停止服务（关闭 stats socket，释放 PID 锁，清理残留文件）
func (s *Service) Stop() {
	if s.socketServer != nil {
		_ = s.socketServer.Close()
	}
	if s.pidLock != nil {
		s.pidLock.Release()
	}
	// 确保 socket 文件也被清理
	CleanupFiles(s.pidFile)
}

// CleanupFiles 清理 PID 文件和对应的 socket 文件（用于进程已退出但文件残留的情况）
func CleanupFiles(pidFile string) {
	_ = os.Remove(pidFile)
	_ = os.Remove(daemon.GetSocketPath(pidFile))
}

// IsAlreadyRunning 判断错误是否为"服务已在运行"
func IsAlreadyRunning(err error) bool {
	return errors.Is(err, daemon.ErrAlreadyRunning)
}

// GetStatus 获取服务状态
func GetStatus(pidFile string) (running bool, pid int, err error) {
	return daemon.GetStatus(pidFile)
}

// StopProcess 停止进程
func StopProcess(pid int) error {
	return daemon.StopProcess(pid)
}

// WaitForStop 等待进程停止
func WaitForStop(pid int, timeout time.Duration) bool {
	return daemon.WaitForStop(pid, timeout)
}

// StartDaemon 以守护进程模式启动
func StartDaemon(args []string, envKey string) (int, error) {
	return daemon.StartDaemon(args, envKey)
}

// IsDaemonChild 判断是否为守护进程子进程
func IsDaemonChild(envKey string) bool {
	return daemon.IsDaemonChild(envKey)
}

// FetchStats 从 Unix Socket 获取统计信息
func FetchStats(pidFile string, timeout time.Duration) (map[string]any, error) {
	socketPath := daemon.GetSocketPath(pidFile)
	return stats.FetchStats(socketPath, timeout)
}

// PrintHelp 打印服务帮助信息
func PrintHelp(name, description string) {
	fmt.Printf("%s %s v%s\n", name, description, version.Version)
	fmt.Println()
	fmt.Println("用法:")
	fmt.Printf("  %s <command> [options]\n", name)
	fmt.Println()
	fmt.Println("命令:")
	fmt.Println("  start     启动服务")
	fmt.Println("  stop      停止服务")
	fmt.Println("  restart   重启服务")
	fmt.Println("  status    查看服务状态")
	fmt.Println()
	fmt.Println("选项:")
	fmt.Println("  -c string  配置文件路径 (默认: config.yaml)")
	fmt.Println("  -d         守护进程模式")
	fmt.Println()
	fmt.Println("示例:")
	fmt.Printf("  %s start              # 前台启动\n", name)
	fmt.Printf("  %s start -d           # 后台启动\n", name)
	fmt.Printf("  %s start -c app.yaml  # 指定配置文件\n", name)
	fmt.Printf("  %s stop               # 停止服务\n", name)
	fmt.Printf("  %s status             # 查看状态\n", name)
}
