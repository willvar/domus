package dofs

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"strings"
	"sync"
	"testing"

	"domus/internal/auth"
	"domus/internal/model"
)

type testLeaseChecker struct {
	mu  sync.Mutex
	err error
}

func (l *testLeaseChecker) Check(context.Context) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.err
}

func (l *testLeaseChecker) Close() error {
	return nil
}

func (l *testLeaseChecker) Fail(err error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.err = err
}

type checkingLeaseRepository struct {
	NamespaceRepository
	lease io.Closer
}

func (r checkingLeaseRepository) AcquireDOFSMountLease(string) (io.Closer, error) {
	return r.lease, nil
}

func enableTestWriteback(t *testing.T, backend *Backend, objects *memoryRangeStore, stateDir string) GenerationRepository {
	t.Helper()
	repository, ok := backend.files.(GenerationRepository)
	if !ok {
		t.Fatal("test repository does not support generations")
	}
	if err := backend.EnableWriteback(stateDir, repository, objects); err != nil {
		t.Fatalf("enable writeback: %v", err)
	}
	return repository
}

func TestWritebackLeaseLossStopsMutationsAndSignalsMountOwner(t *testing.T) {
	backend, objects, record := newTestBackend(t, []byte("lease-protected"))
	namespace, ok := backend.files.(NamespaceRepository)
	if !ok {
		t.Fatal("test repository does not support namespace operations")
	}
	lease := &testLeaseChecker{}
	repository := checkingLeaseRepository{NamespaceRepository: namespace, lease: lease}
	if err := backend.EnableWriteback(t.TempDir(), repository, objects); err != nil {
		t.Fatalf("EnableWriteback() error = %v", err)
	}
	if backend.LeaseLost() == nil {
		t.Fatal("health-checkable lease did not expose a loss signal")
	}

	lease.Fail(errors.New("database session disconnected"))
	if err := backend.writeback.checkLease(context.Background(), lease); err == nil {
		t.Fatal("lease check unexpectedly succeeded")
	}
	select {
	case <-backend.LeaseLost():
	default:
		t.Fatal("lease loss signal was not closed")
	}
	if !errors.Is(backend.LeaseError(), ErrMountLeaseLost) {
		t.Fatalf("LeaseError() = %v", backend.LeaseError())
	}
	if _, err := backend.OpenWrite(record, nil); !errors.Is(err, ErrMountLeaseLost) {
		t.Fatalf("OpenWrite() error = %v, want ErrMountLeaseLost", err)
	}
	if _, err := backend.CreateDirectory(backend.RootKey(), "after-lease-loss"); !errors.Is(err, ErrMountLeaseLost) {
		t.Fatalf("CreateDirectory() error = %v, want ErrMountLeaseLost", err)
	}
}

func TestWritebackCommitPublishesImmutableGeneration(t *testing.T) {
	original := bytes.Repeat([]byte("0123456789abcdef"), 9000)
	backend, objects, record := newTestBackend(t, original)
	repository := enableTestWriteback(t, backend, objects, t.TempDir())
	oldObjectKey := record.StorageKey()

	session, err := backend.OpenWrite(record, nil)
	if err != nil {
		t.Fatalf("open writeback: %v", err)
	}
	patch := []byte("DOFS-generation")
	offset := int64(auth.DefaultChunkSize - 5)
	if _, err := session.WriteAt(patch, offset); err != nil {
		t.Fatalf("write cache: %v", err)
	}
	newSize := int64(len(original) - 97)
	if err := session.Truncate(newSize); err != nil {
		t.Fatalf("truncate cache: %v", err)
	}
	if err := session.Commit(); err != nil {
		t.Fatalf("commit generation: %v", err)
	}
	objects.mu.Lock()
	commitRanges := append([][2]int64(nil), objects.ranges...)
	objects.mu.Unlock()
	wantCipherEnd := encryptedSize(record.Size, auth.DefaultChunkSize) - 1
	if len(commitRanges) != 1 || commitRanges[0] != [2]int64{0, wantCipherEnd} {
		t.Fatalf("commit base reads = %v, want one streaming range [0,%d]", commitRanges, wantCipherEnd)
	}
	if err := session.Close(); err != nil {
		t.Fatalf("close writeback: %v", err)
	}

	current, err := repository.GetByID(backend.UserID(), record.ID)
	if err != nil {
		t.Fatal(err)
	}
	if current.Generation != record.Generation+1 {
		t.Fatalf("generation = %d, want %d", current.Generation, record.Generation+1)
	}
	if current.StorageKey() == oldObjectKey || !strings.HasPrefix(current.StorageKey(), backend.ObjectRoot()) {
		t.Fatalf("generation was not published to an immutable key: %q", current.StorageKey())
	}
	if current.Size != newSize {
		t.Fatalf("size = %d, want %d", current.Size, newSize)
	}

	want := append([]byte(nil), original[:newSize]...)
	copy(want[offset:], patch)
	got, err := backend.ReadAt(current, 0, int(current.Size))
	if err != nil {
		t.Fatalf("read committed generation: %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Fatal("committed plaintext differs from writeback cache")
	}
	objects.mu.Lock()
	_, oldGenerationStillExists := objects.objects[oldObjectKey]
	objects.mu.Unlock()
	if !oldGenerationStillExists {
		t.Fatal("old generation was deleted before open readers could drain")
	}
}

func TestWritebackRecoveryResumesPreparedUpload(t *testing.T) {
	backend, objects, record := newTestBackend(t, []byte("before crash"))
	stateDir := t.TempDir()
	repository := enableTestWriteback(t, backend, objects, stateDir)
	objects.mu.Lock()
	objects.failPuts = 1
	objects.mu.Unlock()

	session, err := backend.OpenWrite(record, nil)
	if err != nil {
		t.Fatal(err)
	}
	want := []byte("recovered after fsync")
	if err := session.Truncate(0); err != nil {
		t.Fatal(err)
	}
	if _, err := session.WriteAt(want, 0); err != nil {
		t.Fatal(err)
	}
	if err := session.Commit(); err == nil {
		t.Fatal("expected injected upload failure")
	}
	if session.entry == nil || session.entry.State != walStatePrepared {
		t.Fatalf("WAL state = %#v, want prepared", session.entry)
	}
	_ = session.cache.Close() // simulate process death without Release
	session.closed = true
	backend.Close()

	kek := bytes.Repeat([]byte{0x2a}, auth.DEKSize)
	recoveredBackend, err := NewBackend(backend.UserID(), backend.Username(), kek, repository, objects)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(recoveredBackend.Close)
	if err := recoveredBackend.EnableWriteback(stateDir, repository, objects); err != nil {
		t.Fatalf("recover writeback: %v", err)
	}
	current, err := repository.GetByID(backend.UserID(), record.ID)
	if err != nil {
		t.Fatal(err)
	}
	got, err := recoveredBackend.ReadAt(current, 0, int(current.Size))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("recovered plaintext = %q, want %q", got, want)
	}
	assertDirectoryEmpty(t, recoveredBackend.writeback.walDir)
	assertDirectoryEmpty(t, recoveredBackend.writeback.cacheDir)
}

func TestWritebackGenerationCASRejectsConcurrentWriter(t *testing.T) {
	backend, objects, record := newTestBackend(t, []byte("base"))
	repository := enableTestWriteback(t, backend, objects, t.TempDir())

	first, err := backend.OpenWrite(record, nil)
	if err != nil {
		t.Fatal(err)
	}
	second, err := backend.OpenWrite(record, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := first.Truncate(0); err != nil {
		t.Fatal(err)
	}
	if _, err := first.WriteAt([]byte("first"), 0); err != nil {
		t.Fatal(err)
	}
	if err := second.Truncate(0); err != nil {
		t.Fatal(err)
	}
	if _, err := second.WriteAt([]byte("second"), 0); err != nil {
		t.Fatal(err)
	}
	if err := first.Commit(); err != nil {
		t.Fatal(err)
	}
	if err := second.Commit(); !errors.Is(err, ErrWriteConflict) {
		t.Fatalf("second commit error = %v, want conflict", err)
	}
	if err := second.Close(); !errors.Is(err, ErrWriteConflict) {
		t.Fatalf("second close error = %v, want conflict", err)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	assertDirectoryEmpty(t, backend.writeback.walDir)

	current, err := repository.GetByID(backend.UserID(), record.ID)
	if err != nil {
		t.Fatal(err)
	}
	got, err := backend.ReadAt(current, 0, int(current.Size))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "first" {
		t.Fatalf("winning generation = %q", got)
	}
}

func TestWritebackConflictRecoveryNeverPublishesDiscardedGeneration(t *testing.T) {
	backend, objects, record := newTestBackend(t, []byte("base"))
	stateDir := t.TempDir()
	repository := enableTestWriteback(t, backend, objects, stateDir)

	first, err := backend.OpenWrite(record, nil)
	if err != nil {
		t.Fatal(err)
	}
	second, err := backend.OpenWrite(record, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := first.Truncate(0); err != nil {
		t.Fatal(err)
	}
	if _, err := first.WriteAt([]byte("winner"), 0); err != nil {
		t.Fatal(err)
	}
	if err := second.Truncate(0); err != nil {
		t.Fatal(err)
	}
	if _, err := second.WriteAt([]byte("discarded"), 0); err != nil {
		t.Fatal(err)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	objects.mu.Lock()
	objects.failDeletes = 1
	objects.mu.Unlock()
	if err := second.Commit(); !errors.Is(err, ErrWriteConflict) {
		t.Fatalf("losing commit error = %v", err)
	}
	if second.entry == nil || second.entry.State != walStateDiscarded {
		t.Fatalf("losing WAL state = %#v, want discarded", second.entry)
	}
	_ = second.cache.Close()
	second.closed = true
	backend.Close()

	kek := bytes.Repeat([]byte{0x2a}, auth.DEKSize)
	recovered, err := NewBackend(backend.UserID(), backend.Username(), kek, repository, objects)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(recovered.Close)
	if err := recovered.EnableWriteback(stateDir, repository, objects); err != nil {
		t.Fatal(err)
	}
	current, err := repository.GetByID(recovered.UserID(), record.ID)
	if err != nil {
		t.Fatal(err)
	}
	got, err := recovered.ReadAt(current, 0, int(current.Size))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "winner" {
		t.Fatalf("recovery published %q, want winning generation", got)
	}
	assertDirectoryEmpty(t, recovered.writeback.walDir)
	assertDirectoryEmpty(t, recovered.writeback.cacheDir)
}

func TestWritebackRejectsSharedStateDirectory(t *testing.T) {
	backend, objects, _ := newTestBackend(t, []byte("locked"))
	stateDir := t.TempDir()
	repository := enableTestWriteback(t, backend, objects, stateDir)

	kek := bytes.Repeat([]byte{0x2a}, auth.DEKSize)
	second, err := NewBackend(backend.UserID(), backend.Username(), kek, repository, objects)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(second.Close)
	if err := second.EnableWriteback(stateDir, repository, objects); err == nil {
		t.Fatal("expected the second mount to reject an in-use state directory")
	}
}

func TestWritebackOpenAndPatchUseDemandFilledSparseCache(t *testing.T) {
	original := bytes.Repeat([]byte("sparse-cache-block"), 20000)
	backend, objects, record := newTestBackend(t, original)
	enableTestWriteback(t, backend, objects, t.TempDir())

	session, err := backend.OpenWrite(record, nil)
	if err != nil {
		t.Fatal(err)
	}
	objects.mu.Lock()
	rangesAfterOpen := len(objects.ranges)
	objects.mu.Unlock()
	if rangesAfterOpen != 0 {
		t.Fatalf("opening a write handle downloaded %d object ranges", rangesAfterOpen)
	}
	patch := []byte("PATCH")
	if _, err := session.WriteAt(patch, 12345); err != nil {
		t.Fatal(err)
	}
	objects.mu.Lock()
	rangesAfterWrite := len(objects.ranges)
	objects.mu.Unlock()
	if rangesAfterWrite != 0 {
		t.Fatalf("a direct patch downloaded %d object ranges before commit", rangesAfterWrite)
	}
	if err := session.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestWritebackFullOverwriteSkipsBaseObjectDownload(t *testing.T) {
	backend, objects, record := newTestBackend(t, bytes.Repeat([]byte("old"), 50000))
	repository := enableTestWriteback(t, backend, objects, t.TempDir())
	session, err := backend.OpenWrite(record, nil)
	if err != nil {
		t.Fatal(err)
	}
	want := bytes.Repeat([]byte("new"), 30000)
	if err := session.Truncate(0); err != nil {
		t.Fatal(err)
	}
	if _, err := session.WriteAt(want, 0); err != nil {
		t.Fatal(err)
	}
	if err := session.Close(); err != nil {
		t.Fatal(err)
	}
	objects.mu.Lock()
	baseReads := len(objects.ranges)
	objects.mu.Unlock()
	if baseReads != 0 {
		t.Fatalf("full overwrite downloaded %d base ranges", baseReads)
	}
	current, err := repository.GetByID(backend.UserID(), record.ID)
	if err != nil {
		t.Fatal(err)
	}
	got, err := backend.ReadAt(current, 0, int(current.Size))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatal("full-overwrite generation differs from replacement")
	}
}

func TestWritebackRecoveryRebuildsUnchangedSparseRanges(t *testing.T) {
	original := bytes.Repeat([]byte("0123456789"), 20000)
	backend, objects, record := newTestBackend(t, original)
	stateDir := t.TempDir()
	repository := enableTestWriteback(t, backend, objects, stateDir)
	objects.mu.Lock()
	objects.failPuts = 1
	objects.mu.Unlock()

	session, err := backend.OpenWrite(record, nil)
	if err != nil {
		t.Fatal(err)
	}
	patch := []byte("recovered-sparse-patch")
	offset := int64(77777)
	if _, err := session.WriteAt(patch, offset); err != nil {
		t.Fatal(err)
	}
	if err := session.Commit(); err == nil {
		t.Fatal("expected injected upload failure")
	}
	if session.entry == nil || len(session.entry.DirtyRanges) != 1 {
		t.Fatalf("prepared WAL did not persist dirty ranges: %#v", session.entry)
	}
	if len(session.loadedRanges) != 1 || session.loadedRanges[0] != session.entry.DirtyRanges[0] {
		t.Fatalf("failed upload materialized clean ranges: loaded=%v dirty=%v", session.loadedRanges, session.entry.DirtyRanges)
	}
	_ = session.cache.Close()
	session.closed = true
	backend.Close()

	kek := bytes.Repeat([]byte{0x2a}, auth.DEKSize)
	recovered, err := NewBackend(backend.UserID(), backend.Username(), kek, repository, objects)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(recovered.Close)
	if err := recovered.EnableWriteback(stateDir, repository, objects); err != nil {
		t.Fatal(err)
	}
	current, err := repository.GetByID(recovered.UserID(), record.ID)
	if err != nil {
		t.Fatal(err)
	}
	got, err := recovered.ReadAt(current, 0, int(current.Size))
	if err != nil {
		t.Fatal(err)
	}
	want := append([]byte(nil), original...)
	copy(want[offset:], patch)
	if !bytes.Equal(got, want) {
		t.Fatal("sparse recovery failed to reconstruct unchanged base ranges")
	}
}

func TestWritebackShrinkThenGrowDoesNotResurrectBaseBytes(t *testing.T) {
	backend, objects, record := newTestBackend(t, []byte("abcdefghij"))
	repository := enableTestWriteback(t, backend, objects, t.TempDir())
	session, err := backend.OpenWrite(record, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := session.Truncate(3); err != nil {
		t.Fatal(err)
	}
	if err := session.Truncate(8); err != nil {
		t.Fatal(err)
	}
	if err := session.Close(); err != nil {
		t.Fatal(err)
	}
	current, err := repository.GetByID(backend.UserID(), record.ID)
	if err != nil {
		t.Fatal(err)
	}
	got, err := backend.ReadAt(current, 0, int(current.Size))
	if err != nil {
		t.Fatal(err)
	}
	want := append([]byte("abc"), make([]byte, 5)...)
	if !bytes.Equal(got, want) {
		t.Fatalf("truncate/regrow plaintext = %v, want %v", got, want)
	}
}

func assertDirectoryEmpty(t *testing.T, directory string) {
	t.Helper()
	entries, err := os.ReadDir(directory)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("directory %s contains %v", directory, entries)
	}
}

var _ GenerationRepository = (model.FileRepo)(nil)
