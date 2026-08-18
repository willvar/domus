package model

import (
	"time"
)

// FileRecord is a plain application DTO returned by FileRepo. Inode identity,
// hierarchy, generations and encrypted object keys are authoritative in DOFS;
// upload/task fields are joined from Domus's small product-metadata tables.
type FileRecord struct {
	ID          int64  `json:"id"`
	UserID      string `json:"user_id"`
	Path        string `json:"path"`
	Parent      string `json:"parent"`
	Name        string `json:"name"`
	IsDir       bool   `json:"is_dir"`
	Size        int64  `json:"size"`
	ContentType string `json:"content_type"`
	ContentHash string `json:"-"`

	ObjectKey  string `json:"-"`
	Generation int64  `json:"-"`

	ThumbnailKey        string  `json:"-"`
	ThumbnailWrappedDEK string  `json:"-"`
	MediaWidth          int     `json:"-"`
	MediaHeight         int     `json:"-"`
	MediaDuration       float64 `json:"-"`
	WrappedDEK          string  `json:"-"`

	Status           string    `json:"status"`
	UploadID         string    `json:"-"`
	TaskID           string    `json:"-"`
	OSSUploadID      string    `json:"-"`
	ClientInstanceID string    `json:"-"`
	LastSeenAt       time.Time `json:"-"`
	CreatedAt        time.Time `json:"created_at"`
	UpdatedAt        time.Time `json:"updated_at"`
}

// UpsertFileOpts lets callers assert that a just-published DOFS generation is
// the exact immutable object/key pair they reserved.
type UpsertFileOpts struct {
	WrappedDEK string
	ObjectKey  string
}

// StorageKey returns the immutable encrypted object owned by DOFS. Logical
// paths are never valid object keys in the sole supported architecture.
func (f FileRecord) StorageKey() string { return f.ObjectKey }

type SearchFileResult struct {
	FileRecord
	Rank float64 `json:"rank"`
}
