//go:build !linux

package dofs

import "errors"

type stateDirectoryLock struct{}

func acquireStateDirectoryLock(_ string) (*stateDirectoryLock, error) {
	return nil, errors.New("DOFS writeback state locking is supported on Linux only")
}

func acquireDOFSFileLock(_, _ string) (*stateDirectoryLock, error) {
	return nil, errors.New("DOFS file locking is supported on Linux only")
}

func (l *stateDirectoryLock) Close() error { return nil }
