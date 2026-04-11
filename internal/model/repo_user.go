package model

import (
	"github.com/google/uuid"
	"gorm.io/gorm"
)

// UserRepo defines user data access operations.
type UserRepo interface {
	Create(username, password, role, wrappedKEK string) (*User, error)
	GetByID(id string) (*User, error)
	GetByUsername(username string) (*User, error)
	List() ([]User, error)
	UpdateRole(id, role string) error
	UpdatePassword(id, password string) error
	UpdateEmail(id, email string) error
	UpdateTOTP(id, secret string, enabled bool) error
	UpdateDisplayName(id, displayName string) error
	Delete(id string) error
	Count() (int, error)
	CountByRole(role string) (int, error)
	GetWrappedKEK(userID string) (string, error)
	SetWrappedKEK(userID, wrappedKEK string) error
}

type gormUserRepo struct{ db *gorm.DB }

func (r *gormUserRepo) Create(username, password, role, wrappedKEK string) (*User, error) {
	hash, err := HashPassword(password)
	if err != nil {
		return nil, err
	}
	user := &User{
		ID:           uuid.New().String(),
		Username:     username,
		PasswordHash: hash,
		Role:         role,
		WrappedKEK:   wrappedKEK,
	}
	if err := r.db.Create(user).Error; err != nil {
		return nil, err
	}
	return user, nil
}

func (r *gormUserRepo) GetByID(id string) (*User, error) {
	u := &User{}
	if err := r.db.First(u, "id = ?", id).Error; err != nil {
		return nil, err
	}
	return u, nil
}

func (r *gormUserRepo) GetByUsername(username string) (*User, error) {
	u := &User{}
	if err := r.db.Where("username = ?", username).First(u).Error; err != nil {
		return nil, err
	}
	return u, nil
}

func (r *gormUserRepo) List() ([]User, error) {
	var users []User
	if err := r.db.Order("id").Find(&users).Error; err != nil {
		return nil, err
	}
	return users, nil
}

func (r *gormUserRepo) UpdateRole(id, role string) error {
	return r.db.Model(&User{}).Where("id = ?", id).Update("role", role).Error
}

func (r *gormUserRepo) UpdatePassword(id, password string) error {
	hash, err := HashPassword(password)
	if err != nil {
		return err
	}
	return r.db.Model(&User{}).Where("id = ?", id).Update("password_hash", hash).Error
}

func (r *gormUserRepo) UpdateEmail(id, email string) error {
	return r.db.Model(&User{}).Where("id = ?", id).Update("email", email).Error
}

func (r *gormUserRepo) UpdateTOTP(id, secret string, enabled bool) error {
	return r.db.Model(&User{}).Where("id = ?", id).Updates(map[string]interface{}{
		"totp_secret":  secret,
		"totp_enabled": enabled,
	}).Error
}

func (r *gormUserRepo) UpdateDisplayName(id, displayName string) error {
	return r.db.Model(&User{}).Where("id = ?", id).Update("display_name", displayName).Error
}

func (r *gormUserRepo) Delete(id string) error {
	return r.db.Where("id = ?", id).Delete(&User{}).Error
}

func (r *gormUserRepo) Count() (int, error) {
	var count int64
	if err := r.db.Model(&User{}).Count(&count).Error; err != nil {
		return 0, err
	}
	return int(count), nil
}

func (r *gormUserRepo) CountByRole(role string) (int, error) {
	var count int64
	if err := r.db.Model(&User{}).Where("role = ?", role).Count(&count).Error; err != nil {
		return 0, err
	}
	return int(count), nil
}

func (r *gormUserRepo) GetWrappedKEK(userID string) (string, error) {
	var wrappedKEK string
	err := r.db.Model(&User{}).Where("id = ?", userID).
		Select("wrapped_kek").Scan(&wrappedKEK).Error
	return wrappedKEK, err
}

func (r *gormUserRepo) SetWrappedKEK(userID, wrappedKEK string) error {
	return r.db.Model(&User{}).Where("id = ?", userID).
		Update("wrapped_kek", wrappedKEK).Error
}
