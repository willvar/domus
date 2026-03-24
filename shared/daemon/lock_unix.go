//go:build !windows

package daemon

import (
	"os"
	"syscall"
)

// tryLock 非阻塞尝试排他锁（只读探测，不创建文件）。
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
func lockFile(f *os.File) error {
	err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
	if err != nil {
		if err == syscall.EWOULDBLOCK || err == syscall.EAGAIN {
			return ErrAlreadyRunning
		}
		return err
	}
	return nil
}

// unlockFile 释放文件锁。
func unlockFile(f *os.File) error {
	return syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
}
