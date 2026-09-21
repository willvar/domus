package model

import "time"

// Task is a user-facing task visible in the task panel. It covers
// client-driven uploads and server-side transcode jobs.
type Task struct {
	ID               int64     `gorm:"primaryKey;autoIncrement" json:"-"`
	UserID           string    `gorm:"not null;index" json:"-"`
	TaskID           string    `gorm:"uniqueIndex;not null" json:"task_id"`
	Type             string    `gorm:"not null" json:"type"` // "upload" | "transcode"
	Status           string    `gorm:"not null;default:'running'" json:"status"` // queued, running, cancelling, completed, failed, cancelled
	Progress         float64   `gorm:"not null;default:0" json:"progress"`
	Phase            string    `gorm:"default:''" json:"phase"`
	Name             string    `gorm:"default:''" json:"name"` // display name (filename, etc.)
	SourcePath       string    `gorm:"default:''" json:"-"`    // transcode source namespace path (display only)
	SourceInode      int64     `gorm:"default:0" json:"-"`     // transcode source DOFS inode (authoritative key)
	Profile          string    `gorm:"default:''" json:"-"`    // transcode rendition profile
	Error            string    `gorm:"default:''" json:"error,omitempty"`
	ClientInstanceID string    `gorm:"-" json:"client_instance_id,omitempty"`
	CreatedAt        time.Time `json:"created_at"`
	UpdatedAt        time.Time `json:"updated_at"`
}

func (Task) TableName() string { return "tasks" }
