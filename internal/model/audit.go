package model

import (
	"log"
	"time"

	"github.com/gofiber/fiber/v2"
	"gorm.io/gorm"
)

type AuditLog struct {
	ID        int64     `gorm:"primaryKey;autoIncrement" json:"id"`
	UserID    string    `gorm:"index" json:"user_id"`
	Username  string    `json:"username"`
	IP        string    `json:"ip"`
	Action    string    `gorm:"not null;index" json:"action"`
	Resource  string    `json:"resource"`
	Detail    string    `json:"detail"`
	Status    string    `gorm:"not null" json:"status"`
	Duration  int64     `json:"duration"`
	CreatedAt time.Time `gorm:"index" json:"created_at"`
}

// AuditWorker batches audit log entries and flushes them to the database
// periodically or when the buffer is full.
type AuditWorker struct {
	ch chan *AuditLog
	db *gorm.DB
}

// NewAuditWorker creates an AuditWorker with the given database connection.
func NewAuditWorker(db *gorm.DB) *AuditWorker {
	return &AuditWorker{
		ch: make(chan *AuditLog, 256),
		db: db,
	}
}

// Start begins the background goroutine that batches and flushes audit entries.
func (w *AuditWorker) Start() {
	go func() {
		buf := make([]*AuditLog, 0, 32)
		ticker := time.NewTicker(2 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case entry, ok := <-w.ch:
				if !ok {
					if len(buf) > 0 {
						w.flushAudit(buf)
					}
					return
				}
				buf = append(buf, entry)
				if len(buf) >= 32 {
					w.flushAudit(buf)
					buf = buf[:0]
				}
			case <-ticker.C:
				if len(buf) > 0 {
					w.flushAudit(buf)
					buf = buf[:0]
				}
			}
		}
	}()
}

func (w *AuditWorker) flushAudit(entries []*AuditLog) {
	if err := w.db.Create(&entries).Error; err != nil {
		log.Printf("[audit] failed to flush %d entries: %v", len(entries), err)
	}
}

// Log sends an audit log entry asynchronously.
func (w *AuditWorker) Log(userID, username, ip, action, resource, detail, status string, duration int64) {
	entry := &AuditLog{
		UserID:   userID,
		Username: username,
		IP:       ip,
		Action:   action,
		Resource: resource,
		Detail:   detail,
		Status:   status,
		Duration: duration,
	}
	select {
	case w.ch <- entry:
	default:
		log.Printf("[audit] channel full, dropping: %s %s %s", action, username, resource)
	}
}

// LogFromCtx is a convenience wrapper that extracts user info from the Fiber context.
func (w *AuditWorker) LogFromCtx(c *fiber.Ctx, action, resource, detail, status string, duration int64) {
	var userID, username string
	if s, ok := c.Locals("session").(*Session); ok {
		userID = s.UserID
		username = s.Username
	}
	w.Log(userID, username, c.IP(), action, resource, detail, status, duration)
}
