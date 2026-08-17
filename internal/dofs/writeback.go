package dofs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"domus/internal/auth"
	"domus/internal/model"
)

var (
	ErrReadOnly       = errors.New("DOFS mount is read-only")
	ErrWriteConflict  = errors.New("DOFS generation changed during writeback")
	ErrHandleClosed   = errors.New("DOFS write handle is closed")
	ErrMountLeaseLost = errors.New("DOFS writable mount lease was lost")
)

const mountLeaseCheckInterval = 5 * time.Second

const (
	walStateDirty     = "dirty"
	walStatePrepared  = "prepared"
	walStateUploaded  = "uploaded"
	walStateDiscarded = "discarded"
)

type byteRange struct {
	Start int64 `json:"start"`
	End   int64 `json:"end"`
}

func addByteRange(ranges []byteRange, start, end int64) []byteRange {
	if start >= end {
		return ranges
	}
	merged := make([]byteRange, 0, len(ranges)+1)
	candidate := byteRange{Start: start, End: end}
	inserted := false
	for _, current := range ranges {
		if current.End < candidate.Start {
			merged = append(merged, current)
			continue
		}
		if candidate.End < current.Start {
			if !inserted {
				merged = append(merged, candidate)
				inserted = true
			}
			merged = append(merged, current)
			continue
		}
		candidate.Start = min(candidate.Start, current.Start)
		candidate.End = max(candidate.End, current.End)
	}
	if !inserted {
		merged = append(merged, candidate)
	}
	return merged
}

func missingByteRanges(ranges []byteRange, start, end int64) []byteRange {
	if start >= end {
		return nil
	}
	cursor := start
	var missing []byteRange
	for _, current := range ranges {
		if current.End <= cursor {
			continue
		}
		if current.Start >= end {
			break
		}
		if current.Start > cursor {
			missing = append(missing, byteRange{Start: cursor, End: min(current.Start, end)})
		}
		cursor = max(cursor, current.End)
		if cursor >= end {
			return missing
		}
	}
	if cursor < end {
		missing = append(missing, byteRange{Start: cursor, End: end})
	}
	return missing
}

type walEntry struct {
	ID               string      `json:"id"`
	UserID           string      `json:"user_id"`
	FileID           int64       `json:"file_id"`
	LogicalPath      string      `json:"logical_path"`
	BaseGeneration   int64       `json:"base_generation"`
	TargetGeneration int64       `json:"target_generation"`
	TargetObjectKey  string      `json:"target_object_key"`
	Size             int64       `json:"size"`
	DirtyRanges      []byteRange `json:"dirty_ranges,omitempty"`
	State            string      `json:"state"`
	UpdatedAt        time.Time   `json:"updated_at"`
}

// WritebackManager owns the private plaintext cache and crash-recovery WAL for
// one user mount. WAL files never contain encryption keys.
type WritebackManager struct {
	backend  *Backend
	repo     GenerationRepository
	objects  GenerationObjectStore
	walDir   string
	cacheDir string
	lock     *stateDirectoryLock
	lease    io.Closer

	mu             sync.Mutex
	closed         bool
	leaseCancel    context.CancelFunc
	leaseLost      chan struct{}
	leaseLostOnce  sync.Once
	leaseErr       error
	leaseMonitored bool
}

// EnableWriteback upgrades a backend to writable mode and recovers any fsynced
// transaction left by an earlier process. It must be called before mounting.
func (b *Backend) EnableWriteback(stateDir string, repo GenerationRepository, objects GenerationObjectStore) error {
	if b.writeback != nil {
		return errors.New("DOFS writeback is already enabled")
	}
	if repo == nil {
		return errors.New("DOFS generation repository is required")
	}
	if objects == nil {
		return errors.New("DOFS generation object store is required")
	}
	namespace, ok := repo.(NamespaceRepository)
	if !ok {
		return errors.New("DOFS namespace repository is required")
	}
	lease, err := namespace.AcquireDOFSMountLease(b.UserID())
	if err != nil {
		return fmt.Errorf("acquire writable DOFS mount lease: %w", err)
	}
	manager, err := newWritebackManager(b, stateDir, repo, objects)
	if err != nil {
		_ = lease.Close()
		return err
	}
	manager.lease = lease
	manager.startLeaseMonitor()
	if err := manager.Recover(); err != nil {
		manager.Close()
		return fmt.Errorf("recover DOFS writeback: %w", err)
	}
	b.writeback = manager
	b.namespace = namespace
	b.genObjects = objects
	if err := b.cleanupIncompleteNamespace(); err != nil {
		manager.Close()
		b.writeback = nil
		b.namespace = nil
		b.genObjects = nil
		return fmt.Errorf("recover DOFS namespace: %w", err)
	}
	return nil
}

func newWritebackManager(backend *Backend, stateDir string, repo GenerationRepository, objects GenerationObjectStore) (*WritebackManager, error) {
	if strings.TrimSpace(stateDir) == "" {
		return nil, errors.New("DOFS writeback state directory is required")
	}
	absolute, err := filepath.Abs(stateDir)
	if err != nil {
		return nil, fmt.Errorf("resolve writeback state directory: %w", err)
	}
	if err := ensurePrivateDirectory(absolute); err != nil {
		return nil, err
	}
	walDir := filepath.Join(absolute, "wal")
	cacheDir := filepath.Join(absolute, "cache")
	if err := ensurePrivateDirectory(walDir); err != nil {
		return nil, err
	}
	if err := ensurePrivateDirectory(cacheDir); err != nil {
		return nil, err
	}
	lock, err := acquireStateDirectoryLock(absolute)
	if err != nil {
		return nil, err
	}
	return &WritebackManager{
		backend: backend, repo: repo, objects: objects,
		walDir: walDir, cacheDir: cacheDir, lock: lock,
		leaseLost: make(chan struct{}),
	}, nil
}

func ensurePrivateDirectory(directory string) error {
	if err := os.MkdirAll(directory, 0700); err != nil {
		return fmt.Errorf("create private directory %s: %w", directory, err)
	}
	info, err := os.Lstat(directory)
	if err != nil {
		return fmt.Errorf("inspect private directory %s: %w", directory, err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return fmt.Errorf("private path %s must be a real directory", directory)
	}
	resolved, err := filepath.EvalSymlinks(directory)
	if err != nil {
		return fmt.Errorf("resolve private directory %s: %w", directory, err)
	}
	if filepath.Clean(resolved) != filepath.Clean(directory) {
		return fmt.Errorf("private path %s must not contain symbolic-link components", directory)
	}
	if err := os.Chmod(directory, 0700); err != nil {
		return fmt.Errorf("protect private directory %s: %w", directory, err)
	}
	return nil
}

func (m *WritebackManager) Close() {
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return
	}
	m.closed = true
	cancel := m.leaseCancel
	m.leaseCancel = nil
	lock := m.lock
	m.lock = nil
	lease := m.lease
	m.lease = nil
	m.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	if lock != nil {
		_ = lock.Close()
	}
	if lease != nil {
		_ = lease.Close()
	}
}

type mountLeaseChecker interface {
	Check(context.Context) error
}

func (m *WritebackManager) startLeaseMonitor() {
	checker, ok := m.lease.(mountLeaseChecker)
	if !ok {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	m.mu.Lock()
	m.leaseCancel = cancel
	m.leaseMonitored = true
	m.mu.Unlock()
	go func() {
		if err := m.checkLease(ctx, checker); err != nil {
			return
		}
		ticker := time.NewTicker(mountLeaseCheckInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if err := m.checkLease(ctx, checker); err != nil {
					return
				}
			}
		}
	}()
}

func (m *WritebackManager) checkLease(ctx context.Context, checker mountLeaseChecker) error {
	checkContext, cancel := context.WithTimeout(ctx, 5*time.Second)
	err := checker.Check(checkContext)
	cancel()
	if err == nil {
		return nil
	}
	m.mu.Lock()
	if !m.closed && m.leaseErr == nil {
		m.leaseErr = fmt.Errorf("%w: %v", ErrMountLeaseLost, err)
		m.leaseLostOnce.Do(func() { close(m.leaseLost) })
	}
	m.mu.Unlock()
	return err
}

func (m *WritebackManager) operationalError() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.leaseErr != nil {
		return m.leaseErr
	}
	if m.closed {
		return ErrHandleClosed
	}
	return nil

}

func (m *WritebackManager) LeaseLost() <-chan struct{} {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.leaseMonitored {
		return nil
	}
	return m.leaseLost

}

func (m *WritebackManager) LeaseError() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.leaseErr
}

func (m *WritebackManager) walPath(id string) string {
	return filepath.Join(m.walDir, id+".json")
}

func (m *WritebackManager) cachePath(id string) string {
	return filepath.Join(m.cacheDir, id+".cache")
}

func (m *WritebackManager) objectKey(record *model.FileRecord, generation int64, transactionID string) string {
	return model.DOFSGenerationObjectKey(m.backend.UserID(), record.ID, generation, transactionID)
}

func (m *WritebackManager) validateEntry(entry *walEntry) error {
	if entry == nil || uuid.Validate(entry.ID) != nil {
		return errors.New("invalid WAL transaction ID")
	}
	if entry.UserID != m.backend.UserID() || entry.FileID <= 0 || entry.BaseGeneration < 0 || entry.Size < 0 {
		return errors.New("WAL entry is outside this mount")
	}
	if entry.TargetGeneration != entry.BaseGeneration+1 {
		return errors.New("invalid WAL generation transition")
	}
	wantPrefix := model.DOFSInodeObjectRoot(m.backend.UserID(), entry.FileID)
	if !strings.HasPrefix(entry.TargetObjectKey, wantPrefix) {
		return errors.New("invalid WAL target object key")
	}
	var previousEnd int64 = -1
	for _, dirty := range entry.DirtyRanges {
		if dirty.Start < 0 || dirty.End <= dirty.Start || (previousEnd >= 0 && dirty.Start <= previousEnd) {
			return errors.New("invalid WAL dirty ranges")
		}
		previousEnd = dirty.End
	}
	switch entry.State {
	case walStateDirty, walStatePrepared, walStateUploaded, walStateDiscarded:
		return nil
	default:
		return errors.New("invalid WAL state")
	}
}

func (m *WritebackManager) persist(entry *walEntry) error {
	if err := m.validateEntry(entry); err != nil {
		return err
	}
	entry.UpdatedAt = time.Now().UTC()
	data, err := json.MarshalIndent(entry, "", "  ")
	if err != nil {
		return fmt.Errorf("encode WAL: %w", err)
	}
	temporary := filepath.Join(m.walDir, entry.ID+"."+uuid.NewString()+".tmp")
	file, err := os.OpenFile(temporary, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return fmt.Errorf("create WAL: %w", err)
	}
	removeTemporary := true
	defer func() {
		_ = file.Close()
		if removeTemporary {
			_ = os.Remove(temporary)
		}
	}()
	if _, err := file.Write(data); err != nil {
		return fmt.Errorf("write WAL: %w", err)
	}
	if err := file.Sync(); err != nil {
		return fmt.Errorf("sync WAL: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close WAL: %w", err)
	}
	if err := os.Rename(temporary, m.walPath(entry.ID)); err != nil {
		return fmt.Errorf("publish WAL: %w", err)
	}
	removeTemporary = false
	return syncDirectory(m.walDir)
}

func syncDirectory(directory string) error {
	file, err := os.Open(directory)
	if err != nil {
		return err
	}
	defer func() { _ = file.Close() }()
	return file.Sync()
}

func (m *WritebackManager) removeLocal(entry *walEntry, removeCache bool) error {
	if err := m.validateEntry(entry); err != nil {
		return err
	}
	if err := os.Remove(m.walPath(entry.ID)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if removeCache {
		if err := os.Remove(m.cachePath(entry.ID)); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	return syncDirectory(m.walDir)
}

// Recover resumes only transactions that reached fsync/prepared. Dirty cache
// entries had no durability promise and are discarded rather than publishing a
// potentially partial file.
func (m *WritebackManager) Recover() error {
	entries, err := os.ReadDir(m.walDir)
	if err != nil {
		return err
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	activeCaches := make(map[string]struct{})
	for _, directoryEntry := range entries {
		if directoryEntry.IsDir() {
			continue
		}
		if strings.HasSuffix(directoryEntry.Name(), ".tmp") {
			_ = os.Remove(filepath.Join(m.walDir, directoryEntry.Name()))
			continue
		}
		if !strings.HasSuffix(directoryEntry.Name(), ".json") {
			continue
		}
		id := strings.TrimSuffix(directoryEntry.Name(), ".json")
		if uuid.Validate(id) != nil {
			return fmt.Errorf("unexpected WAL file %q", directoryEntry.Name())
		}
		data, err := os.ReadFile(m.walPath(id))
		if err != nil {
			return err
		}
		var entry walEntry
		if err := json.Unmarshal(data, &entry); err != nil {
			return fmt.Errorf("decode WAL %s: %w", id, err)
		}
		if entry.ID != id {
			return fmt.Errorf("WAL filename does not match transaction %s", entry.ID)
		}
		if err := m.validateEntry(&entry); err != nil {
			return fmt.Errorf("validate WAL %s: %w", id, err)
		}
		activeCaches[id+".cache"] = struct{}{}
		if entry.State == walStateDirty {
			if err := m.removeLocal(&entry, true); err != nil {
				return err
			}
			delete(activeCaches, id+".cache")
			continue
		}
		if entry.State == walStateDiscarded {
			if err := m.objects.DeleteObject(entry.TargetObjectKey); err != nil {
				return fmt.Errorf("clean discarded WAL object %s: %w", id, err)
			}
			if err := m.removeLocal(&entry, true); err != nil {
				return err
			}
			delete(activeCaches, id+".cache")
			continue
		}

		record, err := m.repo.GetByID(m.backend.UserID(), entry.FileID)
		if errors.Is(err, gorm.ErrRecordNotFound) {
			if entry.State == walStateUploaded {
				_ = m.objects.DeleteObject(entry.TargetObjectKey)
			}
			if err := m.removeLocal(&entry, true); err != nil {
				return err
			}
			delete(activeCaches, id+".cache")
			continue
		}
		if err != nil {
			return err
		}
		if record.Generation == entry.TargetGeneration && record.StorageKey() == entry.TargetObjectKey {
			if err := m.removeLocal(&entry, true); err != nil {
				return err
			}
			delete(activeCaches, id+".cache")
			continue
		}
		if record.Generation != entry.BaseGeneration {
			// This transaction lost its generation CAS (or could never win it
			// after a crash). It is safe to discard because its object was never
			// made current in metadata.
			if err := m.objects.DeleteObject(entry.TargetObjectKey); err != nil {
				return fmt.Errorf("clean stale WAL object %s: %w", id, err)
			}
			if err := m.removeLocal(&entry, true); err != nil {
				return err
			}
			delete(activeCaches, id+".cache")
			continue
		}
		cache, err := openRegularFile(m.cachePath(id))
		if err != nil {
			return fmt.Errorf("open WAL cache %s: %w", id, err)
		}
		session := &WriteSession{
			manager: m, cache: cache, cachePath: m.cachePath(id),
			record: *record, entry: &entry, walReady: true, size: entry.Size,
			dirtyRanges:  append([]byteRange(nil), entry.DirtyRanges...),
			loadedRanges: append([]byteRange(nil), entry.DirtyRanges...),
		}
		cacheInfo, statErr := cache.Stat()
		if statErr != nil {
			_ = cache.Close()
			return statErr
		}
		if cacheInfo.Size() != entry.Size {
			_ = cache.Close()
			return fmt.Errorf("WAL cache %s has size %d, want %d", id, cacheInfo.Size(), entry.Size)
		}
		err = session.commitLocked()
		_ = cache.Close()
		if err != nil {
			return fmt.Errorf("replay WAL %s: %w", id, err)
		}
		_ = os.Remove(session.cachePath)
		delete(activeCaches, id+".cache")
	}

	cacheEntries, err := os.ReadDir(m.cacheDir)
	if err != nil {
		return err
	}
	for _, entry := range cacheEntries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".cache") {
			continue
		}
		if _, active := activeCaches[entry.Name()]; active {
			continue
		}
		id := strings.TrimSuffix(entry.Name(), ".cache")
		if uuid.Validate(id) == nil {
			_ = os.Remove(filepath.Join(m.cacheDir, entry.Name()))
		}
	}
	return nil
}

func openRegularFile(filePath string) (*os.File, error) {
	info, err := os.Lstat(filePath)
	if err != nil {
		return nil, err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return nil, errors.New("cache path is not a regular file")
	}
	return os.OpenFile(filePath, os.O_RDWR, 0600)
}

// WriteSession is an isolated plaintext snapshot for one open file handle.
// Concurrent writers are detected by the generation CAS at commit time.
type WriteSession struct {
	manager      *WritebackManager
	cache        *os.File
	cachePath    string
	record       model.FileRecord
	entry        *walEntry
	walReady     bool
	onCommit     func(*model.FileRecord)
	size         int64
	loadedRanges []byteRange
	dirtyRanges  []byteRange
	terminalErr  error

	mu     sync.Mutex
	closed bool
}

func (b *Backend) OpenWrite(record *model.FileRecord, onCommit func(*model.FileRecord)) (*WriteSession, error) {
	if b.writeback == nil {
		return nil, ErrReadOnly
	}
	return b.writeback.open(record, onCommit)
}

func (m *WritebackManager) open(record *model.FileRecord, onCommit func(*model.FileRecord)) (*WriteSession, error) {
	if err := m.operationalError(); err != nil {
		return nil, err
	}
	if record == nil || record.ID <= 0 || record.IsDir {
		return nil, errors.New("invalid file for writeback")
	}
	current, err := m.repo.GetByID(m.backend.UserID(), record.ID)
	if err != nil {
		return nil, err
	}
	if !m.backend.validStorageRecord(current) {
		return nil, ErrNotReady
	}
	if current.Size < 0 || current.WrappedDEK == "" {
		return nil, errors.New("file cannot be opened for writeback")
	}

	id := uuid.NewString()
	cachePath := m.cachePath(id)
	cache, err := os.OpenFile(cachePath, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return nil, fmt.Errorf("create plaintext cache: %w", err)
	}
	session := &WriteSession{manager: m, cache: cache, cachePath: cachePath, record: *current, size: current.Size, onCommit: onCommit}
	if err := session.initializeSparseCache(); err != nil {
		_ = cache.Close()
		_ = os.Remove(cachePath)
		return nil, err
	}
	return session, nil
}

func (s *WriteSession) initializeSparseCache() error {
	return s.cache.Truncate(s.record.Size)
}

func (s *WriteSession) beginTransactionLocked() error {
	if s.entry != nil {
		if !s.walReady {
			if err := s.manager.persist(s.entry); err != nil {
				return err
			}
			s.walReady = true
		}
		return nil
	}
	transactionID := uuid.NewString()
	newCachePath := s.manager.cachePath(transactionID)
	if err := os.Rename(s.cachePath, newCachePath); err != nil {
		return fmt.Errorf("rotate plaintext cache: %w", err)
	}
	s.cachePath = newCachePath
	entry := &walEntry{
		ID:               transactionID,
		UserID:           s.manager.backend.UserID(),
		FileID:           s.record.ID,
		LogicalPath:      s.record.Path,
		BaseGeneration:   s.record.Generation,
		TargetGeneration: s.record.Generation + 1,
		State:            walStateDirty,
	}
	entry.TargetObjectKey = s.manager.objectKey(&s.record, entry.TargetGeneration, transactionID)
	s.entry = entry
	if err := s.manager.persist(entry); err != nil {
		return err
	}
	s.walReady = true
	return nil
}

func (s *WriteSession) ReadAt(destination []byte, offset int64) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return 0, ErrHandleClosed
	}
	if s.terminalErr != nil {
		return 0, s.terminalErr
	}
	return s.readAtLocked(destination, offset)
}

func (s *WriteSession) readAtLocked(destination []byte, offset int64) (int, error) {
	if offset < 0 {
		return 0, errors.New("negative read offset")
	}
	if len(destination) == 0 {
		return 0, nil
	}
	if offset >= s.size {
		return 0, io.EOF
	}
	wanted := int64(len(destination))
	if remaining := s.size - offset; wanted > remaining {
		wanted = remaining
	}
	baseEnd := min(offset+wanted, s.record.Size)
	missingRanges := missingByteRanges(s.loadedRanges, offset, baseEnd)
	if offset < baseEnd {
		for _, missing := range missingRanges {
			plaintext, err := s.manager.backend.ReadAt(&s.record, missing.Start, int(missing.End-missing.Start))
			if err != nil {
				return 0, fmt.Errorf("fill sparse plaintext cache: %w", err)
			}
			written, err := s.cache.WriteAt(plaintext, missing.Start)
			if err != nil {
				return 0, err
			}
			if written != len(plaintext) {
				return 0, io.ErrShortWrite
			}
			s.loadedRanges = addByteRange(s.loadedRanges, missing.Start, missing.End)
		}
	}
	read, err := s.cache.ReadAt(destination[:wanted], offset)
	if err != nil && !errors.Is(err, io.EOF) {
		return read, err
	}
	if int64(len(destination)) > wanted {
		return read, io.EOF
	}
	return read, nil
}

func (s *WriteSession) WriteAt(data []byte, offset int64) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return 0, ErrHandleClosed
	}
	if s.terminalErr != nil {
		return 0, s.terminalErr
	}
	if err := s.manager.operationalError(); err != nil {
		return 0, err
	}
	if offset < 0 {
		return 0, errors.New("negative write offset")
	}
	if len(data) == 0 {
		return 0, nil
	}
	if err := s.beginTransactionLocked(); err != nil {
		return 0, err
	}
	written, err := s.cache.WriteAt(data, offset)
	if err == nil && written != len(data) {
		err = io.ErrShortWrite
	}
	if written > 0 {
		end := offset + int64(written)
		s.loadedRanges = addByteRange(s.loadedRanges, offset, end)
		s.dirtyRanges = addByteRange(s.dirtyRanges, offset, end)
		if end > s.size {
			s.size = end
		}
	}
	return written, err
}

func (s *WriteSession) Truncate(size int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return ErrHandleClosed
	}
	if s.terminalErr != nil {
		return s.terminalErr
	}
	if err := s.manager.operationalError(); err != nil {
		return err
	}
	if size < 0 {
		return errors.New("negative truncate size")
	}
	if size == s.size {
		return nil
	}
	if err := s.beginTransactionLocked(); err != nil {
		return err
	}
	oldSize := s.size
	if err := s.cache.Truncate(size); err != nil {
		return err
	}
	if size < oldSize {
		// Keep this zero override even while it lies beyond the current EOF:
		// a later grow in the same transaction must not resurrect base bytes.
		s.loadedRanges = addByteRange(s.loadedRanges, size, oldSize)
		s.dirtyRanges = addByteRange(s.dirtyRanges, size, oldSize)
	}
	s.size = size
	return nil
}

func (s *WriteSession) Size() (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return 0, ErrHandleClosed
	}
	if s.terminalErr != nil {
		return 0, s.terminalErr
	}
	return s.size, nil
}

func (s *WriteSession) Commit() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return ErrHandleClosed
	}
	if s.terminalErr != nil {
		return s.terminalErr
	}
	if err := s.manager.operationalError(); err != nil {
		return err
	}
	return s.commitLocked()
}

func (s *WriteSession) commitLocked() error {
	if s.terminalErr != nil {
		return s.terminalErr
	}
	if err := s.manager.operationalError(); err != nil {
		return err
	}
	if s.entry == nil {
		return nil
	}
	entry := s.entry
	if entry.State == walStateDiscarded {
		return s.discardConflictLocked(entry)
	}
	if !s.walReady {
		if err := s.manager.persist(entry); err != nil {
			return err
		}
		s.walReady = true
	}
	if entry.State == walStateDirty {
		if err := s.cache.Sync(); err != nil {
			return fmt.Errorf("sync plaintext cache: %w", err)
		}
		prepared := *entry
		prepared.Size = s.size
		prepared.DirtyRanges = append([]byteRange(nil), s.dirtyRanges...)
		prepared.State = walStatePrepared
		if err := s.manager.persist(&prepared); err != nil {
			return err
		}
		*entry = prepared
	}

	if entry.State == walStatePrepared {
		dek, err := s.manager.backend.fileDEK(&s.record)
		if err != nil {
			return err
		}
		err = s.uploadEncrypted(dek, entry)
		clearBytes(dek)
		if err != nil {
			return err
		}
		uploaded := *entry
		uploaded.State = walStateUploaded
		if err := s.manager.persist(&uploaded); err != nil {
			return err
		}
		*entry = uploaded
	}

	if err := s.manager.operationalError(); err != nil {
		return err
	}
	retiredThumbnailKey := s.record.ThumbnailKey
	switched, err := s.manager.repo.CommitGeneration(
		s.manager.backend.UserID(), entry.FileID, entry.BaseGeneration,
		entry.TargetObjectKey, entry.Size,
	)
	if err != nil {
		return fmt.Errorf("commit file generation: %w", err)
	}
	current, getErr := s.manager.repo.GetByID(s.manager.backend.UserID(), entry.FileID)
	if getErr != nil {
		return fmt.Errorf("read committed generation: %w", getErr)
	}
	if !switched && (current.Generation != entry.TargetGeneration || current.StorageKey() != entry.TargetObjectKey) {
		return s.discardConflictLocked(entry)
	}

	s.record = *current
	if err := s.manager.backend.purgeRetiredThumbnail(retiredThumbnailKey); err != nil {
		log.Printf("[dofs] deferred cleanup for retired thumbnail %q: %v", retiredThumbnailKey, err)
	}
	if err := s.manager.removeLocal(entry, false); err != nil {
		return fmt.Errorf("remove committed WAL: %w", err)
	}
	s.entry = nil
	s.walReady = false
	s.size = current.Size
	s.loadedRanges = nil
	s.dirtyRanges = nil
	if err := s.cache.Truncate(0); err != nil {
		return fmt.Errorf("clear committed plaintext cache: %w", err)
	}
	if err := s.cache.Truncate(current.Size); err != nil {
		return fmt.Errorf("resize sparse plaintext cache: %w", err)
	}
	if s.onCommit != nil {
		copy := s.record
		s.onCommit(&copy)
	}
	return nil
}

func (s *WriteSession) discardConflictLocked(entry *walEntry) error {
	if entry.State != walStateDiscarded {
		discarded := *entry
		discarded.State = walStateDiscarded
		if err := s.manager.persist(&discarded); err != nil {
			return fmt.Errorf("%w (persist discard state: %v)", ErrWriteConflict, err)
		}
		*entry = discarded
	}
	if err := s.manager.objects.DeleteObject(entry.TargetObjectKey); err != nil {
		return fmt.Errorf("%w (object cleanup failed: %v)", ErrWriteConflict, err)
	}
	if err := s.manager.removeLocal(entry, false); err != nil {
		return fmt.Errorf("%w (local cleanup failed: %v)", ErrWriteConflict, err)
	}
	s.entry = nil
	s.walReady = false
	s.terminalErr = ErrWriteConflict
	return ErrWriteConflict
}

func (s *WriteSession) uploadEncrypted(dek []byte, entry *walEntry) error {
	plaintext, err := newSessionPlainReader(s, entry.Size)
	if err != nil {
		return err
	}
	reader, writer := io.Pipe()
	encryptResult := make(chan error, 1)
	go func() {
		defer plaintext.Close()
		err := auth.EncryptStream(dek, plaintext, writer)
		_ = writer.CloseWithError(err)
		encryptResult <- err
	}()
	uploadErr := s.manager.objects.PutObject(entry.TargetObjectKey, reader, encryptedSize(entry.Size, auth.DefaultChunkSize))
	_ = reader.CloseWithError(uploadErr)
	encryptErr := <-encryptResult
	if encryptErr != nil {
		return fmt.Errorf("encrypt generation: %w", encryptErr)
	}
	if uploadErr != nil {
		return fmt.Errorf("upload generation: %w", uploadErr)
	}
	return nil
}

type sessionPlainReader struct {
	session   *WriteSession
	base      io.ReadCloser
	baseUntil int64
	offset    int64
	remaining int64
}

func newSessionPlainReader(session *WriteSession, size int64) (*sessionPlainReader, error) {
	reader := &sessionPlainReader{session: session, remaining: size}
	baseEnd := min(size, session.record.Size)
	missing := missingByteRanges(session.dirtyRanges, 0, baseEnd)
	if len(missing) == 0 {
		return reader, nil
	}
	reader.baseUntil = missing[len(missing)-1].End
	base, err := session.manager.backend.openPlaintextStream(&session.record, reader.baseUntil)
	if err != nil {
		return nil, fmt.Errorf("stream base generation: %w", err)
	}
	reader.base = base
	return reader, nil
}

func (r *sessionPlainReader) Read(destination []byte) (int, error) {
	if r.remaining == 0 {
		return 0, io.EOF
	}
	if int64(len(destination)) > r.remaining {
		destination = destination[:r.remaining]
	}
	wanted := int64(len(destination))
	clear(destination)
	if r.offset < r.baseUntil {
		baseBytes := min(wanted, r.baseUntil-r.offset)
		read, err := io.ReadFull(r.base, destination[:baseBytes])
		if err != nil {
			return read, fmt.Errorf("read base generation: %w", err)
		}
	}
	end := r.offset + wanted
	for _, dirty := range r.session.dirtyRanges {
		if dirty.End <= r.offset {
			continue
		}
		if dirty.Start >= end {
			break
		}
		start := max(dirty.Start, r.offset)
		dirtyEnd := min(dirty.End, end)
		read, err := r.session.cache.ReadAt(destination[start-r.offset:dirtyEnd-r.offset], start)
		if err != nil {
			return read, fmt.Errorf("read dirty plaintext cache: %w", err)
		}
		if int64(read) != dirtyEnd-start {
			return read, io.ErrUnexpectedEOF
		}
	}
	r.offset = end
	r.remaining -= wanted
	return int(wanted), nil
}

func (r *sessionPlainReader) Close() error {
	if r.base == nil {
		return nil
	}
	err := r.base.Close()
	r.base = nil
	return err
}

// Close commits pending writes. On a commit error it deliberately retains the
// cache and WAL for recovery; successful and clean sessions remove plaintext.
func (s *WriteSession) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	if err := s.commitLocked(); err != nil {
		_ = s.cache.Close()
		s.closed = true
		if errors.Is(err, ErrWriteConflict) && s.entry == nil {
			_ = os.Remove(s.cachePath)
		}
		return err
	}
	if err := s.cache.Close(); err != nil {
		s.closed = true
		return err
	}
	s.closed = true
	if err := os.Remove(s.cachePath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

// abort removes a session that failed before it was returned to the kernel.
func (s *WriteSession) abort() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return
	}
	_ = s.cache.Close()
	if s.entry != nil {
		if s.entry.State == walStateUploaded {
			_ = s.manager.objects.DeleteObject(s.entry.TargetObjectKey)
		}
		_ = s.manager.removeLocal(s.entry, false)
	}
	_ = os.Remove(s.cachePath)
	s.closed = true
}
