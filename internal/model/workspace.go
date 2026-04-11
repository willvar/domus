package model

import "time"

// WorkspaceState stores serialised workspace snapshots per user.
type WorkspaceState struct {
	UserID    string    `gorm:"primaryKey" json:"user_id"`
	State     string    `gorm:"type:text" json:"state"` // JSON blob
	UpdatedAt time.Time `json:"updated_at"`
}
