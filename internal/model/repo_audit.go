package model

import (
	"time"

	"gorm.io/gorm"
)

// AuditFilter holds filter criteria for audit log queries.
type AuditFilter struct {
	User   string
	Action string
	From   *time.Time
	To     *time.Time
	Page   int
	Size   int
}

// AuditRepo defines audit log query operations.
// Write operations remain on AuditWorker (already instance-based).
type AuditRepo interface {
	ListLogs(filter AuditFilter) ([]AuditLog, int64, error)
}

type gormAuditRepo struct{ db *gorm.DB }

func (r *gormAuditRepo) ListLogs(filter AuditFilter) ([]AuditLog, int64, error) {
	q := r.db.Model(&AuditLog{})

	if filter.User != "" {
		q = q.Where("username = ?", filter.User)
	}
	if filter.Action != "" {
		q = q.Where("action = ?", filter.Action)
	}
	if filter.From != nil {
		q = q.Where("created_at >= ?", *filter.From)
	}
	if filter.To != nil {
		q = q.Where("created_at <= ?", *filter.To)
	}

	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	page := filter.Page
	size := filter.Size
	if page < 1 {
		page = 1
	}
	if size < 1 || size > 200 {
		size = 50
	}

	var logs []AuditLog
	if err := q.Order("id DESC").Offset((page - 1) * size).Limit(size).Find(&logs).Error; err != nil {
		return nil, 0, err
	}

	return logs, total, nil
}
