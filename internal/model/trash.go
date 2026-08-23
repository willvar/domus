package model

import "time"

const (
	TrashStatePending   = "pending"
	TrashStateReady     = "ready"
	TrashStateRestoring = "restoring"
	TrashStateDeleting  = "deleting"
)

// TrashEntry is Domus product metadata for one user-initiated deletion. The
// payload itself remains a normal DOFS inode under Domus's private namespace;
// object keys, generations and encryption metadata stay authoritative in
// DOFS. OriginalPath is display/restore metadata, never the entry identity.
type TrashEntry struct {
	ID           string `gorm:"primaryKey;size:64" json:"id"`
	UserID       string `gorm:"not null;size:128;uniqueIndex:idx_trash_user_inode,priority:1;index:idx_trash_user_deleted,priority:1" json:"-"`
	RootInode    int64  `gorm:"not null;uniqueIndex:idx_trash_user_inode,priority:2" json:"inode"`
	OriginalPath string `gorm:"not null" json:"original_path"`
	OriginalName string `gorm:"not null" json:"name"`
	IsDir        bool   `gorm:"not null" json:"is_dir"`
	Size         int64  `gorm:"not null;default:0" json:"size"`
	State        string `gorm:"not null;size:16;index" json:"-"`

	// Operation fields make cross-database mutations recoverable. They are
	// populated before touching DOFS and cleared whenever the entry is ready.
	OperationInode int64  `gorm:"not null;default:0" json:"-"`
	OperationPath  string `gorm:"not null;default:''" json:"-"`
	TargetPath     string `gorm:"not null;default:''" json:"-"`

	DeletedAt time.Time `gorm:"not null;index:idx_trash_user_deleted,priority:2,sort:desc" json:"deleted_at"`
	CreatedAt time.Time `json:"-"`
	UpdatedAt time.Time `json:"-"`
}

func (TrashEntry) TableName() string { return "domus_trash_entries" }

// TrashRepo persists deletion events independently from the DOFS namespace.
// State transitions are compare-and-set so concurrent requests cannot mutate
// the same trash entry simultaneously.
type TrashRepo interface {
	Create(entry *TrashEntry) error
	Get(userID, id string) (*TrashEntry, error)
	List(userID string, limit, offset int) ([]TrashEntry, error)
	ListRecoverable() ([]TrashEntry, error)
	Transition(userID, id, fromState, toState string, operationInode int64, operationPath, targetPath string) error
	MarkReady(userID, id, fromState string) error
	Delete(userID, id string) error
	DeleteByUserID(userID string) error
}
