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
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

func (metadataRecord) TableName() string { return "domus_file_metadata" }

// uploadRecord is Domus task/resume state. Its ID is also the durable DOFS
// external-upload reservation ID; ObjectKey and encryption keys are not
// duplicated here.
type uploadRecord struct {
	ID               string `gorm:"primaryKey;size:128"`
	UserID           string `gorm:"not null;size:128;index:idx_domus_upload_user_state;index:idx_domus_upload_user_path"`
	ActorUserID      string `gorm:"not null;default:'';size:128;index:idx_domus_upload_actor_state"`
	ShareID          string `gorm:"not null;default:'';size:128;index"`
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
