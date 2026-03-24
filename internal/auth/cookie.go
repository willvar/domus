package auth

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
)

// SignCookie signs a session ID with HMAC-SHA256, returns "id.signature"
func SignCookie(sessionID, secret string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(sessionID))
	sig := hex.EncodeToString(mac.Sum(nil))
	return sessionID + "." + sig
}

// VerifyCookie verifies a signed cookie value, returns the session ID or error
func VerifyCookie(cookieValue, secret string) (string, error) {
	parts := strings.SplitN(cookieValue, ".", 2)
	if len(parts) != 2 {
		return "", fmt.Errorf("invalid cookie format")
	}
	sessionID, sig := parts[0], parts[1]

	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(sessionID))
	expected := hex.EncodeToString(mac.Sum(nil))

	if !hmac.Equal([]byte(sig), []byte(expected)) {
		return "", fmt.Errorf("invalid signature")
	}
	return sessionID, nil
}
