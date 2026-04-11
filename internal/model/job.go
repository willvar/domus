package model

import "time"

type Job struct {
	ID        int64     `gorm:"primaryKey;autoIncrement" json:"-"`
	UserID    string    `gorm:"not null;index" json:"-"`
	JobID     string    `gorm:"uniqueIndex;not null" json:"-"`
	TaskID    string    `gorm:"default:'';index" json:"-"` // links to user-facing Task (empty for internal jobs)
	Type      string    `gorm:"not null;index" json:"-"`
	Status    string    `gorm:"not null;default:pending;index" json:"-"`
	Progress  float64   `gorm:"not null;default:0" json:"-"`
	Phase     string    `gorm:"default:''" json:"-"`
	Params    string    `gorm:"type:text;default:'{}'" json:"-"`
	Result    string    `gorm:"type:text;default:'{}'" json:"-"`
	ErrorMsg  string    `gorm:"default:''" json:"-"`
	CreatedAt time.Time `json:"-"`
	UpdatedAt time.Time `json:"-"`
}

func (Job) TableName() string { return "jobs" }
