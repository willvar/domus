package model

import (
	"fmt"
	"strings"

	"zephyr/config"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

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

func quoteIdentifier(name string) string {
	return `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
}

// ResetDatabase drops and recreates the target database.
func ResetDatabase(cfg config.DatabaseConfig) error {
	adminCfg := cfg
	adminCfg.DBName = "postgres"
	adminDB, err := gorm.Open(postgres.Open(adminCfg.DSN()), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		return fmt.Errorf("connect to postgres: %w", err)
	}

	targetDB := quoteIdentifier(cfg.DBName)
	terminateSQL := `SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE datname = ? AND pid <> pg_backend_pid()`
	if err := adminDB.Exec(terminateSQL, cfg.DBName).Error; err != nil {
		return fmt.Errorf("terminate database connections for %s: %w", cfg.DBName, err)
	}
	if err := adminDB.Exec("DROP DATABASE IF EXISTS " + targetDB).Error; err != nil {
		return fmt.Errorf("drop database %s: %w", cfg.DBName, err)
	}
	if err := adminDB.Exec("CREATE DATABASE " + targetDB).Error; err != nil {
		return fmt.Errorf("create database %s: %w", cfg.DBName, err)
	}

	sqlDB, _ := adminDB.DB()
	_ = sqlDB.Close()
	return nil
}

// InitDB initializes the database connection, runs migrations, and returns:
//   - the *gorm.DB handle
//   - whether pg_jieba full-text search is available
func InitDB(cfg config.DatabaseConfig) (*gorm.DB, bool, error) {
	if err := ensureDatabase(cfg); err != nil {
		return nil, false, err
	}

	db, err := gorm.Open(postgres.Open(cfg.DSN()), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		return nil, false, fmt.Errorf("open db: %w", err)
	}

	if err := db.AutoMigrate(&User{}, &FileRecord{}, &DBSession{}, &Task{}, &AuditLog{}, &WorkspaceState{}, &Share{}); err != nil {
		return nil, false, fmt.Errorf("auto migrate: %w", err)
	}

	// One-time migration: drop legacy jobs table (dispatcher/jobs subsystem removed)
	db.Exec("DROP TABLE IF EXISTS jobs")

	// One-time migration: drop legacy uploads table (merged into files)
	db.Exec("DROP TABLE IF EXISTS uploads")

	// One-time migration: drop share_type column (sharing is now user-to-user only)
	db.Exec("ALTER TABLE shares DROP COLUMN IF EXISTS share_type")

	// Full-text search: prefer pg_jieba for Chinese segmentation
	db.Exec("CREATE EXTENSION IF NOT EXISTS pg_jieba")
	var ftsConf string
	fts := false
	if err := db.Raw("SELECT cfgname FROM pg_ts_config WHERE cfgname = 'jiebacfg' LIMIT 1").Scan(&ftsConf).Error; err == nil && ftsConf == "jiebacfg" {
		fts = true
		db.Exec("CREATE INDEX IF NOT EXISTS idx_fts ON files USING gin(search_vector)")
	}

	// pg_trgm for ILIKE fallback search (always available, built-in contrib)
	db.Exec("CREATE EXTENSION IF NOT EXISTS pg_trgm")
	db.Exec("CREATE INDEX IF NOT EXISTS idx_files_name_trgm ON files USING gin(name gin_trgm_ops)")

	return db, fts, nil
}
