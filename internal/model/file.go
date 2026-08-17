package model

import (
	"errors"
	"fmt"
	"path"
	"strconv"
	"strings"
	"time"
)

var (
	// ErrFileExists is returned by atomic namespace operations when a sibling
	// already owns the requested name.
	ErrFileExists = errors.New("file already exists")
	// ErrDirectoryNotEmpty prevents replacing or removing a non-empty
	// directory.
	ErrDirectoryNotEmpty = errors.New("directory not empty")
	// ErrNodeTypeMismatch reports file/directory replacement mismatches.
	ErrNodeTypeMismatch = errors.New("file type mismatch")
	// ErrDOFSMountBusy enforces one writable DOFS mount per user across hosts.
	ErrDOFSMountBusy = errors.New("writable DOFS mount already active for user")
)

type FileRecord struct {
	ID          int64  `gorm:"primaryKey;autoIncrement" json:"id"`
	UserID      string `gorm:"not null;uniqueIndex:idx_file_user_path;index:idx_file_user_parent_name_lookup" json:"user_id"`
	Path        string `gorm:"not null;uniqueIndex:idx_file_user_path" json:"path"`
	Parent      string `gorm:"not null;index:idx_file_parent;index:idx_file_user_parent_name_lookup" json:"parent"`
	Name        string `gorm:"not null;index:idx_file_user_name;index:idx_file_user_parent_name_lookup" json:"name"`
	IsDir       bool   `gorm:"not null;default:false" json:"is_dir"`
	Size        int64  `gorm:"not null;default:0" json:"size"`
	ContentType string `gorm:"default:''" json:"content_type"`
	ContentHash string `gorm:"default:''" json:"-"`
	// ObjectKey decouples the logical path from the immutable OSS object used
	// by the current file generation. Empty values are legacy records and fall
	// back to Path.
	ObjectKey string `gorm:"default:'';index" json:"-"`
	// LegacyObjectKey remembers the pre-generation path object so inode
	// collection can reclaim it even after subsequent logical renames.
	LegacyObjectKey      string  `gorm:"default:''" json:"-"`
	HasObjectGenerations bool    `gorm:"not null;default:false" json:"-"`
	Generation           int64   `gorm:"not null;default:0" json:"-"`
	ThumbnailKey         string  `gorm:"default:''" json:"-"`
	ThumbnailWrappedDEK  string  `gorm:"default:''" json:"-"`
	MediaWidth           int     `gorm:"not null;default:0" json:"-"`
	MediaHeight          int     `gorm:"not null;default:0" json:"-"`
	MediaDuration        float64 `gorm:"not null;default:0" json:"-"`
	// Envelope encryption
	WrappedDEK string `gorm:"default:''" json:"-"`
	// Upload-related fields
	Status           string    `gorm:"not null;default:'ready';index" json:"status"`
	UploadID         string    `gorm:"default:'';index" json:"-"`
	TaskID           string    `gorm:"default:'';index" json:"-"`
	OSSUploadID      string    `gorm:"default:''" json:"-"` // S3 multipart upload ID for client-direct-upload
	ChunkSize        int       `gorm:"not null;default:0" json:"-"`
	CompletedParts   string    `gorm:"default:''" json:"-"`
	ClientInstanceID string    `gorm:"default:'';index" json:"-"`
	LastSeenAt       time.Time `json:"-"`
	// Full-text search
	SearchVector string    `gorm:"type:tsvector" json:"-"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

func (FileRecord) TableName() string { return "files" }

// UpsertFileOpts holds optional fields for UpsertFile.
type UpsertFileOpts struct {
	WrappedDEK string
	ObjectKey  string
}

// StorageKey returns the physical OSS object for the current generation.
// Existing rows created before object generations were introduced continue to
// use their logical path without a data migration.
func (f FileRecord) StorageKey() string {
	if f.ObjectKey != "" {
		return f.ObjectKey
	}
	return f.Path
}

// DOFSObjectRoot is outside every username namespace, so browser-resolved
// logical paths cannot overwrite server-published generations.
func DOFSObjectRoot(userID string) string {
	return path.Join(".dofs", "objects", userID) + "/"
}

func DOFSInodeObjectRoot(userID string, inodeID int64) string {
	return path.Join(DOFSObjectRoot(userID), strconv.FormatInt(inodeID, 10)) + "/"
}

func DOFSGenerationObjectKey(userID string, inodeID, generation int64, transactionID string) string {
	return path.Join(
		DOFSInodeObjectRoot(userID, inodeID),
		fmt.Sprintf("%020d-%s.dofs", generation, transactionID),
	)
}

// DOFSDeletedRoot is a metadata-only namespace for unlinked inodes. Moving a
// row here releases its visible parent/name immediately while keeping the
// stable inode available to already-open file handles.
func DOFSDeletedRoot(userID string) string {
	return path.Join(".dofs", "deleted", userID) + "/"
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
