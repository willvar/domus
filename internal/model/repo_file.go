package model

import (
	"fmt"
	"time"

	"gorm.io/gorm"
)

// FileRepo defines file record data access operations.
type FileRepo interface {
	Upsert(userID, path, name string, isDir bool, size int64, contentType, contentHash string, opts ...UpsertFileOpts) error
	Get(userID, path string) (*FileRecord, error)
	Delete(userID, path string) error
	DeleteByPrefix(userID, prefix string) error
	ListByPrefix(userID, prefix string) ([]FileRecord, error)
	ListDirectChildren(userID, parent string) ([]FileRecord, error)
	ListAllChildren(userID, parent string) ([]FileRecord, error)
	Move(userID, oldPath, newPath, newName string) error
	MoveByPrefix(userID, oldPrefix, newPrefix string) error
	SumSizeByPrefix(userID, prefix string) (int64, error)
	UpdateThumbnail(userID, path, thumbnailKey, thumbnailWrappedDEK string, width, height int, duration float64) error
	UpdateSearchVector(userID, path, text string) error
	RebuildAllSearchVectors() (int64, error)
	SearchFiles(userID, query string, limit int) ([]SearchFileResult, error)
	HasFullTextSearch() bool
	// Upload-related
	CreateUpload(userID, uploadID, taskID, ossUploadID, path, name string, fileSize int64) error
	GetUpload(userID, uploadID string) (*FileRecord, error)
	UpdateUploadParts(uploadID, completedParts string) error
	UpdateStatus(uploadID, status string) error
	GetStaleUploads(staleAfter time.Duration) ([]FileRecord, error)
}

type gormFileRepo struct {
	db     *gorm.DB
	hasFTS bool
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
	return r.db.Model(&existing).Updates(updates).Error
}

func (r *gormFileRepo) Get(userID, path string) (*FileRecord, error) {
	var record FileRecord
	if err := r.db.Where("user_id = ? AND path = ?", userID, path).First(&record).Error; err != nil {
		return nil, err
	}
	return &record, nil
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
		"updated_at": time.Now(),
	}).Error
}

func (r *gormFileRepo) MoveByPrefix(userID, oldPrefix, newPrefix string) error {
	return r.db.Model(&FileRecord{}).Where("user_id = ? AND path LIKE ?", userID, oldPrefix+"%").
		Updates(map[string]interface{}{
			"path":       gorm.Expr("REPLACE(path, ?, ?)", oldPrefix, newPrefix),
			"parent":     gorm.Expr("REPLACE(parent, ?, ?)", oldPrefix, newPrefix),
			"updated_at": time.Now(),
		}).Error
}

func (r *gormFileRepo) SumSizeByPrefix(userID, prefix string) (int64, error) {
	var total int64
	err := r.db.Model(&FileRecord{}).Where("user_id = ? AND path LIKE ?", userID, prefix+"%").
		Select("COALESCE(SUM(size), 0)").Scan(&total).Error
	return total, err
}

func (r *gormFileRepo) UpdateThumbnail(userID, path, thumbnailKey, thumbnailWrappedDEK string, width, height int, duration float64) error {
	return r.db.Model(&FileRecord{}).Where("user_id = ? AND path = ?", userID, path).Updates(map[string]interface{}{
		"thumbnail_key":         thumbnailKey,
		"thumbnail_wrapped_dek": thumbnailWrappedDEK,
		"media_width":           width,
		"media_height":          height,
		"media_duration":        duration,
		"updated_at":            time.Now(),
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

func (r *gormFileRepo) RebuildAllSearchVectors() (int64, error) {
	if !r.hasFTS {
		return 0, fmt.Errorf("pg_jieba is not available")
	}
	res := r.db.Exec("UPDATE files SET search_vector = to_tsvector('jiebacfg', name) WHERE status = 'ready'")
	return res.RowsAffected, res.Error
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
		ORDER BY rank DESC
		LIMIT ?
	`, query, userID, pattern, limit).Scan(&results).Error
	return results, err
}

func (r *gormFileRepo) CreateUpload(userID, uploadID, taskID, ossUploadID, path, name string, fileSize int64) error {
	return r.db.Create(&FileRecord{
		UserID:      userID,
		Path:        path,
		Parent:      parentOf(path),
		Name:        name,
		Size:        fileSize,
		Status:      "uploading",
		UploadID:    uploadID,
		TaskID:      taskID,
		OSSUploadID: ossUploadID,
	}).Error
}

func (r *gormFileRepo) GetUpload(userID, uploadID string) (*FileRecord, error) {
	rec := &FileRecord{}
	if err := r.db.Where("upload_id = ? AND user_id = ?", uploadID, userID).First(rec).Error; err != nil {
		return nil, err
	}
	return rec, nil
}

func (r *gormFileRepo) UpdateUploadParts(uploadID, completedParts string) error {
	return r.db.Model(&FileRecord{}).Where("upload_id = ?", uploadID).Updates(map[string]interface{}{
		"completed_parts": completedParts,
		"updated_at":      time.Now(),
	}).Error
}

func (r *gormFileRepo) UpdateStatus(uploadID, status string) error {
	return r.db.Model(&FileRecord{}).Where("upload_id = ?", uploadID).Updates(map[string]interface{}{
		"status":     status,
		"updated_at": time.Now(),
	}).Error
}

func (r *gormFileRepo) GetStaleUploads(staleAfter time.Duration) ([]FileRecord, error) {
	var records []FileRecord
	cutoff := time.Now().Add(-staleAfter)
	if err := r.db.Where("status = ? AND updated_at < ?", "uploading", cutoff).Find(&records).Error; err != nil {
		return nil, err
	}
	return records, nil
}
