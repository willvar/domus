//go:build linux

package workspace

import (
	"context"
	"io"
)

const (
	labelManaged   = "io.domus.workspace.managed"
	labelManagerID = "io.domus.workspace.manager-id"
	labelUserID    = "io.domus.workspace.user-id"
	labelUsername  = "io.domus.workspace.username"
	labelMountID   = "io.domus.workspace.mount-id"
	labelSpecHash  = "io.domus.workspace.spec-hash"
)

type RuntimeImage struct {
	ID        string
	Reference string
}

type RuntimeContainer struct {
	ID      string
	Name    string
	Running bool
	State   string
	Labels  map[string]string
}

type ContainerSpec struct {
	Name           string
	Hostname       string
	Image          RuntimeImage
	UserID         string
	Username       string
	ManagerID      string
	Mountpoint     string
	PasswdPath     string
	GroupPath      string
	MountID        string
	SpecHash       string
	UID            uint32
	GID            uint32
	NetworkMode    string
	ReadOnlyRootFS bool
	MemoryBytes    int64
	MemorySwap     int64
	NanoCPUs       int64
	PIDsLimit      int64
	TmpfsSize      int64
	ShmSize        int64
	StopTimeout    int
	Keepalive      []string
}

type RuntimeExecRequest struct {
	ContainerID string
	Command     []string
	WorkingDir  string
	Environment []string
	UID         uint32
	GID         uint32
	Stdin       []byte
	OutputLimit int64
}

type RuntimeSessionRequest struct {
	ContainerID string
	Command     []string
	WorkingDir  string
	Environment []string
	UID         uint32
	GID         uint32
	Columns     uint
	Rows        uint
}

type RuntimeProcess interface {
	io.ReadWriteCloser
	Resize(context.Context, uint, uint) error
	Wait() (int, error)
}

// ContainerRuntime is deliberately narrower than the Docker API. Manager
// tests use a fake; only the workspace daemon implementation holds access to
// the Docker socket.
type ContainerRuntime interface {
	Ping(context.Context) error
	ResolveImage(context.Context, string, string) (RuntimeImage, error)
	FindByName(context.Context, string) (RuntimeContainer, bool, error)
	ListManaged(context.Context) ([]RuntimeContainer, error)
	Create(context.Context, ContainerSpec) (RuntimeContainer, error)
	Start(context.Context, string) error
	Stop(context.Context, string, int) error
	Remove(context.Context, string) error
	Exec(context.Context, RuntimeExecRequest) (ExecResult, error)
	OpenSession(context.Context, RuntimeSessionRequest) (RuntimeProcess, error)
	Close() error
}
