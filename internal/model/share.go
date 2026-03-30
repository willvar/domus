package model

import (
	"time"
)

// Share represents a file share — either a link share (public) or a user-to-user share.
type Share struct {
	ID           int64      `gorm:"primaryKey;autoIncrement" json:"id"`
	ShareID      string     `gorm:"uniqueIndex;not null" json:"share_id"`     // UUID for link shares
	OwnerID      string     `gorm:"not null;index" json:"owner_id"`           // user who created the share
	FilePath     string     `gorm:"not null" json:"file_path"`                // OSS path of shared file
	FileName     string     `gorm:"not null" json:"file_name"`                // display name
	FileSize     int64      `gorm:"not null;default:0" json:"file_size"`      // plaintext size
	ContentType  string     `gorm:"default:''" json:"content_type"`           // MIME type
	ShareType    string     `gorm:"not null" json:"share_type"`               // "link" or "user"
	TargetUserID string     `gorm:"index" json:"target_user_id,omitempty"`    // for user shares
	WrappedDEK   string     `gorm:"not null" json:"-"`                        // DEK wrapped with share_key or target user's KEK
	Permission   string     `gorm:"not null;default:'read'" json:"permission"` // "read" or "write"
	ExpiresAt    *time.Time `json:"expires_at,omitempty"`                     // nullable, for link shares
	CreatedAt    time.Time  `json:"created_at"`
}

func (Share) TableName() string { return "shares" }

// CreateShare inserts a new share record.
func CreateShare(share *Share) error {
	return db.Create(share).Error
}

// GetShareByID retrieves a share by its ShareID (UUID).
func GetShareByID(shareID string) (*Share, error) {
	var s Share
	if err := db.Where("share_id = ?", shareID).First(&s).Error; err != nil {
		return nil, err
	}
	return &s, nil
}

// ListSharesForFile returns all shares for a given file path owned by the user.
func ListSharesForFile(ownerID, filePath string) ([]Share, error) {
	var shares []Share
	err := db.Where("owner_id = ? AND file_path = ?", ownerID, filePath).
		Order("created_at DESC").Find(&shares).Error
	return shares, err
}

// ListSharesForUser returns all files shared with a specific user.
func ListSharesForUser(targetUserID string) ([]Share, error) {
	var shares []Share
	err := db.Where("share_type = 'user' AND target_user_id = ?", targetUserID).
		Order("created_at DESC").Find(&shares).Error
	return shares, err
}

// DeleteShare removes a share by ID, scoped to the owner.
func DeleteShare(id int64, ownerID string) error {
	return db.Where("id = ? AND owner_id = ?", id, ownerID).Delete(&Share{}).Error
}

// DeleteSharesByPath removes all shares for a file (e.g., when file is deleted).
func DeleteSharesByPath(ownerID, filePath string) error {
	return db.Where("owner_id = ? AND file_path = ?", ownerID, filePath).Delete(&Share{}).Error
}

// GetShareForUser returns a specific share of a file for a target user.
func GetShareForUser(filePath, targetUserID string) (*Share, error) {
	var s Share
	if err := db.Where("file_path = ? AND share_type = 'user' AND target_user_id = ?", filePath, targetUserID).
		First(&s).Error; err != nil {
		return nil, err
	}
	return &s, nil
}
