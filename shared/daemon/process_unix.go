//go:build !windows

package daemon

import (
	"fmt"
	"os"
	"os/exec"
	"syscall"
	"time"
)

// IsRunning 检查进程是否运行
func IsRunning(pid int) bool {
	if pid <= 0 {
		return false
	}

	process, err := os.FindProcess(pid)
	if err != nil {
		return false
	}

	err = process.Signal(syscall.Signal(0))
	return err == nil
}

// StopProcess 停止进程
func StopProcess(pid int) error {
	if pid <= 0 {
		return fmt.Errorf("invalid pid: %d", pid)
	}

	process, err := os.FindProcess(pid)
	if err != nil {
		return fmt.Errorf("find process: %w", err)
	}

	if err := process.Signal(syscall.SIGTERM); err != nil {
		return fmt.Errorf("send SIGTERM: %w", err)
	}

	return nil
}

// StartDaemon 以守护进程模式启动当前程序。
// 等待 500ms 观察子进程是否早退，避免子进程因锁冲突退出后父进程仍报启动成功。
func StartDaemon(args []string, envKey string) (int, error) {
	executable, err := os.Executable()
	if err != nil {
		return 0, fmt.Errorf("get executable path: %w", err)
	}

	cmd := exec.Command(executable, args...)
	cmd.Env = append(os.Environ(), envKey+"=1")

	if err := cmd.Start(); err != nil {
		return 0, fmt.Errorf("start daemon: %w", err)
	}

	waitCh := make(chan error, 1)
	go func() {
		waitCh <- cmd.Wait()
	}()

	select {
	case err := <-waitCh:
		if err != nil {
			return 0, fmt.Errorf("daemon process exited early: %w", err)
		}
		return 0, fmt.Errorf("daemon process exited early (exit status 0)")
	case <-time.After(500 * time.Millisecond):
		return cmd.Process.Pid, nil
	}
}
