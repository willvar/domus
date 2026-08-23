package model

import (
	"errors"
	"fmt"
	"strings"

	"domus/config"

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

// InitDB initializes the product database connection and runs migrations.
// User file contents are not indexed here; filename search is derived from
// the DOFS namespace projection.
func InitDB(cfg config.DatabaseConfig) (*gorm.DB, error) {
	if err := ensureDatabase(cfg); err != nil {
		return nil, err
	}

	db, err := gorm.Open(postgres.Open(cfg.DSN()), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		return nil, fmt.Errorf("open db: %w", err)
	}
	if err := rejectLegacyFileSchema(db); err != nil {
		return nil, err
	}

	// Path-only shares belong to the retired file model and cannot be mapped
	// safely to an independent DOFS inode. Revoke them before adding the inode
	// constraint rather than preserving a stale path capability.
	if err := revokeLegacyPathShares(db); err != nil {
		return nil, err
	}

	if err := db.AutoMigrate(&User{}, &DBSession{}, &Task{}, &AuditLog{}, &WorkspaceState{}, &Share{}, &TrashEntry{}); err != nil {
		return nil, fmt.Errorf("auto migrate: %w", err)
	}
	return db, nil
}

func rejectLegacyFileSchema(db *gorm.DB) error {
	if db.Migrator().HasTable("files") {
		return errors.New("legacy files table detected: this release has no in-place file migration; back up with the previous Domus release, then run `domus reset -c <config> --yes` or use an explicit migration tool")
	}
	return nil
}

func revokeLegacyPathShares(db *gorm.DB) error {
	if !db.Migrator().HasTable(&Share{}) || !db.Migrator().HasColumn(&Share{}, "FileInode") {
		return nil
	}
	if err := db.Exec("DELETE FROM shares WHERE file_inode IS NULL OR file_inode <= 0").Error; err != nil {
		return fmt.Errorf("revoke legacy path-only shares: %w", err)
	}
	return nil
}
