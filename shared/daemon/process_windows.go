//go:build windows

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

	// 使用 Windows API: OpenProcess 检查进程是否存在
	const PROCESS_QUERY_LIMITED_INFORMATION = 0x1000
	handle, err := syscall.OpenProcess(PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		return false
	}
	syscall.CloseHandle(handle)
	return true
}

// StopProcess 停止进程
func StopProcess(pid int) error {
	if pid <= 0 {
		return fmt.Errorf("invalid pid: %d", pid)
	}

	// 先检查进程是否存在
	if !IsRunning(pid) {
		return nil // 进程已不存在，视为成功
	}

	// Windows 上使用 taskkill 强制终止
	cmd := exec.Command("taskkill", "/PID", fmt.Sprintf("%d", pid), "/F", "/T")
	if err := cmd.Run(); err != nil {
		// exit code 128 表示进程不存在，视为成功
		if exitErr, ok := err.(*exec.ExitError); ok && exitErr.ExitCode() == 128 {
			return nil
		}
		return fmt.Errorf("taskkill failed: %w", err)
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

	// Windows 上使用 DETACHED_PROCESS 脱离控制台
	cmd.SysProcAttr = &syscall.SysProcAttr{
		CreationFlags: syscall.CREATE_NEW_PROCESS_GROUP | 0x00000008, // DETACHED_PROCESS
	}

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
