//go:build linux

package dofs

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	dofscore "github.com/willvar/dofs"
)

const (
	testManagedUserID   = "11111111-1111-4111-8111-111111111111"
	testManagedUsername = "alice"
	testManagedUserID2  = "22222222-2222-4222-8222-222222222222"
)

type fakeManagedMount struct {
	done       chan struct{}
	failures   chan error
	unmountErr error
	once       sync.Once
}

func newFakeManagedMount() *fakeManagedMount {
	return &fakeManagedMount{done: make(chan struct{}), failures: make(chan error, 1)}
}

func (m *fakeManagedMount) Unmount() error {
	if m.unmountErr != nil {
		return m.unmountErr
	}
	m.Exit()
	return nil
}

func (m *fakeManagedMount) Done() <-chan struct{}  { return m.done }
func (m *fakeManagedMount) Failures() <-chan error { return m.failures }

func (m *fakeManagedMount) Exit() {
	m.once.Do(func() { close(m.done) })
}

func (m *fakeManagedMount) Fail(err error) {
	m.failures <- err
}

type fakeManagedProvider struct {
	mu         sync.Mutex
	mounts     []*fakeManagedMount
	mountCalls int
	mountErr   error
	started    chan struct{}
	continueAt chan struct{}
	resolveAt  chan struct{}
	resolveGo  chan struct{}
	missing    bool
}

func (p *fakeManagedProvider) ResolveUser(_ context.Context, selector MountUserSelector) (MountIdentity, error) {
	if p.resolveAt != nil {
		select {
		case p.resolveAt <- struct{}{}:
		default:
		}
	}
	if p.resolveGo != nil {
		<-p.resolveGo
	}
	if p.missing {
		return MountIdentity{}, fmt.Errorf("%w: removed", ErrUserNotFound)
	}
	if err := selector.Validate(); err != nil {
		return MountIdentity{}, err
	}
	if selector.UserID == testManagedUserID2 || selector.Username == "bob" {
		return MountIdentity{UserID: testManagedUserID2, Username: "bob"}, nil
	}
	if selector.UserID != "" && selector.UserID != testManagedUserID {
		return MountIdentity{}, errors.New("user not found")
	}
	if selector.Username != "" && selector.Username != testManagedUsername {
		return MountIdentity{}, errors.New("user not found")
	}
	return MountIdentity{UserID: testManagedUserID, Username: testManagedUsername}, nil
}

func TestManagerMountCapacityQueuesDesiredUser(t *testing.T) {
	base := t.TempDir()
	provider := &fakeManagedProvider{}
	manager, err := NewManager(ManagerOptions{
		MountRoot: filepath.Join(base, "mounts"), StateRoot: filepath.Join(base, "state"),
		ControlSocket: filepath.Join(base, "run", "dofs.sock"), SocketMode: 0600, SocketGID: -1,
		Mount:             MountOptions{UID: 1000, GID: 1000, Writable: true},
		ReconcileInterval: time.Hour, MountTimeout: time.Second, MaxMounts: 1,
	}, provider)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		_ = manager.Shutdown(ctx)
		cancel()
		_ = manager.Close()
	}()
	if _, err := manager.Ensure(context.Background(), MountUserSelector{UserID: testManagedUserID}); err != nil {
		t.Fatal(err)
	}
	queued, err := manager.Ensure(context.Background(), MountUserSelector{UserID: testManagedUserID2})
	if err != nil || queued.State != "pending" || !queued.Desired {
		t.Fatalf("capacity result = %+v, %v", queued, err)
	}
	if provider.Calls() != 1 {
		t.Fatalf("provider calls at capacity = %d", provider.Calls())
	}
	reconcileContext, stopReconciler := context.WithCancel(context.Background())
	defer stopReconciler()
	go manager.RunReconciler(reconcileContext)
	if _, err := manager.Unmount(context.Background(), testManagedUserID); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(time.Second)
	for {
		status, err := manager.Status(testManagedUserID2)
		if err == nil && status.State == "mounted" && provider.Calls() == 2 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("queued mount was not woken after capacity release: status=%+v calls=%d err=%v", status, provider.Calls(), err)
		}
		time.Sleep(time.Millisecond)
	}
}

func (p *fakeManagedProvider) Mount(_ context.Context, _ MountIdentity, _, _ string, _ MountOptions) (ManagedMount, error) {
	if p.started != nil {
		select {
		case p.started <- struct{}{}:
		default:
		}
	}
	if p.continueAt != nil {
		<-p.continueAt
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.mountCalls++
	if p.mountErr != nil {
		return nil, p.mountErr
	}
	mount := newFakeManagedMount()
	p.mounts = append(p.mounts, mount)
	return mount, nil
}

func (p *fakeManagedProvider) Calls() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.mountCalls
}

func (p *fakeManagedProvider) LastMount() *fakeManagedMount {
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.mounts) == 0 {
		return nil
	}
	return p.mounts[len(p.mounts)-1]
}

func newTestManager(t *testing.T, base string, provider ManagedMountProvider) *Manager {
	t.Helper()
	manager, err := NewManager(ManagerOptions{
		MountRoot:         filepath.Join(base, "mounts"),
		StateRoot:         filepath.Join(base, "state"),
		ControlSocket:     filepath.Join(base, "run", "dofs.sock"),
		SocketMode:        0600,
		SocketGID:         -1,
		Mount:             MountOptions{UID: 1000, GID: 1000, Writable: true},
		ReconcileInterval: time.Hour,
		MountTimeout:      time.Second,
		MaxMounts:         8,
	}, provider)
	if err != nil {
		t.Fatalf("NewManager() error = %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		_ = manager.Shutdown(ctx)
		cancel()
		_ = manager.Close()
	})
	return manager
}

func TestManagerEnsureIsIdempotentAndUnmountForgetsDesiredState(t *testing.T) {
	base := t.TempDir()
	provider := &fakeManagedProvider{}
	manager := newTestManager(t, base, provider)

	first, err := manager.Ensure(context.Background(), MountUserSelector{Username: testManagedUsername})
	if err != nil {
		t.Fatalf("Ensure() error = %v", err)
	}
	second, err := manager.Ensure(context.Background(), MountUserSelector{UserID: testManagedUserID})
	if err != nil {
		t.Fatalf("second Ensure() error = %v", err)
	}
	if provider.Calls() != 1 {
		t.Fatalf("provider mount calls = %d, want 1", provider.Calls())
	}
	if first.State != "mounted" || second.Mountpoint != first.Mountpoint || !second.Desired {
		t.Fatalf("unexpected mount statuses: first=%+v second=%+v", first, second)
	}
	marker := filepath.Join(base, "state", "desired", testManagedUserID+".json")
	if info, err := os.Stat(marker); err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("desired marker mode: info=%v err=%v", info, err)
	}

	status, err := manager.Unmount(context.Background(), testManagedUserID)
	if err != nil {
		t.Fatalf("Unmount() error = %v", err)
	}
	if status.State != "unmounted" || status.Desired {
		t.Fatalf("unmounted status = %+v", status)
	}
	if _, err := os.Stat(marker); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("desired marker still exists: %v", err)
	}
	if _, err := os.Stat(first.Mountpoint); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("empty mountpoint still exists: %v", err)
	}
}

func TestManagerReconcileForgetsDesiredStateForRemovedUser(t *testing.T) {
	base := t.TempDir()
	provider := &fakeManagedProvider{}
	manager := newTestManager(t, base, provider)
	if _, err := manager.Ensure(context.Background(), MountUserSelector{UserID: testManagedUserID}); err != nil {
		t.Fatal(err)
	}
	mount := provider.LastMount()
	provider.missing = true
	if err := manager.ReconcileOnce(context.Background()); err != nil {
		t.Fatalf("reconcile stale user: %v", err)
	}
	status, err := manager.Status(testManagedUserID)
	if err != nil || status.Desired || status.State != "unmounted" {
		t.Fatalf("removed user status = %+v, %v", status, err)
	}
	select {
	case <-mount.Done():
	default:
		t.Fatal("removed user's mount remained active")
	}
	marker := filepath.Join(base, "state", "desired", testManagedUserID+".json")
	if _, err := os.Stat(marker); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("removed user's desired marker remained: %v", err)
	}
}

func TestManagerSerializesConcurrentEnsure(t *testing.T) {
	provider := &fakeManagedProvider{started: make(chan struct{}, 1), continueAt: make(chan struct{})}
	manager := newTestManager(t, t.TempDir(), provider)
	results := make(chan error, 2)
	for range 2 {
		go func() {
			_, err := manager.Ensure(context.Background(), MountUserSelector{UserID: testManagedUserID})
			results <- err
		}()
	}
	select {
	case <-provider.started:
	case <-time.After(time.Second):
		t.Fatal("provider mount did not start")
	}
	close(provider.continueAt)
	for range 2 {
		if err := <-results; err != nil {
			t.Fatalf("concurrent Ensure() error = %v", err)
		}
	}
	if provider.Calls() != 1 {
		t.Fatalf("provider mount calls = %d, want 1", provider.Calls())
	}
}

func TestManagerRestoresDesiredMountAfterRestart(t *testing.T) {
	base := t.TempDir()
	firstProvider := &fakeManagedProvider{}
	first := newTestManager(t, base, firstProvider)
	if _, err := first.Ensure(context.Background(), MountUserSelector{UserID: testManagedUserID}); err != nil {
		t.Fatal(err)
	}
	shutdownContext, cancel := context.WithTimeout(context.Background(), time.Second)
	if err := first.Shutdown(shutdownContext); err != nil {
		t.Fatalf("first Shutdown() error = %v", err)
	}
	cancel()
	if err := first.Close(); err != nil {
		t.Fatalf("first Close() error = %v", err)
	}

	secondProvider := &fakeManagedProvider{}
	second := newTestManager(t, base, secondProvider)
	status, err := second.Status(testManagedUserID)
	if err != nil || status.State != "pending" || !status.Desired {
		t.Fatalf("restored pending status = %+v, err=%v", status, err)
	}
	if err := second.ReconcileOnce(context.Background()); err != nil {
		t.Fatalf("ReconcileOnce() error = %v", err)
	}
	status, err = second.Status(testManagedUserID)
	if err != nil || status.State != "mounted" || secondProvider.Calls() != 1 {
		t.Fatalf("restored status = %+v calls=%d err=%v", status, secondProvider.Calls(), err)
	}
}

func TestManagerUnexpectedExitIsRemountedByReconcile(t *testing.T) {
	provider := &fakeManagedProvider{}
	manager := newTestManager(t, t.TempDir(), provider)
	if _, err := manager.Ensure(context.Background(), MountUserSelector{UserID: testManagedUserID}); err != nil {
		t.Fatal(err)
	}
	provider.LastMount().Exit()
	deadline := time.Now().Add(time.Second)
	for {
		status, err := manager.Status(testManagedUserID)
		if err == nil && status.State == "failed" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("mount did not transition to failed: %+v err=%v", status, err)
		}
		time.Sleep(time.Millisecond)
	}
	if err := manager.ReconcileOnce(context.Background()); err != nil {
		t.Fatalf("ReconcileOnce() error = %v", err)
	}
	if provider.Calls() != 2 {
		t.Fatalf("provider mount calls = %d, want 2", provider.Calls())
	}
}

func TestManagerReportsUnhealthyLiveMountWithoutStartingASecond(t *testing.T) {
	provider := &fakeManagedProvider{}
	manager := newTestManager(t, t.TempDir(), provider)
	if _, err := manager.Ensure(context.Background(), MountUserSelector{UserID: testManagedUserID}); err != nil {
		t.Fatal(err)
	}
	provider.LastMount().Fail(dofscore.ErrLeaseLost)
	deadline := time.Now().Add(time.Second)
	for {
		status, err := manager.Status(testManagedUserID)
		if err == nil && status.State == "failed" && status.LastError != "" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("mount did not become unhealthy: %+v err=%v", status, err)
		}
		time.Sleep(time.Millisecond)
	}
	if _, err := manager.Ensure(context.Background(), MountUserSelector{UserID: testManagedUserID}); !errors.Is(err, ErrMountUnhealthy) {
		t.Fatalf("Ensure() error = %v, want ErrMountUnhealthy", err)
	}
	if provider.Calls() != 1 {
		t.Fatalf("provider mount calls = %d, want 1", provider.Calls())
	}
	if health := manager.Health(); health.Status != "degraded" || health.Degraded != 1 {
		t.Fatalf("health after mount failure = %+v", health)
	}
}

func TestManagerReconcileReplacesFailedMountAfterNormalUnmount(t *testing.T) {
	provider := &fakeManagedProvider{}
	manager := newTestManager(t, t.TempDir(), provider)
	initial, err := manager.Ensure(context.Background(), MountUserSelector{UserID: testManagedUserID})
	if err != nil {
		t.Fatal(err)
	}
	provider.LastMount().Fail(dofscore.ErrLeaseLost)
	deadline := time.Now().Add(time.Second)
	for {
		status, _ := manager.Status(testManagedUserID)
		if status.State == "failed" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("mount did not enter failed state")
		}
		time.Sleep(time.Millisecond)
	}
	if err := manager.ReconcileOnce(context.Background()); err != nil {
		t.Fatalf("ReconcileOnce() error = %v", err)
	}
	status, err := manager.Status(testManagedUserID)
	if err != nil || status.State != "mounted" || !status.Desired || provider.Calls() != 2 || status.MountID == initial.MountID {
		t.Fatalf("replacement status=%+v calls=%d err=%v", status, provider.Calls(), err)
	}
}

func TestManagerStaleRetryCannotUnmountRecoveredMount(t *testing.T) {
	provider := &fakeManagedProvider{}
	manager := newTestManager(t, t.TempDir(), provider)
	if _, err := manager.Ensure(context.Background(), MountUserSelector{UserID: testManagedUserID}); err != nil {
		t.Fatal(err)
	}
	provider.LastMount().Fail(dofscore.ErrLeaseLost)
	deadline := time.Now().Add(time.Second)
	for {
		status, _ := manager.Status(testManagedUserID)
		if status.State == "failed" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("mount did not enter failed state")
		}
		time.Sleep(time.Millisecond)
	}
	if _, err := manager.unmount(context.Background(), testManagedUserID, false); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.ensure(context.Background(), MountUserSelector{UserID: testManagedUserID}, true); err != nil {
		t.Fatal(err)
	}
	recovered := provider.LastMount()
	statusBefore, err := manager.Status(testManagedUserID)
	if err != nil || statusBefore.State != "mounted" {
		t.Fatalf("recovered status = %+v, %v", statusBefore, err)
	}

	// This is the operation a second reconciliation pass may have snapshotted
	// before the first pass completed. It must re-check state under the per-user
	// operation lock and leave the replacement untouched.
	statusAfter, err := manager.unmount(context.Background(), testManagedUserID, false)
	if err != nil || statusAfter.State != "mounted" || statusAfter.MountID != statusBefore.MountID {
		t.Fatalf("stale retry status = %+v, %v", statusAfter, err)
	}
	select {
	case <-recovered.Done():
		t.Fatal("stale retry unmounted the recovered mount")
	default:
	}
}

func TestManagerRetriesBusyExplicitUnmountWithoutRestoringDesiredState(t *testing.T) {
	provider := &fakeManagedProvider{}
	base := t.TempDir()
	manager := newTestManager(t, base, provider)
	if _, err := manager.Ensure(context.Background(), MountUserSelector{UserID: testManagedUserID}); err != nil {
		t.Fatal(err)
	}
	mount := provider.LastMount()
	mount.unmountErr = errors.New("mount is busy")
	status, err := manager.Unmount(context.Background(), testManagedUserID)
	if err == nil || status.State != "unmount_failed" || status.Desired {
		t.Fatalf("first Unmount() status=%+v err=%v", status, err)
	}
	marker := filepath.Join(base, "state", "desired", testManagedUserID+".json")
	if _, statErr := os.Stat(marker); statErr != nil {
		t.Fatalf("failed unmount did not retain crash-recovery marker: %v", statErr)
	}
	mount.unmountErr = nil
	if err := manager.ReconcileOnce(context.Background()); err != nil {
		t.Fatalf("ReconcileOnce() error = %v", err)
	}
	status, err = manager.Status(testManagedUserID)
	if err != nil || status.State != "unmounted" || status.Desired {
		t.Fatalf("retried unmount status=%+v err=%v", status, err)
	}
	if _, statErr := os.Stat(marker); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("successful retry retained desired marker: %v", statErr)
	}
}

func TestManagerStaleReconcileCannotRecreateExplicitlyUnmountedUser(t *testing.T) {
	provider := &fakeManagedProvider{}
	manager := newTestManager(t, t.TempDir(), provider)
	if _, err := manager.Ensure(context.Background(), MountUserSelector{UserID: testManagedUserID}); err != nil {
		t.Fatal(err)
	}
	provider.resolveAt = make(chan struct{}, 1)
	provider.resolveGo = make(chan struct{})
	reconciled := make(chan error, 1)
	go func() { reconciled <- manager.ReconcileOnce(context.Background()) }()
	select {
	case <-provider.resolveAt:
	case <-time.After(time.Second):
		t.Fatal("reconcile did not reach user resolution")
	}
	if _, err := manager.Unmount(context.Background(), testManagedUserID); err != nil {
		t.Fatal(err)
	}
	close(provider.resolveGo)
	if err := <-reconciled; err != nil {
		t.Fatalf("ReconcileOnce() error = %v", err)
	}
	status, err := manager.Status(testManagedUserID)
	if err != nil || status.State != "unmounted" || status.Desired || provider.Calls() != 1 {
		t.Fatalf("stale reconcile recreated mount: status=%+v calls=%d err=%v", status, provider.Calls(), err)
	}
}

func TestManagerEnsureCancelsFailedUnmountForgetTransaction(t *testing.T) {
	base := t.TempDir()
	provider := &fakeManagedProvider{}
	manager := newTestManager(t, base, provider)
	if _, err := manager.Ensure(context.Background(), MountUserSelector{UserID: testManagedUserID}); err != nil {
		t.Fatal(err)
	}
	mount := provider.LastMount()
	mount.unmountErr = errors.New("mount is busy")
	if status, err := manager.Unmount(context.Background(), testManagedUserID); err == nil || status.Desired {
		t.Fatalf("failed Unmount() = %+v, %v", status, err)
	}
	status, err := manager.Ensure(context.Background(), MountUserSelector{UserID: testManagedUserID})
	if !errors.Is(err, ErrMountUnhealthy) || !status.Desired {
		t.Fatalf("Ensure() did not restore desired state: status=%+v err=%v", status, err)
	}
	mount.unmountErr = nil
	if err := manager.ReconcileOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	status, err = manager.Status(testManagedUserID)
	if err != nil || status.State != "mounted" || !status.Desired || provider.Calls() != 2 {
		t.Fatalf("restored mount = %+v calls=%d err=%v", status, provider.Calls(), err)
	}
	marker := filepath.Join(base, "state", "desired", testManagedUserID+".json")
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("restored desired marker missing: %v", err)
	}
}

func TestManagerStateRootLockRejectsSecondProcess(t *testing.T) {
	base := t.TempDir()
	first := newTestManager(t, base, &fakeManagedProvider{})
	_, err := NewManager(first.options, &fakeManagedProvider{})
	if err == nil {
		t.Fatal("second manager unexpectedly acquired state root")
	}
}

func TestManagerControlSocketLockRejectsDifferentStateRoot(t *testing.T) {
	base := t.TempDir()
	first := newTestManager(t, filepath.Join(base, "first"), &fakeManagedProvider{})
	options := first.options
	options.MountRoot = filepath.Join(base, "second", "mounts")
	options.StateRoot = filepath.Join(base, "second", "state")
	options.ControlSocket = first.options.ControlSocket
	if second, err := NewManager(options, &fakeManagedProvider{}); err == nil {
		_ = second.Close()
		t.Fatal("second manager unexpectedly acquired the same control socket")
	}
}

func TestManagerControlSocketLifecycle(t *testing.T) {
	base := t.TempDir()
	provider := &fakeManagedProvider{}
	manager := newTestManager(t, base, provider)
	serveContext, stop := context.WithCancel(context.Background())
	served := make(chan error, 1)
	go func() { served <- manager.ServeControl(serveContext) }()

	socket := filepath.Join(base, "run", "dofs.sock")
	deadline := time.Now().Add(time.Second)
	for {
		if info, err := os.Stat(socket); err == nil && info.Mode()&os.ModeSocket != 0 {
			if info.Mode().Perm() != 0600 {
				t.Fatalf("control socket mode = %04o, want 0600", info.Mode().Perm())
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("control socket was not created")
		}
		time.Sleep(time.Millisecond)
	}
	client := ControlClient{SocketPath: socket, Timeout: time.Second}
	health, err := client.Health(context.Background(), true)
	if err != nil || health.Status != "ready" {
		t.Fatalf("ready health = %+v, err=%v", health, err)
	}
	status, err := client.Ensure(context.Background(), MountUserSelector{Username: testManagedUsername})
	if err != nil || status.State != "mounted" {
		t.Fatalf("Ensure over socket = %+v, err=%v", status, err)
	}
	listed, err := client.List(context.Background())
	if err != nil || len(listed) != 1 || listed[0].UserID != testManagedUserID {
		t.Fatalf("List over socket = %+v, err=%v", listed, err)
	}

	stop()
	select {
	case err := <-served:
		if err != nil {
			t.Fatalf("ServeControl() error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("ServeControl did not stop")
	}
	if _, err := os.Stat(socket); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("control socket was not removed: %v", err)
	}
}

func TestDecodeMountInfoPath(t *testing.T) {
	got, err := decodeMountInfoPath(`/var/lib/domus/dofs\040mounts/user\134name`)
	if err != nil {
		t.Fatal(err)
	}
	if got != "/var/lib/domus/dofs mounts/user\\name" {
		t.Fatalf("decoded mountinfo path = %q", got)
	}
	if _, err := decodeMountInfoPath(`/broken\0`); err == nil {
		t.Fatal("invalid mountinfo escape was accepted")
	}
}
