package model

import (
	"errors"
	"time"
)

var ErrInvalidShareInode = errors.New("share requires a positive DOFS inode")

// Share represents a user-to-user file share.
type Share struct {
	ID           int64      `gorm:"primaryKey;autoIncrement" json:"id"`
	ShareID      string     `gorm:"uniqueIndex;not null" json:"share_id"`
	OwnerID      string     `gorm:"not null;index" json:"owner_id"`
	FileInode    int64      `gorm:"not null;index:idx_share_owner_inode;check:chk_share_file_inode,file_inode > 0" json:"file_inode"`
	FilePath     string     `gorm:"not null" json:"file_path"`
	FileName     string     `gorm:"not null" json:"file_name"`
	FileSize     int64      `gorm:"not null;default:0" json:"file_size"`
	ContentType  string     `gorm:"default:''" json:"content_type"`
	TargetUserID string     `gorm:"not null;index" json:"target_user_id"`
	WrappedDEK   string     `gorm:"not null" json:"-"`                         // DEK wrapped with target user's KEK
	Permission   string     `gorm:"not null;default:'read'" json:"permission"` // "read" or "write"
	ExpiresAt    *time.Time `json:"expires_at,omitempty"`
	CreatedAt    time.Time  `json:"created_at"`
}

func (Share) TableName() string { return "shares" }

// ShareFileView is a Share record augmented with the owner's username for display.
type ShareFileView struct {
	Share
	OwnerUsername string `json:"owner_username"`
}
