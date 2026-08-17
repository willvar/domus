package dofs

import (
	"bytes"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"strings"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"domus/internal/auth"
	"domus/internal/model"
)

var ErrNotDirectory = errors.New("not a directory")

func (b *Backend) namespaceReady() error {
	if b.namespace == nil || b.genObjects == nil || b.writeback == nil {
		return ErrReadOnly
	}
	if err := b.writeback.operationalError(); err != nil {
		return err
	}
	return nil
}

func (b *Backend) ensureDirectory(directoryKey string) error {
	if !b.validDirectoryKey(directoryKey) {
		return ErrNotFound
	}
	if directoryKey == b.rootKey {
		return nil
	}
	record, err := b.files.Get(b.userID, directoryKey)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return ErrNotFound
		}
		return err
	}
	if !b.validRecord(record) {
		return ErrNotFound
	}
	if !record.IsDir {
		return ErrNotDirectory
	}
	return nil
}

func childPath(parentKey, name string, directory bool) string {
	filePath := parentKey + name
	if directory {
		filePath += "/"
	}
	return filePath
}

// CreateDirectory atomically creates a metadata-only directory entry. DOFS
// does not need zero-byte directory marker objects in OSS.
func (b *Backend) CreateDirectory(parentKey, name string) (*model.FileRecord, error) {
	if err := b.namespaceReady(); err != nil {
		return nil, err
	}
	if !validComponent(name) {
		return nil, ErrInvalidName
	}
	if err := b.ensureDirectory(parentKey); err != nil {
		return nil, err
	}
	return b.namespace.CreateDOFSNode(b.userID, childPath(parentKey, name, true), name, "", true)
}

// CreateFile publishes a valid encrypted empty generation before making the
// namespace entry visible. A crash can leave only a hidden "creating" row and
// an object below that inode's private generation prefix; mount recovery
// removes both.
func (b *Backend) CreateFile(parentKey, name string) (*model.FileRecord, error) {
	if err := b.namespaceReady(); err != nil {
		return nil, err
	}
	if !validComponent(name) {
		return nil, ErrInvalidName
	}
	if err := b.ensureDirectory(parentKey); err != nil {
		return nil, err
	}

	dek, err := auth.GenerateDEK()
	if err != nil {
		return nil, err
	}
	defer clearBytes(dek)
	b.dekMu.Lock()
	kek := append([]byte(nil), b.kek...)
	b.dekMu.Unlock()
	wrapped, err := auth.WrapDEK(kek, dek)
	clearBytes(kek)
	if err != nil {
		return nil, fmt.Errorf("wrap new file DEK: %w", err)
	}

	record, err := b.namespace.CreateDOFSNode(
		b.userID, childPath(parentKey, name, false), name, hex.EncodeToString(wrapped), false,
	)
	if err != nil {
		return nil, err
	}
	abort := true
	defer func() {
		if abort {
			_ = b.namespace.AbortDOFSFile(b.userID, record.ID)
		}
	}()

	targetKey := model.DOFSGenerationObjectKey(b.userID, record.ID, 1, uuid.NewString())
	var ciphertext bytes.Buffer
	if err := auth.EncryptStream(dek, bytes.NewReader(nil), &ciphertext); err != nil {
		return nil, fmt.Errorf("encrypt empty file: %w", err)
	}
	if err := b.genObjects.PutObject(targetKey, bytes.NewReader(ciphertext.Bytes()), int64(ciphertext.Len())); err != nil {
		if cleanupErr := b.genObjects.DeleteObject(targetKey); cleanupErr != nil {
			// Keep the hidden row so mount recovery can retry its inode prefix.
			abort = false
			return nil, fmt.Errorf("upload empty file generation: %w (cleanup failed: %v)", err, cleanupErr)
		}
		return nil, fmt.Errorf("upload empty file generation: %w", err)
	}
	published, err := b.namespace.FinalizeDOFSFile(b.userID, record.ID, targetKey)
	if err != nil {
		// The database may have committed before returning an ambiguous
		// connection error. Keep both the row and object; recovery will either
		// preserve the ready file or remove the still-creating inode.
		abort = false
		return nil, fmt.Errorf("publish empty file metadata: %w", err)
	}
	if !published {
		if cleanupErr := b.genObjects.DeleteObject(targetKey); cleanupErr != nil {
			// Keep the hidden row so mount recovery can retry its inode prefix.
			abort = false
			return nil, fmt.Errorf("empty file metadata changed before publication (cleanup failed: %v)", cleanupErr)
		}
		return nil, errors.New("empty file metadata changed before publication")
	}
	abort = false
	current, err := b.namespace.GetByID(b.userID, record.ID)
	if err != nil {
		return nil, err
	}
	return current, nil
}

// RemoveChild detaches a file or empty directory. Physical generations are
// reclaimed immediately only when no open file handle still refers to them.
func (b *Backend) RemoveChild(parentKey, name string, directory bool) error {
	if err := b.namespaceReady(); err != nil {
		return err
	}
	if !validComponent(name) {
		return ErrInvalidName
	}
	if err := b.ensureDirectory(parentKey); err != nil {
		return err
	}
	existing, err := b.LookupChild(parentKey, name)
	if err != nil {
		return err
	}
	if existing.IsDir && !directory {
		return ErrIsDirectory
	}
	if !existing.IsDir && directory {
		return ErrNotDirectory
	}

	b.handleMu.Lock()
	defer b.handleMu.Unlock()
	removed, err := b.namespace.RemoveDOFSNode(b.userID, existing.Path, directory)
	if err != nil {
		return err
	}
	if b.openFiles[removed.ID] == 0 {
		if err := b.purgeDeletedLocked(removed.ID); err != nil {
			log.Printf("[dofs] deferred cleanup for unlinked inode %d: %v", removed.ID, err)
		}
	}
	return nil
}

// RenameChild is a metadata transaction; immutable generations and legacy
// object keys stay in place. This avoids an OSS copy/delete window entirely.
func (b *Backend) RenameChild(oldParentKey, name, newParentKey, newName string, noReplace bool) (*model.FileRecord, error) {
	if err := b.namespaceReady(); err != nil {
		return nil, err
	}
	if !validComponent(name) || !validComponent(newName) {
		return nil, ErrInvalidName
	}
	if err := b.ensureDirectory(oldParentKey); err != nil {
		return nil, err
	}
	if err := b.ensureDirectory(newParentKey); err != nil {
		return nil, err
	}
	source, err := b.LookupChild(oldParentKey, name)
	if err != nil {
		return nil, err
	}
	if source.IsDir && strings.HasPrefix(newParentKey, source.Path) {
		return nil, ErrInvalidName
	}
	newPath := childPath(newParentKey, newName, source.IsDir)

	b.handleMu.Lock()
	defer b.handleMu.Unlock()
	renamed, replaced, err := b.namespace.RenameDOFSNode(
		b.userID, source.Path, newPath, newName, source.IsDir, !noReplace,
	)
	if err != nil {
		return nil, err
	}
	if replaced != nil && b.openFiles[replaced.ID] == 0 {
		if err := b.purgeDeletedLocked(replaced.ID); err != nil {
			log.Printf("[dofs] deferred cleanup for replaced inode %d: %v", replaced.ID, err)
		}
	}
	return renamed, nil
}

func (b *Backend) retainFile(id int64) error {
	if b.namespace == nil {
		return nil
	}
	b.handleMu.Lock()
	defer b.handleMu.Unlock()
	record, err := b.namespace.GetByID(b.userID, id)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return ErrNotFound
		}
		return err
	}
	if record.Status != "ready" || record.IsDir {
		return ErrNotReady
	}
	b.openFiles[id]++
	return nil
}

func (b *Backend) releaseFile(id int64) error {
	if b.namespace == nil {
		return nil
	}
	b.handleMu.Lock()
	defer b.handleMu.Unlock()
	if b.openFiles[id] > 1 {
		b.openFiles[id]--
		return nil
	}
	delete(b.openFiles, id)
	if err := b.purgeDeletedLocked(id); err != nil {
		log.Printf("[dofs] deferred cleanup after closing inode %d: %v", id, err)
	}
	return nil
}

func (b *Backend) purgeDeletedLocked(id int64) error {
	record, err := b.namespace.GetByID(b.userID, id)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if record.Status != "deleted" {
		return nil
	}
	if !b.validStorageRecord(record) {
		return errors.New("refusing to purge a deleted inode with an out-of-scope object key")
	}
	thumbnail, trackedThumbnail, err := b.prepareRetiredThumbnailLocked(record.ThumbnailKey, record.ID)
	if err != nil {
		return err
	}
	inodeRoot := model.DOFSInodeObjectRoot(b.userID, record.ID)
	if record.HasObjectGenerations || strings.HasPrefix(record.StorageKey(), inodeRoot) {
		if err := b.genObjects.DeleteObjectPrefix(inodeRoot); err != nil {
			return fmt.Errorf("delete inode generations: %w", err)
		}
	}
	exactKeys := []string{record.StorageKey(), record.LegacyObjectKey}
	deletedExact := make(map[string]struct{}, len(exactKeys))
	for _, key := range exactKeys {
		if key == "" || strings.HasPrefix(key, inodeRoot) {
			continue
		}
		if !b.validOwnedObjectKey(key) {
			return errors.New("refusing to delete an out-of-scope retained object key")
		}
		if _, duplicate := deletedExact[key]; duplicate {
			continue
		}
		if err := b.genObjects.DeleteObject(key); err != nil {
			return fmt.Errorf("delete retained path object: %w", err)
		}
		deletedExact[key] = struct{}{}
	}
	if record.ThumbnailKey != "" && !trackedThumbnail {
		if err := b.genObjects.DeleteObject(record.ThumbnailKey); err != nil {
			return fmt.Errorf("delete untracked thumbnail object: %w", err)
		}
	}
	if thumbnail != nil && b.openFiles[thumbnail.ID] == 0 {
		if err := b.purgeDeletedLocked(thumbnail.ID); err != nil {
			return fmt.Errorf("purge retired thumbnail inode: %w", err)
		}
	}
	purged, err := b.namespace.PurgeDeletedDOFSNode(b.userID, id)
	if err != nil {
		return err
	}
	if !purged {
		return errors.New("deleted inode changed during purge")
	}
	b.dekMu.Lock()
	if entry, ok := b.dekCache[record.ID]; ok {
		clearBytes(entry.dek)
		delete(b.dekCache, record.ID)
	}
	b.dekMu.Unlock()
	return nil
}

// prepareRetiredThumbnailLocked resolves both current and pre-upgrade
// thumbnail records. Current repository operations tombstone the linked inode
// transactionally; a ready record is migrated to that state here before any
// object is removed. The caller holds handleMu, so an open thumbnail remains
// readable until releaseFile performs its normal deferred purge.
func (b *Backend) prepareRetiredThumbnailLocked(thumbnailKey string, sourceID int64) (*model.FileRecord, bool, error) {
	if thumbnailKey == "" {
		return nil, false, nil
	}
	if !b.validOwnedObjectKey(thumbnailKey) {
		return nil, false, errors.New("refusing to delete an out-of-scope thumbnail key")
	}
	thumbnail, err := b.namespace.GetByStorageKey(b.userID, thumbnailKey)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("resolve retired thumbnail inode: %w", err)
	}
	if thumbnail.ID == sourceID {
		return nil, false, nil
	}
	switch thumbnail.Status {
	case "ready":
		thumbnail, err = b.namespace.RemoveDOFSNode(b.userID, thumbnail.Path, false)
		if err != nil {
			return nil, true, fmt.Errorf("retire legacy thumbnail inode: %w", err)
		}
	case "deleted":
		// Already detached atomically with its source.
	default:
		return nil, true, fmt.Errorf("retired thumbnail inode has unexpected status %q", thumbnail.Status)
	}
	if !b.validStorageRecord(thumbnail) || thumbnail.StorageKey() != thumbnailKey {
		return nil, true, errors.New("refusing to purge a mismatched thumbnail inode")
	}
	return thumbnail, true, nil
}

func (b *Backend) purgeRetiredThumbnail(thumbnailKey string) error {
	if thumbnailKey == "" || b.namespace == nil {
		return nil
	}
	b.handleMu.Lock()
	defer b.handleMu.Unlock()
	thumbnail, tracked, err := b.prepareRetiredThumbnailLocked(thumbnailKey, 0)
	if err != nil {
		return err
	}
	if !tracked {
		return b.genObjects.DeleteObject(thumbnailKey)
	}
	if thumbnail != nil && b.openFiles[thumbnail.ID] == 0 {
		return b.purgeDeletedLocked(thumbnail.ID)
	}
	return nil
}

func (b *Backend) cleanupIncompleteNamespace() error {
	creating, err := b.namespace.ListCreatingDOFSNodes(b.userID)
	if err != nil {
		return err
	}
	for i := range creating {
		record := &creating[i]
		if err := b.genObjects.DeleteObjectPrefix(model.DOFSInodeObjectRoot(b.userID, record.ID)); err != nil {
			return err
		}
		if err := b.namespace.AbortDOFSFile(b.userID, record.ID); err != nil {
			return err
		}
	}

	deleted, err := b.namespace.ListDeletedDOFSNodes(b.userID)
	if err != nil {
		return err
	}
	b.handleMu.Lock()
	defer b.handleMu.Unlock()
	for i := range deleted {
		if err := b.purgeDeletedLocked(deleted[i].ID); err != nil {
			return err
		}
	}
	return nil
}

// A compile-time assertion also protects command wiring from accidentally
// passing a repository implementation that lacks namespace transactions.
var _ NamespaceRepository = (model.FileRepo)(nil)
