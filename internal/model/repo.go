package model

import "gorm.io/gorm"

// Repos holds all repository interfaces. Injected into handlers and services.
type Repos struct {
	Users     UserRepo
	Files     FileRepo
	Sessions  SessionRepo
	Tasks     TaskRepo
	Audit     AuditRepo
	Shares    ShareRepo
	Workspace WorkspaceRepo
	Cleanup   UserCleanupRepo
}

// NewRepos creates a Repos backed by GORM.
func NewRepos(db *gorm.DB, onTaskUpdate TaskUpdateFunc) *Repos {
	taskRepo := &gormTaskRepo{db: db, onTaskUpdate: onTaskUpdate}
	return &Repos{
		Users:     &gormUserRepo{db: db},
		Files:     nil, // wired once to internal/fileview after DOFS opens
		Sessions:  NewSessionStore(db),
		Tasks:     taskRepo,
		Audit:     &gormAuditRepo{db: db},
		Shares:    &gormShareRepo{db: db},
		Workspace: &gormWorkspaceRepo{db: db},
		Cleanup:   &gormUserCleanupRepo{db: db},
	}
}
