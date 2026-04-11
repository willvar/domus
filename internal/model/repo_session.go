package model

import "time"

// SessionRepo defines session data access operations.
// SessionStore already implements most of these methods; we add CleanExpired
// and SetPopulateKEK to complete the interface.
type SessionRepo interface {
	Create(userID, username, role string) (string, error)
	Get(id string) *Session
	Delete(id string)
	DeleteByUserID(userID string)
	DeleteByUserIDExcept(userID, exceptSessionID string)
	CleanExpired()
	SetPopulateKEK(fn func(*Session))
}

// CleanExpired removes all expired sessions from the database.
func (s *SessionStore) CleanExpired() {
	s.DB.Where("expires_at < ?", time.Now()).Delete(&DBSession{})
}

// SetPopulateKEK sets the optional hook to load KEK after session retrieval.
func (s *SessionStore) SetPopulateKEK(fn func(*Session)) {
	s.PopulateKEK = fn
}
