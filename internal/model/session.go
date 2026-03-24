package model

import (
	"crypto/rand"
	"encoding/hex"
	"time"

	"gorm.io/gorm"
)

// Session is the lightweight in-memory representation used by middleware/handlers.
type Session struct {
	UserID      string
	Username    string
	Role        string
	Permissions int64
	CreatedAt   time.Time
}

// DBSession is the GORM model for persistent sessions.
type DBSession struct {
	ID          string    `gorm:"primaryKey"`
	UserID      string    `gorm:"not null;index"`
	Username    string    `gorm:"not null"`
	Role        string    `gorm:"not null"`
	Permissions int64     `gorm:"not null"`
	CreatedAt   time.Time `gorm:"not null"`
	ExpiresAt   time.Time `gorm:"not null;index"`
}

func (DBSession) TableName() string { return "sessions" }

// SessionStore provides database-backed session management. It holds its own
// *gorm.DB reference for proper dependency injection.
type SessionStore struct {
	DB *gorm.DB
}

// NewSessionStore creates a SessionStore with the given database connection.
func NewSessionStore(db *gorm.DB) *SessionStore {
	return &SessionStore{DB: db}
}

func generateSessionID() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

func (s *SessionStore) Create(userID string, username, role string, permissions int64) (string, error) {
	id, err := generateSessionID()
	if err != nil {
		return "", err
	}

	now := time.Now()
	dbSession := &DBSession{
		ID:          id,
		UserID:      userID,
		Username:    username,
		Role:        role,
		Permissions: permissions,
		CreatedAt:   now,
		ExpiresAt:   now.Add(7 * 24 * time.Hour),
	}
	if err := s.DB.Create(dbSession).Error; err != nil {
		return "", err
	}
	return id, nil
}

func (s *SessionStore) Get(id string) *Session {
	var dbSession DBSession
	if err := s.DB.Where("id = ? AND expires_at > ?", id, time.Now()).First(&dbSession).Error; err != nil {
		return nil
	}
	return &Session{
		UserID:      dbSession.UserID,
		Username:    dbSession.Username,
		Role:        dbSession.Role,
		Permissions: dbSession.Permissions,
		CreatedAt:   dbSession.CreatedAt,
	}
}

func (s *SessionStore) Delete(id string) {
	s.DB.Delete(&DBSession{}, "id = ?", id)
}

// DeleteByUserID removes all sessions for a given user.
func (s *SessionStore) DeleteByUserID(userID string) {
	s.DB.Where("user_id = ?", userID).Delete(&DBSession{})
}

// DeleteByUserIDExcept removes all sessions for a user except the specified one.
func (s *SessionStore) DeleteByUserIDExcept(userID string, exceptSessionID string) {
	s.DB.Where("user_id = ? AND id != ?", userID, exceptSessionID).Delete(&DBSession{})
}

// CleanExpiredSessions removes all expired sessions from the database.
// It uses the package-level db variable.
func CleanExpiredSessions() {
	db.Where("expires_at < ?", time.Now()).Delete(&DBSession{})
}
