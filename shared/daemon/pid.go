// Package daemon 提供守护进程管理功能，包括 PID 文件操作和进程状态检查
package daemon

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// ErrAlreadyRunning 表示服务已在运行（锁冲突）
var ErrAlreadyRunning = errors.New("service already running")

// PIDLock 持有锁文件的文件描述符，进程退出时内核自动释放锁
type PIDLock struct {
	file    *os.File
	pidFile string
}

// AcquirePID 创建/打开锁文件，获取排他锁，写入当前 PID。
// 锁冲突时返回 ErrAlreadyRunning。
func AcquirePID(pidFile string) (*PIDLock, error) {
	dir := filepath.Dir(pidFile)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("create pid directory: %w", err)
	}

	f, err := os.OpenFile(pidFile, os.O_CREATE|os.O_RDWR, 0644)
	if err != nil {
		return nil, fmt.Errorf("open pid file: %w", err)
	}

	if err := lockFile(f); err != nil {
		f.Close()
		if err == ErrAlreadyRunning {
			return nil, ErrAlreadyRunning
		}
		return nil, fmt.Errorf("lock pid file: %w", err)
	}

	pid := os.Getpid()
	if err := f.Truncate(0); err != nil {
		unlockFile(f)
		f.Close()
		return nil, fmt.Errorf("truncate pid file: %w", err)
	}
	if _, err := f.WriteString(strconv.Itoa(pid)); err != nil {
		unlockFile(f)
		f.Close()
		return nil, fmt.Errorf("write pid file: %w", err)
	}
	if err := f.Sync(); err != nil {
		unlockFile(f)
		f.Close()
		return nil, fmt.Errorf("sync pid file: %w", err)
	}

	return &PIDLock{file: f, pidFile: pidFile}, nil
}

// Release 释放锁并关闭文件描述符（不删除锁文件）
func (l *PIDLock) Release() {
	if l.file != nil {
		unlockFile(l.file)
		l.file.Close()
		l.file = nil
	}
}

// ReadPID 读取 PID 文件
func ReadPID(pidFile string) (int, error) {
	data, err := os.ReadFile(pidFile)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, fmt.Errorf("read pid file: %w", err)
	}

	pidStr := strings.TrimSpace(string(data))
	if pidStr == "" {
		return 0, nil
	}

	pid, err := strconv.Atoi(pidStr)
	if err != nil {
		return 0, fmt.Errorf("parse pid: %w", err)
	}

	return pid, nil
}

// GetStatus 获取进程状态（锁优先，PID 为辅）
func GetStatus(pidFile string) (running bool, pid int, err error) {
	acquired, unlock, err := tryLock(pidFile)
	if err != nil {
		if os.IsNotExist(err) {
			return false, 0, nil
		}
		return false, 0, err
	}

	if acquired {
		unlock()
		return false, 0, nil
	}

	// 锁被持有 = 服务在运行，读 PID 作为附加信息
	pid, readErr := ReadPID(pidFile)
	if readErr != nil {
		return true, 0, fmt.Errorf("service is running but pid unreadable: %w", readErr)
	}
	if pid == 0 {
		return true, 0, fmt.Errorf("service is running but pid file is empty")
	}
	return true, pid, nil
}

// GetSocketPath 根据 PID 文件路径生成对应的 Socket 文件路径
func GetSocketPath(pidFile string) string {
	if strings.HasSuffix(pidFile, ".pid") {
		return strings.TrimSuffix(pidFile, ".pid") + ".sock"
	}
	return pidFile + ".sock"
}

// IsDaemonChild 检查当前进程是否为守护进程子进程
func IsDaemonChild(envKey string) bool {
	return os.Getenv(envKey) == "1"
}

// WaitForStop 等待进程停止
func WaitForStop(pid int, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if !IsRunning(pid) {
			return true
		}
		time.Sleep(100 * time.Millisecond)
	}
	return false
}
