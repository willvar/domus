package model

import "time"

// FileRepo is Domus's application-facing projection of the independent DOFS
// namespace. Implementations must derive file identity, hierarchy, sizes,
// generations and wrapped keys from DOFS; only product metadata and transient
// direct-upload task state may live in the Domus database.
type FileRepo interface {
	Upsert(userID, path, name string, isDir bool, size int64, contentType, contentHash string, opts ...UpsertFileOpts) error
	Get(userID, path string) (*FileRecord, error)
	GetByID(userID string, id int64) (*FileRecord, error)
	GetByStorageKey(userID, objectKey string) (*FileRecord, error)
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
	SearchFiles(userID, query string, limit int) ([]SearchFileResult, error)

	CreateUpload(userID, uploadID, taskID, ossUploadID, path, name string, fileSize int64, clientInstanceID string) error
	GetUpload(userID, uploadID string) (*FileRecord, error)
	UpdateStatus(uploadID, status string) error
	TouchUpload(uploadID string, seenAt time.Time) error
	ListActiveUploads(userID string) ([]FileRecord, error)
	CancelUploadsForOtherInstances(userID, clientInstanceID string, cutoff time.Time) ([]FileRecord, error)
	GetStaleUploads(staleAfter time.Duration) ([]FileRecord, error)
}
