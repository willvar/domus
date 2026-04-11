package model

import (
	"strings"
	"time"
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

// UpsertFileOpts holds optional fields for UpsertFile.
type UpsertFileOpts struct {
	WrappedDEK string
}

// SearchFileResult holds a search result with rank score.
type SearchFileResult struct {
	FileRecord
	Rank float64 `json:"rank"`
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
