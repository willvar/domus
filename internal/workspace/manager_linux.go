//go:build linux

package workspace

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"domus/internal/dofs"

	"github.com/google/uuid"
	"golang.org/x/sys/unix"
)

const (
	workspaceMarkerVersion = 1
	identityFilesVersion   = 1
)

type FilesystemService interface {
	Ensure(context.Context, dofs.MountUserSelector) (dofs.MountStatus, error)
	Status(context.Context, string) (dofs.MountStatus, error)
	Unmount(context.Context, string) (dofs.MountStatus, error)
	Health(context.Context, bool) (dofs.ManagerHealth, error)
}

type ManagerOptions struct {
	StateRoot          string
	ControlSocket      string
	SocketMode         os.FileMode
	SocketGID          int
	DOFSMountRoot      string
	Image              string
	PullPolicy         string
	ContainerPrefix    string
	UID                uint32
	GID                uint32
	NetworkMode        string
	ReadOnlyRootFS     bool
	MemoryBytes        int64
	MemorySwapBytes    int64
	NanoCPUs           int64
	PIDsLimit          int64
	TmpfsSizeBytes     int64
	ShmSizeBytes       int64
	MaxRunning         int
	MaxSessionsPerUser int
	IdleTimeout        time.Duration
	ReconcileInterval  time.Duration
	OperationTimeout   time.Duration
	ExecTimeout        time.Duration
	ExecOutputLimit    int64
	StopTimeout        time.Duration
	Shell              []string
	Keepalive          []string
	Logf               func(string, ...any)
	OnReady            func() error
}

type desiredMarker struct {
	Version    int       `json:"version"`
	UserID     string    `json:"user_id"`
	Username   string    `json:"username"`
	LastUsedAt time.Time `json:"last_used_at"`
}

type Manager struct {
	options     ManagerOptions
	filesystem  FilesystemService
	runtime     ContainerRuntime
	managerID   string
	desiredDir  string
	identityDir string
	lockFile    *os.File

	ctx    context.Context
	cancel context.CancelFunc
	wake   chan struct{}

	mu              sync.Mutex
	desired         map[string]desiredMarker
	statuses        map[string]Status
	operations      map[string]chan struct{}
	teardowns       map[string]chan struct{}
	pendingSessions map[string]map[*managedSession]struct{}
	sessions        map[string]map[*managedSession]struct{}
	closing         bool
	lastReconcileAt time.Time
	lastError       string
	operationWG     sync.WaitGroup
	sessionWG       sync.WaitGroup
	shutdownOnce    sync.Once
	shutdownErr     error
	closeOnce       sync.Once
	closeErr        error
	capacityMu      sync.Mutex

	imageMu         sync.Mutex
	resolvedImage   RuntimeImage
	imageResolvedAt time.Time
	markerMu        sync.Mutex
}

func NewManager(options ManagerOptions, filesystem FilesystemService, runtime ContainerRuntime) (*Manager, error) {
	if filesystem == nil {
		return nil, errors.New("workspace DOFS client is required")
	}
	if runtime == nil {
		return nil, errors.New("workspace container runtime is required")
	}
	if err := validateManagerOptions(&options); err != nil {
		return nil, err
	}
	if err := ensurePrivateDirectory(options.StateRoot); err != nil {
		return nil, err
	}
	desiredDir := filepath.Join(options.StateRoot, "desired")
	if err := ensurePrivateDirectory(desiredDir); err != nil {
		return nil, err
	}
	identityDir := filepath.Join(options.StateRoot, "identities")
	if err := ensurePrivateDirectory(identityDir); err != nil {
		return nil, err
	}
	lockFile, err := acquireManagerLock(filepath.Join(options.StateRoot, "manager.lock"))
	if err != nil {
		return nil, err
	}
	managerID, err := loadOrCreateManagerID(options.StateRoot)
	if err != nil {
		_ = releaseManagerLock(lockFile)
		return nil, err
	}
	ctx, cancel := context.WithCancel(context.Background())
	manager := &Manager{
		options: options, filesystem: filesystem, runtime: runtime,
		managerID: managerID, desiredDir: desiredDir, identityDir: identityDir, lockFile: lockFile,
		ctx: ctx, cancel: cancel, wake: make(chan struct{}, 1),
		desired: make(map[string]desiredMarker), statuses: make(map[string]Status),
		operations: make(map[string]chan struct{}), teardowns: make(map[string]chan struct{}),
		pendingSessions: make(map[string]map[*managedSession]struct{}),
		sessions:        make(map[string]map[*managedSession]struct{}),
	}
	if err := manager.loadDesiredMarkers(); err != nil {
		cancel()
		_ = releaseManagerLock(lockFile)
		return nil, err
	}
	return manager, nil
}

func validateManagerOptions(options *ManagerOptions) error {
	var err error
	options.StateRoot, err = validateHostPath("state root", options.StateRoot)
	if err != nil {
		return err
	}
	options.ControlSocket, err = validateHostPath("control socket", options.ControlSocket)
	if err != nil {
		return err
	}
	options.DOFSMountRoot, err = validateHostPath("DOFS mount root", options.DOFSMountRoot)
	if err != nil {
		return err
	}
	if pathsOverlap(options.StateRoot, options.DOFSMountRoot) {
		return errors.New("workspace state root and DOFS mount root must not overlap")
	}
	if options.ControlSocket == options.StateRoot || pathInside(options.StateRoot, options.ControlSocket) {
		return errors.New("workspace control socket must not be inside the state root")
	}
	if options.SocketMode == 0 {
		options.SocketMode = 0600
	}
	if options.SocketMode.Perm()&^os.FileMode(0660) != 0 {
		return errors.New("workspace control socket must not grant world or execute access")
	}
	if options.SocketGID < -1 {
		return errors.New("workspace control socket GID is invalid")
	}
	options.Image = strings.TrimSpace(options.Image)
	if options.Image == "" || !safeRuntimeName(options.ContainerPrefix) {
		return errors.New("workspace image and container prefix are required")
	}
	switch options.PullPolicy {
	case "never", "if_not_present", "always":
	default:
		return errors.New("invalid workspace image pull policy")
	}
	if options.UID == 0 || options.GID == 0 {
		return errors.New("workspace container identity must be non-root")
	}
	options.NetworkMode = strings.TrimSpace(options.NetworkMode)
	if !safeNetworkMode(options.NetworkMode) {
		return errors.New("workspace network mode must be none, bridge, or a named non-host network")
	}
	if !options.ReadOnlyRootFS {
		return errors.New("workspace container root filesystem must be read-only")
	}
	if options.MaxRunning <= 0 || options.MaxSessionsPerUser <= 0 || options.IdleTimeout <= 0 ||
		options.ReconcileInterval <= 0 || options.OperationTimeout <= 0 || options.ExecTimeout <= 0 ||
		options.ExecOutputLimit <= 0 {
		return errors.New("workspace limits and timeouts must be greater than zero")
	}
	if options.ExecOutputLimit > MaxExecOutputBytes {
		return fmt.Errorf("workspace exec output limit must not exceed %d bytes", MaxExecOutputBytes)
	}
	if options.StopTimeout < time.Second {
		return errors.New("workspace stop timeout must be at least one second")
	}
	if options.MemoryBytes <= 0 || options.MemorySwapBytes < options.MemoryBytes || options.NanoCPUs <= 0 ||
		options.PIDsLimit <= 0 || options.TmpfsSizeBytes <= 0 || options.ShmSizeBytes <= 0 {
		return errors.New("workspace resource limits are invalid")
	}
	if err := validateArgv(options.Shell); err != nil {
		return fmt.Errorf("workspace shell: %w", err)
	}
	if err := validateArgv(options.Keepalive); err != nil {
		return fmt.Errorf("workspace keepalive: %w", err)
	}
	return nil
}

func validateHostPath(name, value string) (string, error) {
	if strings.TrimSpace(value) == "" || !filepath.IsAbs(value) {
		return "", fmt.Errorf("workspace %s must be absolute", name)
	}
	clean := filepath.Clean(value)
	if clean == string(filepath.Separator) {
		return "", fmt.Errorf("workspace %s must not be the filesystem root", name)
	}
	return clean, nil
}

func pathInside(parent, child string) bool {
	relative, err := filepath.Rel(parent, child)
	return err == nil && relative != "." && relative != ".." && relative != "" && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}

func pathsOverlap(first, second string) bool {
	return first == second || pathInside(first, second) || pathInside(second, first)
}

func safeName(value string) bool {
	return ValidIdentityName(value)
}

func safeRuntimeName(value string) bool {
	return len(value) <= 63 && safeName(value)
}

func safeNetworkMode(value string) bool {
	if value == "none" || value == "bridge" {
		return true
	}
	if value == "" || value == "host" || strings.HasPrefix(value, "container:") || strings.HasPrefix(value, "service:") {
		return false
	}
	return safeRuntimeName(value)
}

func validIdentity(identity Identity) bool {
	return safeName(strings.TrimSpace(identity.UserID)) && safeName(strings.TrimSpace(identity.Username))
}

func normalizeIdentity(identity Identity) Identity {
	identity.UserID = strings.TrimSpace(identity.UserID)
	identity.Username = strings.TrimSpace(identity.Username)
	return identity
}

func ensurePrivateDirectory(directory string) error {
	if err := os.MkdirAll(directory, 0700); err != nil {
		return fmt.Errorf("create workspace directory %s: %w", directory, err)
	}
	info, err := os.Lstat(directory)
	if err != nil {
		return fmt.Errorf("inspect workspace directory %s: %w", directory, err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return fmt.Errorf("workspace path %s must be a real directory", directory)
	}
	resolved, err := filepath.EvalSymlinks(directory)
	if err != nil {
		return fmt.Errorf("resolve workspace directory %s: %w", directory, err)
	}
	if filepath.Clean(resolved) != filepath.Clean(directory) {
		return fmt.Errorf("workspace path %s must not contain symbolic-link components", directory)
	}
	if err := os.Chmod(directory, 0700); err != nil {
		return fmt.Errorf("protect workspace directory %s: %w", directory, err)
	}
	return nil
}

func acquireManagerLock(lockPath string) (*os.File, error) {
	file, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, fmt.Errorf("open workspace manager lock: %w", err)
	}
	if err := unix.Flock(int(file.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		_ = file.Close()
		return nil, fmt.Errorf("acquire workspace manager lock: %w", err)
	}
	return file, nil
}

func releaseManagerLock(file *os.File) error {
	if file == nil {
		return nil
	}
	unlockErr := unix.Flock(int(file.Fd()), unix.LOCK_UN)
	closeErr := file.Close()
	return errors.Join(unlockErr, closeErr)
}

func loadOrCreateManagerID(stateRoot string) (string, error) {
	identityPath := filepath.Join(stateRoot, "manager-id")
	data, err := os.ReadFile(identityPath)
	if err == nil {
		value := strings.TrimSpace(string(data))
		if _, parseErr := uuid.Parse(value); parseErr != nil {
			return "", errors.New("workspace manager-id file is invalid")
		}
		return value, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return "", fmt.Errorf("read workspace manager identity: %w", err)
	}
	value := uuid.NewString()
	file, err := os.OpenFile(identityPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return "", fmt.Errorf("create workspace manager identity: %w", err)
	}
	if _, err := io.WriteString(file, value+"\n"); err != nil {
		_ = file.Close()
		return "", fmt.Errorf("write workspace manager identity: %w", err)
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return "", fmt.Errorf("sync workspace manager identity: %w", err)
	}
	if err := file.Close(); err != nil {
		return "", fmt.Errorf("close workspace manager identity: %w", err)
	}
	return value, syncDirectory(stateRoot)
}

func syncDirectory(directory string) error {
	file, err := os.Open(directory)
	if err != nil {
		return err
	}
	defer func() { _ = file.Close() }()
	return file.Sync()
}

func (m *Manager) markerPath(userID string) string {
	return filepath.Join(m.desiredDir, userID+".json")
}

func (m *Manager) loadDesiredMarkers() error {
	entries, err := os.ReadDir(m.desiredDir)
	if err != nil {
		return fmt.Errorf("read workspace desired state: %w", err)
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		userID := strings.TrimSuffix(entry.Name(), ".json")
		if !safeName(userID) {
			return fmt.Errorf("invalid workspace desired marker %q", entry.Name())
		}
		marker, err := readMarker(m.markerPath(userID))
		if err != nil {
			return err
		}
		if marker.Version != workspaceMarkerVersion || marker.UserID != userID || !validIdentity(Identity{UserID: marker.UserID, Username: marker.Username}) {
			return fmt.Errorf("invalid workspace desired marker contents for %s", userID)
		}
		m.desired[userID] = marker
		m.statuses[userID] = Status{
			UserID: userID, Username: marker.Username, State: "pending", Desired: true,
			LastUsedAt: marker.LastUsedAt, UpdatedAt: time.Now().UTC(),
		}
	}
	return nil
}

func readMarker(markerPath string) (desiredMarker, error) {
	info, err := os.Lstat(markerPath)
	if err != nil {
		return desiredMarker{}, fmt.Errorf("inspect workspace marker: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() || info.Size() > 8192 {
		return desiredMarker{}, errors.New("workspace marker is not a protected regular file")
	}
	file, err := os.Open(markerPath)
	if err != nil {
		return desiredMarker{}, err
	}
	defer func() { _ = file.Close() }()
	var marker desiredMarker
	decoder := json.NewDecoder(file)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&marker); err != nil {
		return desiredMarker{}, fmt.Errorf("decode workspace marker: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return desiredMarker{}, errors.New("workspace marker contains trailing data")
	}
	return marker, nil
}

func (m *Manager) persistMarker(marker desiredMarker) error {
	temporary, err := os.CreateTemp(m.desiredDir, ".workspace-*.tmp")
	if err != nil {
		return fmt.Errorf("create workspace marker: %w", err)
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
		return err
	}
	if err := json.NewEncoder(temporary).Encode(marker); err != nil {
		return err
	}
	if err := temporary.Sync(); err != nil {
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	if err := os.Rename(temporaryPath, m.markerPath(marker.UserID)); err != nil {
		return fmt.Errorf("publish workspace marker: %w", err)
	}
	committed = true
	return syncDirectory(m.desiredDir)
}

func (m *Manager) removeMarker(userID string) error {
	err := os.Remove(m.markerPath(userID))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove workspace marker: %w", err)
	}
	return syncDirectory(m.desiredDir)
}

func (m *Manager) ensureIdentityFiles(identity Identity) (string, string, error) {
	directory := filepath.Join(m.identityDir, identity.UserID)
	if err := ensurePrivateDirectory(directory); err != nil {
		return "", "", err
	}
	home := path.Join("/workspace/home", identity.Username)
	passwd := fmt.Sprintf(
		"root:x:0:0:root:/root:/sbin/nologin\n%s:x:%d:%d:Domus workspace:%s:/bin/bash\n",
		identity.Username, m.options.UID, m.options.GID, home,
	)
	group := fmt.Sprintf("root:x:0:\n%s:x:%d:\n", identity.Username, m.options.GID)
	passwdPath := filepath.Join(directory, "passwd")
	groupPath := filepath.Join(directory, "group")
	if err := writeAtomicIdentityFile(directory, passwdPath, []byte(passwd)); err != nil {
		return "", "", fmt.Errorf("write workspace passwd file: %w", err)
	}
	if err := writeAtomicIdentityFile(directory, groupPath, []byte(group)); err != nil {
		return "", "", fmt.Errorf("write workspace group file: %w", err)
	}
	return passwdPath, groupPath, nil
}

func writeAtomicIdentityFile(directory, target string, contents []byte) error {
	temporary, err := os.CreateTemp(directory, ".identity-*.tmp")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	committed := false
	defer func() {
		_ = temporary.Close()
		if !committed {
			_ = os.Remove(temporaryPath)
		}
	}()
	if err := temporary.Chmod(0644); err != nil {
		return err
	}
	if _, err := temporary.Write(contents); err != nil {
		return err
	}
	if err := temporary.Sync(); err != nil {
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	if err := os.Rename(temporaryPath, target); err != nil {
		return err
	}
	committed = true
	return syncDirectory(directory)
}

func (m *Manager) removeIdentityFiles(userID string) error {
	directory := filepath.Join(m.identityDir, userID)
	info, err := os.Lstat(directory)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return errors.New("workspace identity path is not a real directory")
	}
	entries, err := os.ReadDir(directory)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		name := entry.Name()
		if name != "passwd" && name != "group" && !(strings.HasPrefix(name, ".identity-") && strings.HasSuffix(name, ".tmp")) {
			return fmt.Errorf("unexpected workspace identity file %q", name)
		}
		entryInfo, err := os.Lstat(filepath.Join(directory, name))
		if err != nil {
			return err
		}
		if entryInfo.Mode()&os.ModeSymlink != 0 || !entryInfo.Mode().IsRegular() {
			return fmt.Errorf("workspace identity file %q is not regular", name)
		}
		if err := os.Remove(filepath.Join(directory, name)); err != nil {
			return err
		}
	}
	if err := os.Remove(directory); err != nil {
		return err
	}
	return syncDirectory(m.identityDir)
}

func (m *Manager) beginOperation(ctx context.Context, userID string) (func(), error) {
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

func (m *Manager) operationContext(ctx context.Context) (context.Context, context.CancelFunc) {
	operationCtx, cancel := context.WithTimeout(ctx, m.options.OperationTimeout)
	stopManagerCancellation := context.AfterFunc(m.ctx, cancel)
	return operationCtx, func() {
		stopManagerCancellation()
		cancel()
	}
}

func (m *Manager) Ensure(ctx context.Context, identity Identity) (Status, error) {
	identity = normalizeIdentity(identity)
	if !validIdentity(identity) {
		return Status{}, fmt.Errorf("%w: invalid user identity", ErrInvalidRequest)
	}
	operationCtx, cancel := m.operationContext(ctx)
	defer cancel()
	finish, err := m.beginOperation(operationCtx, identity.UserID)
	if err != nil {
		return Status{}, err
	}
	defer finish()
	return m.ensureUser(operationCtx, identity, true)
}

func (m *Manager) ensureUser(ctx context.Context, identity Identity, touch bool) (Status, error) {
	now := time.Now().UTC()
	m.markerMu.Lock()
	m.mu.Lock()
	marker, desired := m.desired[identity.UserID]
	m.mu.Unlock()
	needsPersist := !desired
	if !desired {
		marker = desiredMarker{Version: workspaceMarkerVersion, UserID: identity.UserID, Username: identity.Username, LastUsedAt: now}
	} else {
		if marker.Username != identity.Username {
			m.markerMu.Unlock()
			return Status{}, fmt.Errorf("%w: persisted username does not match", ErrConflict)
		}
		if touch {
			marker.LastUsedAt = now
			needsPersist = true
		}
	}
	if marker.LastUsedAt.IsZero() {
		marker.LastUsedAt = now
		needsPersist = true
	}
	if needsPersist {
		if err := m.persistMarker(marker); err != nil {
			m.markerMu.Unlock()
			return Status{}, err
		}
	}
	m.mu.Lock()
	m.desired[identity.UserID] = marker
	m.mu.Unlock()
	m.markerMu.Unlock()

	mount, err := m.filesystem.Ensure(ctx, dofs.MountUserSelector{UserID: identity.UserID})
	if err != nil {
		return m.failStatus(identity, "failed", err)
	}
	if mount.UserID != identity.UserID || mount.Username != identity.Username {
		return m.failStatus(identity, "failed", fmt.Errorf("%w: DOFS identity mismatch", ErrConflict))
	}
	expectedMountpoint := filepath.Join(m.options.DOFSMountRoot, identity.UserID)
	if filepath.Clean(mount.Mountpoint) != expectedMountpoint {
		return m.failStatus(identity, "failed", fmt.Errorf("%w: DOFS returned unexpected mountpoint", ErrConflict))
	}
	if mount.State != "mounted" {
		status := Status{
			UserID: identity.UserID, Username: identity.Username, Mountpoint: expectedMountpoint,
			MountID: mount.MountID, State: "pending", Desired: true, LastUsedAt: marker.LastUsedAt,
			UpdatedAt: now, LastError: mount.LastError,
		}
		m.setStatus(status)
		return status, nil
	}
	if mount.MountID == "" || !mount.Desired || !mount.Writable || !mount.AllowOther ||
		mount.UID != m.options.UID || mount.GID != m.options.GID {
		return m.failStatus(identity, "failed", fmt.Errorf("%w: DOFS mount identity is unsafe", ErrConflict))
	}
	image, err := m.resolveImage(ctx)
	if err != nil {
		return m.failStatus(identity, "failed", err)
	}
	specHash, err := m.specHash(image.ID)
	if err != nil {
		return m.failStatus(identity, "failed", err)
	}
	passwdPath, groupPath, err := m.ensureIdentityFiles(identity)
	if err != nil {
		return m.failStatus(identity, "failed", err)
	}
	name := m.containerName(identity.UserID)
	container, found, err := m.runtime.FindByName(ctx, name)
	if err != nil {
		return m.failStatus(identity, "failed", err)
	}
	if found {
		if err := m.validateContainerOwner(container, identity.UserID); err != nil {
			return m.failStatus(identity, "failed", err)
		}
		if container.Labels[labelUsername] != identity.Username {
			return m.failStatus(identity, "failed", fmt.Errorf("%w: container username label does not match", ErrConflict))
		}
		if container.Labels[labelMountID] != mount.MountID || container.Labels[labelSpecHash] != specHash {
			if err := m.destroyContainer(ctx, container, false); err != nil {
				return m.failStatus(identity, "failed", err)
			}
			found = false
		}
	}
	if !found || !container.Running {
		container, err = m.ensureContainerRunning(ctx, identity, mount, image, specHash, passwdPath, groupPath, container, found)
		if errors.Is(err, ErrCapacity) {
			status := m.statusFromContainer(identity, mount, image, specHash, container, marker, "pending", err.Error())
			m.setStatus(status)
			return status, nil
		}
		if err != nil {
			return m.failStatus(identity, "failed", err)
		}
	}
	status := m.statusFromContainer(identity, mount, image, specHash, container, marker, "running", "")
	m.setStatus(status)
	m.logf("Workspace ready for user %s (%s) in container %s", identity.Username, identity.UserID, container.ID)
	return status, nil
}

// ensureContainerRunning serializes the capacity check with create/start.
// Per-user lifecycle locking prevents duplicate containers for one user;
// capacityMu prevents two different users from both observing the final free
// slot and exceeding MaxRunning.
func (m *Manager) ensureContainerRunning(ctx context.Context, identity Identity, mount dofs.MountStatus, image RuntimeImage, specHash, passwdPath, groupPath string, container RuntimeContainer, found bool) (RuntimeContainer, error) {
	m.capacityMu.Lock()
	defer m.capacityMu.Unlock()
	var err error
	if !found {
		if err = m.checkCapacity(ctx, ""); err != nil {
			return container, err
		}
		container, err = m.runtime.Create(ctx, m.containerSpec(identity, mount, image, specHash, passwdPath, groupPath))
		if err != nil {
			return container, fmt.Errorf("create workspace container: %w", err)
		}
	}
	if !container.Running {
		if err = m.checkCapacity(ctx, container.ID); err != nil {
			return container, err
		}
		if err = m.runtime.Start(ctx, container.ID); err != nil {
			return container, fmt.Errorf("start workspace container: %w", err)
		}
		container.Running = true
		container.State = "running"
	}
	return container, nil
}

func (m *Manager) resolveImage(ctx context.Context) (RuntimeImage, error) {
	m.imageMu.Lock()
	defer m.imageMu.Unlock()
	if m.resolvedImage.ID != "" && (m.options.PullPolicy != "always" || time.Since(m.imageResolvedAt) < m.options.ReconcileInterval) {
		return m.resolvedImage, nil
	}
	image, err := m.runtime.ResolveImage(ctx, m.options.Image, m.options.PullPolicy)
	if err != nil {
		return RuntimeImage{}, fmt.Errorf("resolve workspace image: %w", err)
	}
	if image.ID == "" {
		return RuntimeImage{}, errors.New("container runtime returned an empty workspace image ID")
	}
	m.resolvedImage = image
	m.imageResolvedAt = time.Now()
	return image, nil
}

// Preflight verifies every dependency needed to admit the first user and
// resolves the configured image before the service announces readiness.
// In particular, pull_policy=never fails startup when the reviewed image was
// not preloaded instead of deferring the surprise to the first terminal.
func (m *Manager) Preflight(ctx context.Context) error {
	if err := m.runtime.Ping(ctx); err != nil {
		return fmt.Errorf("workspace runtime is unavailable: %w", err)
	}
	if _, err := m.filesystem.Health(ctx, false); err != nil {
		return fmt.Errorf("DOFS is unavailable: %w", err)
	}
	_, err := m.resolveImage(ctx)
	return err
}

func (m *Manager) specHash(imageID string) (string, error) {
	spec := struct {
		ImageID, NetworkMode                         string
		UID, GID                                     uint32
		ReadOnlyRootFS                               bool
		MemoryBytes, MemorySwap, NanoCPUs, PIDsLimit int64
		TmpfsSize, ShmSize                           int64
		StopTimeout                                  time.Duration
		IdentityFilesVersion                         int
		Keepalive                                    []string
	}{
		ImageID: imageID, NetworkMode: m.options.NetworkMode, UID: m.options.UID, GID: m.options.GID,
		ReadOnlyRootFS: m.options.ReadOnlyRootFS, MemoryBytes: m.options.MemoryBytes,
		MemorySwap: m.options.MemorySwapBytes, NanoCPUs: m.options.NanoCPUs, PIDsLimit: m.options.PIDsLimit,
		TmpfsSize: m.options.TmpfsSizeBytes, ShmSize: m.options.ShmSizeBytes,
		StopTimeout:          m.options.StopTimeout,
		IdentityFilesVersion: identityFilesVersion,
		Keepalive:            append([]string(nil), m.options.Keepalive...),
	}
	encoded, err := json.Marshal(spec)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:]), nil
}

func (m *Manager) containerName(userID string) string {
	return m.options.ContainerPrefix + "-" + userID
}

func (m *Manager) containerSpec(identity Identity, mount dofs.MountStatus, image RuntimeImage, specHash, passwdPath, groupPath string) ContainerSpec {
	hostname := "domus-" + identity.UserID
	if len(hostname) > 63 {
		hostname = hostname[:63]
	}
	return ContainerSpec{
		Name: m.containerName(identity.UserID), Hostname: hostname, Image: image,
		UserID: identity.UserID, Username: identity.Username, ManagerID: m.managerID,
		Mountpoint: mount.Mountpoint, PasswdPath: passwdPath, GroupPath: groupPath,
		MountID: mount.MountID, SpecHash: specHash,
		UID: m.options.UID, GID: m.options.GID, NetworkMode: m.options.NetworkMode,
		ReadOnlyRootFS: m.options.ReadOnlyRootFS, MemoryBytes: m.options.MemoryBytes,
		MemorySwap: m.options.MemorySwapBytes, NanoCPUs: m.options.NanoCPUs, PIDsLimit: m.options.PIDsLimit,
		TmpfsSize: m.options.TmpfsSizeBytes, ShmSize: m.options.ShmSizeBytes,
		StopTimeout: int(m.options.StopTimeout / time.Second), Keepalive: append([]string(nil), m.options.Keepalive...),
	}
}

func (m *Manager) validateContainerOwner(container RuntimeContainer, userID string) error {
	if container.Labels[labelManaged] != "true" || container.Labels[labelManagerID] != m.managerID || container.Labels[labelUserID] != userID {
		return fmt.Errorf("%w: container name %s is not owned by this manager", ErrConflict, container.Name)
	}
	return nil
}

func (m *Manager) checkCapacity(ctx context.Context, excludeID string) error {
	containers, err := m.runtime.ListManaged(ctx)
	if err != nil {
		return err
	}
	running := 0
	for _, container := range containers {
		if container.ID != excludeID && container.Running && container.Labels[labelManagerID] == m.managerID {
			running++
		}
	}
	if running >= m.options.MaxRunning {
		return fmt.Errorf("%w (limit %d)", ErrCapacity, m.options.MaxRunning)
	}
	return nil
}

func (m *Manager) destroyContainer(ctx context.Context, container RuntimeContainer, closePending bool) error {
	m.closeUserSessions(container.Labels[labelUserID], closePending)
	if container.Running {
		if err := m.runtime.Stop(ctx, container.ID, int(m.options.StopTimeout/time.Second)); err != nil {
			return fmt.Errorf("stop workspace container: %w", err)
		}
	}
	if err := m.runtime.Remove(ctx, container.ID); err != nil {
		return fmt.Errorf("remove workspace container: %w", err)
	}
	return nil
}

func (m *Manager) statusFromContainer(identity Identity, mount dofs.MountStatus, image RuntimeImage, specHash string, container RuntimeContainer, marker desiredMarker, state, lastError string) Status {
	m.mu.Lock()
	active := m.userSessionCountLocked(identity.UserID)
	m.mu.Unlock()
	return Status{
		UserID: identity.UserID, Username: identity.Username, ContainerID: container.ID,
		Container: container.Name, Mountpoint: mount.Mountpoint, MountID: mount.MountID,
		ImageID: image.ID, SpecHash: specHash, State: state, Desired: true,
		ActiveExecs: active, LastUsedAt: marker.LastUsedAt, UpdatedAt: time.Now().UTC(), LastError: lastError,
	}
}

func (m *Manager) failStatus(identity Identity, state string, err error) (Status, error) {
	m.mu.Lock()
	marker := m.desired[identity.UserID]
	active := m.userSessionCountLocked(identity.UserID)
	m.mu.Unlock()
	status := Status{
		UserID: identity.UserID, Username: identity.Username, State: state, Desired: true,
		ActiveExecs: active, LastUsedAt: marker.LastUsedAt, UpdatedAt: time.Now().UTC(), LastError: err.Error(),
	}
	m.setStatus(status)
	return status, err
}

func (m *Manager) setStatus(status Status) {
	m.mu.Lock()
	m.statuses[status.UserID] = status
	m.mu.Unlock()
}

func (m *Manager) Status(_ context.Context, userID string) (Status, error) {
	if !safeName(userID) {
		return Status{}, fmt.Errorf("%w: invalid user ID", ErrInvalidRequest)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	status, ok := m.statuses[userID]
	if !ok {
		return Status{}, ErrNotManaged
	}
	status.ActiveExecs = m.userSessionCountLocked(userID)
	return status, nil
}

func (m *Manager) List(_ context.Context) ([]Status, error) {
	m.mu.Lock()
	statuses := make([]Status, 0, len(m.statuses))
	for userID, status := range m.statuses {
		status.ActiveExecs = m.userSessionCountLocked(userID)
		statuses = append(statuses, status)
	}
	m.mu.Unlock()
	sort.Slice(statuses, func(first, second int) bool { return statuses[first].UserID < statuses[second].UserID })
	return statuses, nil
}

func validateArgv(command []string) error {
	if len(command) == 0 || len(command) > 64 {
		return errors.New("command must contain between 1 and 64 arguments")
	}
	total := 0
	for _, argument := range command {
		if argument == "" || strings.IndexByte(argument, 0) >= 0 || len(argument) > 4096 {
			return errors.New("command contains an invalid argument")
		}
		total += len(argument)
	}
	if total > 64*1024 {
		return errors.New("command is too large")
	}
	return nil
}

func validateWorkingDirectory(value, username string) (string, error) {
	if strings.TrimSpace(value) == "" {
		return path.Join("/workspace/home", username), nil
	}
	if !path.IsAbs(value) || strings.IndexByte(value, 0) >= 0 {
		return "", errors.New("working directory must be an absolute container path")
	}
	clean := path.Clean(value)
	if clean != "/workspace" && !strings.HasPrefix(clean, "/workspace/") {
		return "", errors.New("working directory must be inside /workspace")
	}
	return clean, nil
}

func buildEnvironment(input map[string]string, identity Identity) ([]string, error) {
	if len(input) > 128 {
		return nil, errors.New("too many environment variables")
	}
	values := make(map[string]string, len(input)+4)
	total := 0
	for key, value := range input {
		if !validEnvironmentName(key) || strings.IndexByte(value, 0) >= 0 {
			return nil, errors.New("environment contains an invalid entry")
		}
		total += len(key) + len(value)
		if total > 64*1024 {
			return nil, errors.New("environment is too large")
		}
		values[key] = value
	}
	values["HOME"] = path.Join("/workspace/home", identity.Username)
	values["USER"] = identity.Username
	values["LOGNAME"] = identity.Username
	values["DOMUS_USER_ID"] = identity.UserID
	values["HISTFILE"] = path.Join("/workspace/home", identity.Username, ".bash_history")
	values["SHELL"] = "/bin/bash"
	values["TERM"] = "xterm-256color"
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	environment := make([]string, 0, len(keys))
	for _, key := range keys {
		environment = append(environment, key+"="+values[key])
	}
	return environment, nil
}

func validEnvironmentName(value string) bool {
	if value == "" || len(value) > 256 || !(value[0] == '_' || value[0] >= 'A' && value[0] <= 'Z' || value[0] >= 'a' && value[0] <= 'z') {
		return false
	}
	for index := 1; index < len(value); index++ {
		character := value[index]
		if character == '_' || character >= 'A' && character <= 'Z' || character >= 'a' && character <= 'z' || character >= '0' && character <= '9' {
			continue
		}
		return false
	}
	return true
}

func (m *Manager) userSessionCountLocked(userID string) int {
	return len(m.pendingSessions[userID]) + len(m.sessions[userID])
}

// reserveSession closes the idle-reap window before Ensure starts. A pending
// reservation counts against the per-user limit and keeps reconciliation from
// tearing down the workspace, but container replacement does not close it.
// Once Ensure succeeds, promoteSession atomically turns it into active work.
func (m *Manager) reserveSession(ctx context.Context, identity Identity) (*managedSession, error) {
	for {
		m.mu.Lock()
		if m.closing {
			m.mu.Unlock()
			return nil, ErrManagerStopping
		}
		userID := identity.UserID
		if teardownDone := m.teardowns[userID]; teardownDone != nil {
			m.mu.Unlock()
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-m.ctx.Done():
				return nil, ErrManagerStopping
			case <-teardownDone:
				continue
			}
		}
		if m.userSessionCountLocked(userID) >= m.options.MaxSessionsPerUser {
			m.mu.Unlock()
			return nil, ErrSessionLimit
		}
		session := &managedSession{manager: m, identity: identity}
		if m.pendingSessions[userID] == nil {
			m.pendingSessions[userID] = make(map[*managedSession]struct{})
		}
		m.pendingSessions[userID][session] = struct{}{}
		m.sessionWG.Add(1)
		if status, ok := m.statuses[userID]; ok {
			status.ActiveExecs = m.userSessionCountLocked(userID)
			status.UpdatedAt = time.Now().UTC()
			m.statuses[userID] = status
		}
		m.mu.Unlock()
		return session, nil
	}
}

func (m *Manager) promoteSession(session *managedSession) bool {
	if session == nil {
		return false
	}
	session.stateMu.Lock()
	defer session.stateMu.Unlock()
	if session.closed {
		return false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	userID := session.identity.UserID
	if m.closing {
		return false
	}
	if _, pending := m.pendingSessions[userID][session]; !pending {
		return false
	}
	delete(m.pendingSessions[userID], session)
	if len(m.pendingSessions[userID]) == 0 {
		delete(m.pendingSessions, userID)
	}
	if m.sessions[userID] == nil {
		m.sessions[userID] = make(map[*managedSession]struct{})
	}
	m.sessions[userID][session] = struct{}{}
	session.promoted = true
	return true
}

func (m *Manager) Exec(ctx context.Context, request ExecRequest) (ExecResult, error) {
	request.Identity = normalizeIdentity(request.Identity)
	if !validIdentity(request.Identity) {
		return ExecResult{}, fmt.Errorf("%w: invalid user identity", ErrInvalidRequest)
	}
	if err := validateArgv(request.Command); err != nil {
		return ExecResult{}, fmt.Errorf("%w: %v", ErrInvalidRequest, err)
	}
	workingDir, err := validateWorkingDirectory(request.WorkingDir, request.Username)
	if err != nil {
		return ExecResult{}, fmt.Errorf("%w: %v", ErrInvalidRequest, err)
	}
	environment, err := buildEnvironment(request.Environment, request.Identity)
	if err != nil {
		return ExecResult{}, fmt.Errorf("%w: %v", ErrInvalidRequest, err)
	}
	if len(request.Stdin) > 1024*1024 {
		return ExecResult{}, fmt.Errorf("%w: stdin exceeds 1 MiB", ErrInvalidRequest)
	}
	if request.TimeoutSeconds < 0 {
		return ExecResult{}, fmt.Errorf("%w: timeout must not be negative", ErrInvalidRequest)
	}
	placeholder, err := m.reserveSession(ctx, request.Identity)
	if err != nil {
		return ExecResult{}, err
	}
	defer placeholder.release()
	status, err := m.Ensure(ctx, request.Identity)
	if err != nil {
		return ExecResult{}, err
	}
	if status.State != "running" {
		return ExecResult{}, fmt.Errorf("%w: workspace is %s", ErrUnavailable, status.State)
	}
	timeout := m.options.ExecTimeout
	if request.TimeoutSeconds > 0 {
		requestedSeconds := int64(request.TimeoutSeconds)
		if requestedSeconds <= int64(timeout/time.Second) {
			timeout = time.Duration(requestedSeconds) * time.Second
		}
	}
	execCtx, cancel := context.WithTimeout(ctx, timeout)
	stopManagerCancellation := context.AfterFunc(m.ctx, cancel)
	if !placeholder.attachCancel(cancel) {
		stopManagerCancellation()
		cancel()
		placeholder.release()
		return ExecResult{}, ErrManagerStopping
	}
	if !m.promoteSession(placeholder) {
		stopManagerCancellation()
		cancel()
		return ExecResult{}, ErrManagerStopping
	}
	defer func() {
		stopManagerCancellation()
		cancel()
	}()
	result, err := m.runtime.Exec(execCtx, RuntimeExecRequest{
		ContainerID: status.ContainerID, Command: append([]string(nil), request.Command...),
		WorkingDir: workingDir, Environment: environment, UID: m.options.UID, GID: m.options.GID,
		Stdin: append([]byte(nil), request.Stdin...), OutputLimit: m.options.ExecOutputLimit,
	})
	m.touch(request.Identity)
	return result, err
}

func (m *Manager) OpenSession(ctx context.Context, request SessionRequest) (Session, error) {
	request.Identity = normalizeIdentity(request.Identity)
	if !validIdentity(request.Identity) {
		return nil, fmt.Errorf("%w: invalid user identity", ErrInvalidRequest)
	}
	command := request.Command
	if len(command) == 0 {
		command = m.options.Shell
	}
	if err := validateArgv(command); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidRequest, err)
	}
	workingDir, err := validateWorkingDirectory(request.WorkingDir, request.Username)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidRequest, err)
	}
	environment, err := buildEnvironment(request.Environment, request.Identity)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidRequest, err)
	}
	if request.Columns == 0 {
		request.Columns = 80
	}
	if request.Rows == 0 {
		request.Rows = 24
	}
	if request.Columns > 1000 || request.Rows > 1000 {
		return nil, fmt.Errorf("%w: terminal dimensions are too large", ErrInvalidRequest)
	}
	session, err := m.reserveSession(ctx, request.Identity)
	if err != nil {
		return nil, err
	}
	status, err := m.Ensure(ctx, request.Identity)
	if err != nil {
		session.release()
		return nil, err
	}
	if status.State != "running" {
		session.release()
		return nil, fmt.Errorf("%w: workspace is %s", ErrUnavailable, status.State)
	}
	openContext, cancelOpen := context.WithCancel(ctx)
	if !session.attachCancel(cancelOpen) {
		cancelOpen()
		session.release()
		return nil, ErrManagerStopping
	}
	if !m.promoteSession(session) {
		cancelOpen()
		session.release()
		return nil, ErrManagerStopping
	}
	stopManagerCancellation := context.AfterFunc(m.ctx, cancelOpen)
	process, err := m.runtime.OpenSession(openContext, RuntimeSessionRequest{
		ContainerID: status.ContainerID, Command: append([]string(nil), command...), WorkingDir: workingDir,
		Environment: environment, UID: m.options.UID, GID: m.options.GID,
		Columns: request.Columns, Rows: request.Rows,
	})
	stopManagerCancellation()
	cancelOpen()
	if err != nil {
		session.release()
		return nil, err
	}
	if !session.attachProcess(process) {
		_ = process.Close()
		session.release()
		return nil, ErrManagerStopping
	}
	m.touch(request.Identity)
	return session, nil
}

type managedSession struct {
	manager  *Manager
	identity Identity
	stateMu  sync.Mutex
	process  RuntimeProcess
	cancel   context.CancelFunc
	promoted bool
	closed   bool
	once     sync.Once
}

func (s *managedSession) attachCancel(cancel context.CancelFunc) bool {
	s.stateMu.Lock()
	defer s.stateMu.Unlock()
	if s.closed {
		return false
	}
	s.cancel = cancel
	return true
}

func (s *managedSession) attachProcess(process RuntimeProcess) bool {
	s.stateMu.Lock()
	defer s.stateMu.Unlock()
	if s.closed {
		return false
	}
	s.process = process
	return true
}

func (s *managedSession) currentProcess() RuntimeProcess {
	s.stateMu.Lock()
	defer s.stateMu.Unlock()
	return s.process
}

func (s *managedSession) Read(buffer []byte) (int, error) {
	process := s.currentProcess()
	if process == nil {
		return 0, io.EOF
	}
	return process.Read(buffer)
}

func (s *managedSession) Write(buffer []byte) (int, error) {
	process := s.currentProcess()
	if process == nil {
		return 0, io.ErrClosedPipe
	}
	return process.Write(buffer)
}

func (s *managedSession) Resize(ctx context.Context, columns, rows uint) error {
	if columns == 0 || rows == 0 || columns > 1000 || rows > 1000 {
		return fmt.Errorf("%w: invalid terminal dimensions", ErrInvalidRequest)
	}
	process := s.currentProcess()
	if process == nil {
		return io.ErrClosedPipe
	}
	return process.Resize(ctx, columns, rows)
}

func (s *managedSession) Wait() (int, error) {
	process := s.currentProcess()
	if process == nil {
		s.release()
		return -1, io.ErrClosedPipe
	}
	exitCode, err := process.Wait()
	s.release()
	return exitCode, err
}

func (s *managedSession) Close() error {
	var err error
	s.stateMu.Lock()
	s.closed = true
	process := s.process
	cancel := s.cancel
	promoted := s.promoted
	s.stateMu.Unlock()
	if cancel != nil {
		cancel()
	}
	if process != nil {
		err = process.Close()
		s.release()
	} else if cancel == nil || !promoted {
		// A pending reservation has no runtime work to wait for. A promoted
		// non-interactive Exec releases itself only after cancellation returns.
		s.release()
	}
	if err != nil && s.manager != nil {
		s.manager.logf("Close workspace session for user %s: %v", s.identity.UserID, err)
	}
	return err
}

func (s *managedSession) release() {
	s.once.Do(func() {
		if s.manager != nil {
			s.manager.removeSession(s)
		}
	})
}
func (m *Manager) removeSession(session *managedSession) {
	userID := session.identity.UserID
	now := time.Now().UTC()
	m.markerMu.Lock()
	m.mu.Lock()
	delete(m.pendingSessions[userID], session)
	if len(m.pendingSessions[userID]) == 0 {
		delete(m.pendingSessions, userID)
	}
	delete(m.sessions[userID], session)
	if len(m.sessions[userID]) == 0 {
		delete(m.sessions, userID)
	}
	marker, desired := m.desired[userID]
	if desired {
		marker.LastUsedAt = now
		m.desired[userID] = marker
	}
	if status, ok := m.statuses[userID]; ok {
		status.ActiveExecs = m.userSessionCountLocked(userID)
		status.LastUsedAt = now
		status.UpdatedAt = now
		m.statuses[userID] = status
	}
	m.mu.Unlock()
	if desired {
		if err := m.persistMarker(marker); err != nil {
			m.logf("Persist workspace activity for user %s: %v", userID, err)
		}
	}
	m.markerMu.Unlock()
	m.sessionWG.Done()
}

func (m *Manager) closeUserSessions(userID string, includePending bool) {
	m.mu.Lock()
	sessions := make([]*managedSession, 0, m.userSessionCountLocked(userID))
	for session := range m.sessions[userID] {
		sessions = append(sessions, session)
	}
	if includePending {
		for session := range m.pendingSessions[userID] {
			sessions = append(sessions, session)
		}
	}
	m.mu.Unlock()
	closeManagedSessions(sessions)
}

func (m *Manager) closeAllSessions() {
	m.mu.Lock()
	sessions := make([]*managedSession, 0)
	for _, userSessions := range m.pendingSessions {
		for session := range userSessions {
			sessions = append(sessions, session)
		}
	}
	for _, userSessions := range m.sessions {
		for session := range userSessions {
			sessions = append(sessions, session)
		}
	}
	m.mu.Unlock()
	closeManagedSessions(sessions)
}

func closeManagedSessions(sessions []*managedSession) {
	var wait sync.WaitGroup
	wait.Add(len(sessions))
	for _, session := range sessions {
		go func() {
			defer wait.Done()
			_ = session.Close()
		}()
	}
	wait.Wait()
}

func (m *Manager) touch(identity Identity) {
	now := time.Now().UTC()
	m.markerMu.Lock()
	m.mu.Lock()
	marker, ok := m.desired[identity.UserID]
	if ok {
		marker.LastUsedAt = now
		m.desired[identity.UserID] = marker
	}
	if status, exists := m.statuses[identity.UserID]; exists {
		status.LastUsedAt = now
		status.UpdatedAt = now
		m.statuses[identity.UserID] = status
	}
	m.mu.Unlock()
	if ok {
		if err := m.persistMarker(marker); err != nil {
			m.logf("Persist workspace activity for user %s: %v", identity.UserID, err)
		}
	}
	m.markerMu.Unlock()
}

func (m *Manager) Stop(ctx context.Context, userID string) (Status, error) {
	return m.teardown(ctx, userID, false)
}

func (m *Manager) Remove(ctx context.Context, userID string) (Status, error) {
	return m.teardown(ctx, userID, true)
}

func (m *Manager) teardown(ctx context.Context, userID string, removeStatus bool) (Status, error) {
	if !safeName(userID) {
		return Status{}, fmt.Errorf("%w: invalid user ID", ErrInvalidRequest)
	}
	operationCtx, cancel := m.operationContext(ctx)
	defer cancel()
	finish, err := m.beginOperation(operationCtx, userID)
	if err != nil {
		return Status{}, err
	}
	defer finish()
	endTeardown, err := m.beginTeardown(userID)
	if err != nil {
		return Status{}, err
	}
	defer endTeardown()
	return m.teardownUser(operationCtx, userID, true, removeStatus)
}

func (m *Manager) beginTeardown(userID string) (func(), error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closing {
		return nil, ErrManagerStopping
	}
	if m.teardowns[userID] != nil {
		return nil, fmt.Errorf("%w: workspace teardown is already active", ErrConflict)
	}
	done := make(chan struct{})
	m.teardowns[userID] = done
	var once sync.Once
	return func() {
		once.Do(func() {
			m.mu.Lock()
			if m.teardowns[userID] == done {
				delete(m.teardowns, userID)
				close(done)
			}
			m.mu.Unlock()
		})
	}, nil
}

func (m *Manager) teardownUser(ctx context.Context, userID string, forget, removeStatus bool) (Status, error) {
	name := m.containerName(userID)
	container, found, err := m.runtime.FindByName(ctx, name)
	if err != nil {
		return Status{}, err
	}
	if found {
		if err := m.validateContainerOwner(container, userID); err != nil {
			return Status{}, err
		}
		if err := m.destroyContainer(ctx, container, true); err != nil {
			return Status{}, err
		}
	}
	if _, err := m.filesystem.Unmount(ctx, userID); err != nil {
		var apiError *dofs.ControlAPIError
		if !errors.As(err, &apiError) || apiError.StatusCode != 404 {
			return Status{}, fmt.Errorf("unmount workspace DOFS: %w", err)
		}
	}
	// Forget the desired state last. If Docker removal or DOFS unmounting fails,
	// reconciliation retains enough state to retry instead of leaking an
	// untracked plaintext mount.
	if forget {
		m.markerMu.Lock()
		if removeStatus {
			if err := m.removeIdentityFiles(userID); err != nil {
				m.markerMu.Unlock()
				return Status{}, fmt.Errorf("remove workspace identity files: %w", err)
			}
		}
		if err := m.removeMarker(userID); err != nil {
			m.markerMu.Unlock()
			return Status{}, err
		}
		m.mu.Lock()
		delete(m.desired, userID)
		m.mu.Unlock()
		m.markerMu.Unlock()
	}
	status := Status{UserID: userID, State: "stopped", Desired: false, UpdatedAt: time.Now().UTC()}
	m.mu.Lock()
	if previous, ok := m.statuses[userID]; ok {
		status.Username = previous.Username
		status.LastUsedAt = previous.LastUsedAt
	}
	if removeStatus {
		delete(m.statuses, userID)
	} else {
		m.statuses[userID] = status
	}
	m.mu.Unlock()
	m.logf("Workspace stopped for user %s", userID)
	return status, nil
}

func (m *Manager) ReconcileOnce(ctx context.Context) error {
	if err := m.runtime.Ping(ctx); err != nil {
		m.recordReconcile(err)
		return fmt.Errorf("workspace runtime is unavailable: %w", err)
	}
	if _, err := m.filesystem.Health(ctx, false); err != nil {
		m.recordReconcile(err)
		return fmt.Errorf("DOFS is unavailable: %w", err)
	}
	m.mu.Lock()
	markers := make([]desiredMarker, 0, len(m.desired))
	for _, marker := range m.desired {
		markers = append(markers, marker)
	}
	m.mu.Unlock()
	sort.Slice(markers, func(first, second int) bool { return markers[first].UserID < markers[second].UserID })
	var reconcileErrors []error
	now := time.Now().UTC()
	for _, marker := range markers {
		m.mu.Lock()
		active := m.userSessionCountLocked(marker.UserID)
		m.mu.Unlock()
		if active == 0 && now.Sub(marker.LastUsedAt) >= m.options.IdleTimeout {
			operationCtx, cancel := context.WithTimeout(ctx, m.options.OperationTimeout)
			finish, err := m.beginOperation(operationCtx, marker.UserID)
			if err == nil {
				var endTeardown func()
				endTeardown, err = m.beginTeardown(marker.UserID)
				if err == nil {
					// The initial candidate came from a stale snapshot. Once the
					// teardown barrier is visible, re-read activity and LastUsedAt;
					// pending reservations created before the barrier abort the reap,
					// while new reservations wait until this decision completes.
					m.mu.Lock()
					current, desired := m.desired[marker.UserID]
					stillIdle := desired && m.userSessionCountLocked(marker.UserID) == 0 &&
						time.Since(current.LastUsedAt) >= m.options.IdleTimeout
					m.mu.Unlock()
					if stillIdle {
						_, err = m.teardownUser(operationCtx, marker.UserID, true, false)
					}
					endTeardown()
				}
				finish()
			}
			cancel()
			if err != nil {
				reconcileErrors = append(reconcileErrors, fmt.Errorf("reap idle workspace %s: %w", marker.UserID, err))
			}
			continue
		}
		operationCtx, cancel := context.WithTimeout(ctx, m.options.OperationTimeout)
		finish, err := m.beginOperation(operationCtx, marker.UserID)
		if err == nil {
			m.mu.Lock()
			_, stillDesired := m.desired[marker.UserID]
			m.mu.Unlock()
			if stillDesired {
				_, err = m.ensureUser(operationCtx, Identity{UserID: marker.UserID, Username: marker.Username}, false)
			}
			finish()
		}
		cancel()
		if err != nil {
			reconcileErrors = append(reconcileErrors, fmt.Errorf("reconcile workspace %s: %w", marker.UserID, err))
		}
	}
	if err := m.reconcileOrphanContainers(ctx); err != nil {
		reconcileErrors = append(reconcileErrors, err)
	}
	err := errors.Join(reconcileErrors...)
	m.recordReconcile(err)
	return err
}

// reconcileOrphanContainers removes only containers carrying this manager's
// persistent identity. It handles crashes between marker deletion and Docker
// cleanup, as well as container-prefix changes, without touching containers
// owned by another manager.
func (m *Manager) reconcileOrphanContainers(ctx context.Context) error {
	containers, err := m.runtime.ListManaged(ctx)
	if err != nil {
		return fmt.Errorf("list managed workspace containers: %w", err)
	}
	var cleanupErrors []error
	byUser := make(map[string][]RuntimeContainer)
	for _, candidate := range containers {
		if candidate.Labels[labelManagerID] != m.managerID {
			continue
		}
		userID := candidate.Labels[labelUserID]
		if !safeName(userID) || candidate.ID == "" {
			cleanupErrors = append(cleanupErrors, fmt.Errorf("%w: owned container %s has invalid identity labels", ErrConflict, candidate.ID))
			continue
		}
		byUser[userID] = append(byUser[userID], candidate)
	}
	userIDs := make([]string, 0, len(byUser))
	for userID := range byUser {
		userIDs = append(userIDs, userID)
	}
	sort.Strings(userIDs)
	for _, userID := range userIDs {
		m.mu.Lock()
		_, desired := m.desired[userID]
		m.mu.Unlock()
		needsCleanup := false
		for _, candidate := range byUser[userID] {
			if !desired || candidate.Name != m.containerName(userID) {
				needsCleanup = true
				break
			}
		}
		if !needsCleanup {
			continue
		}
		operationCtx, cancel := m.operationContext(ctx)
		finish, operationErr := m.beginOperation(operationCtx, userID)
		if operationErr == nil {
			m.mu.Lock()
			_, stillDesired := m.desired[userID]
			m.mu.Unlock()
			allRemoved := true
			for _, candidate := range byUser[userID] {
				if stillDesired && candidate.Name == m.containerName(userID) {
					continue
				}
				if candidate.Running {
					if err := m.runtime.Stop(operationCtx, candidate.ID, int(m.options.StopTimeout/time.Second)); err != nil {
						operationErr = errors.Join(operationErr, fmt.Errorf("stop %s: %w", candidate.ID, err))
						allRemoved = false
						continue
					}
				}
				if err := m.runtime.Remove(operationCtx, candidate.ID); err != nil {
					operationErr = errors.Join(operationErr, fmt.Errorf("remove %s: %w", candidate.ID, err))
					allRemoved = false
				}
			}
			// A user may have multiple leftovers after a prefix change. Never
			// detach DOFS until every exact-owned container for that user has
			// been removed.
			if allRemoved && !stillDesired {
				_, unmountErr := m.filesystem.Unmount(operationCtx, userID)
				if unmountErr != nil {
					var apiError *dofs.ControlAPIError
					if !errors.As(unmountErr, &apiError) || apiError.StatusCode != 404 {
						operationErr = errors.Join(operationErr, unmountErr)
					}
				}
			}
			finish()
		}
		cancel()
		if operationErr != nil {
			cleanupErrors = append(cleanupErrors, fmt.Errorf("clean orphan workspace containers for %s: %w", userID, operationErr))
		}
	}
	return errors.Join(cleanupErrors...)
}

func (m *Manager) recordReconcile(err error) {
	m.mu.Lock()
	m.lastReconcileAt = time.Now().UTC()
	if err != nil {
		m.lastError = err.Error()
	} else {
		m.lastError = ""
	}
	m.mu.Unlock()
}

func (m *Manager) RunReconciler(ctx context.Context) {
	_ = m.ReconcileOnce(ctx)
	ticker := time.NewTicker(m.options.ReconcileInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-m.wake:
			_ = m.ReconcileOnce(ctx)
		case <-ticker.C:
			_ = m.ReconcileOnce(ctx)
		}
	}
}

func (m *Manager) Health(ctx context.Context, ready bool) (Health, error) {
	m.mu.Lock()
	health := Health{
		Status: "ready", ProtocolVersion: ControlProtocolVersion, MaxRunning: m.options.MaxRunning,
		Desired: len(m.desired), LastReconcileAt: m.lastReconcileAt, LastError: m.lastError,
	}
	for _, status := range m.statuses {
		if status.State == "running" {
			health.Running++
		}
		if status.Desired && status.State != "running" {
			health.Degraded++
		}
	}
	activeUsers := make(map[string]struct{}, len(m.pendingSessions)+len(m.sessions))
	for userID := range m.pendingSessions {
		activeUsers[userID] = struct{}{}
	}
	for userID := range m.sessions {
		activeUsers[userID] = struct{}{}
	}
	for userID := range activeUsers {
		health.ActiveExecs += m.userSessionCountLocked(userID)
	}
	closing := m.closing
	m.mu.Unlock()
	if !ready {
		health.Status = "live"
		return health, nil
	}
	if closing {
		health.Status = "stopping"
		return health, ErrManagerStopping
	}
	if err := m.runtime.Ping(ctx); err != nil {
		health.Status = "unavailable"
		health.LastError = err.Error()
		return health, err
	}
	filesystemHealth, filesystemErr := m.filesystem.Health(ctx, true)
	if filesystemErr != nil {
		health.LastError = filesystemErr.Error()
		if filesystemHealth.Status == "degraded" {
			health.Status = "degraded"
			health.Degraded += filesystemHealth.Degraded
			return health, filesystemErr
		}
		health.Status = "unavailable"
		return health, filesystemErr
	}
	if health.Degraded > 0 {
		health.Status = "degraded"
		return health, errors.New("one or more desired workspaces are degraded")
	}
	return health, nil
}

func (m *Manager) Shutdown(ctx context.Context) error {
	m.shutdownOnce.Do(func() {
		m.mu.Lock()
		m.closing = true
		cleanupUsers := make(map[string]struct{}, len(m.desired))
		for userID := range m.desired {
			cleanupUsers[userID] = struct{}{}
		}
		m.mu.Unlock()
		m.cancel()
		m.closeAllSessions()
		waitDone := make(chan struct{})
		go func() {
			m.operationWG.Wait()
			m.sessionWG.Wait()
			close(waitDone)
		}()
		select {
		case <-ctx.Done():
			m.shutdownErr = errors.Join(m.shutdownErr, ctx.Err())
			return
		case <-waitDone:
		}
		containerFailures := make(map[string]error)
		containers, err := m.runtime.ListManaged(ctx)
		if err != nil {
			m.shutdownErr = errors.Join(m.shutdownErr, fmt.Errorf("list workspace containers during shutdown: %w", err))
			return
		}
		for _, container := range containers {
			if container.Labels[labelManagerID] != m.managerID {
				continue
			}
			userID := container.Labels[labelUserID]
			if !safeName(userID) || container.ID == "" {
				m.shutdownErr = errors.Join(m.shutdownErr, fmt.Errorf("%w: owned container %s has invalid identity labels", ErrConflict, container.ID))
				continue
			}
			cleanupUsers[userID] = struct{}{}
			var containerErr error
			if container.Running {
				containerErr = m.runtime.Stop(ctx, container.ID, int(m.options.StopTimeout/time.Second))
			}
			if containerErr == nil {
				containerErr = m.runtime.Remove(ctx, container.ID)
			}
			if containerErr != nil {
				containerFailures[userID] = errors.Join(containerFailures[userID], containerErr)
				m.shutdownErr = errors.Join(m.shutdownErr, fmt.Errorf("remove workspace container %s: %w", container.ID, containerErr))
			}
		}
		userIDs := make([]string, 0, len(cleanupUsers))
		for userID := range cleanupUsers {
			userIDs = append(userIDs, userID)
		}
		sort.Strings(userIDs)
		for _, userID := range userIDs {
			if containerFailures[userID] == nil {
				_, unmountErr := m.filesystem.Unmount(ctx, userID)
				if unmountErr != nil {
					var apiError *dofs.ControlAPIError
					if !errors.As(unmountErr, &apiError) || apiError.StatusCode != 404 {
						m.shutdownErr = errors.Join(m.shutdownErr, fmt.Errorf("unmount workspace DOFS for %s: %w", userID, unmountErr))
					}
				}
			}
		}
	})
	return m.shutdownErr
}

func (m *Manager) Close() error {
	m.closeOnce.Do(func() {
		m.cancel()
		m.closeErr = errors.Join(m.runtime.Close(), releaseManagerLock(m.lockFile))
	})
	return m.closeErr
}

func (m *Manager) logf(format string, values ...any) {
	if m.options.Logf != nil {
		m.options.Logf(format, values...)
	}
}

func (m *Manager) ManagerID() string {
	return m.managerID
}
