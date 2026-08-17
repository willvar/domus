package dofs

import (
	"bytes"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"sync"

	"gorm.io/gorm"

	"domus/internal/auth"
	"domus/internal/model"
)

const encryptedHeaderSize int64 = 5

var (
	ErrInvalidName = errors.New("invalid file name")
	ErrNotFound    = errors.New("file not found")
	ErrNotReady    = errors.New("file not ready")
	ErrIsDirectory = errors.New("cannot read a directory")
)

// FileRepository is the metadata subset needed by the read-only DOFS mount.
type FileRepository interface {
	Get(userID, path string) (*model.FileRecord, error)
	ListDirectChildren(userID, parent string) ([]model.FileRecord, error)
}

// RangeObjectStore is the object-storage capability needed for random reads.
// start and end are inclusive ciphertext offsets.
type RangeObjectStore interface {
	GetObjectRange(key string, start, end int64) (io.ReadCloser, error)
}

// GenerationRepository provides the compare-and-swap metadata operations used
// by writable mounts.
type GenerationRepository interface {
	FileRepository
	GetByID(userID string, id int64) (*model.FileRecord, error)
	GetByStorageKey(userID, objectKey string) (*model.FileRecord, error)
	CommitGeneration(userID string, id, expectedGeneration int64, objectKey string, size int64) (bool, error)
}

// NamespaceRepository provides the transactional metadata primitives needed
// for Unix directory-entry operations on writable mounts.
type NamespaceRepository interface {
	GenerationRepository
	CreateDOFSNode(userID, path, name, wrappedDEK string, isDir bool) (*model.FileRecord, error)
	FinalizeDOFSFile(userID string, id int64, objectKey string) (bool, error)
	AbortDOFSFile(userID string, id int64) error
	RemoveDOFSNode(userID, path string, isDir bool) (*model.FileRecord, error)
	RenameDOFSNode(userID, oldPath, newPath, newName string, isDir, replace bool) (*model.FileRecord, *model.FileRecord, error)
	ListCreatingDOFSNodes(userID string) ([]model.FileRecord, error)
	ListDeletedDOFSNodes(userID string) ([]model.FileRecord, error)
	PurgeDeletedDOFSNode(userID string, id int64) (bool, error)
	AcquireDOFSMountLease(userID string) (io.Closer, error)
}

// GenerationObjectStore can publish immutable encrypted generations and clean
// up an uncommitted upload after a CAS conflict.
type GenerationObjectStore interface {
	RangeObjectStore
	PutObject(key string, reader io.Reader, size int64) error
	DeleteObject(key string) error
	DeleteObjectPrefix(prefix string) error
}

type cachedDEK struct {
	wrapped string
	dek     []byte
}

// Backend exposes one user's encrypted object namespace as plaintext file
// operations. It is deliberately user-scoped so a DOFS worker never needs to
// resolve paths or keys for another user.
type Backend struct {
	userID     string
	username   string
	rootKey    string
	objectRoot string
	kek        []byte
	files      FileRepository
	objects    RangeObjectStore
	dekMu      sync.Mutex
	dekCache   map[int64]*cachedDEK // stable inode ID -> decrypted file key
	writeback  *WritebackManager
	namespace  NamespaceRepository
	genObjects GenerationObjectStore
	handleMu   sync.Mutex
	openFiles  map[int64]int
}

func NewBackend(userID, username string, kek []byte, files FileRepository, objects RangeObjectStore) (*Backend, error) {
	if strings.TrimSpace(userID) != userID || !validComponent(userID) {
		return nil, errors.New("DOFS user ID is required")
	}
	if !validComponent(username) {
		return nil, errors.New("DOFS username is invalid")
	}
	if len(kek) != auth.DEKSize {
		return nil, fmt.Errorf("DOFS KEK must be %d bytes", auth.DEKSize)
	}
	if files == nil {
		return nil, errors.New("DOFS file repository is required")
	}
	if objects == nil {
		return nil, errors.New("DOFS range object store is required")
	}

	return &Backend{
		userID:     userID,
		username:   username,
		rootKey:    username + "/",
		objectRoot: model.DOFSObjectRoot(userID),
		kek:        append([]byte(nil), kek...),
		files:      files,
		objects:    objects,
		dekCache:   make(map[int64]*cachedDEK),
		openFiles:  make(map[int64]int),
	}, nil
}

func (b *Backend) UserID() string     { return b.userID }
func (b *Backend) Username() string   { return b.username }
func (b *Backend) RootKey() string    { return b.rootKey }
func (b *Backend) ObjectRoot() string { return b.objectRoot }
func (b *Backend) Writable() bool     { return b.writeback != nil }

// LeaseLost is non-nil for production writable mounts backed by a health-
// checkable distributed lease. The channel closes when that lease's database
// session disappears; callers should stop exposing the mount immediately.
func (b *Backend) LeaseLost() <-chan struct{} {
	if b.writeback == nil {
		return nil
	}
	return b.writeback.LeaseLost()
}

func (b *Backend) LeaseError() error {
	if b.writeback == nil {
		return nil
	}
	return b.writeback.LeaseError()
}

// Close removes plaintext key material from the worker's caches on unmount.
func (b *Backend) Close() {
	if b.writeback != nil {
		b.writeback.Close()
	}
	b.dekMu.Lock()
	defer b.dekMu.Unlock()
	clearBytes(b.kek)
	for inodeID, entry := range b.dekCache {
		clearBytes(entry.dek)
		delete(b.dekCache, inodeID)
	}
}

func clearBytes(data []byte) {
	for i := range data {
		data[i] = 0
	}
}

func validComponent(name string) bool {
	return name != "" && name != "." && name != ".." && !strings.ContainsAny(name, "/\x00")
}

func (b *Backend) validRecord(record *model.FileRecord) bool {
	if record == nil {
		return false
	}
	if record.UserID != b.userID || !strings.HasPrefix(record.Path, b.rootKey) || record.Status != "ready" {
		return false
	}
	return b.validStorageRecord(record)
}

func (b *Backend) validOwnedObjectKey(objectKey string) bool {
	return strings.HasPrefix(objectKey, b.rootKey) || strings.HasPrefix(objectKey, b.objectRoot)
}

func (b *Backend) validStorageRecord(record *model.FileRecord) bool {
	if record == nil || record.ID <= 0 || record.UserID != b.userID || (record.Status != "ready" && record.Status != "deleted") {
		return false
	}
	return b.validOwnedObjectKey(record.StorageKey())
}

// LookupChild resolves one direct child below a physical directory key.
func (b *Backend) LookupChild(parentKey, name string) (*model.FileRecord, error) {
	if !validComponent(name) {
		return nil, ErrInvalidName
	}
	if !b.validDirectoryKey(parentKey) {
		return nil, ErrNotFound
	}

	filePath := parentKey + name
	record, err := b.files.Get(b.userID, filePath)
	if err == nil {
		if b.validRecord(record) && !record.IsDir && record.Parent == parentKey {
			return record, nil
		}
		return nil, ErrNotFound
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, fmt.Errorf("lookup file metadata: %w", err)
	}

	dirPath := filePath + "/"
	record, err = b.files.Get(b.userID, dirPath)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("lookup directory metadata: %w", err)
	}
	if !b.validRecord(record) || !record.IsDir || record.Parent != parentKey {
		return nil, ErrNotFound
	}
	return record, nil
}

func (b *Backend) validDirectoryKey(key string) bool {
	return strings.HasPrefix(key, b.rootKey) && strings.HasSuffix(key, "/")
}

// ListChildren returns ready records that are strictly inside the requested
// user directory. The repository already applies user_id filtering; the extra
// checks keep the mount boundary explicit if an implementation regresses.
func (b *Backend) ListChildren(directoryKey string) ([]model.FileRecord, error) {
	if !b.validDirectoryKey(directoryKey) {
		return nil, ErrNotFound
	}
	records, err := b.files.ListDirectChildren(b.userID, directoryKey)
	if err != nil {
		return nil, fmt.Errorf("list directory metadata: %w", err)
	}

	byName := make(map[string]model.FileRecord, len(records))
	for i := range records {
		record := records[i]
		if !b.validRecord(&record) || record.Parent != directoryKey || !validComponent(record.Name) {
			continue
		}
		existing, duplicate := byName[record.Name]
		if !duplicate || (existing.IsDir && !record.IsDir) || (existing.IsDir == record.IsDir && record.ID < existing.ID) {
			// Historical databases could contain both "name" and "name/".
			// Lookup has always checked the file spelling first, so Readdir uses
			// the same deterministic compatibility rule.
			byName[record.Name] = record
		}
	}
	filtered := make([]model.FileRecord, 0, len(byName))
	for _, record := range byName {
		filtered = append(filtered, record)
	}
	sort.Slice(filtered, func(i, j int) bool {
		if filtered[i].IsDir != filtered[j].IsDir {
			return filtered[i].IsDir
		}
		return filtered[i].Name < filtered[j].Name
	})
	return filtered, nil
}

// ReadAt returns a plaintext range from an encrypted OSS object. Empty reads
// and reads beyond EOF succeed with an empty result, matching read(2).
func (b *Backend) ReadAt(record *model.FileRecord, offset int64, length int) ([]byte, error) {
	if !b.validStorageRecord(record) {
		return nil, ErrNotReady
	}
	if record.IsDir {
		return nil, ErrIsDirectory
	}
	if offset < 0 || length < 0 {
		return nil, errors.New("invalid read range")
	}
	if length == 0 || offset >= record.Size {
		return []byte{}, nil
	}
	if record.WrappedDEK == "" {
		return nil, errors.New("file has no wrapped DEK")
	}

	wanted := int64(length)
	if remaining := record.Size - offset; wanted > remaining {
		wanted = remaining
	}
	plainEnd := offset + wanted - 1
	chunkSize := int64(auth.DefaultChunkSize)
	startChunk := offset / chunkSize
	endChunk := plainEnd / chunkSize
	cipherChunkSize := chunkSize + auth.NonceSize + auth.TagSize

	cipherStart := encryptedHeaderSize + startChunk*cipherChunkSize
	cipherEnd := encryptedHeaderSize + (endChunk+1)*cipherChunkSize - 1
	totalCipherSize := encryptedSize(record.Size, chunkSize)
	if cipherEnd >= totalCipherSize {
		cipherEnd = totalCipherSize - 1
	}

	reader, err := b.objects.GetObjectRange(record.StorageKey(), cipherStart, cipherEnd)
	if err != nil {
		return nil, fmt.Errorf("read encrypted object range: %w", err)
	}
	defer func() { _ = reader.Close() }()

	dek, err := b.fileDEK(record)
	if err != nil {
		return nil, err
	}
	defer clearBytes(dek)
	trimStart := offset - startChunk*chunkSize
	trimEnd := plainEnd - startChunk*chunkSize
	var plaintext bytes.Buffer
	plaintext.Grow(int(wanted))
	if err := auth.DecryptRange(dek, reader, &plaintext, int(chunkSize), uint64(startChunk), trimStart, trimEnd); err != nil {
		return nil, fmt.Errorf("decrypt object range: %w", err)
	}
	if int64(plaintext.Len()) != wanted {
		return nil, fmt.Errorf("short plaintext read: got %d bytes, want %d", plaintext.Len(), wanted)
	}
	return plaintext.Bytes(), nil
}

type plaintextObjectStream struct {
	reader *io.PipeReader
	object io.ReadCloser
	once   sync.Once
}

func (s *plaintextObjectStream) Read(destination []byte) (int, error) {
	return s.reader.Read(destination)
}

func (s *plaintextObjectStream) Close() error {
	var closeErr error
	s.once.Do(func() {
		_ = s.reader.Close()
		closeErr = s.object.Close()
	})
	return closeErr
}

// openPlaintextStream decrypts the base object sequentially without creating
// a complete plaintext copy. plainLimit controls the last authenticated
// ciphertext chunk fetched from OSS; callers may stop reading within it.
func (b *Backend) openPlaintextStream(record *model.FileRecord, plainLimit int64) (io.ReadCloser, error) {
	if !b.validStorageRecord(record) {
		return nil, ErrNotReady
	}
	if record.IsDir {
		return nil, ErrIsDirectory
	}
	if plainLimit <= 0 || plainLimit > record.Size {
		return nil, errors.New("invalid plaintext stream limit")
	}
	if record.WrappedDEK == "" {
		return nil, errors.New("file has no wrapped DEK")
	}

	chunkSize := int64(auth.DefaultChunkSize)
	cipherChunkSize := chunkSize + auth.NonceSize + auth.TagSize
	endChunk := (plainLimit - 1) / chunkSize
	cipherEnd := encryptedHeaderSize + (endChunk+1)*cipherChunkSize - 1
	if totalCipherSize := encryptedSize(record.Size, chunkSize); cipherEnd >= totalCipherSize {
		cipherEnd = totalCipherSize - 1
	}
	object, err := b.objects.GetObjectRange(record.StorageKey(), 0, cipherEnd)
	if err != nil {
		return nil, fmt.Errorf("open encrypted object stream: %w", err)
	}
	dek, err := b.fileDEK(record)
	if err != nil {
		_ = object.Close()
		return nil, err
	}

	reader, writer := io.Pipe()
	stream := &plaintextObjectStream{reader: reader, object: object}
	go func() {
		err := auth.DecryptStream(dek, object, writer)
		clearBytes(dek)
		_ = object.Close()
		_ = writer.CloseWithError(err)
	}()
	return stream, nil
}

func encryptedSize(plainSize, chunkSize int64) int64 {
	if plainSize <= 0 {
		return encryptedHeaderSize
	}
	chunks := (plainSize + chunkSize - 1) / chunkSize
	return encryptedHeaderSize + plainSize + chunks*(auth.NonceSize+auth.TagSize)
}

func (b *Backend) fileDEK(record *model.FileRecord) ([]byte, error) {
	b.dekMu.Lock()
	defer b.dekMu.Unlock()

	if entry, ok := b.dekCache[record.ID]; ok {
		if entry.wrapped == record.WrappedDEK {
			return append([]byte(nil), entry.dek...), nil
		}
	}

	wrapped, err := hex.DecodeString(record.WrappedDEK)
	if err != nil {
		return nil, fmt.Errorf("decode wrapped DEK: %w", err)
	}
	dek, err := auth.UnwrapDEK(b.kek, wrapped)
	if err != nil {
		return nil, fmt.Errorf("unwrap file DEK: %w", err)
	}
	entry := &cachedDEK{wrapped: record.WrappedDEK, dek: dek}
	if previous, loaded := b.dekCache[record.ID]; loaded {
		clearBytes(previous.dek)
	}
	b.dekCache[record.ID] = entry
	return append([]byte(nil), entry.dek...), nil
}
