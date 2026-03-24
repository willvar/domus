package model

import (
	"fmt"

	"zephyr/config"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// Permission bitmask constants
const (
	PermRead   int64 = 1
	PermUpload int64 = 2
	PermEdit   int64 = 4
	PermDelete int64 = 8
	PermAll    int64 = 15
)

// DefaultPermissions returns the default permission bitmask for a role.
func DefaultPermissions(role string) int64 {
	switch role {
	case "admin":
		return PermAll
	case "user":
		return PermAll
	default:
		return PermAll
	}
}

var db *gorm.DB

// SetDB sets the package-level db variable. This is intended for use in tests
// where the caller manages the database connection directly.
func SetDB(d *gorm.DB) {
	db = d
}

func ensureDatabase(cfg config.DatabaseConfig) error {
	// Connect to default "postgres" database to check/create target database
	adminCfg := cfg
	adminCfg.DBName = "postgres"
	adminDSN := adminCfg.DSN()
	adminDB, err := gorm.Open(postgres.Open(adminDSN), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		return fmt.Errorf("connect to postgres: %w", err)
	}

	var count int64
	adminDB.Raw("SELECT COUNT(*) FROM pg_database WHERE datname = ?", cfg.DBName).Scan(&count)
	if count == 0 {
		if err := adminDB.Exec(fmt.Sprintf("CREATE DATABASE %q", cfg.DBName)).Error; err != nil {
			return fmt.Errorf("create database %s: %w", cfg.DBName, err)
		}
	}

	sqlDB, _ := adminDB.DB()
	_ = sqlDB.Close()
	return nil
}

// InitDB initializes the database connection, runs migrations, and returns the
// underlying *gorm.DB so that callers (e.g. cmd/root.go) can pass it to other
// components that need their own reference.
func InitDB(cfg config.DatabaseConfig) (*gorm.DB, error) {
	if err := ensureDatabase(cfg); err != nil {
		return nil, err
	}

	var err error
	db, err = gorm.Open(postgres.Open(cfg.DSN()), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		return nil, fmt.Errorf("open db: %w", err)
	}

	if err := db.AutoMigrate(&User{}, &TrashItem{}, &UploadRecord{}, &Bookmark{}, &FileRecord{}, &DBSession{}, &Job{}, &AuditLog{}); err != nil {
		return nil, fmt.Errorf("auto migrate: %w", err)
	}

	return db, nil
}
