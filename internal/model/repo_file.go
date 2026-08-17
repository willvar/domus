package model

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// FileRepo defines file record data access operations.
type FileRepo interface {
	Upsert(userID, path, name string, isDir bool, size int64, contentType, contentHash string, opts ...UpsertFileOpts) error
	Get(userID, path string) (*FileRecord, error)
	GetByID(userID string, id int64) (*FileRecord, error)
	GetByStorageKey(userID, objectKey string) (*FileRecord, error)
	CommitGeneration(userID string, id, expectedGeneration int64, objectKey string, size int64) (bool, error)
	CreateDOFSNode(userID, path, name, wrappedDEK string, isDir bool) (*FileRecord, error)
	FinalizeDOFSFile(userID string, id int64, objectKey string) (bool, error)
	AbortDOFSFile(userID string, id int64) error
	RemoveDOFSNode(userID, path string, isDir bool) (*FileRecord, error)
	RenameDOFSNode(userID, oldPath, newPath, newName string, isDir, replace bool) (*FileRecord, *FileRecord, error)
	ListCreatingDOFSNodes(userID string) ([]FileRecord, error)
	ListDeletedDOFSNodes(userID string) ([]FileRecord, error)
	PurgeDeletedDOFSNode(userID string, id int64) (bool, error)
	AcquireDOFSMountLease(userID string) (io.Closer, error)
	Delete(userID, path string) error
	DeleteByPrefix(userID, prefix string) error
	ListByPrefix(userID, prefix string) ([]FileRecord, error)
	ListDirectChildren(userID, parent string) ([]FileRecord, error)
	ListAllChildren(userID, parent string) ([]FileRecord, error)
	Move(userID, oldPath, newPath, newName string) error
	MoveByPrefix(userID, oldPrefix, newPrefix string) error
	UpdateThumbnail(userID, path, thumbnailKey, thumbnailWrappedDEK string, width, height int, duration float64) error
	UpdateThumbnailIfGeneration(userID, path string, fileID, generation int64, thumbnailKey, thumbnailWrappedDEK string, width, height int, duration float64) (bool, error)
	UpdateContentType(userID, path, contentType string) error
	UpdateSearchVector(userID, path, text string) error
	SearchFiles(userID, query string, limit int) ([]SearchFileResult, error)
	HasFullTextSearch() bool
	// Upload-related
	CreateUpload(userID, uploadID, taskID, ossUploadID, path, name string, fileSize int64, clientInstanceID string) error
	GetUpload(userID, uploadID string) (*FileRecord, error)
	UpdateStatus(uploadID, status string) error
	TouchUpload(uploadID string, seenAt time.Time) error
	ListActiveUploads(userID string) ([]FileRecord, error)
	CancelUploadsForOtherInstances(userID, clientInstanceID string, cutoff time.Time) ([]FileRecord, error)
	GetStaleUploads(staleAfter time.Duration) ([]FileRecord, error)
}

type gormFileRepo struct {
	db     *gorm.DB
	hasFTS bool
}

type dofsAdvisoryLease struct {
	conn *sql.Conn
	key  string
	once sync.Once
	mu   sync.Mutex
	err  error
}

// Check verifies that the exact PostgreSQL session holding the advisory lock
// is still alive. A sql.Conn is pinned to one underlying connection, so a
// successful query also proves the session containing the lock still exists.
func (l *dofsAdvisoryLease) Check(ctx context.Context) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.conn == nil {
		return errors.New("DOFS advisory lease connection is closed")
	}
	var one int
	if err := l.conn.QueryRowContext(ctx, "SELECT 1").Scan(&one); err != nil {
		return fmt.Errorf("check DOFS advisory lease session: %w", err)
	}
	if one != 1 {
		return errors.New("invalid DOFS advisory lease heartbeat")
	}
	return nil
}

func (l *dofsAdvisoryLease) Close() error {
	l.once.Do(func() {
		l.mu.Lock()
		defer l.mu.Unlock()
		if l.conn == nil {
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		var released bool
		if err := l.conn.QueryRowContext(ctx, "SELECT pg_advisory_unlock(hashtextextended($1, 0))", l.key).Scan(&released); err != nil {
			l.err = err
		} else if !released {
			l.err = errors.New("DOFS advisory lease was not held")
		}
		if err := l.conn.Close(); l.err == nil {
			l.err = err
		}
		l.conn = nil
	})
	return l.err
}

func (r *gormFileRepo) AcquireDOFSMountLease(userID string) (io.Closer, error) {
	sqlDB, err := r.db.DB()
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	conn, err := sqlDB.Conn(ctx)
	if err != nil {
		return nil, err
	}
	key := "domus:dofs:writable:" + userID
	var acquired bool
	if err := conn.QueryRowContext(ctx, "SELECT pg_try_advisory_lock(hashtextextended($1, 0))", key).Scan(&acquired); err != nil {
		_ = conn.Close()
		return nil, err
	}
	if !acquired {
		_ = conn.Close()
		return nil, ErrDOFSMountBusy
	}
	return &dofsAdvisoryLease{conn: conn, key: key}, nil
}

func (r *gormFileRepo) Upsert(userID, path, name string, isDir bool, size int64, contentType, contentHash string, opts ...UpsertFileOpts) error {
	record := FileRecord{
		UserID:      userID,
		Path:        path,
		Parent:      parentOf(path),
		Name:        name,
		IsDir:       isDir,
		Size:        size,
		ContentType: contentType,
		ContentHash: contentHash,
		UpdatedAt:   time.Now(),
	}
	if len(opts) > 0 {
		record.WrappedDEK = opts[0].WrappedDEK
		record.ObjectKey = opts[0].ObjectKey
	}
	if !isDir {
		if record.ObjectKey == "" {
			record.ObjectKey = path
		}
		record.HasObjectGenerations = strings.HasPrefix(record.ObjectKey, ".dofs/objects/")
		record.Generation = 1
	}
	var existing FileRecord
	err := r.db.Where("user_id = ? AND path = ?", userID, path).First(&existing).Error
	if err != nil {
		return r.db.Create(&record).Error
	}
	updates := map[string]interface{}{
		"name":         name,
		"parent":       parentOf(path),
		"is_dir":       isDir,
		"size":         size,
		"content_type": contentType,
		"status":       "ready",
		"updated_at":   time.Now(),
	}
	if contentHash != "" {
		updates["content_hash"] = contentHash
	}
	if len(opts) > 0 && opts[0].WrappedDEK != "" {
		updates["wrapped_dek"] = opts[0].WrappedDEK
	}
	if !isDir {
		objectKey := path
		if len(opts) > 0 && opts[0].ObjectKey != "" {
			objectKey = opts[0].ObjectKey
		}
		updates["object_key"] = objectKey
		updates["generation"] = gorm.Expr("generation + 1")
		if existing.HasObjectGenerations || strings.HasPrefix(existing.ObjectKey, ".dofs/objects/") {
			updates["has_object_generations"] = true
		}
	}
	return r.db.Model(&existing).Updates(updates).Error
}

func (r *gormFileRepo) Get(userID, path string) (*FileRecord, error) {
	var record FileRecord
	if err := r.db.Where("user_id = ? AND path = ?", userID, path).First(&record).Error; err != nil {
		return nil, err
	}
	return &record, nil
}

func (r *gormFileRepo) GetByID(userID string, id int64) (*FileRecord, error) {
	var record FileRecord
	if err := r.db.Where("user_id = ? AND id = ?", userID, id).First(&record).Error; err != nil {
		return nil, err
	}
	return &record, nil
}

func (r *gormFileRepo) GetByStorageKey(userID, objectKey string) (*FileRecord, error) {
	var record FileRecord
	if err := r.db.Where(
		"user_id = ? AND (object_key = ? OR (object_key = '' AND path = ?))",
		userID, objectKey, objectKey,
	).Order("id DESC").First(&record).Error; err != nil {
		return nil, err
	}
	return &record, nil
}

// CommitGeneration atomically switches a file to an already-uploaded immutable
// object. RowsAffected is the compare-and-swap result: false means another
// writer committed a generation first.
func (r *gormFileRepo) CommitGeneration(userID string, id, expectedGeneration int64, objectKey string, size int64) (bool, error) {
	var switched bool
	err := r.db.Transaction(func(tx *gorm.DB) error {
		var current FileRecord
		err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("user_id = ? AND id = ? AND generation = ? AND is_dir = ? AND status IN ?", userID, id, expectedGeneration, false, []string{"ready", "deleted"}).
			First(&current).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil
		}
		if err != nil {
			return err
		}
		if _, err := retireLinkedThumbnail(tx, &current); err != nil {
			return err
		}
		result := tx.Model(&FileRecord{}).
			Where("user_id = ? AND id = ? AND generation = ? AND is_dir = ? AND status IN ?", userID, id, expectedGeneration, false, []string{"ready", "deleted"}).
			Updates(map[string]interface{}{
				"object_key": objectKey,
				"legacy_object_key": gorm.Expr(`CASE
					WHEN legacy_object_key = '' AND object_key = '' THEN path
					WHEN legacy_object_key = '' AND object_key NOT LIKE '.dofs/objects/%' THEN object_key
					ELSE legacy_object_key END`),
				"has_object_generations": true,
				"generation":             expectedGeneration + 1,
				"size":                   size,
				"content_hash":           "",
				"thumbnail_key":          "",
				"thumbnail_wrapped_dek":  "",
				"media_width":            0,
				"media_height":           0,
				"media_duration":         0,
				"updated_at":             time.Now(),
			})
		switched = result.RowsAffected == 1
		return result.Error
	})
	return switched, err
}

// CreateDOFSNode reserves one visible namespace entry. Files start in the
// hidden "creating" state so the encrypted empty generation can be uploaded
// before the entry becomes observable; directories need no OSS object and are
// ready immediately.
func (r *gormFileRepo) CreateDOFSNode(userID, filePath, name, wrappedDEK string, isDir bool) (*FileRecord, error) {
	now := time.Now()
	status := "creating"
	if isDir {
		status = "ready"
	}
	record := &FileRecord{
		UserID: userID, Path: filePath, Parent: parentOf(filePath), Name: name,
		IsDir: isDir, WrappedDEK: wrappedDEK, Status: status,
		CreatedAt: now, UpdatedAt: now,
	}
	err := r.db.Transaction(func(tx *gorm.DB) error {
		if err := lockDOFSNamespaceName(tx, userID, record.Parent, name); err != nil {
			return err
		}
		var conflicts int64
		if err := tx.Model(&FileRecord{}).
			Where("user_id = ? AND parent = ? AND name = ? AND status <> ?", userID, record.Parent, name, "deleted").
			Count(&conflicts).Error; err != nil {
			return err
		}
		if conflicts != 0 {
			return ErrFileExists
		}
		result := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(record)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return ErrFileExists
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return record, nil
}

// PostgreSQL transaction-scoped advisory locks serialize absent-row namespace
// operations without imposing a new unique constraint on historical data that
// may contain both "name" and "name/" records.
func lockDOFSNamespaceName(tx *gorm.DB, userID, parent, name string) error {
	key := fmt.Sprintf("domus:dofs:namespace:%d:%s:%d:%s:%s", len(userID), userID, len(parent), parent, name)
	return tx.Exec("SELECT pg_advisory_xact_lock(hashtextextended(?, 0))", key).Error
}

// FinalizeDOFSFile publishes the already-uploaded encrypted empty generation.
func (r *gormFileRepo) FinalizeDOFSFile(userID string, id int64, objectKey string) (bool, error) {
	result := r.db.Model(&FileRecord{}).
		Where("user_id = ? AND id = ? AND is_dir = ? AND status = ? AND generation = ?", userID, id, false, "creating", 0).
		Updates(map[string]interface{}{
			"object_key":             objectKey,
			"has_object_generations": true,
			"generation":             1,
			"status":                 "ready",
			"updated_at":             time.Now(),
		})
	return result.RowsAffected == 1, result.Error
}

func (r *gormFileRepo) AbortDOFSFile(userID string, id int64) error {
	return r.db.Where("user_id = ? AND id = ? AND status = ?", userID, id, "creating").Delete(&FileRecord{}).Error
}

func tombstoneDOFSRecord(tx *gorm.DB, record *FileRecord) error {
	token := strconv.FormatInt(record.ID, 10) + "-" + uuid.NewString()
	deletedRoot := DOFSDeletedRoot(record.UserID)
	updates := map[string]interface{}{
		"path":       deletedRoot + token,
		"parent":     deletedRoot,
		"name":       token,
		"status":     "deleted",
		"updated_at": time.Now(),
	}
	if record.ObjectKey == "" {
		updates["object_key"] = record.Path
	}
	if err := tx.Model(&FileRecord{}).
		Where("user_id = ? AND id = ? AND status = ?", record.UserID, record.ID, "ready").
		Updates(updates).Error; err != nil {
		return err
	}
	record.Path = deletedRoot + token
	record.Parent = deletedRoot
	record.Name = token
	record.Status = "deleted"
	record.UpdatedAt = updates["updated_at"].(time.Time)
	if objectKey, ok := updates["object_key"].(string); ok {
		record.ObjectKey = objectKey
	}
	return nil
}

// retireLinkedThumbnail hides the internal thumbnail inode in the same
// transaction that detaches it from its source. Its encrypted object remains
// available until DOFS observes that no open handle still references it.
func retireLinkedThumbnail(tx *gorm.DB, source *FileRecord) (*FileRecord, error) {
	if source == nil || source.ThumbnailKey == "" {
		return nil, nil
	}
	var thumbnail FileRecord
	err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
		Where("user_id = ? AND id <> ? AND status = ?", source.UserID, source.ID, "ready").
		Where("object_key = ? OR (object_key = '' AND path = ?)", source.ThumbnailKey, source.ThumbnailKey).
		Order("id DESC").First(&thumbnail).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if err := tombstoneDOFSRecord(tx, &thumbnail); err != nil {
		return nil, err
	}
	return &thumbnail, nil
}

// RemoveDOFSNode atomically detaches an entry. The row is tombstoned instead
// of deleted so already-open FUSE handles retain normal Unix unlink semantics.
func (r *gormFileRepo) RemoveDOFSNode(userID, filePath string, isDir bool) (*FileRecord, error) {
	var removed FileRecord
	err := r.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("user_id = ? AND path = ? AND status = ?", userID, filePath, "ready").
			First(&removed).Error; err != nil {
			return err
		}
		if removed.IsDir != isDir {
			return ErrNodeTypeMismatch
		}
		if isDir {
			var children int64
			if err := tx.Model(&FileRecord{}).
				Where("user_id = ? AND parent = ? AND status <> ?", userID, removed.Path, "deleted").
				Count(&children).Error; err != nil {
				return err
			}
			if children != 0 {
				return ErrDirectoryNotEmpty
			}
		}
		if _, err := retireLinkedThumbnail(tx, &removed); err != nil {
			return err
		}
		return tombstoneDOFSRecord(tx, &removed)
	})
	if err != nil {
		return nil, err
	}
	return &removed, nil
}

// RenameDOFSNode performs a metadata-only rename. Legacy physical object keys
// are frozen before logical paths move, so no non-atomic OSS copy/delete is
// required. A replaced destination is returned as a tombstone for deferred
// cleanup after its open handles drain.
func (r *gormFileRepo) RenameDOFSNode(userID, oldPath, newPath, newName string, isDir, replace bool) (*FileRecord, *FileRecord, error) {
	var renamed FileRecord
	var replaced *FileRecord
	err := r.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("user_id = ? AND path = ? AND status = ?", userID, oldPath, "ready").
			First(&renamed).Error; err != nil {
			return err
		}
		if renamed.IsDir != isDir {
			return ErrNodeTypeMismatch
		}
		if oldPath == newPath {
			return nil
		}
		if err := lockDOFSNamespaceName(tx, userID, parentOf(newPath), newName); err != nil {
			return err
		}

		var destination FileRecord
		destinationErr := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("user_id = ? AND parent = ? AND name = ? AND status <> ?", userID, parentOf(newPath), newName, "deleted").
			First(&destination).Error
		switch {
		case destinationErr == nil:
			if destination.ID == renamed.ID {
				return nil
			}
			if !replace {
				return ErrFileExists
			}
			if destination.Status != "ready" {
				return ErrFileExists
			}
			if destination.IsDir != renamed.IsDir {
				return ErrNodeTypeMismatch
			}
			if destination.IsDir {
				var children int64
				if err := tx.Model(&FileRecord{}).
					Where("user_id = ? AND parent = ? AND status <> ?", userID, destination.Path, "deleted").
					Count(&children).Error; err != nil {
					return err
				}
				if children != 0 {
					return ErrDirectoryNotEmpty
				}
			}
			copy := destination
			if _, err := retireLinkedThumbnail(tx, &copy); err != nil {
				return err
			}
			if err := tombstoneDOFSRecord(tx, &copy); err != nil {
				return err
			}
			replaced = &copy
		case destinationErr != gorm.ErrRecordNotFound:
			return destinationErr
		}

		now := time.Now()
		if !renamed.IsDir {
			objectKey := renamed.ObjectKey
			if objectKey == "" {
				objectKey = renamed.Path
			}
			if err := tx.Model(&FileRecord{}).
				Where("user_id = ? AND id = ? AND status = ?", userID, renamed.ID, "ready").
				Updates(map[string]interface{}{
					"path": newPath, "parent": parentOf(newPath), "name": newName,
					"object_key": objectKey, "updated_at": now,
				}).Error; err != nil {
				return err
			}
		} else {
			var conflicts int64
			if err := tx.Model(&FileRecord{}).
				Where("user_id = ? AND status <> ? AND LEFT(path, CHAR_LENGTH(?)) = ?", userID, "deleted", newPath, newPath).
				Count(&conflicts).Error; err != nil {
				return err
			}
			if conflicts != 0 {
				return ErrFileExists
			}
			// Preserve physical keys for every legacy object before updating its
			// logical path. Generated objects are already path-independent.
			if err := tx.Model(&FileRecord{}).
				Where("user_id = ? AND status <> ? AND object_key = ? AND LEFT(path, CHAR_LENGTH(?)) = ?", userID, "deleted", "", oldPath, oldPath).
				Update("object_key", gorm.Expr("path")).Error; err != nil {
				return err
			}
			query := `UPDATE files
				SET path = CASE WHEN id = ? THEN ? ELSE ? || SUBSTRING(path FROM CHAR_LENGTH(?) + 1) END,
				    parent = CASE WHEN id = ? THEN ? ELSE ? || SUBSTRING(parent FROM CHAR_LENGTH(?) + 1) END,
				    name = CASE WHEN id = ? THEN ? ELSE name END,
				    updated_at = ?
				WHERE user_id = ? AND status <> ? AND LEFT(path, CHAR_LENGTH(?)) = ?`
			if err := tx.Exec(query,
				renamed.ID, newPath, newPath, oldPath,
				renamed.ID, parentOf(newPath), newPath, oldPath,
				renamed.ID, newName, now,
				userID, "deleted", oldPath, oldPath,
			).Error; err != nil {
				return fmt.Errorf("rename directory metadata: %w", err)
			}
		}
		return tx.Where("user_id = ? AND id = ?", userID, renamed.ID).First(&renamed).Error
	})
	if err != nil {
		return nil, nil, err
	}
	return &renamed, replaced, nil
}

func (r *gormFileRepo) ListCreatingDOFSNodes(userID string) ([]FileRecord, error) {
	var records []FileRecord
	err := r.db.Where("user_id = ? AND status = ?", userID, "creating").Order("id ASC").Find(&records).Error
	return records, err
}

func (r *gormFileRepo) ListDeletedDOFSNodes(userID string) ([]FileRecord, error) {
	var records []FileRecord
	err := r.db.Where("user_id = ? AND status = ?", userID, "deleted").Order("id ASC").Find(&records).Error
	return records, err
}

func (r *gormFileRepo) PurgeDeletedDOFSNode(userID string, id int64) (bool, error) {
	result := r.db.Where("user_id = ? AND id = ? AND status = ?", userID, id, "deleted").Delete(&FileRecord{})
	return result.RowsAffected == 1, result.Error
}

func (r *gormFileRepo) Delete(userID, path string) error {
	return r.db.Where("user_id = ? AND path = ?", userID, path).Delete(&FileRecord{}).Error
}

func (r *gormFileRepo) DeleteByPrefix(userID, prefix string) error {
	return r.db.Where("user_id = ? AND path LIKE ?", userID, prefix+"%").Delete(&FileRecord{}).Error
}

func (r *gormFileRepo) ListByPrefix(userID, prefix string) ([]FileRecord, error) {
	var records []FileRecord
	err := r.db.Where("user_id = ? AND path LIKE ?", userID, prefix+"%").Find(&records).Error
	return records, err
}

func (r *gormFileRepo) ListDirectChildren(userID, parent string) ([]FileRecord, error) {
	var records []FileRecord
	err := r.db.Where("user_id = ? AND parent = ? AND status != ?", userID, parent, "deleted").
		Order("is_dir DESC, name ASC").Find(&records).Error
	return records, err
}

func (r *gormFileRepo) ListAllChildren(userID, parent string) ([]FileRecord, error) {
	var records []FileRecord
	err := r.db.Where("user_id = ? AND parent = ?", userID, parent).Find(&records).Error
	return records, err
}

func (r *gormFileRepo) Move(userID, oldPath, newPath, newName string) error {
	return r.db.Model(&FileRecord{}).Where("user_id = ? AND path = ?", userID, oldPath).Updates(map[string]interface{}{
		"path":       newPath,
		"parent":     parentOf(newPath),
		"name":       newName,
		"object_key": gorm.Expr("CASE WHEN object_key = '' OR object_key = ? THEN ? ELSE object_key END", oldPath, newPath),
		"updated_at": time.Now(),
	}).Error
}

func (r *gormFileRepo) MoveByPrefix(userID, oldPrefix, newPrefix string) error {
	return r.db.Model(&FileRecord{}).Where("user_id = ? AND path LIKE ?", userID, oldPrefix+"%").
		Updates(map[string]interface{}{
			"path":       gorm.Expr("REPLACE(path, ?, ?)", oldPrefix, newPrefix),
			"parent":     gorm.Expr("REPLACE(parent, ?, ?)", oldPrefix, newPrefix),
			"object_key": gorm.Expr("CASE WHEN object_key LIKE ? THEN REPLACE(object_key, ?, ?) ELSE object_key END", oldPrefix+"%", oldPrefix, newPrefix),
			"updated_at": time.Now(),
		}).Error
}

func (r *gormFileRepo) UpdateThumbnail(userID, path, thumbnailKey, thumbnailWrappedDEK string, width, height int, duration float64) error {
	return r.db.Transaction(func(tx *gorm.DB) error {
		var current FileRecord
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("user_id = ? AND path = ?", userID, path).First(&current).Error; err != nil {
			return err
		}
		if current.ThumbnailKey != thumbnailKey {
			if _, err := retireLinkedThumbnail(tx, &current); err != nil {
				return err
			}
		}
		return tx.Model(&FileRecord{}).Where("user_id = ? AND id = ?", userID, current.ID).Updates(map[string]interface{}{
			"thumbnail_key":         thumbnailKey,
			"thumbnail_wrapped_dek": thumbnailWrappedDEK,
			"media_width":           width,
			"media_height":          height,
			"media_duration":        duration,
			"updated_at":            time.Now(),
		}).Error
	})
}

func (r *gormFileRepo) UpdateThumbnailIfGeneration(userID, path string, fileID, generation int64, thumbnailKey, thumbnailWrappedDEK string, width, height int, duration float64) (bool, error) {
	var updated bool
	err := r.db.Transaction(func(tx *gorm.DB) error {
		var current FileRecord
		err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("user_id = ? AND path = ? AND id = ? AND generation = ? AND status = ?", userID, path, fileID, generation, "ready").
			First(&current).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil
		}
		if err != nil {
			return err
		}
		if current.ThumbnailKey != thumbnailKey {
			if _, err := retireLinkedThumbnail(tx, &current); err != nil {
				return err
			}
		}
		result := tx.Model(&FileRecord{}).
			Where("user_id = ? AND path = ? AND id = ? AND generation = ? AND status = ?", userID, path, fileID, generation, "ready").
			Updates(map[string]interface{}{
				"thumbnail_key":         thumbnailKey,
				"thumbnail_wrapped_dek": thumbnailWrappedDEK,
				"media_width":           width,
				"media_height":          height,
				"media_duration":        duration,
				"updated_at":            time.Now(),
			})
		updated = result.RowsAffected == 1
		return result.Error
	})
	return updated, err
}

func (r *gormFileRepo) UpdateContentType(userID, path, contentType string) error {
	return r.db.Model(&FileRecord{}).Where("user_id = ? AND path = ? AND status = ?", userID, path, "ready").Updates(map[string]interface{}{
		"content_type": contentType,
		"updated_at":   time.Now(),
	}).Error
}

func (r *gormFileRepo) UpdateSearchVector(userID, path, text string) error {
	if !r.hasFTS {
		return nil
	}
	return r.db.Exec(
		"UPDATE files SET search_vector = to_tsvector('jiebacfg', ?) WHERE user_id = ? AND path = ?",
		text, userID, path,
	).Error
}

func (r *gormFileRepo) HasFullTextSearch() bool {
	return r.hasFTS
}

func (r *gormFileRepo) SearchFiles(userID, query string, limit int) ([]SearchFileResult, error) {
	if limit <= 0 {
		limit = 50
	}
	var results []SearchFileResult

	if r.hasFTS {
		err := r.db.Raw(`
			SELECT f.*, ts_rank(f.search_vector, q) AS rank
			FROM files f, plainto_tsquery('jiebacfg', ?) q
			WHERE f.user_id = ?
			  AND f.status = 'ready'
			  AND f.search_vector @@ q
			  AND f.name NOT LIKE '.%'
			  AND f.path NOT LIKE '%/__trash__/%'
			ORDER BY rank DESC
			LIMIT ?
		`, query, userID, limit).Scan(&results).Error
		return results, err
	}

	pattern := "%" + query + "%"
	err := r.db.Raw(`
		SELECT *, similarity(name, ?) AS rank
		FROM files
		WHERE user_id = ?
		  AND status = 'ready'
		  AND name ILIKE ?
		  AND name NOT LIKE '.%'
		  AND path NOT LIKE '%/__trash__/%'
		ORDER BY rank DESC
		LIMIT ?
	`, query, userID, pattern, limit).Scan(&results).Error
	return results, err
}

func (r *gormFileRepo) CreateUpload(userID, uploadID, taskID, ossUploadID, path, name string, fileSize int64, clientInstanceID string) error {
	now := time.Now()
	return r.db.Create(&FileRecord{
		UserID:           userID,
		Path:             path,
		Parent:           parentOf(path),
		Name:             name,
		Size:             fileSize,
		Status:           "uploading",
		UploadID:         uploadID,
		TaskID:           taskID,
		OSSUploadID:      ossUploadID,
		ClientInstanceID: clientInstanceID,
		LastSeenAt:       now,
		CreatedAt:        now,
		UpdatedAt:        now,
	}).Error
}

func (r *gormFileRepo) GetUpload(userID, uploadID string) (*FileRecord, error) {
	rec := &FileRecord{}
	if err := r.db.Where("upload_id = ? AND user_id = ?", uploadID, userID).First(rec).Error; err != nil {
		return nil, err
	}
	return rec, nil
}

func (r *gormFileRepo) UpdateStatus(uploadID, status string) error {
	return r.db.Model(&FileRecord{}).Where("upload_id = ?", uploadID).Updates(map[string]interface{}{
		"status":     status,
		"updated_at": time.Now(),
	}).Error
}

func (r *gormFileRepo) TouchUpload(uploadID string, seenAt time.Time) error {
	return r.db.Model(&FileRecord{}).Where("upload_id = ?", uploadID).Updates(map[string]interface{}{
		"last_seen_at": seenAt,
		"updated_at":   seenAt,
	}).Error
}

func (r *gormFileRepo) ListActiveUploads(userID string) ([]FileRecord, error) {
	var records []FileRecord
	if err := r.db.Where("user_id = ? AND status = ?", userID, "uploading").Find(&records).Error; err != nil {
		return nil, err
	}
	return records, nil
}

func (r *gormFileRepo) CancelUploadsForOtherInstances(userID, clientInstanceID string, cutoff time.Time) ([]FileRecord, error) {
	var records []FileRecord
	q := r.db.Where("user_id = ? AND status = ? AND last_seen_at < ?", userID, "uploading", cutoff)
	if clientInstanceID != "" {
		q = q.Where("client_instance_id <> ?", clientInstanceID)
	}
	if err := q.Find(&records).Error; err != nil {
		return nil, err
	}
	if len(records) == 0 {
		return records, nil
	}
	ids := make([]string, 0, len(records))
	for _, rec := range records {
		ids = append(ids, rec.UploadID)
	}
	if err := r.db.Model(&FileRecord{}).Where("upload_id IN ?", ids).Updates(map[string]interface{}{
		"status":     "cancelled",
		"updated_at": time.Now(),
	}).Error; err != nil {
		return nil, err
	}
	return records, nil
}

func (r *gormFileRepo) GetStaleUploads(staleAfter time.Duration) ([]FileRecord, error) {
	var records []FileRecord
	cutoff := time.Now().Add(-staleAfter)
	if err := r.db.Where("status = ? AND updated_at < ?", "uploading", cutoff).Find(&records).Error; err != nil {
		return nil, err
	}
	return records, nil
}
