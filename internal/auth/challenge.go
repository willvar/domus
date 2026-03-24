package auth

import (
	"crypto/hmac"
	"crypto/rand"
	"encoding/hex"
	"sync"
	"time"
)

// timedStore is a generic in-memory key-value store with per-entry TTL.
type timedStore[T any] struct {
	mu      sync.Mutex
	entries map[string]timedEntry[T]
}

type timedEntry[T any] struct {
	Value     T
	ExpiresAt time.Time
}

func newTimedStore[T any]() *timedStore[T] {
	return &timedStore[T]{entries: make(map[string]timedEntry[T])}
}

func (s *timedStore[T]) Set(key string, val T, ttl time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.entries[key] = timedEntry[T]{Value: val, ExpiresAt: time.Now().Add(ttl)}
}

func (s *timedStore[T]) Get(key string) (T, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.entries[key]
	if !ok || time.Now().After(e.ExpiresAt) {
		delete(s.entries, key)
		var zero T
		return zero, false
	}
	return e.Value, true
}

func (s *timedStore[T]) Delete(key string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.entries, key)
}

func (s *timedStore[T]) Clean() {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	for k, e := range s.entries {
		if now.After(e.ExpiresAt) {
			delete(s.entries, k)
		}
	}
}

// --- Exported Types ---

// LoginChallenge holds a verified identity pending session creation.
// Methods is empty for direct login, non-empty when additional verification is required.
type LoginChallenge struct {
	UserID      string
	Username    string
	Role        string
	Permissions int64
	Methods     []string // [] = direct, ["email"] / ["otp"] / ["email","otp"] = need code
	EmailCode   string   // stored email code (if methods contains "email")
}

// EmailBindEntry holds a pending email-bind verification code.
type EmailBindEntry struct {
	Email string
	Code  string
}

// --- Constants ---

const (
	MaxLoginAttempts = 3
	LockoutDuration  = 5 * time.Minute
)

// --- Login Attempt (unexported) ---

type loginAttempt struct {
	FailCount   int
	LockedUntil time.Time
}

// --- ChallengeManager ---

// ChallengeManager holds all in-memory timed stores for authentication challenges,
// email bind codes, pending TOTPs, login rate-limiting, and OTP replay prevention.
type ChallengeManager struct {
	LoginChallenges *timedStore[*LoginChallenge]
	EmailBindCodes  *timedStore[*EmailBindEntry]
	PendingTOTPs    *timedStore[string]
	LoginAttempts   *timedStore[*loginAttempt]
	UsedOTPCodes    *timedStore[bool]
	loginAttemptMu  sync.Mutex
}

// NewChallengeManager creates a ChallengeManager with all stores initialized.
func NewChallengeManager() *ChallengeManager {
	return &ChallengeManager{
		LoginChallenges: newTimedStore[*LoginChallenge](),
		EmailBindCodes:  newTimedStore[*EmailBindEntry](),
		PendingTOTPs:    newTimedStore[string](),
		LoginAttempts:   newTimedStore[*loginAttempt](),
		UsedOTPCodes:    newTimedStore[bool](),
	}
}

func generateToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// --- Login Challenge Methods ---

// CreateLoginChallenge stores a challenge and returns a random token (5-min TTL).
func (cm *ChallengeManager) CreateLoginChallenge(ch *LoginChallenge) (string, error) {
	token, err := generateToken()
	if err != nil {
		return "", err
	}
	cm.LoginChallenges.Set(token, ch, 5*time.Minute)
	return token, nil
}

// GetLoginChallenge retrieves a challenge by token. Returns nil if expired or not found.
func (cm *ChallengeManager) GetLoginChallenge(token string) *LoginChallenge {
	ch, ok := cm.LoginChallenges.Get(token)
	if !ok {
		return nil
	}
	return ch
}

// DeleteLoginChallenge removes a challenge by token.
func (cm *ChallengeManager) DeleteLoginChallenge(token string) {
	cm.LoginChallenges.Delete(token)
}

// --- Email Bind Code Methods ---

// StoreEmailBindCode stores a verification code for email binding.
func (cm *ChallengeManager) StoreEmailBindCode(userID, email, code string) {
	cm.EmailBindCodes.Set(userID, &EmailBindEntry{Email: email, Code: code}, 5*time.Minute)
}

// VerifyEmailBindCode checks the code and deletes the entry on success.
func (cm *ChallengeManager) VerifyEmailBindCode(userID, email, code string) bool {
	e, ok := cm.EmailBindCodes.Get(userID)
	if !ok {
		return false
	}
	if hmac.Equal([]byte(e.Email), []byte(email)) && hmac.Equal([]byte(e.Code), []byte(code)) {
		cm.EmailBindCodes.Delete(userID)
		return true
	}
	return false
}

// --- Pending TOTP Methods ---

// StorePendingTOTP stores a pending TOTP secret for a user (10-min TTL).
func (cm *ChallengeManager) StorePendingTOTP(userID, secret string) {
	cm.PendingTOTPs.Set(userID, secret, 10*time.Minute)
}

// GetPendingTOTP retrieves a pending TOTP secret for a user.
func (cm *ChallengeManager) GetPendingTOTP(userID string) string {
	secret, ok := cm.PendingTOTPs.Get(userID)
	if !ok {
		return ""
	}
	return secret
}

// DeletePendingTOTP removes a pending TOTP secret for a user.
func (cm *ChallengeManager) DeletePendingTOTP(userID string) {
	cm.PendingTOTPs.Delete(userID)
}

// --- Login Rate Limiter Methods ---

func loginAttemptKey(ip, method, username string) string {
	return ip + ":" + username + ":" + method
}

// CheckLoginLocked returns true if the ip+method+username is currently locked out.
func (cm *ChallengeManager) CheckLoginLocked(ip, method, username string) bool {
	cm.loginAttemptMu.Lock()
	defer cm.loginAttemptMu.Unlock()
	a, ok := cm.LoginAttempts.Get(loginAttemptKey(ip, method, username))
	if !ok {
		return false
	}
	return time.Now().Before(a.LockedUntil)
}

// RecordLoginFailure increments the fail counter and locks after MaxLoginAttempts.
// Returns true if the account is now (or already was) locked.
func (cm *ChallengeManager) RecordLoginFailure(ip, method, username string) bool {
	cm.loginAttemptMu.Lock()
	defer cm.loginAttemptMu.Unlock()
	key := loginAttemptKey(ip, method, username)
	a, ok := cm.LoginAttempts.Get(key)
	if !ok {
		a = &loginAttempt{}
	}
	a.FailCount++
	if a.FailCount >= MaxLoginAttempts {
		if a.LockedUntil.IsZero() || time.Now().After(a.LockedUntil) {
			a.LockedUntil = time.Now().Add(LockoutDuration)
		}
	}
	cm.LoginAttempts.Set(key, a, LockoutDuration+time.Minute)
	return time.Now().Before(a.LockedUntil)
}

// ClearLoginAttempts resets the fail counter on successful login.
func (cm *ChallengeManager) ClearLoginAttempts(ip, method, username string) {
	cm.loginAttemptMu.Lock()
	defer cm.loginAttemptMu.Unlock()
	cm.LoginAttempts.Delete(loginAttemptKey(ip, method, username))
}

// --- OTP Replay Prevention Methods ---

// MarkOTPUsed records an OTP code as used for a user (90-second TTL).
func (cm *ChallengeManager) MarkOTPUsed(userID, code string) {
	cm.UsedOTPCodes.Set(userID+":"+code, true, 90*time.Second)
}

// IsOTPUsed checks if an OTP code was already used by this user.
func (cm *ChallengeManager) IsOTPUsed(userID, code string) bool {
	_, ok := cm.UsedOTPCodes.Get(userID + ":" + code)
	return ok
}

// --- Cleanup ---

// CleanAllExpiredEntries removes expired entries from all timed stores.
func (cm *ChallengeManager) CleanAllExpiredEntries() {
	cm.LoginChallenges.Clean()
	cm.EmailBindCodes.Clean()
	cm.PendingTOTPs.Clean()
	cm.LoginAttempts.Clean()
	cm.UsedOTPCodes.Clean()
}
