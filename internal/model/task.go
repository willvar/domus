package model

import "time"

// Task is a user-facing task visible in the task panel.
// It is currently used for client-driven uploads.
type Task struct {
	ID               int64     `gorm:"primaryKey;autoIncrement" json:"-"`
	UserID           string    `gorm:"not null;index" json:"-"`
	TaskID           string    `gorm:"uniqueIndex;not null" json:"task_id"`
	Type             string    `gorm:"not null" json:"type"`                     // "upload"
	Status           string    `gorm:"not null;default:'running'" json:"status"` // running, completed, failed, cancelled
	Progress         float64   `gorm:"not null;default:0" json:"progress"`
	Phase            string    `gorm:"default:''" json:"phase"`
	Name             string    `gorm:"default:''" json:"name"` // display name (filename, etc.)
	ClientInstanceID string    `gorm:"-" json:"client_instance_id,omitempty"`
	CreatedAt        time.Time `json:"created_at"`
	UpdatedAt        time.Time `json:"updated_at"`
}

func (Task) TableName() string { return "tasks" }
