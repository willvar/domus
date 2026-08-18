//go:build linux

package dofs

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"golang.org/x/sys/unix"
)

var (
	ErrInvalidUserSelector = errors.New("exactly one of user_id or username is required")
	ErrUserNotFound        = errors.New("DOFS user was not found")
	ErrMountNotManaged     = errors.New("DOFS mount is not managed")
	ErrManagerStopping     = errors.New("DOFS manager is stopping")
	ErrForeignMount        = errors.New("mountpoint is occupied by a live or foreign mount")
	ErrMountUnhealthy      = errors.New("existing DOFS mount is unhealthy")
)

const desiredMarkerVersion = 1

type MountUserSelector struct {
	UserID   string `json:"user_id,omitempty"`
	Username string `json:"username,omitempty"`
}

func (s MountUserSelector) Validate() error {
	hasID := strings.TrimSpace(s.UserID) != ""
	hasUsername := strings.TrimSpace(s.Username) != ""
	if hasID == hasUsername {
		return ErrInvalidUserSelector
	}
	return nil
}

type MountIdentity struct {
	UserID   string `json:"user_id"`
	Username string `json:"username"`
}

// MountOptions are the Domus manager's host-facing mount policy. The actual
// FUSE implementation lives in github.com/willvar/dofs; keeping this small
// transport type here avoids coupling lifecycle orchestration to filesystem
// internals.
type MountOptions struct {
	Debug      bool
	AllowOther bool
	UID        uint32
	GID        uint32
	Writable   bool
}

// ManagedMount is the lifecycle surface the manager needs from a mounted
// filesystem. Done must close only after the FUSE serve loop has exited and
// the backend has released its plaintext keys, local lock, and database lease.
type ManagedMount interface {
	Unmount() error
	Done() <-chan struct{}
}

type ManagedMountFailureReporter interface {
	Failures() <-chan error
}

// ManagedMountProvider resolves users and constructs one user-scoped FUSE
// backend. The production provider owns database/OSS wiring; tests can use a
// mount-free implementation.
type ManagedMountProvider interface {
	ResolveUser(context.Context, MountUserSelector) (MountIdentity, error)
	Mount(context.Context, MountIdentity, string, string, MountOptions) (ManagedMount, error)
}

type ManagerOptions struct {
	MountRoot         string
	StateRoot         string
	ControlSocket     string
	SocketMode        os.FileMode
	SocketGID         int
	Mount             MountOptions
	ReconcileInterval time.Duration
	MountTimeout      time.Duration
	MaxMounts         int
	Logf              func(string, ...any)
	OnReady           func() error
}

type MountStatus struct {
	UserID     string    `json:"user_id"`
	Username   string    `json:"username,omitempty"`
	Mountpoint string    `json:"mountpoint"`
	MountID    string    `json:"mount_id,omitempty"`
	State      string    `json:"state"`
	Desired    bool      `json:"desired"`
	Writable   bool      `json:"writable"`
	AllowOther bool      `json:"allow_other"`
	UID        uint32    `json:"uid"`
	GID        uint32    `json:"gid"`
	MountedAt  time.Time `json:"mounted_at,omitempty"`
	UpdatedAt  time.Time `json:"updated_at"`
	LastError  string    `json:"last_error,omitempty"`
}

type ManagerHealth struct {
	Status          string    `json:"status"`
	MaxMounts       int       `json:"max_mounts"`
	DesiredMounts   int       `json:"desired_mounts"`
	Mounted         int       `json:"mounted"`
	Degraded        int       `json:"degraded"`
	LastReconcileAt time.Time `json:"last_reconcile_at,omitempty"`
	LastError       string    `json:"last_error,omitempty"`
}

type managedMountEntry struct {
	status MountStatus
	handle ManagedMount
}

type desiredMountMarker struct {
	Version int    `json:"version"`
	UserID  string `json:"user_id"`
}

// Manager owns every production DOFS mount on one Linux host. Desired-state
// markers survive process restarts; writeback state remains in a separate
// private tree and is never removed by normal unmounts.
type Manager struct {
	options        ManagerOptions
	provider       ManagedMountProvider
	desiredRoot    string
	usersStateRoot string
	lock           *stateDirectoryLock
	socketLock     *stateDirectoryLock

	ctx    context.Context
	cancel context.CancelFunc
	wake   chan struct{}

	mu              sync.Mutex
	entries         map[string]*managedMountEntry
	desired         map[string]bool
	forgetting      map[string]bool
	operations      map[string]chan struct{}
	closing         bool
	lastReconcileAt time.Time
	lastError       string
	operationWG     sync.WaitGroup
	shutdownOnce    sync.Once
	shutdownErr     error
	closeOnce       sync.Once
	closeErr        error
}

func NewManager(options ManagerOptions, provider ManagedMountProvider) (*Manager, error) {
	if provider == nil {
		return nil, errors.New("DOFS managed mount provider is required")
	}
	if options.ReconcileInterval <= 0 {
		return nil, errors.New("DOFS reconcile interval must be greater than zero")
	}
	if options.MountTimeout <= 0 {
		return nil, errors.New("DOFS mount timeout must be greater than zero")
	}
	if options.MaxMounts <= 0 {
		return nil, errors.New("DOFS maximum mount count must be greater than zero")
	}
	if options.SocketMode == 0 {
		options.SocketMode = 0600
	}
	if options.SocketMode.Perm()&^os.FileMode(0660) != 0 {
		return nil, errors.New("DOFS control socket mode must not grant world or execute access")
	}
	if options.SocketGID < -1 {
		return nil, errors.New("DOFS control socket GID is invalid")
	}

	var err error
	options.MountRoot, err = validateManagerPath("mount root", options.MountRoot)
	if err != nil {
		return nil, err
	}
	options.StateRoot, err = validateManagerPath("state root", options.StateRoot)
	if err != nil {
		return nil, err
	}
	options.ControlSocket, err = validateManagerPath("control socket", options.ControlSocket)
	if err != nil {
		return nil, err
	}
	if pathsOverlap(options.MountRoot, options.StateRoot) {
		return nil, errors.New("DOFS mount root and state root must not overlap")
	}
	if pathInside(options.MountRoot, options.ControlSocket) {
		return nil, errors.New("DOFS control socket must not be inside the mount root")
	}
	if options.MountRoot == options.ControlSocket {
		return nil, errors.New("DOFS control socket must not equal the mount root")
	}
	if options.StateRoot == options.ControlSocket || pathInside(options.StateRoot, options.ControlSocket) {
		return nil, errors.New("DOFS control socket must not be inside the state root")
	}
	if err := ensurePrivateDirectory(options.MountRoot); err != nil {
		return nil, err
	}
	if err := ensurePrivateDirectory(options.StateRoot); err != nil {
		return nil, err
	}
	desiredRoot := filepath.Join(options.StateRoot, "desired")
	usersStateRoot := filepath.Join(options.StateRoot, "users")
	if err := ensurePrivateDirectory(desiredRoot); err != nil {
		return nil, err
	}
	if err := ensurePrivateDirectory(usersStateRoot); err != nil {
		return nil, err
	}
	if err := ensureSocketParent(filepath.Dir(options.ControlSocket)); err != nil {
		return nil, err
	}

	lock, err := acquireStateDirectoryLock(options.StateRoot)
	if err != nil {
		return nil, fmt.Errorf("acquire DOFS manager lock: %w", err)
	}
	socketLock, err := acquireDOFSFileLock(options.ControlSocket+".lock", "DOFS control socket")
	if err != nil {
		_ = lock.Close()
		return nil, err
	}
	ctx, cancel := context.WithCancel(context.Background())
	manager := &Manager{
		options: options, provider: provider,
		desiredRoot: desiredRoot, usersStateRoot: usersStateRoot, lock: lock, socketLock: socketLock,
		ctx: ctx, cancel: cancel, wake: make(chan struct{}, 1),
		entries: make(map[string]*managedMountEntry),
		desired: make(map[string]bool), forgetting: make(map[string]bool), operations: make(map[string]chan struct{}),
	}
	if err := manager.loadDesiredMarkers(); err != nil {
		cancel()
		_ = socketLock.Close()
		_ = lock.Close()
		return nil, err
	}
	return manager, nil
}

func validateManagerPath(name, path string) (string, error) {
	if strings.TrimSpace(path) == "" {
		return "", fmt.Errorf("DOFS %s is required", name)
	}
	if !filepath.IsAbs(path) {
		return "", fmt.Errorf("DOFS %s must be absolute", name)
	}
	clean := filepath.Clean(path)
	if clean == string(filepath.Separator) {
		return "", fmt.Errorf("DOFS %s must not be the filesystem root", name)
	}
	return clean, nil
}

func pathsOverlap(first, second string) bool {
	return first == second || pathInside(first, second) || pathInside(second, first)
}

func pathInside(parent, child string) bool {
	relative, err := filepath.Rel(parent, child)
	return err == nil && relative != "." && relative != ".." && relative != "" && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}

func ensureSocketParent(directory string) error {
	info, err := os.Lstat(directory)
	if errors.Is(err, os.ErrNotExist) {
		if err := os.MkdirAll(directory, 0750); err != nil {
			return fmt.Errorf("create DOFS socket directory: %w", err)
		}
		info, err = os.Lstat(directory)
	}
	if err != nil {
		return fmt.Errorf("inspect DOFS socket directory: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return errors.New("DOFS socket parent must be a real directory")
	}
	resolved, err := filepath.EvalSymlinks(directory)
	if err != nil {
		return fmt.Errorf("resolve DOFS socket directory: %w", err)
	}
	if filepath.Clean(resolved) != filepath.Clean(directory) {
		return errors.New("DOFS socket parent must not contain symbolic-link components")
	}
	return nil
}

func safeUserID(userID string) bool {
	if len(userID) == 0 || len(userID) > 128 || strings.HasPrefix(userID, ".") {
		return false
	}
	for _, character := range userID {
		if (character >= 'a' && character <= 'z') || (character >= 'A' && character <= 'Z') ||
			(character >= '0' && character <= '9') || character == '-' || character == '_' {
			continue
		}
		return false
	}
	return true
}

func (m *Manager) desiredMarkerPath(userID string) string {
	return filepath.Join(m.desiredRoot, userID+".json")
}

func (m *Manager) mountpoint(userID string) string {
	return filepath.Join(m.options.MountRoot, userID)
}

func (m *Manager) stateDirectory(userID string) string {
	return filepath.Join(m.usersStateRoot, userID)
}

func (m *Manager) loadDesiredMarkers() error {
	entries, err := os.ReadDir(m.desiredRoot)
	if err != nil {
		return fmt.Errorf("read DOFS desired state: %w", err)
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		userID := strings.TrimSuffix(entry.Name(), ".json")
		if !safeUserID(userID) {
			return fmt.Errorf("invalid DOFS desired marker name %q", entry.Name())
		}
		path := m.desiredMarkerPath(userID)
		info, err := os.Lstat(path)
		if err != nil {
			return fmt.Errorf("inspect DOFS desired marker %s: %w", userID, err)
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() || info.Size() > 4096 {
			return fmt.Errorf("invalid DOFS desired marker %s", userID)
		}
		file, err := os.Open(path)
		if err != nil {
			return fmt.Errorf("open DOFS desired marker %s: %w", userID, err)
		}
		var marker desiredMountMarker
		decoder := json.NewDecoder(file)
		decoder.DisallowUnknownFields()
		decodeErr := decoder.Decode(&marker)
		if decodeErr == nil {
			var trailing any
			if trailingErr := decoder.Decode(&trailing); !errors.Is(trailingErr, io.EOF) {
				if trailingErr == nil {
					decodeErr = errors.New("multiple JSON values")
				} else {
					decodeErr = trailingErr
				}
			}
		}
		closeErr := file.Close()
		if decodeErr != nil {
			return fmt.Errorf("decode DOFS desired marker %s: %w", userID, decodeErr)
		}
		if closeErr != nil {
			return fmt.Errorf("close DOFS desired marker %s: %w", userID, closeErr)
		}
		if marker.Version != desiredMarkerVersion || marker.UserID != userID {
			return fmt.Errorf("invalid DOFS desired marker contents for %s", userID)
		}
		m.desired[userID] = true
	}
	return nil
}

func (m *Manager) persistDesiredMarker(userID string) error {
	marker := desiredMountMarker{Version: desiredMarkerVersion, UserID: userID}
	temporary, err := os.CreateTemp(m.desiredRoot, ".desired-*.tmp")
	if err != nil {
		return fmt.Errorf("create DOFS desired marker: %w", err)
	}
	temporaryPath := temporary.Name()
	committed := false
	defer func() {
		_ = temporary.Close()
		if !committed {
			_ = os.Remove(temporaryPath)
		}
	}()
	if err := temporary.Chmod(0600); err != nil {
		return fmt.Errorf("protect DOFS desired marker: %w", err)
	}
	if err := json.NewEncoder(temporary).Encode(marker); err != nil {
		return fmt.Errorf("write DOFS desired marker: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		return fmt.Errorf("sync DOFS desired marker: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close DOFS desired marker: %w", err)
	}
	if err := os.Rename(temporaryPath, m.desiredMarkerPath(userID)); err != nil {
		return fmt.Errorf("publish DOFS desired marker: %w", err)
	}
	committed = true
	return syncDirectory(m.desiredRoot)
}

func (m *Manager) removeDesiredMarker(userID string) error {
	err := os.Remove(m.desiredMarkerPath(userID))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove DOFS desired marker: %w", err)
	}
	return syncDirectory(m.desiredRoot)
}

func (m *Manager) beginUserOperation(ctx context.Context, userID string) (func(), error) {
	for {
		m.mu.Lock()
		if m.closing {
			m.mu.Unlock()
			return nil, ErrManagerStopping
		}
		if running, ok := m.operations[userID]; ok {
			m.mu.Unlock()
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-running:
				continue
			}
		}
		done := make(chan struct{})
		m.operations[userID] = done
		m.operationWG.Add(1)
		m.mu.Unlock()
		var once sync.Once
		return func() {
			once.Do(func() {
				m.mu.Lock()
				delete(m.operations, userID)
				close(done)
				m.mu.Unlock()
				m.operationWG.Done()
			})
		}, nil
	}
}

func (m *Manager) Ensure(ctx context.Context, selector MountUserSelector) (MountStatus, error) {
	return m.ensure(ctx, selector, false)
}

// ensure is also used by reconciliation. When onlyIfDesired is true, the
// desired-state check happens after acquiring the per-user operation lock so
// a stale reconcile snapshot cannot recreate a mount just removed by an
// explicit concurrent Unmount.
func (m *Manager) ensure(ctx context.Context, selector MountUserSelector, onlyIfDesired bool) (MountStatus, error) {
	if err := selector.Validate(); err != nil {
		return MountStatus{}, err
	}
	identity, err := m.provider.ResolveUser(ctx, selector)
	if err != nil {
		return MountStatus{}, fmt.Errorf("resolve DOFS user: %w", err)
	}
	if !safeUserID(identity.UserID) || strings.TrimSpace(identity.Username) == "" {
		return MountStatus{}, errors.New("resolved DOFS user identity is invalid")
	}
	if selector.UserID != "" && identity.UserID != strings.TrimSpace(selector.UserID) {
		return MountStatus{}, errors.New("resolved DOFS user ID does not match selector")
	}

	finish, err := m.beginUserOperation(ctx, identity.UserID)
	if err != nil {
		return MountStatus{}, err
	}
	defer finish()
	if onlyIfDesired {
		m.mu.Lock()
		stillDesired := m.desired[identity.UserID]
		m.mu.Unlock()
		if !stillDesired {
			return MountStatus{}, nil
		}
	} else {
		m.mu.Lock()
		cancelForget := m.forgetting[identity.UserID]
		m.mu.Unlock()
		if cancelForget {
			// A fresh external Ensure supersedes a failed explicit Unmount. Re-publish
			// the marker before changing memory so a crash cannot lose the restored
			// desired state (Workspace reconciliation relies on this behavior).
			if err := m.persistDesiredMarker(identity.UserID); err != nil {
				return MountStatus{}, err
			}
			m.mu.Lock()
			m.desired[identity.UserID] = true
			delete(m.forgetting, identity.UserID)
			if entry := m.entries[identity.UserID]; entry != nil {
				entry.status.Desired = true
				entry.status.UpdatedAt = time.Now().UTC()
			}
			m.mu.Unlock()
		}
	}

	m.mu.Lock()
	desired := m.desired[identity.UserID]
	entry := m.entries[identity.UserID]
	if entry != nil && entry.handle != nil && entry.status.State == "mounted" && desired {
		status := entry.status
		m.mu.Unlock()
		return status, nil
	}
	if entry != nil && entry.handle != nil && desired {
		status := entry.status
		m.mu.Unlock()
		return status, fmt.Errorf("%w: %s", ErrMountUnhealthy, status.LastError)
	}
	m.mu.Unlock()

	if !desired {
		if err := m.persistDesiredMarker(identity.UserID); err != nil {
			return MountStatus{}, err
		}
		m.mu.Lock()
		m.desired[identity.UserID] = true
		m.mu.Unlock()
	}

	m.mu.Lock()
	entry = m.entries[identity.UserID]
	if entry != nil && entry.handle != nil && entry.status.State == "mounted" {
		entry.status.Desired = true
		entry.status.Username = identity.Username
		entry.status.UpdatedAt = time.Now().UTC()
		status := entry.status
		m.mu.Unlock()
		return status, nil
	}
	if entry != nil && entry.handle != nil {
		status := entry.status
		m.mu.Unlock()
		return status, fmt.Errorf("%w: %s", ErrMountUnhealthy, status.LastError)
	}
	activeMounts := 0
	for _, candidate := range m.entries {
		if candidate.handle != nil || candidate.status.State == "mounting" {
			activeMounts++
		}
	}
	if activeMounts >= m.options.MaxMounts {
		status := m.baseStatus(identity)
		status.State = "pending"
		status.Desired = true
		status.LastError = fmt.Sprintf("waiting for DOFS mount capacity (limit %d)", m.options.MaxMounts)
		status.UpdatedAt = time.Now().UTC()
		m.entries[identity.UserID] = &managedMountEntry{status: status}
		m.mu.Unlock()
		return status, nil
	}
	status := m.baseStatus(identity)
	status.State = "mounting"
	status.Desired = true
	status.UpdatedAt = time.Now().UTC()
	m.entries[identity.UserID] = &managedMountEntry{status: status}
	m.mu.Unlock()

	handle, mountErr := m.startMount(identity)
	if mountErr != nil {
		m.mu.Lock()
		entry = m.entries[identity.UserID]
		entry.status.State = "failed"
		entry.status.LastError = mountErr.Error()
		entry.status.UpdatedAt = time.Now().UTC()
		failed := entry.status
		m.mu.Unlock()
		return failed, mountErr
	}

	m.mu.Lock()
	entry = m.entries[identity.UserID]
	entry.handle = handle
	entry.status.State = "mounted"
	entry.status.MountID = uuid.NewString()
	entry.status.LastError = ""
	entry.status.MountedAt = time.Now().UTC()
	entry.status.UpdatedAt = entry.status.MountedAt
	mounted := entry.status
	m.mu.Unlock()
	go m.watchMount(identity.UserID, handle)
	m.logf("DOFS mounted user %s (%s) at %s", identity.Username, identity.UserID, mounted.Mountpoint)
	return mounted, nil
}

func (m *Manager) baseStatus(identity MountIdentity) MountStatus {
	return MountStatus{
		UserID: identity.UserID, Username: identity.Username,
		Mountpoint: m.mountpoint(identity.UserID), Writable: m.options.Mount.Writable,
		AllowOther: m.options.Mount.AllowOther, UID: m.options.Mount.UID, GID: m.options.Mount.GID,
	}
}

func (m *Manager) startMount(identity MountIdentity) (ManagedMount, error) {
	mountpoint := m.mountpoint(identity.UserID)
	stateDirectory := m.stateDirectory(identity.UserID)
	if err := m.prepareMountpoint(mountpoint); err != nil {
		return nil, err
	}
	if err := ensurePrivateDirectory(stateDirectory); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(m.ctx, m.options.MountTimeout)
	defer cancel()
	handle, err := m.provider.Mount(ctx, identity, mountpoint, stateDirectory, m.options.Mount)
	if err != nil {
		return nil, fmt.Errorf("mount DOFS user %s: %w", identity.UserID, err)
	}
	if handle == nil || handle.Done() == nil {
		return nil, errors.New("DOFS provider returned an invalid mount handle")
	}
	return handle, nil
}

func (m *Manager) prepareMountpoint(mountpoint string) error {
	if mounted, err := inspectLinuxMount(mountpoint); err != nil {
		return err
	} else if mounted != nil {
		if !isDOFSMount(mounted) {
			return fmt.Errorf("%w: %s contains %s", ErrForeignMount, mountpoint, mounted.FilesystemType)
		}
		healthy, probeErr := probeMountedFilesystem(mountpoint)
		if probeErr != nil && !isDisconnectedMountError(probeErr) {
			return fmt.Errorf("%w: cannot safely probe %s: %v", ErrForeignMount, mountpoint, probeErr)
		}
		if healthy {
			return fmt.Errorf("%w: live DOFS mount at %s", ErrForeignMount, mountpoint)
		}
		if err := unmountDisconnectedDOFS(mountpoint); err != nil {
			return err
		}
		m.logf("Removed disconnected DOFS mount at %s", mountpoint)
	}

	info, err := os.Lstat(mountpoint)
	if errors.Is(err, os.ErrNotExist) {
		if err := os.Mkdir(mountpoint, 0700); err != nil {
			return fmt.Errorf("create DOFS mountpoint: %w", err)
		}
		info, err = os.Lstat(mountpoint)
	}
	if err != nil {
		return fmt.Errorf("inspect DOFS mountpoint: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return errors.New("DOFS mountpoint must be a real directory")
	}
	if err := os.Chmod(mountpoint, 0700); err != nil {
		return fmt.Errorf("protect DOFS mountpoint: %w", err)
	}
	entries, err := os.ReadDir(mountpoint)
	if err != nil {
		return fmt.Errorf("read DOFS mountpoint: %w", err)
	}
	if len(entries) != 0 {
		return errors.New("DOFS mountpoint must be empty")
	}
	return nil
}

func (m *Manager) watchMount(userID string, handle ManagedMount) {
	var failures <-chan error
	if reporter, ok := handle.(ManagedMountFailureReporter); ok {
		failures = reporter.Failures()
	}
	for {
		select {
		case failure := <-failures:
			if failure == nil {
				failures = nil
				continue
			}
			m.mu.Lock()
			entry := m.entries[userID]
			if entry != nil && entry.handle == handle {
				entry.status.State = "failed"
				entry.status.LastError = failure.Error()
				entry.status.UpdatedAt = time.Now().UTC()
			}
			m.mu.Unlock()
			m.logf("DOFS mount for user %s became unhealthy: %v", userID, failure)
			m.signalReconcile()
		case <-handle.Done():
			m.mu.Lock()
			entry := m.entries[userID]
			if entry == nil || entry.handle != handle {
				m.mu.Unlock()
				return
			}
			entry.handle = nil
			entry.status.UpdatedAt = time.Now().UTC()
			if m.closing || !m.desired[userID] || entry.status.State == "unmounting" {
				entry.status.State = "unmounted"
				entry.status.LastError = ""
			} else {
				entry.status.State = "failed"
				if entry.status.LastError == "" {
					entry.status.LastError = "FUSE serve loop exited unexpectedly"
				}
			}
			status := entry.status
			m.mu.Unlock()
			if status.State == "failed" {
				m.logf("DOFS mount for user %s exited unexpectedly", userID)
				m.signalReconcile()
			}
			return
		}
	}
}

func (m *Manager) signalReconcile() {
	select {
	case m.wake <- struct{}{}:
	default:
	}
}

func (m *Manager) Unmount(ctx context.Context, userID string) (MountStatus, error) {
	if !safeUserID(userID) {
		return MountStatus{}, errors.New("invalid DOFS user ID")
	}
	return m.unmount(ctx, userID, true)
}

func (m *Manager) unmount(ctx context.Context, userID string, forget bool) (MountStatus, error) {
	finish, err := m.beginUserOperation(ctx, userID)
	if err != nil {
		return MountStatus{}, err
	}
	defer finish()

	m.mu.Lock()
	if !forget {
		entry := m.entries[userID]
		stillNeedsRetry := m.forgetting[userID] || (entry != nil && entry.handle != nil &&
			(entry.status.State == "failed" || entry.status.State == "unmount_failed"))
		if !stillNeedsRetry {
			if entry != nil {
				status := entry.status
				status.Desired = m.desired[userID]
				m.mu.Unlock()
				return status, nil
			}
			m.mu.Unlock()
			return MountStatus{}, nil
		}
	}
	if forget {
		delete(m.desired, userID)
		m.forgetting[userID] = true
	}
	forgetAfterUnmount := m.forgetting[userID]
	entry := m.entries[userID]
	if entry == nil {
		entry = &managedMountEntry{status: MountStatus{
			UserID: userID, Mountpoint: m.mountpoint(userID), State: "unmounted",
			Writable: m.options.Mount.Writable, AllowOther: m.options.Mount.AllowOther,
			UID: m.options.Mount.UID, GID: m.options.Mount.GID,
			UpdatedAt: time.Now().UTC(),
		}}
		m.entries[userID] = entry
	}
	entry.status.Desired = m.desired[userID]
	handle := entry.handle
	if handle != nil {
		entry.status.State = "unmounting"
		entry.status.UpdatedAt = time.Now().UTC()
	}
	m.mu.Unlock()

	if handle != nil {
		if err := handle.Unmount(); err != nil {
			m.mu.Lock()
			entry.status.State = "unmount_failed"
			entry.status.LastError = err.Error()
			entry.status.UpdatedAt = time.Now().UTC()
			status := entry.status
			m.mu.Unlock()
			return status, fmt.Errorf("unmount DOFS user %s: %w", userID, err)
		}
		select {
		case <-ctx.Done():
			return MountStatus{}, ctx.Err()
		case <-handle.Done():
		}
	} else if mounted, inspectErr := inspectLinuxMount(m.mountpoint(userID)); inspectErr != nil {
		return MountStatus{}, inspectErr
	} else if mounted != nil {
		if !isDOFSMount(mounted) {
			return MountStatus{}, fmt.Errorf("%w: %s", ErrForeignMount, m.mountpoint(userID))
		}
		healthy, probeErr := probeMountedFilesystem(m.mountpoint(userID))
		if healthy || (probeErr != nil && !isDisconnectedMountError(probeErr)) {
			return MountStatus{}, fmt.Errorf("%w: unmanaged live mount at %s", ErrForeignMount, m.mountpoint(userID))
		}
		if err := unmountDisconnectedDOFS(m.mountpoint(userID)); err != nil {
			return MountStatus{}, err
		}
	}

	cleanupErr := removeEmptyRealDirectory(m.mountpoint(userID))
	var markerErr error
	if cleanupErr == nil && forgetAfterUnmount {
		// Commit the durable desired-state deletion only after the mount and its
		// mountpoint are gone. A crash before here reloads the marker and safely
		// recovers/retries instead of leaving an untracked live plaintext mount.
		markerErr = m.removeDesiredMarker(userID)
	}
	m.mu.Lock()
	entry.handle = nil
	if cleanupErr != nil || markerErr != nil {
		entry.status.State = "blocked"
		entry.status.LastError = errors.Join(cleanupErr, markerErr).Error()
	} else {
		entry.status.State = "unmounted"
		entry.status.LastError = ""
		if forgetAfterUnmount {
			delete(m.forgetting, userID)
		}
	}
	entry.status.Desired = m.desired[userID]
	entry.status.UpdatedAt = time.Now().UTC()
	status := entry.status
	m.mu.Unlock()
	if finalErr := errors.Join(cleanupErr, markerErr); finalErr != nil {
		return status, fmt.Errorf("finalize unmounted DOFS path for user %s: %w", userID, finalErr)
	}
	m.logf("DOFS unmounted user %s from %s", userID, status.Mountpoint)
	// A successful unmount may have released the only free capacity slot. Wake
	// the reconciler so a durable pending Ensure does not wait for the ticker.
	m.signalReconcile()
	return status, nil
}

func removeEmptyRealDirectory(path string) error {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return errors.New("refusing to remove non-directory mountpoint")
	}
	return os.Remove(path)
}

func (m *Manager) Status(userID string) (MountStatus, error) {
	if !safeUserID(userID) {
		return MountStatus{}, errors.New("invalid DOFS user ID")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if entry := m.entries[userID]; entry != nil {
		status := entry.status
		status.Desired = m.desired[userID]
		return status, nil
	}
	if m.desired[userID] {
		return MountStatus{
			UserID: userID, Mountpoint: m.mountpoint(userID), State: "pending", Desired: true,
			Writable: m.options.Mount.Writable, AllowOther: m.options.Mount.AllowOther,
			UID: m.options.Mount.UID, GID: m.options.Mount.GID,
		}, nil
	}
	return MountStatus{}, ErrMountNotManaged
}

func (m *Manager) List() []MountStatus {
	m.mu.Lock()
	defer m.mu.Unlock()
	statuses := make([]MountStatus, 0, len(m.entries)+len(m.desired))
	seen := make(map[string]bool, len(m.entries))
	for userID, entry := range m.entries {
		status := entry.status
		status.Desired = m.desired[userID]
		statuses = append(statuses, status)
		seen[userID] = true
	}
	for userID := range m.desired {
		if seen[userID] {
			continue
		}
		statuses = append(statuses, MountStatus{
			UserID: userID, Mountpoint: m.mountpoint(userID), State: "pending", Desired: true,
			Writable: m.options.Mount.Writable, AllowOther: m.options.Mount.AllowOther,
			UID: m.options.Mount.UID, GID: m.options.Mount.GID,
		})
	}
	sort.Slice(statuses, func(i, j int) bool { return statuses[i].UserID < statuses[j].UserID })
	return statuses
}

func (m *Manager) Health() ManagerHealth {
	m.mu.Lock()
	defer m.mu.Unlock()
	health := ManagerHealth{
		Status: "ready", MaxMounts: m.options.MaxMounts, DesiredMounts: len(m.desired),
		LastReconcileAt: m.lastReconcileAt, LastError: m.lastError,
	}
	for userID := range m.desired {
		entry := m.entries[userID]
		if entry != nil && entry.status.State == "mounted" && entry.handle != nil {
			health.Mounted++
		} else {
			health.Degraded++
		}
	}
	for userID, entry := range m.entries {
		if m.desired[userID] {
			continue
		}
		if entry.status.State == "blocked" || entry.status.State == "unmount_failed" {
			health.Degraded++
		}
	}
	if m.closing {
		health.Status = "stopping"
	} else if health.Degraded > 0 || health.LastError != "" {
		health.Status = "degraded"
	}
	return health
}

func (m *Manager) ReconcileOnce(ctx context.Context) error {
	m.mu.Lock()
	if m.closing {
		m.mu.Unlock()
		return ErrManagerStopping
	}
	desiredIDs := make([]string, 0, len(m.desired))
	for userID := range m.desired {
		desiredIDs = append(desiredIDs, userID)
	}
	retryUnmountSet := make(map[string]struct{})
	for userID, entry := range m.entries {
		if entry.handle != nil && (entry.status.State == "failed" || entry.status.State == "unmount_failed") {
			retryUnmountSet[userID] = struct{}{}
		}
	}
	for userID := range m.forgetting {
		retryUnmountSet[userID] = struct{}{}
	}
	m.mu.Unlock()
	retryUnmountIDs := make([]string, 0, len(retryUnmountSet))
	for userID := range retryUnmountSet {
		retryUnmountIDs = append(retryUnmountIDs, userID)
	}
	sort.Strings(desiredIDs)
	sort.Strings(retryUnmountIDs)

	var reconcileErrors []error
	for _, userID := range retryUnmountIDs {
		if _, err := m.unmount(ctx, userID, false); err != nil {
			reconcileErrors = append(reconcileErrors, fmt.Errorf("retry unmount user %s: %w", userID, err))
		}
	}
	for _, userID := range desiredIDs {
		if _, err := m.ensure(ctx, MountUserSelector{UserID: userID}, true); errors.Is(err, ErrUserNotFound) {
			// The product database is authoritative for user existence. A crash
			// after account deletion or an older reset may leave a durable marker;
			// forget it instead of keeping the whole manager permanently degraded.
			if _, forgetErr := m.unmount(ctx, userID, true); forgetErr != nil {
				reconcileErrors = append(reconcileErrors, fmt.Errorf("forget removed user %s: %w", userID, forgetErr))
				m.recordReconcileFailure(userID, forgetErr)
			} else {
				m.logf("DOFS forgot stale desired state for removed user %s", userID)
			}
		} else if err != nil {
			reconcileErrors = append(reconcileErrors, fmt.Errorf("user %s: %w", userID, err))
			m.recordReconcileFailure(userID, err)
		}
	}
	if err := m.reconcileOrphanMountpoints(ctx); err != nil {
		reconcileErrors = append(reconcileErrors, err)
	}
	reconcileErr := errors.Join(reconcileErrors...)
	m.mu.Lock()
	m.lastReconcileAt = time.Now().UTC()
	if reconcileErr != nil {
		m.lastError = reconcileErr.Error()
	} else {
		m.lastError = ""
	}
	m.mu.Unlock()
	return reconcileErr
}

func (m *Manager) recordReconcileFailure(userID string, reconcileErr error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	entry := m.entries[userID]
	if entry == nil {
		entry = &managedMountEntry{status: MountStatus{
			UserID: userID, Mountpoint: m.mountpoint(userID), Writable: m.options.Mount.Writable,
			AllowOther: m.options.Mount.AllowOther, UID: m.options.Mount.UID, GID: m.options.Mount.GID,
		}}
		m.entries[userID] = entry
	}
	if entry.handle == nil {
		entry.status.State = "failed"
		entry.status.Desired = m.desired[userID]
		entry.status.LastError = reconcileErr.Error()
		entry.status.UpdatedAt = time.Now().UTC()
	}
}

func (m *Manager) reconcileOrphanMountpoints(ctx context.Context) error {
	directoryEntries, err := os.ReadDir(m.options.MountRoot)
	if err != nil {
		return fmt.Errorf("scan DOFS mount root: %w", err)
	}
	var reconcileErrors []error
	for _, directoryEntry := range directoryEntries {
		select {
		case <-ctx.Done():
			return errors.Join(append(reconcileErrors, ctx.Err())...)
		default:
		}
		userID := directoryEntry.Name()
		if !safeUserID(userID) {
			reconcileErrors = append(reconcileErrors, fmt.Errorf("unexpected path in DOFS mount root: %s", userID))
			continue
		}
		m.mu.Lock()
		entry := m.entries[userID]
		desired := m.desired[userID]
		active := entry != nil && entry.handle != nil
		m.mu.Unlock()
		if active || desired {
			continue
		}
		mountpoint := m.mountpoint(userID)
		mounted, inspectErr := inspectLinuxMount(mountpoint)
		if inspectErr != nil {
			reconcileErrors = append(reconcileErrors, inspectErr)
			continue
		}
		if mounted == nil {
			if removeErr := removeEmptyRealDirectory(mountpoint); removeErr != nil {
				reconcileErrors = append(reconcileErrors, fmt.Errorf("orphan mountpoint %s: %w", userID, removeErr))
			}
			continue
		}
		if !isDOFSMount(mounted) {
			err := fmt.Errorf("%w: %s contains %s", ErrForeignMount, mountpoint, mounted.FilesystemType)
			m.recordBlockedMount(userID, err)
			reconcileErrors = append(reconcileErrors, err)
			continue
		}
		healthy, probeErr := probeMountedFilesystem(mountpoint)
		if healthy || (probeErr != nil && !isDisconnectedMountError(probeErr)) {
			err := fmt.Errorf("%w: unmanaged live mount at %s", ErrForeignMount, mountpoint)
			m.recordBlockedMount(userID, err)
			reconcileErrors = append(reconcileErrors, err)
			continue
		}
		if unmountErr := unmountDisconnectedDOFS(mountpoint); unmountErr != nil {
			m.recordBlockedMount(userID, unmountErr)
			reconcileErrors = append(reconcileErrors, unmountErr)
			continue
		}
		_ = removeEmptyRealDirectory(mountpoint)
	}
	return errors.Join(reconcileErrors...)
}

func (m *Manager) recordBlockedMount(userID string, blockedErr error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	entry := m.entries[userID]
	if entry == nil {
		entry = &managedMountEntry{status: MountStatus{
			UserID: userID, Mountpoint: m.mountpoint(userID), Writable: m.options.Mount.Writable,
			AllowOther: m.options.Mount.AllowOther, UID: m.options.Mount.UID, GID: m.options.Mount.GID,
		}}
		m.entries[userID] = entry
	}
	entry.status.State = "blocked"
	entry.status.LastError = blockedErr.Error()
	entry.status.UpdatedAt = time.Now().UTC()
}

func (m *Manager) RunReconciler(ctx context.Context) {
	ticker := time.NewTicker(m.options.ReconcileInterval)
	defer ticker.Stop()
	for {
		if err := m.ReconcileOnce(ctx); err != nil && !errors.Is(err, context.Canceled) && !errors.Is(err, ErrManagerStopping) {
			m.logf("DOFS reconcile incomplete: %v", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-m.ctx.Done():
			return
		case <-ticker.C:
		case <-m.wake:
		}
	}
}

func (m *Manager) Shutdown(ctx context.Context) error {
	m.shutdownOnce.Do(func() {
		m.mu.Lock()
		m.closing = true
		m.mu.Unlock()
		m.cancel()

		operationsDone := make(chan struct{})
		go func() {
			m.operationWG.Wait()
			close(operationsDone)
		}()
		select {
		case <-ctx.Done():
			m.shutdownErr = ctx.Err()
			return
		case <-operationsDone:
		}

		m.mu.Lock()
		handles := make(map[string]ManagedMount)
		for userID, entry := range m.entries {
			if entry.handle != nil {
				entry.status.State = "unmounting"
				entry.status.UpdatedAt = time.Now().UTC()
				handles[userID] = entry.handle
			}
		}
		m.mu.Unlock()

		type result struct {
			userID string
			err    error
		}
		results := make(chan result, len(handles))
		for userID, handle := range handles {
			go func(userID string, handle ManagedMount) {
				if err := handle.Unmount(); err != nil {
					results <- result{userID: userID, err: err}
					return
				}
				select {
				case <-ctx.Done():
					results <- result{userID: userID, err: ctx.Err()}
				case <-handle.Done():
					results <- result{userID: userID}
				}
			}(userID, handle)
		}
		var errorsByMount []error
		for range handles {
			mountResult := <-results
			if mountResult.err != nil {
				errorsByMount = append(errorsByMount, fmt.Errorf("user %s: %w", mountResult.userID, mountResult.err))
			}
		}
		m.shutdownErr = errors.Join(errorsByMount...)
	})
	return m.shutdownErr
}

func (m *Manager) Close() error {
	m.closeOnce.Do(func() {
		m.cancel()
		if m.lock != nil {
			m.closeErr = m.lock.Close()
			m.lock = nil
		}
		if m.socketLock != nil {
			if err := m.socketLock.Close(); m.closeErr == nil {
				m.closeErr = err
			}
			m.socketLock = nil
		}
	})
	return m.closeErr
}

func (m *Manager) logf(format string, arguments ...any) {
	if m.options.Logf != nil {
		m.options.Logf(format, arguments...)
	}
}

type linuxMountInfo struct {
	Mountpoint     string
	FilesystemType string
	Source         string
}

func inspectLinuxMount(target string) (*linuxMountInfo, error) {
	file, err := os.Open("/proc/self/mountinfo")
	if err != nil {
		return nil, fmt.Errorf("open mountinfo: %w", err)
	}
	defer func() { _ = file.Close() }()
	target = filepath.Clean(target)
	var match *linuxMountInfo
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 4096), 1024*1024)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) < 10 {
			continue
		}
		separator := -1
		for index := 6; index < len(fields); index++ {
			if fields[index] == "-" {
				separator = index
				break
			}
		}
		if separator < 0 || separator+2 >= len(fields) {
			continue
		}
		mountpoint, decodeErr := decodeMountInfoPath(fields[4])
		if decodeErr != nil {
			return nil, decodeErr
		}
		if filepath.Clean(mountpoint) != target {
			continue
		}
		match = &linuxMountInfo{
			Mountpoint: mountpoint, FilesystemType: fields[separator+1], Source: fields[separator+2],
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read mountinfo: %w", err)
	}
	return match, nil
}

func decodeMountInfoPath(value string) (string, error) {
	var decoded strings.Builder
	decoded.Grow(len(value))
	for index := 0; index < len(value); {
		if value[index] != '\\' {
			decoded.WriteByte(value[index])
			index++
			continue
		}
		if index+3 >= len(value) {
			return "", errors.New("invalid escaped path in mountinfo")
		}
		octal := value[index+1 : index+4]
		parsed, err := strconv.ParseUint(octal, 8, 8)
		if err != nil {
			return "", fmt.Errorf("invalid mountinfo escape %q: %w", octal, err)
		}
		decoded.WriteByte(byte(parsed))
		index += 4
	}
	return decoded.String(), nil
}

func isDOFSMount(info *linuxMountInfo) bool {
	return info != nil && info.FilesystemType == "fuse.dofs" && strings.HasPrefix(info.Source, "dofs:")
}

func probeMountedFilesystem(path string) (bool, error) {
	var stat unix.Statfs_t
	if err := unix.Statfs(path, &stat); err != nil {
		return false, err
	}
	return true, nil
}

func isDisconnectedMountError(err error) bool {
	return errors.Is(err, unix.ENOTCONN) || errors.Is(err, unix.ENODEV) || errors.Is(err, unix.EIO)
}

func unmountDisconnectedDOFS(path string) error {
	if info, err := inspectLinuxMount(path); err != nil {
		return err
	} else if info == nil {
		return nil
	} else if !isDOFSMount(info) {
		return fmt.Errorf("%w: refusing to unmount %s", ErrForeignMount, path)
	}

	directErr := unix.Unmount(path, 0)
	if directErr == nil {
		return nil
	}
	if info, inspectErr := inspectLinuxMount(path); inspectErr == nil && info == nil {
		return nil
	}
	for _, helper := range []string{"fusermount3", "fusermount"} {
		binary, lookupErr := exec.LookPath(helper)
		if lookupErr != nil {
			continue
		}
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		output, commandErr := exec.CommandContext(ctx, binary, "-u", path).CombinedOutput()
		cancel()
		if commandErr == nil {
			if info, inspectErr := inspectLinuxMount(path); inspectErr != nil {
				return inspectErr
			} else if info != nil {
				return fmt.Errorf("%s reported success but %s remains mounted", helper, path)
			}
			return nil
		}
		directErr = fmt.Errorf("%s: %w (%s)", helper, commandErr, strings.TrimSpace(string(output)))
	}
	return fmt.Errorf("unmount disconnected DOFS at %s without lazy detach: %w", path, directErr)
}
