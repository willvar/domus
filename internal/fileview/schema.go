package fileview

import "time"

// metadataRecord contains only Domus product metadata. File identity,
// hierarchy, size, generations and wrapped keys live exclusively in DOFS.
type metadataRecord struct {
	UserID        string  `gorm:"primaryKey;size:128"`
	Inode         uint64  `gorm:"primaryKey"`
	Generation    int64   `gorm:"not null;default:0"`
	ContentType   string  `gorm:"not null;default:''"`
	ContentHash   string  `gorm:"not null;default:''"`
	Thumbnail     uint64  `gorm:"not null;default:0"`
	MediaWidth    int     `gorm:"not null;default:0"`
	MediaHeight   int     `gorm:"not null;default:0"`
	MediaDuration float64 `gorm:"not null;default:0"`
	// MediaCodecs is the MSE codec string of the source media (for example
	// "avc1.640028,mp4a.40.2"), probed by the worker from the FUSE mount.
	MediaCodecs string `gorm:"not null;default:''"`
	// MediaMeta is the JSON-encoded ffprobe summary of the source media
	// (container/stream properties and recording tags), probed by the worker.
	MediaMeta  string `gorm:"not null;default:''"`
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

func (metadataRecord) TableName() string { return "domus_file_metadata" }

// renditionRecord describes a server-transcoded derivative of one source file
// generation (a playback quality). Segment inodes are stored, never object
// keys or key material; those stay authoritative in DOFS.
type renditionRecord struct {
	UserID            string `gorm:"primaryKey;size:128"`
	SourceInode       uint64 `gorm:"primaryKey"`
	Profile           string `gorm:"primaryKey;size:32"`
	SourceGeneration  int64  `gorm:"not null;default:0"`
	Status            string `gorm:"not null;size:16;index"` // queued, running, ready, failed, cancelled
	TaskID            string `gorm:"not null;default:'';index"`
	InitInode         uint64 `gorm:"not null;default:0"`
	Codecs            string `gorm:"not null;default:''"`
	MediaWidth        int    `gorm:"not null;default:0"`
	MediaHeight       int    `gorm:"not null;default:0"`
	Segments          string `gorm:"not null;default:'[]'"`
	PublishedDuration float64 `gorm:"not null;default:0"`
	Error             string  `gorm:"not null;default:''"`
	CreatedAt         time.Time
	UpdatedAt         time.Time
}

func (renditionRecord) TableName() string { return "domus_file_renditions" }

// renditionSegment is one JSON entry of renditionRecord.Segments.
type renditionSegment struct {
	Inode    uint64  `json:"inode"`
	Duration float64 `json:"duration"`
}

// uploadRecord is Domus task/resume state. Its ID is also the durable DOFS
// external-upload reservation ID; ObjectKey and encryption keys are not
// duplicated here.
type uploadRecord struct {
	ID               string `gorm:"primaryKey;size:128"`
	UserID           string `gorm:"not null;size:128;index:idx_domus_upload_user_state;index:idx_domus_upload_user_path"`
	Inode            uint64 `gorm:"not null;index"`
	Path             string `gorm:"not null;index:idx_domus_upload_user_path"`
	Parent           string `gorm:"not null"`
	Name             string `gorm:"not null"`
	Size             int64  `gorm:"not null"`
	ContentType      string `gorm:"not null;default:''"`
	TaskID           string `gorm:"not null;default:'';index"`
	OSSUploadID      string `gorm:"not null;default:''"`
	Status           string `gorm:"not null;index:idx_domus_upload_user_state"`
	ClientInstanceID string `gorm:"not null;default:'';index"`
	LastSeenAt       time.Time
	CreatedAt        time.Time
	UpdatedAt        time.Time
}

func (uploadRecord) TableName() string { return "domus_file_uploads" }

// RenditionArtifact is one resolved derived artifact (init segment or media
// segment): the immutable DOFS object reference plus its plaintext size.
type RenditionArtifact struct {
	Inode      uint64 `json:"-"`
	ObjectKey  string `json:"-"`
	WrappedDEK string `json:"-"`
	Size       int64  `json:"size"`
	Duration   float64 `json:"duration,omitempty"`
}

// Rendition is the handler-facing projection of renditionRecord with DOFS
// node references resolved to object keys.
type Rendition struct {
	SourceInode      int64               `json:"-"`
	SourceGeneration int64               `json:"-"`
	Profile          string              `json:"profile"`
	Status       string              `json:"status"`
	TaskID       string              `json:"task_id"`
	Codecs       string              `json:"codecs,omitempty"`
	MediaWidth   int                 `json:"width,omitempty"`
	MediaHeight  int                 `json:"height,omitempty"`
	Error        string              `json:"error,omitempty"`
	Init         *RenditionArtifact  `json:"-"`
	Segments     []RenditionArtifact `json:"-"`
}
