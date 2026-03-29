//go:build windows

package daemon

import (
	"os"
	"syscall"
	"unsafe"
)

var (
	modkernel32      = syscall.NewLazyDLL("kernel32.dll")
	procLockFileEx   = modkernel32.NewProc("LockFileEx")
	procUnlockFileEx = modkernel32.NewProc("UnlockFileEx")
)

const (
	lockfileExclusiveLock   = 0x00000002
	lockfileFailImmediately = 0x00000001

	// ERROR_LOCK_VIOLATION is returned when the lock is held by another process.
	errorLockViolation = 33

	// lockOffset 锁区间放在高位，避免与 PID 文件内容（offset 0 起的几个字节）重叠。
	// Windows 的 LockFileEx 是字节区间锁，若锁在 offset 0 会阻止其他句柄读取 PID 内容。
	lockOffset = 0x7FFFFFFF
)

// tryLock 非阻塞尝试排他锁（只读探测，不创建文件）。
// OpenFile 不带 O_CREATE，ENOENT 原样返回由调用方处理。
// acquired=false 分支立即 close fd；unlock() 闭包同时 unlock + close。
func tryLock(path string) (acquired bool, unlock func(), err error) {
	f, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		return false, nil, err
	}

	err = lockFile(f)
	if err != nil {
		f.Close()
		if err == ErrAlreadyRunning {
			return false, nil, nil
		}
		return false, nil, err
	}

	return true, func() {
		unlockFile(f)
		f.Close()
	}, nil
}

// lockFile 对已打开的文件加非阻塞排他锁。
// ERROR_LOCK_VIOLATION → 返回 ErrAlreadyRunning。
func lockFile(f *os.File) error {
	ol := syscall.Overlapped{Offset: lockOffset}
	flags := uint32(lockfileExclusiveLock | lockfileFailImmediately)
	r1, _, err := procLockFileEx.Call(
		uintptr(f.Fd()),
		uintptr(flags),
		0,
		1, 0,
		uintptr(unsafe.Pointer(&ol)),
	)
	if r1 == 0 {
		if errno, ok := err.(syscall.Errno); ok && errno == errorLockViolation {
			return ErrAlreadyRunning
		}
		return err
	}
	return nil
}

// unlockFile 释放文件锁。
func unlockFile(f *os.File) error {
	ol := syscall.Overlapped{Offset: lockOffset}
	r1, _, err := procUnlockFileEx.Call(
		uintptr(f.Fd()),
		0,
		1, 0,
		uintptr(unsafe.Pointer(&ol)),
	)
	if r1 == 0 {
		return err
	}
	return nil
}
