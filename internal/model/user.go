package model

import (
	"time"

	"golang.org/x/crypto/bcrypt"
)

type User struct {
	ID           string    `gorm:"primaryKey;type:varchar(36)" json:"id"`
	Username     string    `gorm:"uniqueIndex;not null" json:"username"`
	DisplayName  string    `gorm:"default:''" json:"display_name"`
	PasswordHash string    `gorm:"not null" json:"-"`
	Role         string    `gorm:"not null;default:user" json:"role"`
	Email        string    `gorm:"default:''" json:"email"`
	TOTPSecret   string    `gorm:"default:''" json:"-"`
	TOTPEnabled  bool      `gorm:"default:false" json:"totp_enabled"`
	WrappedKEK   string    `gorm:"default:''" json:"-"`
	CreatedAt    time.Time `json:"created_at"`
}

// Password helpers

func HashPassword(password string) (string, error) {
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	return string(hash), err
}

func CheckPassword(hash, password string) bool {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) == nil
}
