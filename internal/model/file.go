package model

import (
	"strings"
	"time"

	"gorm.io/gorm"
)

type FileRecord struct {
	ID            int64     `gorm:"primaryKey;autoIncrement" json:"id"`
	UserID        string    `gorm:"not null;uniqueIndex:idx_file_user_path" json:"user_id"`
	Path          string    `gorm:"not null;uniqueIndex:idx_file_user_path" json:"path"`
	Parent        string    `gorm:"not null;index:idx_file_parent" json:"parent"`
	Name          string    `gorm:"not null;index:idx_file_user_name" json:"name"`
	IsDir         bool      `gorm:"not null;default:false" json:"is_dir"`
	Size          int64     `gorm:"not null;default:0" json:"size"`
	ContentType   string    `gorm:"default:''" json:"content_type"`
	ContentHash   string    `gorm:"default:''" json:"-"`
	ThumbnailKey  string    `gorm:"default:''" json:"-"`
	MediaWidth    int       `gorm:"not null;default:0" json:"-"`
	MediaHeight   int       `gorm:"not null;default:0" json:"-"`
	MediaDuration float64   `gorm:"not null;default:0" json:"-"`
	// Upload-related fields
	Status         string `gorm:"not null;default:'ready';index" json:"status"`
	UploadID       string `gorm:"default:'';index" json:"-"`
	ChunkSize      int    `gorm:"not null;default:0" json:"-"`
	CompletedParts string `gorm:"default:''" json:"-"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
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

type Bookmark struct {
	ID        int64  `gorm:"primaryKey;autoIncrement" json:"id"`
	UserID    string `gorm:"not null;uniqueIndex:idx_user_path" json:"user_id"`
	Name      string `gorm:"not null" json:"name"`
	Path      string `gorm:"not null;uniqueIndex:idx_user_path" json:"path"`
	Icon      string `gorm:"default:folder" json:"icon"`
	SortOrder int    `gorm:"default:0" json:"sort_order"`
}

// FileRecord operations

func UpsertFile(userID string, path, name string, isDir bool, size int64, contentType, contentHash string) error {
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
	return db.Model(&existing).Updates(updates).Error
}

func UpdateFileThumbnail(userID, path, thumbnailKey string, width, height int, duration float64) error {
	return db.Model(&FileRecord{}).Where("user_id = ? AND path = ?", userID, path).Updates(map[string]interface{}{
		"thumbnail_key":  thumbnailKey,
		"media_width":    width,
		"media_height":   height,
		"media_duration": duration,
		"updated_at":     time.Now(),
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
func ListDirectChildren(parent string) ([]FileRecord, error) {
	var records []FileRecord
	err := db.Where("parent = ? AND status != ?", parent, "deleted").
		Order("is_dir DESC, name ASC").Find(&records).Error
	return records, err
}

// ListAllChildren returns all direct children under a parent path regardless of status.
// Used for conflict detection during uploads where we need to see uploading/processing files too.
func ListAllChildren(parent string) ([]FileRecord, error) {
	var records []FileRecord
	err := db.Where("parent = ?", parent).Find(&records).Error
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

func CreateUploadFile(userID, uploadID, path, name string, fileSize int64, chunkSize int) error {
	return db.Create(&FileRecord{
		UserID:    userID,
		Path:      path,
		Parent:    parentOf(path),
		Name:      name,
		Size:      fileSize,
		Status:    "uploading",
		UploadID:  uploadID,
		ChunkSize: chunkSize,
	}).Error
}

func GetUploadFile(uploadID string) (*FileRecord, error) {
	r := &FileRecord{}
	if err := db.Where("upload_id = ?", uploadID).First(r).Error; err != nil {
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

// Bookmark operations

func ListBookmarks(userID string) ([]Bookmark, error) {
	var bookmarks []Bookmark
	if err := db.Where("user_id = ?", userID).Order("sort_order, id").Find(&bookmarks).Error; err != nil {
		return nil, err
	}
	return bookmarks, nil
}

func CreateBookmark(userID string, name, path, icon string, sortOrder int) (*Bookmark, error) {
	b := &Bookmark{
		UserID:    userID,
		Name:      name,
		Path:      path,
		Icon:      icon,
		SortOrder: sortOrder,
	}
	if err := db.Create(b).Error; err != nil {
		return nil, err
	}
	return b, nil
}

func UpdateBookmark(id int64, userID string, name, path, icon string, sortOrder int) error {
	return db.Model(&Bookmark{}).Where("id = ? AND user_id = ?", id, userID).Updates(map[string]interface{}{
		"name":       name,
		"path":       path,
		"icon":       icon,
		"sort_order": sortOrder,
	}).Error
}

func DeleteBookmark(id int64, userID string) error {
	return db.Where("id = ? AND user_id = ?", id, userID).Delete(&Bookmark{}).Error
}
