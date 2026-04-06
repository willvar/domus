package model

import (
	"fmt"
	"strings"
	"time"

	"gorm.io/gorm"
)

type FileRecord struct {
	ID                  int64   `gorm:"primaryKey;autoIncrement" json:"id"`
	UserID              string  `gorm:"not null;uniqueIndex:idx_file_user_path" json:"user_id"`
	Path                string  `gorm:"not null;uniqueIndex:idx_file_user_path" json:"path"`
	Parent              string  `gorm:"not null;index:idx_file_parent" json:"parent"`
	Name                string  `gorm:"not null;index:idx_file_user_name" json:"name"`
	IsDir               bool    `gorm:"not null;default:false" json:"is_dir"`
	Size                int64   `gorm:"not null;default:0" json:"size"`
	ContentType         string  `gorm:"default:''" json:"content_type"`
	ContentHash         string  `gorm:"default:''" json:"-"`
	ThumbnailKey        string  `gorm:"default:''" json:"-"`
	ThumbnailWrappedDEK string  `gorm:"default:''" json:"-"`
	MediaWidth          int     `gorm:"not null;default:0" json:"-"`
	MediaHeight         int     `gorm:"not null;default:0" json:"-"`
	MediaDuration       float64 `gorm:"not null;default:0" json:"-"`
	// Envelope encryption
	WrappedDEK string `gorm:"default:''" json:"-"`
	// Upload-related fields
	Status         string `gorm:"not null;default:'ready';index" json:"status"`
	UploadID       string `gorm:"default:'';index" json:"-"`
	TaskID         string `gorm:"default:'';index" json:"-"`
	ChunkSize      int    `gorm:"not null;default:0" json:"-"`
	CompletedParts string `gorm:"default:''" json:"-"`
	// Full-text search
	SearchVector string    `gorm:"type:tsvector" json:"-"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

func (FileRecord) TableName() string { return "files" }

type TrashItem struct {
	ID           int64     `gorm:"primaryKey;autoIncrement" json:"id"`
	UserID       string    `gorm:"not null;index" json:"user_id"`
	OriginalPath string    `gorm:"not null" json:"original_path"`
	TrashKey     string    `gorm:"not null" json:"trash_key"`
	Size         int64     `gorm:"not null;default:0" json:"size"`
	IsDir        bool      `gorm:"not null;default:false" json:"is_dir"`
	DeletedAt    time.Time `gorm:"autoCreateTime" json:"deleted_at"`
}

func (TrashItem) TableName() string { return "trash" }

// FileRecord operations

// UpsertFileOpts holds optional fields for UpsertFile.
type UpsertFileOpts struct {
	WrappedDEK string
}

func UpsertFile(userID string, path, name string, isDir bool, size int64, contentType, contentHash string, opts ...UpsertFileOpts) error {
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
	// Try to find existing record
	var existing FileRecord
	err := db.Where("user_id = ? AND path = ?", userID, path).First(&existing).Error
	if err != nil {
		// Not found, create
		return db.Create(&record).Error
	}
	// Update existing
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
	return db.Model(&existing).Updates(updates).Error
}

func UpdateFileThumbnail(userID, path, thumbnailKey, thumbnailWrappedDEK string, width, height int, duration float64) error {
	return db.Model(&FileRecord{}).Where("user_id = ? AND path = ?", userID, path).Updates(map[string]interface{}{
		"thumbnail_key":         thumbnailKey,
		"thumbnail_wrapped_dek": thumbnailWrappedDEK,
		"media_width":           width,
		"media_height":          height,
		"media_duration":        duration,
		"updated_at":            time.Now(),
	}).Error
}

func GetFile(userID string, path string) (*FileRecord, error) {
	var record FileRecord
	if err := db.Where("user_id = ? AND path = ?", userID, path).First(&record).Error; err != nil {
		return nil, err
	}
	return &record, nil
}

func SumFileSizeByPrefix(userID string, prefix string) (int64, error) {
	var total int64
	err := db.Model(&FileRecord{}).Where("user_id = ? AND path LIKE ?", userID, prefix+"%").
		Select("COALESCE(SUM(size), 0)").Scan(&total).Error
	return total, err
}

func DeleteFile(userID string, path string) error {
	return db.Where("user_id = ? AND path = ?", userID, path).Delete(&FileRecord{}).Error
}

func DeleteFilesByPrefix(userID string, prefix string) error {
	return db.Where("user_id = ? AND path LIKE ?", userID, prefix+"%").Delete(&FileRecord{}).Error
}

// ListFilesByPrefix returns all file records under a prefix (recursive).
func ListFilesByPrefix(userID, prefix string) ([]FileRecord, error) {
	var records []FileRecord
	err := db.Where("user_id = ? AND path LIKE ?", userID, prefix+"%").Find(&records).Error
	return records, err
}

// UpdateFileSearchVector updates the full-text search index for a file.
// text is the combined content to index (file name, optionally + file content).
// No-op when pg_jieba is not available (ILIKE fallback doesn't need a search vector).
func UpdateFileSearchVector(userID, path, text string) error {
	if !hasFTS {
		return nil
	}
	return db.Exec(
		"UPDATE files SET search_vector = to_tsvector('jiebacfg', ?) WHERE user_id = ? AND path = ?",
		text, userID, path,
	).Error
}

// RebuildAllSearchVectors rebuilds the full-text search index for all files.
// Useful after pg_jieba becomes available on a previously fallback-only instance.
func RebuildAllSearchVectors() (int64, error) {
	if !hasFTS {
		return 0, fmt.Errorf("pg_jieba is not available")
	}
	res := db.Exec("UPDATE files SET search_vector = to_tsvector('jiebacfg', name) WHERE status = 'ready'")
	return res.RowsAffected, res.Error
}

// HasFullTextSearch returns whether pg_jieba full-text search is available.
func HasFullTextSearch() bool {
	return hasFTS
}

// SearchFileResult holds a search result with rank score.
type SearchFileResult struct {
	FileRecord
	Rank float64 `json:"rank"`
}

// SearchFiles performs full-text search across a user's files.
// Uses pg_jieba tsvector when available, falls back to ILIKE with pg_trgm.
func SearchFiles(userID, query string, limit int) ([]SearchFileResult, error) {
	if limit <= 0 {
		limit = 50
	}
	var results []SearchFileResult

	if hasFTS {
		err := db.Raw(`
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

	// Fallback: ILIKE on file name (accelerated by pg_trgm GIN index)
	pattern := "%" + query + "%"
	err := db.Raw(`
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

func MoveFile(userID string, oldPath, newPath, newName string) error {
	return db.Model(&FileRecord{}).Where("user_id = ? AND path = ?", userID, oldPath).Updates(map[string]interface{}{
		"path":       newPath,
		"parent":     parentOf(newPath),
		"name":       newName,
		"updated_at": time.Now(),
	}).Error
}

// parentOf returns the parent directory path for a given path.
// e.g. "admin/docs/file.txt" -> "admin/docs/", "admin/docs/" -> "admin/", "admin/" -> ""
func parentOf(path string) string {
	p := strings.TrimSuffix(path, "/")
	if idx := strings.LastIndex(p, "/"); idx >= 0 {
		return p[:idx+1]
	}
	return ""
}

// ListDirectChildren returns direct children (files and dirs) under a parent path.
// Returns files in all visible statuses (ready, uploading, processing). Failed files are hidden.
func ListDirectChildren(userID, parent string) ([]FileRecord, error) {
	var records []FileRecord
	err := db.Where("user_id = ? AND parent = ? AND status != ?", userID, parent, "deleted").
		Order("is_dir DESC, name ASC").Find(&records).Error
	return records, err
}

// ListAllChildren returns all direct children under a parent path regardless of status.
// Used for conflict detection during uploads where we need to see uploading/processing files too.
func ListAllChildren(userID, parent string) ([]FileRecord, error) {
	var records []FileRecord
	err := db.Where("user_id = ? AND parent = ?", userID, parent).Find(&records).Error
	return records, err
}

func MoveFilesByPrefix(userID string, oldPrefix, newPrefix string) error {
	// Update all records whose path starts with oldPrefix
	// Replace the prefix portion of path and parent
	return db.Model(&FileRecord{}).Where("user_id = ? AND path LIKE ?", userID, oldPrefix+"%").
		Updates(map[string]interface{}{
			"path":       gorm.Expr("REPLACE(path, ?, ?)", oldPrefix, newPrefix),
			"parent":     gorm.Expr("REPLACE(parent, ?, ?)", oldPrefix, newPrefix),
			"updated_at": time.Now(),
		}).Error
}

// Trash operations

func CreateTrashRecord(userID string, originalPath, trashKey string, size int64, isDir bool) error {
	return db.Create(&TrashItem{
		UserID:       userID,
		OriginalPath: originalPath,
		TrashKey:     trashKey,
		Size:         size,
		IsDir:        isDir,
	}).Error
}

func ListTrash(userID string) ([]TrashItem, error) {
	var items []TrashItem
	if err := db.Where("user_id = ?", userID).Order("deleted_at DESC").Find(&items).Error; err != nil {
		return nil, err
	}
	return items, nil
}

func GetTrashItem(id int64, userID string) (*TrashItem, error) {
	t := &TrashItem{}
	if err := db.Where("id = ? AND user_id = ?", id, userID).First(t).Error; err != nil {
		return nil, err
	}
	return t, nil
}

func DeleteTrashRecord(id int64) error {
	return db.Delete(&TrashItem{}, id).Error
}

func ClearTrash(userID string) ([]TrashItem, error) {
	items, err := ListTrash(userID)
	if err != nil {
		return nil, err
	}
	if err := db.Where("user_id = ?", userID).Delete(&TrashItem{}).Error; err != nil {
		return nil, err
	}
	return items, nil
}

// Upload-related operations (merged into FileRecord)

func CreateUploadFile(userID, uploadID, taskID, path, name string, fileSize int64, chunkSize int) error {
	return db.Create(&FileRecord{
		UserID:    userID,
		Path:      path,
		Parent:    parentOf(path),
		Name:      name,
		Size:      fileSize,
		Status:    "uploading",
		UploadID:  uploadID,
		TaskID:    taskID,
		ChunkSize: chunkSize,
	}).Error
}

func GetUploadFile(userID, uploadID string) (*FileRecord, error) {
	r := &FileRecord{}
	if err := db.Where("upload_id = ? AND user_id = ?", uploadID, userID).First(r).Error; err != nil {
		return nil, err
	}
	return r, nil
}

func UpdateUploadFileParts(uploadID, completedParts string) error {
	return db.Model(&FileRecord{}).Where("upload_id = ?", uploadID).Updates(map[string]interface{}{
		"completed_parts": completedParts,
		"updated_at":      time.Now(),
	}).Error
}

func UpdateFileStatus(uploadID, status string) error {
	return db.Model(&FileRecord{}).Where("upload_id = ?", uploadID).Updates(map[string]interface{}{
		"status":     status,
		"updated_at": time.Now(),
	}).Error
}

// GetStaleUploadFiles returns files still in "uploading" status that haven't been updated within the given duration.
func GetStaleUploadFiles(staleAfter time.Duration) ([]FileRecord, error) {
	var records []FileRecord
	cutoff := time.Now().Add(-staleAfter)
	if err := db.Where("status = ? AND updated_at < ?", "uploading", cutoff).Find(&records).Error; err != nil {
		return nil, err
	}
	return records, nil
}
