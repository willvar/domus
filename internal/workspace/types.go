package workspace

import (
	"context"
	"errors"
	"io"
	"time"
)

const ControlProtocolVersion = 1

const MaxExecOutputBytes int64 = 16 * 1024 * 1024

var (
	ErrUnavailable      = errors.New("workspace service is unavailable")
	ErrInvalidRequest   = errors.New("invalid workspace request")
	ErrNotManaged       = errors.New("workspace is not managed")
	ErrConflict         = errors.New("workspace conflicts with existing runtime state")
	ErrCapacity         = errors.New("workspace capacity is exhausted")
	ErrOutputLimit      = errors.New("workspace command output limit exceeded")
	ErrSessionLimit     = errors.New("workspace session limit exceeded")
	ErrManagerStopping  = errors.New("workspace manager is stopping")
	ErrUnsupported      = errors.New("workspace execution is supported on Linux only")
	ErrContainerStopped = errors.New("workspace container stopped unexpectedly")
)

// Identity is resolved by the authenticated Domus control plane. The
// workspace daemon still asks DOFS to resolve UserID independently and checks
// that both identities agree before binding any host path.
type Identity struct {
	UserID   string `json:"user_id"`
	Username string `json:"username"`
}

// ValidIdentityName reports whether a user ID or username can be represented
// safely in Docker labels, generated passwd/group entries, and workspace path
// components. It is platform-neutral so the web control plane can reject a
// newly-created incompatible account even when it is built on Windows.
func ValidIdentityName(value string) bool {
	if len(value) == 0 || len(value) > 128 || value[0] == '.' || value[0] == '-' {
		return false
	}
	for _, character := range value {
		if character >= 'a' && character <= 'z' || character >= 'A' && character <= 'Z' ||
			character >= '0' && character <= '9' || character == '_' || character == '.' || character == '-' {
			continue
		}
		return false
	}
	return true
}

type EnsureRequest struct {
	Identity
}

type Status struct {
	UserID      string    `json:"user_id"`
	Username    string    `json:"username,omitempty"`
	ContainerID string    `json:"container_id,omitempty"`
	Container   string    `json:"container,omitempty"`
	Mountpoint  string    `json:"mountpoint,omitempty"`
	MountID     string    `json:"mount_id,omitempty"`
	ImageID     string    `json:"image_id,omitempty"`
	SpecHash    string    `json:"spec_hash,omitempty"`
	State       string    `json:"state"`
	Desired     bool      `json:"desired"`
	ActiveExecs int       `json:"active_execs"`
	LastUsedAt  time.Time `json:"last_used_at,omitempty"`
	UpdatedAt   time.Time `json:"updated_at"`
	LastError   string    `json:"last_error,omitempty"`
}

type Health struct {
	Status          string    `json:"status"`
	ProtocolVersion int       `json:"protocol_version"`
	MaxRunning      int       `json:"max_running"`
	Desired         int       `json:"desired"`
	Running         int       `json:"running"`
	ActiveExecs     int       `json:"active_execs"`
	Degraded        int       `json:"degraded"`
	LastReconcileAt time.Time `json:"last_reconcile_at,omitempty"`
	LastError       string    `json:"last_error,omitempty"`
}

// ExecRequest is the bounded, non-interactive execution surface used by
// preview/transcode jobs and other server-owned tasks. Command is passed as an
// argv vector and is never interpreted by a host shell.
type ExecRequest struct {
	Identity
	Command        []string          `json:"command"`
	WorkingDir     string            `json:"working_dir,omitempty"`
	Environment    map[string]string `json:"environment,omitempty"`
	TimeoutSeconds int               `json:"timeout_seconds,omitempty"`
	Stdin          []byte            `json:"stdin,omitempty"`
}

type ExecResult struct {
	ExitCode  int           `json:"exit_code"`
	Stdout    []byte        `json:"stdout,omitempty"`
	Stderr    []byte        `json:"stderr,omitempty"`
	Duration  time.Duration `json:"duration"`
	Truncated bool          `json:"truncated,omitempty"`
}

type SessionRequest struct {
	Identity
	Command     []string          `json:"command,omitempty"`
	WorkingDir  string            `json:"working_dir,omitempty"`
	Environment map[string]string `json:"environment,omitempty"`
	Columns     uint              `json:"columns,omitempty"`
	Rows        uint              `json:"rows,omitempty"`
}

// Session is a raw TTY stream. Output is read from Read, terminal input is
// written to Write, and Wait returns the process exit code.
type Session interface {
	io.ReadWriteCloser
	Resize(context.Context, uint, uint) error
	Wait() (int, error)
}

// Service is implemented by the local Unix-socket client and by fakes in
// higher-level tests. It keeps Docker-specific types out of the web process.
type Service interface {
	Ensure(context.Context, Identity) (Status, error)
	Status(context.Context, string) (Status, error)
	List(context.Context) ([]Status, error)
	Stop(context.Context, string) (Status, error)
	Remove(context.Context, string) (Status, error)
	Exec(context.Context, ExecRequest) (ExecResult, error)
	OpenSession(context.Context, SessionRequest) (Session, error)
	Health(context.Context, bool) (Health, error)
}

type APIError struct {
	StatusCode int
	Code       string
	Message    string
}

func (e *APIError) Error() string {
	if e.Message == "" {
		return "workspace control API " + e.Code
	}
	return "workspace control API " + e.Code + ": " + e.Message
}
