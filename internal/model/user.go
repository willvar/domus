package model

import (
	"time"

	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"
)

type User struct {
	ID           string    `gorm:"primaryKey;type:varchar(36)" json:"id"`
	Username     string    `gorm:"uniqueIndex;not null" json:"username"`
	DisplayName  string    `gorm:"default:''" json:"display_name"`
	PasswordHash string    `gorm:"not null" json:"-"`
	Role         string    `gorm:"not null;default:user" json:"role"`
	Permissions  int64     `gorm:"not null;default:15" json:"permissions"`
	Email        string    `gorm:"default:''" json:"email"`
	TOTPSecret   string    `gorm:"default:''" json:"-"`
	TOTPEnabled  bool      `gorm:"default:false" json:"totp_enabled"`
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

// User operations

func CreateUser(username, password, role string, permissions int64) (*User, error) {
	hash, err := HashPassword(password)
	if err != nil {
		return nil, err
	}
	user := &User{
		ID:           uuid.New().String(),
		Username:     username,
		PasswordHash: hash,
		Role:         role,
		Permissions:  permissions,
	}
	if err := db.Create(user).Error; err != nil {
		return nil, err
	}
	return user, nil
}

func GetUserByUsername(username string) (*User, error) {
	u := &User{}
	if err := db.Where("username = ?", username).First(u).Error; err != nil {
		return nil, err
	}
	return u, nil
}

func GetUserByID(id string) (*User, error) {
	u := &User{}
	if err := db.First(u, "id = ?", id).Error; err != nil {
		return nil, err
	}
	return u, nil
}

func ListUsers() ([]User, error) {
	var users []User
	if err := db.Order("id").Find(&users).Error; err != nil {
		return nil, err
	}
	return users, nil
}

func UpdateUser(id string, role string, permissions int64) error {
	return db.Model(&User{}).Where("id = ?", id).Updates(map[string]interface{}{
		"role":        role,
		"permissions": permissions,
	}).Error
}

func UpdateUserPassword(id string, password string) error {
	hash, err := HashPassword(password)
	if err != nil {
		return err
	}
	return db.Model(&User{}).Where("id = ?", id).Update("password_hash", hash).Error
}

func UpdateUserEmail(id string, email string) error {
	return db.Model(&User{}).Where("id = ?", id).Update("email", email).Error
}

func UpdateUserTOTP(id string, secret string, enabled bool) error {
	return db.Model(&User{}).Where("id = ?", id).Updates(map[string]interface{}{
		"totp_secret":  secret,
		"totp_enabled": enabled,
	}).Error
}

func UpdateUserDisplayName(id string, displayName string) error {
	return db.Model(&User{}).Where("id = ?", id).Update("display_name", displayName).Error
}


func DeleteUser(id string) error {
	return db.Where("id = ?", id).Delete(&User{}).Error
}

func UserCount() (int, error) {
	var count int64
	if err := db.Model(&User{}).Count(&count).Error; err != nil {
		return 0, err
	}
	return int(count), nil
}
