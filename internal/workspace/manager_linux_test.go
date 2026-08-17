//go:build linux

package workspace

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"domus/internal/dofs"
)

type fakeFilesystem struct {
	mu         sync.Mutex
	mounts     map[string]dofs.MountStatus
	unmounted  []string
	health     dofs.ManagerHealth
	healthErr  error
	unmountErr error
}

func (f *fakeFilesystem) Ensure(_ context.Context, selector dofs.MountUserSelector) (dofs.MountStatus, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	mount, ok := f.mounts[selector.UserID]
	if !ok {
		return dofs.MountStatus{}, dofs.ErrUserNotFound
	}
	return mount, nil
}

func (f *fakeFilesystem) Status(_ context.Context, userID string) (dofs.MountStatus, error) {
	return f.Ensure(context.Background(), dofs.MountUserSelector{UserID: userID})
}

func (f *fakeFilesystem) Unmount(_ context.Context, userID string) (dofs.MountStatus, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.unmountErr != nil {
		return dofs.MountStatus{}, f.unmountErr
	}
	f.unmounted = append(f.unmounted, userID)
	mount, ok := f.mounts[userID]
	if !ok {
		return dofs.MountStatus{}, &dofs.ControlAPIError{StatusCode: 404, Code: "not_managed", Message: "not managed"}
	}
	mount.State = "unmounted"
	mount.Desired = false
	return mount, nil
}

func (f *fakeFilesystem) Health(context.Context, bool) (dofs.ManagerHealth, error) {
	if f.health.Status != "" {
		return f.health, f.healthErr
	}
	if f.healthErr != nil {
		return dofs.ManagerHealth{}, f.healthErr
	}
	return dofs.ManagerHealth{Status: "ready"}, nil
}

func TestManagerHealthPreservesDOFSDegradedState(t *testing.T) {
	manager, filesystem, _, _ := newTestManager(t, nil)
	filesystem.health = dofs.ManagerHealth{Status: "degraded", Degraded: 2}
	filesystem.healthErr = errors.New("two desired mounts are degraded")
	health, err := manager.Health(t.Context(), true)
	if err == nil || health.Status != "degraded" || health.Degraded != 2 {
		t.Fatalf("Health() = %+v, %v", health, err)
	}
}

func TestManagerHealthTreatsPendingDesiredWorkspaceAsDegraded(t *testing.T) {
	manager, _, _, identity := newTestManager(t, nil)
	manager.mu.Lock()
	manager.desired[identity.UserID] = desiredMarker{
		Version: workspaceMarkerVersion, UserID: identity.UserID, Username: identity.Username, LastUsedAt: time.Now().UTC(),
	}
	manager.statuses[identity.UserID] = Status{
		UserID: identity.UserID, Username: identity.Username, State: "pending", Desired: true,
	}
	manager.mu.Unlock()
	health, err := manager.Health(t.Context(), true)
	if err == nil || health.Status != "degraded" || health.Degraded != 1 {
		t.Fatalf("Health() = %+v, %v", health, err)
	}
}

type fakeRuntime struct {
	mu             sync.Mutex
	containers     map[string]RuntimeContainer
	created        []ContainerSpec
	started        []string
	stopped        []string
	removed        []string
	pingErr        error
	resolveErr     error
	resolveCalls   int
	execRequest    RuntimeExecRequest
	execFunc       func(context.Context, RuntimeExecRequest) (ExecResult, error)
	sessionRequest RuntimeSessionRequest
	sessionFunc    func(context.Context, RuntimeSessionRequest) (RuntimeProcess, error)
	closed         bool
}

func newFakeRuntime() *fakeRuntime {
	return &fakeRuntime{containers: make(map[string]RuntimeContainer)}
}

func (r *fakeRuntime) Ping(context.Context) error { return r.pingErr }

func (r *fakeRuntime) ResolveImage(context.Context, string, string) (RuntimeImage, error) {
	r.mu.Lock()
	r.resolveCalls++
	r.mu.Unlock()
	if r.resolveErr != nil {
		return RuntimeImage{}, r.resolveErr
	}
	return RuntimeImage{ID: "sha256:image", Reference: "test:latest"}, nil
}

func TestManagerPreflightResolvesImageBeforeAdmission(t *testing.T) {
	manager, _, runtime, _ := newTestManager(t, nil)
	if err := manager.Preflight(t.Context()); err != nil {
		t.Fatalf("Preflight() error = %v", err)
	}
	runtime.mu.Lock()
	resolveCalls := runtime.resolveCalls
	created := len(runtime.created)
	runtime.mu.Unlock()
	if resolveCalls != 1 || created != 0 {
		t.Fatalf("preflight resolve/create counts = %d/%d, want 1/0", resolveCalls, created)
	}
}

func TestManagerPreflightFailsWhenReviewedImageIsUnavailable(t *testing.T) {
	manager, _, runtime, _ := newTestManager(t, nil)
	runtime.resolveErr = errors.New("image is not preloaded")
	err := manager.Preflight(t.Context())
	if err == nil || !strings.Contains(err.Error(), "resolve workspace image: image is not preloaded") {
		t.Fatalf("Preflight() error = %v", err)
	}
	runtime.mu.Lock()
	created := len(runtime.created)
	runtime.mu.Unlock()
	if created != 0 {
		t.Fatalf("failed preflight created %d containers", created)
	}
}

func TestOpenSessionReleasesReservationWhenClosedDuringRuntimeAttach(t *testing.T) {
	manager, _, runtime, identity := newTestManager(t, nil)
	attachStarted := make(chan struct{})
	allowAttach := make(chan struct{})
	runtime.sessionFunc = func(context.Context, RuntimeSessionRequest) (RuntimeProcess, error) {
		close(attachStarted)
		<-allowAttach
		return &fakeProcess{}, nil
	}
	result := make(chan error, 1)
	go func() {
		_, err := manager.OpenSession(t.Context(), SessionRequest{Identity: identity})
		result <- err
	}()
	<-attachStarted
	manager.closeUserSessions(identity.UserID, true)
	close(allowAttach)
	if err := <-result; !errors.Is(err, ErrManagerStopping) {
		t.Fatalf("OpenSession() error = %v, want ErrManagerStopping", err)
	}
	manager.mu.Lock()
	active := manager.userSessionCountLocked(identity.UserID)
	manager.mu.Unlock()
	if active != 0 {
		t.Fatalf("active session reservations = %d, want 0", active)
	}
}

func (r *fakeRuntime) FindByName(_ context.Context, name string) (RuntimeContainer, bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	container, ok := r.containers[name]
	return container, ok, nil
}

func (r *fakeRuntime) ListManaged(context.Context) ([]RuntimeContainer, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	result := make([]RuntimeContainer, 0, len(r.containers))
	for _, container := range r.containers {
		if container.Labels[labelManaged] == "true" {
			result = append(result, container)
		}
	}
	return result, nil
}

func (r *fakeRuntime) Create(_ context.Context, spec ContainerSpec) (RuntimeContainer, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.created = append(r.created, spec)
	container := RuntimeContainer{
		ID: "container-" + spec.MountID, Name: spec.Name, State: "created",
		Labels: map[string]string{
			labelManaged: "true", labelManagerID: spec.ManagerID, labelUserID: spec.UserID,
			labelUsername: spec.Username, labelMountID: spec.MountID, labelSpecHash: spec.SpecHash,
		},
	}
	r.containers[spec.Name] = container
	return container, nil
}

func (r *fakeRuntime) Start(_ context.Context, containerID string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.started = append(r.started, containerID)
	for name, container := range r.containers {
		if container.ID == containerID {
			container.Running = true
			container.State = "running"
			r.containers[name] = container
		}
	}
	return nil
}

func (r *fakeRuntime) Stop(_ context.Context, containerID string, _ int) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.stopped = append(r.stopped, containerID)
	for name, container := range r.containers {
		if container.ID == containerID {
			container.Running = false
			container.State = "exited"
			r.containers[name] = container
		}
	}
	return nil
}

func (r *fakeRuntime) Remove(_ context.Context, containerID string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.removed = append(r.removed, containerID)
	for name, container := range r.containers {
		if container.ID == containerID {
			delete(r.containers, name)
		}
	}
	return nil
}

func (r *fakeRuntime) Exec(ctx context.Context, request RuntimeExecRequest) (ExecResult, error) {
	r.mu.Lock()
	r.execRequest = request
	execFunc := r.execFunc
	r.mu.Unlock()
	if execFunc != nil {
		return execFunc(ctx, request)
	}
	return ExecResult{ExitCode: 0, Stdout: []byte("ok\n")}, nil
}

func (r *fakeRuntime) OpenSession(ctx context.Context, request RuntimeSessionRequest) (RuntimeProcess, error) {
	r.mu.Lock()
	r.sessionRequest = request
	sessionFunc := r.sessionFunc
	r.mu.Unlock()
	if sessionFunc != nil {
		return sessionFunc(ctx, request)
	}
	return &fakeProcess{}, nil
}

func (r *fakeRuntime) Close() error {
	r.closed = true
	return nil
}

type fakeProcess struct {
	mu     sync.Mutex
	input  bytes.Buffer
	closed bool
}

type blockingCloseProcess struct {
	started chan<- struct{}
	release <-chan struct{}
}

func (p *blockingCloseProcess) Read([]byte) (int, error)                 { return 0, io.EOF }
func (p *blockingCloseProcess) Write(data []byte) (int, error)           { return len(data), nil }
func (p *blockingCloseProcess) Resize(context.Context, uint, uint) error { return nil }
func (p *blockingCloseProcess) Wait() (int, error)                       { return 0, nil }
func (p *blockingCloseProcess) Close() error {
	p.started <- struct{}{}
	<-p.release
	return nil
}

func TestManagerClosesSessionsConcurrently(t *testing.T) {
	manager, _, _, identity := newTestManager(t, nil)
	started := make(chan struct{}, 2)
	release := make(chan struct{})
	for range 2 {
		session, err := manager.reserveSession(t.Context(), identity)
		if err != nil {
			t.Fatal(err)
		}
		if !manager.promoteSession(session) || !session.attachProcess(&blockingCloseProcess{started: started, release: release}) {
			t.Fatal("failed to prepare managed session")
		}
	}
	done := make(chan struct{})
	go func() {
		manager.closeUserSessions(identity.UserID, true)
		close(done)
	}()
	for index := 0; index < 2; index++ {
		select {
		case <-started:
		case <-time.After(time.Second):
			close(release)
			t.Fatal("session closes were serialized")
		}
	}
	close(release)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("parallel session close did not finish")
	}
}

func (p *fakeProcess) Read([]byte) (int, error) { return 0, io.EOF }
func (p *fakeProcess) Write(data []byte) (int, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.input.Write(data)
}
func (p *fakeProcess) Resize(context.Context, uint, uint) error { return nil }
func (p *fakeProcess) Wait() (int, error)                       { return 0, nil }
func (p *fakeProcess) Close() error {
	p.closed = true
	return nil
}

func newTestManager(t *testing.T, mutate func(*ManagerOptions)) (*Manager, *fakeFilesystem, *fakeRuntime, Identity) {
	t.Helper()
	root := t.TempDir()
	mountRoot := filepath.Join(root, "mounts")
	stateRoot := filepath.Join(root, "state")
	if err := os.MkdirAll(mountRoot, 0700); err != nil {
		t.Fatal(err)
	}
	identity := Identity{UserID: "user-1", Username: "alice"}
	mountpoint := filepath.Join(mountRoot, identity.UserID)
	if err := os.Mkdir(mountpoint, 0700); err != nil {
		t.Fatal(err)
	}
	filesystem := &fakeFilesystem{mounts: map[string]dofs.MountStatus{
		identity.UserID: {
			UserID: identity.UserID, Username: identity.Username, Mountpoint: mountpoint,
			MountID: "mount-1", State: "mounted", Desired: true, Writable: true, AllowOther: true, UID: 1000, GID: 1000,
		},
	}}
	runtime := newFakeRuntime()
	options := ManagerOptions{
		StateRoot: stateRoot, ControlSocket: filepath.Join(root, "run", "workspace.sock"),
		SocketMode: 0600, SocketGID: -1, DOFSMountRoot: mountRoot,
		Image: "test:latest", PullPolicy: "never", ContainerPrefix: "domus-test",
		UID: 1000, GID: 1000, NetworkMode: "bridge", ReadOnlyRootFS: true,
		MemoryBytes: 1024 * 1024, MemorySwapBytes: 1024 * 1024, NanoCPUs: 1_000_000_000,
		PIDsLimit: 64, TmpfsSizeBytes: 1024 * 1024, ShmSizeBytes: 1024 * 1024,
		MaxRunning: 4, MaxSessionsPerUser: 2, IdleTimeout: time.Hour,
		ReconcileInterval: time.Minute, OperationTimeout: 5 * time.Second,
		ExecTimeout: time.Minute, ExecOutputLimit: 1024 * 1024, StopTimeout: time.Second,
		Shell: []string{"/bin/sh"}, Keepalive: []string{"/bin/sh", "-c", "while :; do sleep 3600; done"},
	}
	if mutate != nil {
		mutate(&options)
	}
	manager, err := NewManager(options, filesystem, runtime)
	if err != nil {
		t.Fatalf("NewManager() error = %v", err)
	}
	t.Cleanup(func() { _ = manager.Close() })
	return manager, filesystem, runtime, identity
}

func TestEnsurePrivateDirectoryRejectsSymlinkComponents(t *testing.T) {
	realParent := filepath.Join(t.TempDir(), "real")
	if err := os.Mkdir(realParent, 0700); err != nil {
		t.Fatal(err)
	}
	aliasParent := filepath.Join(filepath.Dir(realParent), "alias")
	if err := os.Symlink(realParent, aliasParent); err != nil {
		t.Fatal(err)
	}
	if err := ensurePrivateDirectory(filepath.Join(aliasParent, "state")); err == nil {
		t.Fatal("workspace directory with a symlink component was accepted")
	}
}

func TestValidateManagerOptionsRejectsUnsafeIsolation(t *testing.T) {
	manager, _, _, _ := newTestManager(t, nil)
	tests := []struct {
		name    string
		mutate  func(*ManagerOptions)
		wantErr string
	}{
		{
			name: "host network",
			mutate: func(options *ManagerOptions) {
				options.NetworkMode = "host"
			},
			wantErr: "workspace network mode must be none, bridge, or a named non-host network",
		},
		{
			name: "container namespace network",
			mutate: func(options *ManagerOptions) {
				options.NetworkMode = "container:foreign"
			},
			wantErr: "workspace network mode must be none, bridge, or a named non-host network",
		},
		{
			name: "writable container root",
			mutate: func(options *ManagerOptions) {
				options.ReadOnlyRootFS = false
			},
			wantErr: "workspace container root filesystem must be read-only",
		},
		{
			name: "oversized exec output",
			mutate: func(options *ManagerOptions) {
				options.ExecOutputLimit = MaxExecOutputBytes + 1
			},
			wantErr: "workspace exec output limit must not exceed 16777216 bytes",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			options := manager.options
			test.mutate(&options)
			if err := validateManagerOptions(&options); err == nil || err.Error() != test.wantErr {
				t.Fatalf("validateManagerOptions() error = %v, want %q", err, test.wantErr)
			}
		})
	}
}

func TestManagerEnsureCreatesAndReusesHardenedContainer(t *testing.T) {
	manager, _, runtime, identity := newTestManager(t, nil)
	status, err := manager.Ensure(t.Context(), identity)
	if err != nil {
		t.Fatalf("Ensure() error = %v", err)
	}
	if status.State != "running" || status.MountID != "mount-1" || status.ContainerID == "" {
		t.Fatalf("unexpected status: %+v", status)
	}
	if len(runtime.created) != 1 || len(runtime.started) != 1 {
		t.Fatalf("create/start counts = %d/%d", len(runtime.created), len(runtime.started))
	}
	spec := runtime.created[0]
	if spec.Mountpoint == "" || spec.PasswdPath == "" || spec.GroupPath == "" ||
		spec.MountID != "mount-1" || spec.UID != 1000 || spec.GID != 1000 ||
		!spec.ReadOnlyRootFS || spec.MemoryBytes <= 0 || spec.PIDsLimit <= 0 || spec.NetworkMode != "bridge" {
		t.Fatalf("unsafe or incomplete container spec: %+v", spec)
	}
	passwd, err := os.ReadFile(spec.PasswdPath)
	if err != nil || !strings.Contains(string(passwd), "alice:x:1000:1000:Domus workspace:/workspace/home/alice:/bin/bash") {
		t.Fatalf("workspace passwd = %q, %v", passwd, err)
	}
	group, err := os.ReadFile(spec.GroupPath)
	if err != nil || !strings.Contains(string(group), "alice:x:1000:") {
		t.Fatalf("workspace group = %q, %v", group, err)
	}
	second, err := manager.Ensure(t.Context(), identity)
	if err != nil || second.ContainerID != status.ContainerID {
		t.Fatalf("second Ensure() = %+v, %v", second, err)
	}
	if len(runtime.created) != 1 {
		t.Fatalf("reused workspace created %d containers", len(runtime.created))
	}
}

func TestManagerRecreatesContainerWhenMountIDChanges(t *testing.T) {
	manager, filesystem, runtime, identity := newTestManager(t, nil)
	first, err := manager.Ensure(t.Context(), identity)
	if err != nil {
		t.Fatal(err)
	}
	filesystem.mu.Lock()
	mount := filesystem.mounts[identity.UserID]
	mount.MountID = "mount-2"
	filesystem.mounts[identity.UserID] = mount
	filesystem.mu.Unlock()
	second, err := manager.Ensure(t.Context(), identity)
	if err != nil {
		t.Fatal(err)
	}
	if second.ContainerID == first.ContainerID || len(runtime.removed) != 1 || len(runtime.created) != 2 {
		t.Fatalf("container was not safely recreated: first=%+v second=%+v removed=%v", first, second, runtime.removed)
	}
}

func TestManagerRefusesForeignContainerWithManagedName(t *testing.T) {
	manager, _, runtime, identity := newTestManager(t, nil)
	runtime.containers[manager.containerName(identity.UserID)] = RuntimeContainer{
		ID: "foreign", Name: manager.containerName(identity.UserID), Running: true,
		Labels: map[string]string{labelManaged: "false"},
	}
	_, err := manager.Ensure(t.Context(), identity)
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("Ensure() error = %v, want ErrConflict", err)
	}
	if len(runtime.removed) != 0 || len(runtime.stopped) != 0 {
		t.Fatalf("foreign container was modified: stopped=%v removed=%v", runtime.stopped, runtime.removed)
	}
}

func TestManagerMountIdentityMismatchFailsClosed(t *testing.T) {
	manager, filesystem, runtime, identity := newTestManager(t, nil)
	filesystem.mu.Lock()
	mount := filesystem.mounts[identity.UserID]
	mount.Mountpoint = filepath.Join(filepath.Dir(filepath.Dir(mount.Mountpoint)), "attacker")
	filesystem.mounts[identity.UserID] = mount
	filesystem.mu.Unlock()
	_, err := manager.Ensure(t.Context(), identity)
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("Ensure() error = %v, want ErrConflict", err)
	}
	if len(runtime.created) != 0 {
		t.Fatal("container was created for an unexpected DOFS path")
	}
}

func TestManagerRequiresWritableDesiredDOFSMount(t *testing.T) {
	manager, filesystem, runtime, identity := newTestManager(t, nil)
	filesystem.mu.Lock()
	mount := filesystem.mounts[identity.UserID]
	mount.Writable = false
	filesystem.mounts[identity.UserID] = mount
	filesystem.mu.Unlock()
	if _, err := manager.Ensure(t.Context(), identity); !errors.Is(err, ErrConflict) {
		t.Fatalf("Ensure() error = %v, want ErrConflict", err)
	}
	if len(runtime.created) != 0 {
		t.Fatal("container was created for a read-only DOFS mount")
	}
}

func TestManagerRequiresDOFSAllowOtherForContainerHandoff(t *testing.T) {
	manager, filesystem, runtime, identity := newTestManager(t, nil)
	filesystem.mu.Lock()
	mount := filesystem.mounts[identity.UserID]
	mount.AllowOther = false
	filesystem.mounts[identity.UserID] = mount
	filesystem.mu.Unlock()
	if _, err := manager.Ensure(t.Context(), identity); !errors.Is(err, ErrConflict) {
		t.Fatalf("Ensure() error = %v, want ErrConflict", err)
	}
	if len(runtime.created) != 0 {
		t.Fatal("container was created for a DOFS mount without allow_other")
	}
}

func TestManagerRefusesOwnedContainerWithDifferentUsername(t *testing.T) {
	manager, _, runtime, identity := newTestManager(t, nil)
	runtime.containers[manager.containerName(identity.UserID)] = RuntimeContainer{
		ID: "wrong-username", Name: manager.containerName(identity.UserID), Running: true,
		Labels: map[string]string{
			labelManaged: "true", labelManagerID: manager.ManagerID(), labelUserID: identity.UserID,
			labelUsername: "mallory", labelMountID: "mount-1", labelSpecHash: "unknown",
		},
	}
	if _, err := manager.Ensure(t.Context(), identity); !errors.Is(err, ErrConflict) {
		t.Fatalf("Ensure() error = %v, want ErrConflict", err)
	}
	if len(runtime.removed) != 0 || len(runtime.stopped) != 0 {
		t.Fatalf("mismatched container was modified: stopped=%v removed=%v", runtime.stopped, runtime.removed)
	}
}

func TestManagerCapacityReturnsPending(t *testing.T) {
	manager, _, runtime, identity := newTestManager(t, func(options *ManagerOptions) { options.MaxRunning = 1 })
	runtime.containers["other"] = RuntimeContainer{
		ID: "other", Name: "other", Running: true,
		Labels: map[string]string{labelManaged: "true", labelManagerID: manager.ManagerID(), labelUserID: "other"},
	}
	status, err := manager.Ensure(t.Context(), identity)
	if err != nil {
		t.Fatal(err)
	}
	if status.State != "pending" || !errors.Is(errors.New(status.LastError), ErrCapacity) && !stringsContains(status.LastError, "capacity") {
		t.Fatalf("unexpected capacity status: %+v", status)
	}
	if len(runtime.created) != 0 {
		t.Fatal("container was created beyond capacity")
	}
}

func TestManagerSerializesCrossUserCapacityChecks(t *testing.T) {
	manager, filesystem, runtime, firstIdentity := newTestManager(t, func(options *ManagerOptions) { options.MaxRunning = 1 })
	secondIdentity := Identity{UserID: "user-2", Username: "bob"}
	secondMountpoint := filepath.Join(manager.options.DOFSMountRoot, secondIdentity.UserID)
	if err := os.Mkdir(secondMountpoint, 0700); err != nil {
		t.Fatal(err)
	}
	filesystem.mu.Lock()
	filesystem.mounts[secondIdentity.UserID] = dofs.MountStatus{
		UserID: secondIdentity.UserID, Username: secondIdentity.Username, Mountpoint: secondMountpoint,
		MountID: "mount-2", State: "mounted", Desired: true, Writable: true, AllowOther: true, UID: 1000, GID: 1000,
	}
	filesystem.mu.Unlock()

	start := make(chan struct{})
	results := make(chan Status, 2)
	errorsFound := make(chan error, 2)
	var workers sync.WaitGroup
	for _, identity := range []Identity{firstIdentity, secondIdentity} {
		workers.Add(1)
		go func(identity Identity) {
			defer workers.Done()
			<-start
			status, err := manager.Ensure(t.Context(), identity)
			results <- status
			errorsFound <- err
		}(identity)
	}
	close(start)
	workers.Wait()
	close(results)
	close(errorsFound)
	for err := range errorsFound {
		if err != nil {
			t.Fatalf("concurrent Ensure() error = %v", err)
		}
	}
	runningStatuses := 0
	for status := range results {
		if status.State == "running" {
			runningStatuses++
		}
	}
	if runningStatuses != 1 {
		t.Fatalf("running workspace statuses = %d, want 1", runningStatuses)
	}
	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	runningContainers := 0
	for _, container := range runtime.containers {
		if container.Running {
			runningContainers++
		}
	}
	if runningContainers != 1 || len(runtime.created) != 1 {
		t.Fatalf("runtime exceeded capacity: running=%d created=%d", runningContainers, len(runtime.created))
	}
}

func stringsContains(value, wanted string) bool {
	return bytes.Contains([]byte(value), []byte(wanted))
}

func TestManagerExecUsesBoundedContainerRequest(t *testing.T) {
	manager, _, runtime, identity := newTestManager(t, nil)
	identity.UserID = "  " + identity.UserID + "  "
	identity.Username = "  " + identity.Username + "  "
	result, err := manager.Exec(t.Context(), ExecRequest{
		Identity: identity, Command: []string{"printf", "ok"}, Environment: map[string]string{"LANG": "C"},
	})
	if err != nil || string(result.Stdout) != "ok\n" {
		t.Fatalf("Exec() = %+v, %v", result, err)
	}
	request := runtime.execRequest
	if request.ContainerID == "" || request.WorkingDir != "/workspace/home/alice" || request.OutputLimit <= 0 ||
		!containsString(request.Environment, "USER=alice") || !containsString(request.Environment, "DOMUS_USER_ID=user-1") {
		t.Fatalf("unexpected runtime request: %+v", request)
	}
	if _, err := manager.Exec(t.Context(), ExecRequest{Identity: identity, Command: []string{"pwd"}, WorkingDir: "/etc"}); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("unsafe working directory error = %v", err)
	}
	if _, err := manager.Exec(t.Context(), ExecRequest{Identity: identity, Command: []string{"pwd"}, TimeoutSeconds: -1}); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("negative timeout error = %v", err)
	}
}

func TestManagerShutdownCancelsExecBeforeDestroyingContainer(t *testing.T) {
	manager, filesystem, runtime, identity := newTestManager(t, nil)
	started := make(chan struct{})
	finished := make(chan struct{})
	var startOnce sync.Once
	var finishOnce sync.Once
	runtime.mu.Lock()
	runtime.execFunc = func(ctx context.Context, _ RuntimeExecRequest) (ExecResult, error) {
		startOnce.Do(func() { close(started) })
		<-ctx.Done()
		finishOnce.Do(func() { close(finished) })
		return ExecResult{}, ctx.Err()
	}
	runtime.mu.Unlock()

	execResult := make(chan error, 1)
	go func() {
		_, err := manager.Exec(context.Background(), ExecRequest{Identity: identity, Command: []string{"sleep", "60"}})
		execResult <- err
	}()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("Exec did not start")
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := manager.Shutdown(shutdownCtx); err != nil {
		t.Fatalf("Shutdown() error = %v", err)
	}
	select {
	case <-finished:
	default:
		t.Fatal("Shutdown destroyed runtime state before Exec exited")
	}
	if err := <-execResult; !errors.Is(err, context.Canceled) {
		t.Fatalf("Exec() error after shutdown = %v, want context.Canceled", err)
	}
	runtime.mu.Lock()
	remaining := len(runtime.containers)
	runtime.mu.Unlock()
	if remaining != 0 {
		t.Fatalf("Shutdown left %d containers", remaining)
	}
	filesystem.mu.Lock()
	unmounted := append([]string(nil), filesystem.unmounted...)
	filesystem.mu.Unlock()
	if len(unmounted) != 1 || unmounted[0] != identity.UserID {
		t.Fatalf("Shutdown unmounted = %v", unmounted)
	}
}

func TestManagerShutdownCancelsSessionOpenBeforeDestroyingContainer(t *testing.T) {
	manager, _, runtime, identity := newTestManager(t, nil)
	started := make(chan struct{})
	finished := make(chan struct{})
	var startOnce sync.Once
	var finishOnce sync.Once
	runtime.mu.Lock()
	runtime.sessionFunc = func(ctx context.Context, _ RuntimeSessionRequest) (RuntimeProcess, error) {
		startOnce.Do(func() { close(started) })
		<-ctx.Done()
		finishOnce.Do(func() { close(finished) })
		return nil, ctx.Err()
	}
	runtime.mu.Unlock()
	openResult := make(chan error, 1)
	go func() {
		_, err := manager.OpenSession(context.Background(), SessionRequest{Identity: identity, Columns: 80, Rows: 24})
		openResult <- err
	}()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("OpenSession did not start")
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := manager.Shutdown(shutdownCtx); err != nil {
		t.Fatal(err)
	}
	select {
	case <-finished:
	default:
		t.Fatal("Shutdown destroyed runtime state before session setup exited")
	}
	if err := <-openResult; !errors.Is(err, context.Canceled) {
		t.Fatalf("OpenSession() error after shutdown = %v, want context.Canceled", err)
	}
}

func TestManagerIdleReconcileRemovesContainerAndUnmountsDOFS(t *testing.T) {
	manager, filesystem, runtime, identity := newTestManager(t, func(options *ManagerOptions) { options.IdleTimeout = time.Millisecond })
	if _, err := manager.Ensure(t.Context(), identity); err != nil {
		t.Fatal(err)
	}
	time.Sleep(5 * time.Millisecond)
	if err := manager.ReconcileOnce(t.Context()); err != nil {
		t.Fatal(err)
	}
	status, err := manager.Status(t.Context(), identity.UserID)
	if err != nil || status.State != "stopped" || status.Desired {
		t.Fatalf("idle status = %+v, %v", status, err)
	}
	if len(runtime.removed) != 1 || len(filesystem.unmounted) != 1 {
		t.Fatalf("idle cleanup removed=%v unmounted=%v", runtime.removed, filesystem.unmounted)
	}
}

func TestPendingSessionPreventsIdleReconcileTeardown(t *testing.T) {
	manager, _, runtime, identity := newTestManager(t, func(options *ManagerOptions) { options.IdleTimeout = time.Hour })
	if _, err := manager.Ensure(t.Context(), identity); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-2 * time.Hour).UTC()
	manager.markerMu.Lock()
	manager.mu.Lock()
	marker := manager.desired[identity.UserID]
	marker.LastUsedAt = old
	manager.desired[identity.UserID] = marker
	manager.mu.Unlock()
	if err := manager.persistMarker(marker); err != nil {
		manager.markerMu.Unlock()
		t.Fatal(err)
	}
	manager.markerMu.Unlock()

	pending, err := manager.reserveSession(t.Context(), identity)
	if err != nil {
		t.Fatal(err)
	}
	defer pending.release()
	if err := manager.ReconcileOnce(t.Context()); err != nil {
		t.Fatal(err)
	}
	runtime.mu.Lock()
	removed := len(runtime.removed)
	runtime.mu.Unlock()
	if removed != 0 {
		t.Fatal("idle reconciliation removed a workspace with a pending command")
	}
}

func TestContainerReplacementDoesNotCancelPendingCaller(t *testing.T) {
	manager, filesystem, _, identity := newTestManager(t, nil)
	if _, err := manager.Ensure(t.Context(), identity); err != nil {
		t.Fatal(err)
	}
	pending, err := manager.reserveSession(t.Context(), identity)
	if err != nil {
		t.Fatal(err)
	}
	defer pending.release()
	filesystem.mu.Lock()
	mount := filesystem.mounts[identity.UserID]
	mount.MountID = "mount-2"
	filesystem.mounts[identity.UserID] = mount
	filesystem.mu.Unlock()
	if _, err := manager.Ensure(t.Context(), identity); err != nil {
		t.Fatal(err)
	}
	if !manager.promoteSession(pending) {
		t.Fatal("mount-id replacement closed the pending caller that initiated it")
	}
}

func TestSessionReservationWaitsForTeardownBarrier(t *testing.T) {
	manager, _, _, identity := newTestManager(t, nil)
	endTeardown, err := manager.beginTeardown(identity.UserID)
	if err != nil {
		t.Fatal(err)
	}
	result := make(chan error, 1)
	var reserved *managedSession
	go func() {
		var reserveErr error
		reserved, reserveErr = manager.reserveSession(context.Background(), identity)
		result <- reserveErr
	}()
	select {
	case err := <-result:
		t.Fatalf("reservation crossed active teardown barrier: %v", err)
	case <-time.After(20 * time.Millisecond):
	}
	endTeardown()
	select {
	case err := <-result:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("reservation did not resume after teardown barrier")
	}
	reserved.release()
}

func TestSessionCloseStartsFreshIdleTimeout(t *testing.T) {
	manager, _, runtime, identity := newTestManager(t, func(options *ManagerOptions) { options.IdleTimeout = time.Hour })
	session, err := manager.OpenSession(t.Context(), SessionRequest{Identity: identity, Columns: 80, Rows: 24})
	if err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-2 * time.Hour).UTC()
	manager.markerMu.Lock()
	manager.mu.Lock()
	marker := manager.desired[identity.UserID]
	marker.LastUsedAt = old
	manager.desired[identity.UserID] = marker
	manager.mu.Unlock()
	if err := manager.persistMarker(marker); err != nil {
		manager.markerMu.Unlock()
		t.Fatal(err)
	}
	manager.markerMu.Unlock()
	if err := manager.ReconcileOnce(t.Context()); err != nil {
		t.Fatal(err)
	}
	if len(runtime.removed) != 0 {
		t.Fatal("active workspace was reaped")
	}
	if err := session.Close(); err != nil {
		t.Fatal(err)
	}
	persisted, err := readMarker(manager.markerPath(identity.UserID))
	if err != nil {
		t.Fatal(err)
	}
	if !persisted.LastUsedAt.After(old) || time.Since(persisted.LastUsedAt) > time.Second {
		t.Fatalf("session close did not refresh persisted activity: %v", persisted.LastUsedAt)
	}
	if err := manager.ReconcileOnce(t.Context()); err != nil {
		t.Fatal(err)
	}
	if len(runtime.removed) != 0 {
		t.Fatal("workspace was reaped before the fresh idle timeout")
	}
}

func TestTeardownFailureRetainsDesiredMarkerForRetry(t *testing.T) {
	manager, filesystem, _, identity := newTestManager(t, nil)
	if _, err := manager.Ensure(t.Context(), identity); err != nil {
		t.Fatal(err)
	}
	filesystem.mu.Lock()
	filesystem.unmountErr = errors.New("temporary unmount failure")
	filesystem.mu.Unlock()
	if _, err := manager.Stop(t.Context(), identity.UserID); err == nil {
		t.Fatal("Stop() succeeded despite DOFS unmount failure")
	}
	manager.mu.Lock()
	_, desired := manager.desired[identity.UserID]
	manager.mu.Unlock()
	if !desired {
		t.Fatal("failed teardown discarded desired state")
	}
	if _, err := os.Stat(manager.markerPath(identity.UserID)); err != nil {
		t.Fatalf("failed teardown marker = %v", err)
	}
}

func TestRemoveDeletesIdentityFilesWhileStopRetainsThem(t *testing.T) {
	manager, _, _, identity := newTestManager(t, nil)
	if _, err := manager.Ensure(t.Context(), identity); err != nil {
		t.Fatal(err)
	}
	identityDirectory := filepath.Join(manager.identityDir, identity.UserID)
	if _, err := manager.Stop(t.Context(), identity.UserID); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(identityDirectory, "passwd")); err != nil {
		t.Fatalf("Stop removed reusable identity files: %v", err)
	}
	if _, err := manager.Ensure(t.Context(), identity); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Remove(t.Context(), identity.UserID); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(identityDirectory); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("Remove retained identity directory: %v", err)
	}
}

func TestReconcileRemovesOwnedStalePrefixContainer(t *testing.T) {
	manager, _, runtime, identity := newTestManager(t, nil)
	if _, err := manager.Ensure(t.Context(), identity); err != nil {
		t.Fatal(err)
	}
	stale := RuntimeContainer{
		ID: "stale-prefix", Name: "old-prefix-" + identity.UserID, Running: true,
		Labels: map[string]string{
			labelManaged: "true", labelManagerID: manager.ManagerID(), labelUserID: identity.UserID,
		},
	}
	runtime.mu.Lock()
	runtime.containers[stale.Name] = stale
	runtime.mu.Unlock()
	if err := manager.ReconcileOnce(t.Context()); err != nil {
		t.Fatal(err)
	}
	runtime.mu.Lock()
	_, staleExists := runtime.containers[stale.Name]
	_, currentExists := runtime.containers[manager.containerName(identity.UserID)]
	runtime.mu.Unlock()
	if staleExists || !currentExists {
		t.Fatalf("stale/current containers exist = %v/%v", staleExists, currentExists)
	}
}

func TestReconcileRemovesAllUndesiredOwnedContainers(t *testing.T) {
	manager, filesystem, runtime, identity := newTestManager(t, nil)
	for index, name := range []string{"old-a-" + identity.UserID, "old-b-" + identity.UserID} {
		runtime.containers[name] = RuntimeContainer{
			ID: fmt.Sprintf("orphan-%d", index), Name: name, Running: true,
			Labels: map[string]string{
				labelManaged: "true", labelManagerID: manager.ManagerID(), labelUserID: identity.UserID,
			},
		}
	}
	if err := manager.ReconcileOnce(t.Context()); err != nil {
		t.Fatal(err)
	}
	runtime.mu.Lock()
	remaining := len(runtime.containers)
	runtime.mu.Unlock()
	filesystem.mu.Lock()
	unmounted := append([]string(nil), filesystem.unmounted...)
	filesystem.mu.Unlock()
	if remaining != 0 || len(unmounted) != 1 || unmounted[0] != identity.UserID {
		t.Fatalf("orphan cleanup remaining=%d unmounted=%v", remaining, unmounted)
	}
}

func TestShutdownUnmountsOwnedOrphanUser(t *testing.T) {
	manager, filesystem, runtime, _ := newTestManager(t, nil)
	orphan := Identity{UserID: "orphan-user", Username: "orphan"}
	mountpoint := filepath.Join(manager.options.DOFSMountRoot, orphan.UserID)
	if err := os.Mkdir(mountpoint, 0700); err != nil {
		t.Fatal(err)
	}
	filesystem.mu.Lock()
	filesystem.mounts[orphan.UserID] = dofs.MountStatus{
		UserID: orphan.UserID, Username: orphan.Username, Mountpoint: mountpoint,
		MountID: "orphan-mount", State: "mounted", Desired: true, Writable: true, AllowOther: true, UID: 1000, GID: 1000,
	}
	filesystem.mu.Unlock()
	runtime.containers["old-"+orphan.UserID] = RuntimeContainer{
		ID: "orphan-container", Name: "old-" + orphan.UserID, Running: true,
		Labels: map[string]string{
			labelManaged: "true", labelManagerID: manager.ManagerID(), labelUserID: orphan.UserID,
		},
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := manager.Shutdown(shutdownCtx); err != nil {
		t.Fatal(err)
	}
	filesystem.mu.Lock()
	defer filesystem.mu.Unlock()
	if !slices.Contains(filesystem.unmounted, orphan.UserID) {
		t.Fatalf("shutdown did not unmount orphan user: %v", filesystem.unmounted)
	}
}
