package model

import "gorm.io/gorm"

// Repos holds all repository interfaces. Injected into Handler, Dispatcher, etc.
type Repos struct {
	db           *gorm.DB // unexported; used only by WithTx
	hasFTS       bool
	onTaskUpdate TaskUpdateFunc

	Users     UserRepo
	Files     FileRepo
	Trash     TrashRepo
	Sessions  SessionRepo
	Jobs      JobRepo
	Tasks     TaskRepo
	Audit     AuditRepo
	Shares    ShareRepo
	Workspace WorkspaceRepo
	Cleanup   UserCleanupRepo
}

// NewRepos creates a Repos backed by GORM.
func NewRepos(db *gorm.DB, hasFTS bool, onTaskUpdate TaskUpdateFunc) *Repos {
	taskRepo := &gormTaskRepo{db: db, onTaskUpdate: onTaskUpdate}
	return &Repos{
		db:           db,
		hasFTS:       hasFTS,
		onTaskUpdate: onTaskUpdate,
		Users:        &gormUserRepo{db: db},
		Files:        &gormFileRepo{db: db, hasFTS: hasFTS},
		Trash:        &gormTrashRepo{db: db},
		Sessions:     NewSessionStore(db),
		Jobs:         &gormJobRepo{db: db, tasks: taskRepo},
		Tasks:        taskRepo,
		Audit:        &gormAuditRepo{db: db},
		Shares:       &gormShareRepo{db: db},
		Workspace:    &gormWorkspaceRepo{db: db},
		Cleanup:      &gormUserCleanupRepo{db: db},
	}
}

// WithTx executes fn within a database transaction. The fn receives a Repos
// where every repo is bound to the same transaction.
func (r *Repos) WithTx(fn func(tx *Repos) error) error {
	return r.db.Transaction(func(tx *gorm.DB) error {
		return fn(NewRepos(tx, r.hasFTS, r.onTaskUpdate))
	})
}
