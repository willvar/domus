package dofs

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"testing"

	"gorm.io/gorm"

	"domus/internal/auth"
	"domus/internal/model"
)

type memoryRangeStore struct {
	mu          sync.Mutex
	objects     map[string][]byte
	ranges      [][2]int64
	puts        []string
	deletes     []string
	failPuts    int
	failDeletes int
}

func (s *memoryRangeStore) GetObjectRange(key string, start, end int64) (io.ReadCloser, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	data, ok := s.objects[key]
	if !ok {
		return nil, fmt.Errorf("object %q not found", key)
	}
	if start < 0 || end < start || end >= int64(len(data)) {
		return nil, fmt.Errorf("invalid range [%d,%d] for %d-byte object", start, end, len(data))
	}
	s.ranges = append(s.ranges, [2]int64{start, end})
	return io.NopCloser(bytes.NewReader(data[start : end+1])), nil
}

func (s *memoryRangeStore) PutObject(key string, reader io.Reader, size int64) error {
	s.mu.Lock()
	if s.failPuts > 0 {
		s.failPuts--
		s.mu.Unlock()
		return errors.New("injected object upload failure")
	}
	s.mu.Unlock()

	data, err := io.ReadAll(io.LimitReader(reader, size+1))
	if err != nil {
		return err
	}
	if int64(len(data)) != size {
		return fmt.Errorf("uploaded %d bytes, want %d", len(data), size)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.objects[key] = data
	s.puts = append(s.puts, key)
	return nil
}

func (s *memoryRangeStore) DeleteObject(key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.failDeletes > 0 {
		s.failDeletes--
		return errors.New("injected object deletion failure")
	}
	delete(s.objects, key)
	s.deletes = append(s.deletes, key)
	return nil
}

func (s *memoryRangeStore) DeleteObjectPrefix(prefix string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.failDeletes > 0 {
		s.failDeletes--
		return errors.New("injected object-prefix deletion failure")
	}
	for key := range s.objects {
		if strings.HasPrefix(key, prefix) {
			delete(s.objects, key)
			s.deletes = append(s.deletes, key)
		}
	}
	return nil
}

func newTestBackend(t *testing.T, plaintext []byte) (*Backend, *memoryRangeStore, *model.FileRecord) {
	t.Helper()
	const (
		userID   = "user-1"
		username = "alice"
		filePath = "alice/home/alice/data.bin"
	)

	repos := model.NewMemRepos(nil)
	for _, directory := range []struct {
		path string
		name string
	}{
		{path: "alice/home/", name: "home"},
		{path: "alice/home/alice/", name: "alice"},
	} {
		if err := repos.Files.Upsert(userID, directory.path, directory.name, true, 0, "", ""); err != nil {
			t.Fatal(err)
		}
	}

	kek := bytes.Repeat([]byte{0x2a}, auth.DEKSize)
	dek := bytes.Repeat([]byte{0x5c}, auth.DEKSize)
	wrapped, err := auth.WrapDEK(kek, dek)
	if err != nil {
		t.Fatal(err)
	}
	var ciphertext bytes.Buffer
	if err := auth.EncryptStream(dek, bytes.NewReader(plaintext), &ciphertext); err != nil {
		t.Fatal(err)
	}
	if err := repos.Files.Upsert(
		userID, filePath, "data.bin", false, int64(len(plaintext)), "application/octet-stream", "",
		model.UpsertFileOpts{WrappedDEK: fmt.Sprintf("%x", wrapped)},
	); err != nil {
		t.Fatal(err)
	}
	record, err := repos.Files.Get(userID, filePath)
	if err != nil {
		t.Fatal(err)
	}

	objects := &memoryRangeStore{objects: map[string][]byte{filePath: ciphertext.Bytes()}}
	backend, err := NewBackend(userID, username, kek, repos.Files, objects)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(backend.Close)
	return backend, objects, record
}

func TestNewBackendRejectsTraversalScopeComponents(t *testing.T) {
	repos := model.NewMemRepos(nil)
	objects := &memoryRangeStore{objects: make(map[string][]byte)}
	kek := bytes.Repeat([]byte{1}, auth.DEKSize)
	tests := []struct {
		userID   string
		username string
	}{
		{userID: ".", username: "alice"},
		{userID: "..", username: "alice"},
		{userID: " user-1", username: "alice"},
		{userID: "user-1", username: "."},
		{userID: "user-1", username: ".."},
	}
	for _, test := range tests {
		if backend, err := NewBackend(test.userID, test.username, kek, repos.Files, objects); err == nil {
			backend.Close()
			t.Fatalf("accepted scope userID=%q username=%q", test.userID, test.username)
		}
	}
}

func TestListChildrenUsesFileFirstForLegacyCrossTypeCollision(t *testing.T) {
	backend, _, file := newTestBackend(t, []byte("legacy collision"))
	repository := backend.files.(model.FileRepo)
	if err := repository.Upsert(backend.UserID(), file.Path+"/", file.Name, true, 0, "", ""); err != nil {
		t.Fatal(err)
	}
	records, err := backend.ListChildren(file.Parent)
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 1 || records[0].ID != file.ID || records[0].IsDir {
		t.Fatalf("legacy collision listing = %+v, want file inode %d", records, file.ID)
	}
}

func TestBackendReadAtDecryptsRequestedRanges(t *testing.T) {
	plaintext := make([]byte, 2*auth.DefaultChunkSize+317)
	for i := range plaintext {
		plaintext[i] = byte((i*31 + 7) % 251)
	}
	backend, objectStore, record := newTestBackend(t, plaintext)

	tests := []struct {
		name   string
		offset int64
		length int
	}{
		{name: "first bytes", offset: 0, length: 29},
		{name: "inside one chunk", offset: 1024, length: 4096},
		{name: "crosses chunk boundary", offset: auth.DefaultChunkSize - 17, length: 80},
		{name: "multiple chunks", offset: 13, length: auth.DefaultChunkSize + 900},
		{name: "clamps at eof", offset: int64(len(plaintext) - 41), length: 4096},
		{name: "at eof", offset: int64(len(plaintext)), length: 128},
		{name: "past eof", offset: int64(len(plaintext) + 10), length: 128},
		{name: "empty request", offset: 0, length: 0},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := backend.ReadAt(record, test.offset, test.length)
			if err != nil {
				t.Fatalf("ReadAt: %v", err)
			}
			start := min(test.offset, int64(len(plaintext)))
			end := min(start+int64(test.length), int64(len(plaintext)))
			if test.length == 0 {
				end = start
			}
			want := plaintext[start:end]
			if !bytes.Equal(got, want) {
				t.Fatalf("plaintext mismatch: got %d bytes, want %d", len(got), len(want))
			}
		})
	}

	objectStore.mu.Lock()
	defer objectStore.mu.Unlock()
	if len(objectStore.ranges) == 0 {
		t.Fatal("expected encrypted range requests")
	}
	for _, requested := range objectStore.ranges {
		if requested[0] < encryptedHeaderSize {
			t.Fatalf("range unexpectedly included encryption header: %v", requested)
		}
	}
}

func TestBackendNamespaceLookupAndListing(t *testing.T) {
	backend, _, record := newTestBackend(t, []byte("hello DOFS"))

	home, err := backend.LookupChild(backend.RootKey(), "home")
	if err != nil {
		t.Fatalf("lookup home: %v", err)
	}
	if !home.IsDir || home.Path != "alice/home/" {
		t.Fatalf("unexpected home record: %+v", home)
	}

	alice, err := backend.LookupChild(home.Path, "alice")
	if err != nil {
		t.Fatalf("lookup user home: %v", err)
	}
	children, err := backend.ListChildren(alice.Path)
	if err != nil {
		t.Fatalf("list user home: %v", err)
	}
	if len(children) != 1 || children[0].Path != record.Path {
		t.Fatalf("unexpected children: %+v", children)
	}

	if _, err := backend.LookupChild(alice.Path, "../root"); err != ErrInvalidName {
		t.Fatalf("expected invalid-name error, got %v", err)
	}
	if _, err := backend.ListChildren("bob/"); err != ErrNotFound {
		t.Fatalf("expected namespace rejection, got %v", err)
	}
}

func TestBackendRejectsWrongOwnerRecord(t *testing.T) {
	backend, _, record := newTestBackend(t, []byte("secret"))
	record.UserID = "another-user"
	if _, err := backend.ReadAt(record, 0, 6); err != ErrNotReady {
		t.Fatalf("expected ownership rejection, got %v", err)
	}
}

func TestBackendDEKCacheIsBoundedByStableInode(t *testing.T) {
	backend, _, record := newTestBackend(t, []byte("secret"))
	first, err := backend.fileDEK(record)
	if err != nil {
		t.Fatal(err)
	}
	clearBytes(first)
	nextGeneration := *record
	nextGeneration.ObjectKey = model.DOFSGenerationObjectKey(backend.UserID(), record.ID, record.Generation+1, "next")
	second, err := backend.fileDEK(&nextGeneration)
	if err != nil {
		t.Fatal(err)
	}
	clearBytes(second)
	backend.dekMu.Lock()
	cacheEntries := len(backend.dekCache)
	backend.dekMu.Unlock()
	if cacheEntries != 1 {
		t.Fatalf("DEK cache entries = %d, want one per stable inode", cacheEntries)
	}
	if backend.validOwnedObjectKey("bob/home/bob/secret") {
		t.Fatal("cross-user object key was accepted")
	}
}

func TestBackendWritableNamespaceCreateRenameAndRemove(t *testing.T) {
	backend, objects, _ := newTestBackend(t, []byte("existing"))
	repository := enableTestWriteback(t, backend, objects, t.TempDir())
	parent := "alice/home/alice/"

	directory, err := backend.CreateDirectory(parent, "projects")
	if err != nil {
		t.Fatalf("create directory: %v", err)
	}
	if !directory.IsDir || directory.Path != parent+"projects/" || directory.ID <= 0 {
		t.Fatalf("unexpected directory: %+v", directory)
	}
	file, err := backend.CreateFile(directory.Path, "empty.txt")
	if err != nil {
		t.Fatalf("create file: %v", err)
	}
	if file.Generation != 1 || file.Status != "ready" || file.Size != 0 || !strings.HasPrefix(file.StorageKey(), backend.ObjectRoot()) {
		t.Fatalf("unexpected new file: %+v", file)
	}
	if _, err := backend.CreateFile(directory.Path, "empty.txt"); !errors.Is(err, model.ErrFileExists) {
		t.Fatalf("duplicate create error = %v, want ErrFileExists", err)
	}

	renamed, err := backend.RenameChild(parent, "projects", parent, "archive", false)
	if err != nil {
		t.Fatalf("rename directory: %v", err)
	}
	if renamed.Path != parent+"archive/" {
		t.Fatalf("renamed path = %q", renamed.Path)
	}
	movedFile, err := backend.LookupChild(renamed.Path, "empty.txt")
	if err != nil {
		t.Fatalf("lookup moved child: %v", err)
	}
	if movedFile.ID != file.ID || movedFile.StorageKey() != file.StorageKey() {
		t.Fatalf("directory rename changed inode/object identity: before=%+v after=%+v", file, movedFile)
	}
	if err := backend.RemoveChild(parent, "archive", true); !errors.Is(err, model.ErrDirectoryNotEmpty) {
		t.Fatalf("remove non-empty directory error = %v", err)
	}
	if err := backend.RemoveChild(renamed.Path, "empty.txt", false); err != nil {
		t.Fatalf("unlink file: %v", err)
	}
	if _, err := repository.GetByID(backend.UserID(), file.ID); !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("purged file metadata error = %v, want record not found", err)
	}
	if err := backend.RemoveChild(parent, "archive", true); err != nil {
		t.Fatalf("remove empty directory: %v", err)
	}
}

func TestUnlinkDefersFailedPhysicalCleanupWithoutReversingSuccess(t *testing.T) {
	backend, objects, record := newTestBackend(t, []byte("deferred cleanup"))
	repository := enableTestWriteback(t, backend, objects, t.TempDir())
	objects.mu.Lock()
	objects.failDeletes = 1
	objects.mu.Unlock()

	if err := backend.RemoveChild(record.Parent, record.Name, false); err != nil {
		t.Fatalf("unlink reported a post-commit cleanup error: %v", err)
	}
	if _, err := repository.Get(backend.UserID(), record.Path); !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("unlinked path lookup error = %v", err)
	}
	tombstone, err := repository.GetByID(backend.UserID(), record.ID)
	if err != nil || tombstone.Status != "deleted" {
		t.Fatalf("retained tombstone = %+v, err=%v", tombstone, err)
	}
	if err := backend.cleanupIncompleteNamespace(); err != nil {
		t.Fatal(err)
	}
	if _, err := repository.GetByID(backend.UserID(), record.ID); !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("deferred tombstone cleanup error = %v", err)
	}
}

func TestBackendUnlinkDefersObjectPurgeUntilOpenHandleRelease(t *testing.T) {
	backend, objects, record := newTestBackend(t, []byte("open-unlink-data"))
	repository := enableTestWriteback(t, backend, objects, t.TempDir())
	if err := backend.retainFile(record.ID); err != nil {
		t.Fatalf("retain file: %v", err)
	}
	if err := backend.RemoveChild(record.Parent, record.Name, false); err != nil {
		t.Fatalf("unlink retained file: %v", err)
	}
	tombstone, err := repository.GetByID(backend.UserID(), record.ID)
	if err != nil {
		t.Fatalf("get tombstone: %v", err)
	}
	if tombstone.Status != "deleted" || !strings.HasPrefix(tombstone.Path, model.DOFSDeletedRoot(backend.UserID())) {
		t.Fatalf("unexpected tombstone: %+v", tombstone)
	}
	got, err := backend.ReadAt(record, 0, int(record.Size))
	if err != nil || string(got) != "open-unlink-data" {
		t.Fatalf("read retained inode: %q, %v", got, err)
	}
	objects.mu.Lock()
	_, existsBeforeRelease := objects.objects[record.StorageKey()]
	objects.mu.Unlock()
	if !existsBeforeRelease {
		t.Fatal("object was deleted while a handle remained open")
	}
	if err := backend.releaseFile(record.ID); err != nil {
		t.Fatalf("release file: %v", err)
	}
	if _, err := repository.GetByID(backend.UserID(), record.ID); !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("metadata after final release error = %v", err)
	}
	objects.mu.Lock()
	_, existsAfterRelease := objects.objects[record.StorageKey()]
	objects.mu.Unlock()
	if existsAfterRelease {
		t.Fatal("object survived final handle release")
	}
}

func TestBackendThumbnailPurgeHonorsOpenHandle(t *testing.T) {
	backend, objects, source := newTestBackend(t, []byte("source"))
	enableTestWriteback(t, backend, objects, t.TempDir())
	repository := backend.files.(model.FileRepo)
	thumbnailPath := source.Parent + ".user/derived/preview.jpg"
	thumbnailKey := model.DOFSGenerationObjectKey(backend.UserID(), source.ID+1, 1, "preview")
	if err := repository.Upsert(
		backend.UserID(), thumbnailPath, "preview.jpg", false, 7, "image/jpeg", "",
		model.UpsertFileOpts{WrappedDEK: "wrapped-thumbnail", ObjectKey: thumbnailKey},
	); err != nil {
		t.Fatal(err)
	}
	thumbnail, err := repository.Get(backend.UserID(), thumbnailPath)
	if err != nil {
		t.Fatal(err)
	}
	objects.mu.Lock()
	objects.objects[thumbnailKey] = []byte("ciphertext")
	objects.mu.Unlock()
	if err := repository.UpdateThumbnail(backend.UserID(), source.Path, thumbnailKey, "wrapped-thumbnail", 1, 1, 0); err != nil {
		t.Fatal(err)
	}
	if err := backend.retainFile(thumbnail.ID); err != nil {
		t.Fatal(err)
	}
	if err := backend.RemoveChild(source.Parent, source.Name, false); err != nil {
		t.Fatal(err)
	}
	retired, err := repository.GetByID(backend.UserID(), thumbnail.ID)
	if err != nil || retired.Status != "deleted" {
		t.Fatalf("retired thumbnail = %+v, %v", retired, err)
	}
	objects.mu.Lock()
	_, existsWhileOpen := objects.objects[thumbnailKey]
	objects.mu.Unlock()
	if !existsWhileOpen {
		t.Fatal("thumbnail object was deleted while its file handle remained open")
	}
	if err := backend.releaseFile(thumbnail.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := repository.GetByID(backend.UserID(), thumbnail.ID); !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("thumbnail metadata after release error = %v", err)
	}
	objects.mu.Lock()
	_, existsAfterRelease := objects.objects[thumbnailKey]
	objects.mu.Unlock()
	if existsAfterRelease {
		t.Fatal("thumbnail object survived its final file handle")
	}
}

func TestBackendWriteRetiresAttachedThumbnail(t *testing.T) {
	backend, objects, source := newTestBackend(t, []byte("before"))
	enableTestWriteback(t, backend, objects, t.TempDir())
	repository := backend.files.(model.FileRepo)
	thumbnailPath := source.Parent + ".user/derived/write-preview.jpg"
	thumbnailKey := model.DOFSGenerationObjectKey(backend.UserID(), source.ID+1, 1, "write-preview")
	if err := repository.Upsert(
		backend.UserID(), thumbnailPath, "write-preview.jpg", false, 7, "image/jpeg", "",
		model.UpsertFileOpts{WrappedDEK: "wrapped-thumbnail", ObjectKey: thumbnailKey},
	); err != nil {
		t.Fatal(err)
	}
	thumbnail, err := repository.Get(backend.UserID(), thumbnailPath)
	if err != nil {
		t.Fatal(err)
	}
	objects.mu.Lock()
	objects.objects[thumbnailKey] = []byte("ciphertext")
	objects.mu.Unlock()
	if err := repository.UpdateThumbnail(backend.UserID(), source.Path, thumbnailKey, "wrapped-thumbnail", 1, 1, 0); err != nil {
		t.Fatal(err)
	}
	current, err := repository.GetByID(backend.UserID(), source.ID)
	if err != nil {
		t.Fatal(err)
	}
	session, err := backend.OpenWrite(current, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := session.WriteAt([]byte("after"), 0); err != nil {
		t.Fatal(err)
	}
	if err := session.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := repository.GetByID(backend.UserID(), thumbnail.ID); !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("thumbnail metadata after source write error = %v", err)
	}
	objects.mu.Lock()
	_, exists := objects.objects[thumbnailKey]
	objects.mu.Unlock()
	if exists {
		t.Fatal("thumbnail object survived source generation change")
	}
}

func TestBackendRenameNoReplaceAndAtomicReplacement(t *testing.T) {
	backend, objects, _ := newTestBackend(t, []byte("base"))
	enableTestWriteback(t, backend, objects, t.TempDir())
	parent := "alice/home/alice/"
	first, err := backend.CreateFile(parent, "first.txt")
	if err != nil {
		t.Fatal(err)
	}
	second, err := backend.CreateFile(parent, "second.txt")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := backend.RenameChild(parent, first.Name, parent, second.Name, true); !errors.Is(err, model.ErrFileExists) {
		t.Fatalf("no-replace rename error = %v", err)
	}
	renamed, err := backend.RenameChild(parent, first.Name, parent, second.Name, false)
	if err != nil {
		t.Fatal(err)
	}
	if renamed.ID != first.ID || renamed.Path != second.Path {
		t.Fatalf("replacement did not preserve source inode: %+v", renamed)
	}
	if _, err := backend.namespace.GetByID(backend.UserID(), second.ID); !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("replaced inode was not reclaimed: %v", err)
	}
}
