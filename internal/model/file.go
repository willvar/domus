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
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
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

type UploadRecord struct {
	ID             int64     `gorm:"primaryKey;autoIncrement" json:"id"`
	UserID         string    `gorm:"not null;index" json:"user_id"`
	UploadID       string    `gorm:"not null" json:"upload_id"`
	OSSKey         string    `gorm:"not null" json:"oss_key"`
	FileName       string    `gorm:"not null" json:"file_name"`
	FileSize       int64     `gorm:"not null" json:"file_size"`
	ChunkSize      int       `gorm:"not null" json:"chunk_size"`
	CompletedParts string    `gorm:"default:'[]'" json:"completed_parts"`
	Status         string    `gorm:"not null;default:active" json:"status"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}

func (UploadRecord) TableName() string { return "uploads" }

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
// Uses B-tree index on the parent column for O(log n) lookup.
func ListDirectChildren(parent string) ([]FileRecord, error) {
	var records []FileRecord
	err := db.Where("parent = ?", parent).Order("is_dir DESC, name ASC").Find(&records).Error
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

// Upload record operations

func CreateUploadRecord(userID string, uploadID, ossKey, fileName string, fileSize int64, chunkSize int) error {
	return db.Create(&UploadRecord{
		UserID:    userID,
		UploadID:  uploadID,
		OSSKey:    ossKey,
		FileName:  fileName,
		FileSize:  fileSize,
		ChunkSize: chunkSize,
	}).Error
}

func GetUploadRecord(uploadID string) (*UploadRecord, error) {
	r := &UploadRecord{}
	if err := db.Where("upload_id = ?", uploadID).First(r).Error; err != nil {
		return nil, err
	}
	return r, nil
}

func UpdateUploadParts(uploadID, completedParts string) error {
	return db.Model(&UploadRecord{}).Where("upload_id = ?", uploadID).Updates(map[string]interface{}{
		"completed_parts": completedParts,
		"updated_at":      time.Now(),
	}).Error
}

func UpdateUploadStatus(uploadID, status string) error {
	return db.Model(&UploadRecord{}).Where("upload_id = ?", uploadID).Updates(map[string]interface{}{
		"status":     status,
		"updated_at": time.Now(),
	}).Error
}

// GetStaleUploads returns uploads that are still 'active' but haven't been updated within the given duration.
func GetStaleUploads(staleAfter time.Duration) ([]UploadRecord, error) {
	var records []UploadRecord
	cutoff := time.Now().Add(-staleAfter)
	if err := db.Where("status = ? AND updated_at < ?", "active", cutoff).Find(&records).Error; err != nil {
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
