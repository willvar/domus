package auth

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1"
	"encoding/base32"
	"encoding/binary"
	"fmt"
	"math"
	"net/url"
	"time"
)

// GenerateTOTPSecret generates a 20-byte random secret and returns it as a base32 string.
func GenerateTOTPSecret() (string, error) {
	secret := make([]byte, 20)
	if _, err := rand.Read(secret); err != nil {
		return "", err
	}
	return base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(secret), nil
}

// GenerateTOTPURI returns an otpauth:// URI for QR code generation.
func GenerateTOTPURI(secret, username string) string {
	return fmt.Sprintf("otpauth://totp/Domus:%s?secret=%s&issuer=%s&digits=6&period=30",
		url.PathEscape(username), secret, "Domus")
}

// ValidateTOTP checks a 6-digit TOTP code against the secret, allowing ±1 time step drift.
func ValidateTOTP(secret, code string) bool {
	key, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(secret)
	if err != nil {
		return false
	}
	now := time.Now().Unix() / 30
	for _, offset := range []int64{-1, 0, 1} {
		if computeTOTP(key, uint64(now+offset)) == code {
			return true
		}
	}
	return false
}

// computeTOTP generates a 6-digit TOTP code for the given key and counter (RFC 6238 / RFC 4226).
func computeTOTP(key []byte, counter uint64) string {
	buf := make([]byte, 8)
	binary.BigEndian.PutUint64(buf, counter)

	mac := hmac.New(sha1.New, key)
	mac.Write(buf)
	hash := mac.Sum(nil)

	offset := hash[len(hash)-1] & 0x0f
	code := binary.BigEndian.Uint32(hash[offset:offset+4]) & 0x7fffffff

	return fmt.Sprintf("%06d", code%uint32(math.Pow10(6)))
}
