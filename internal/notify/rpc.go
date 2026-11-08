// Package notify sends best-effort invalidation hints, never business commands.
package notify

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"net/rpc"
	"os"
	"path/filepath"
	"sync/atomic"
	"syscall"
	"time"
)

const sendTimeout = 100 * time.Millisecond

var socketPath atomic.Value

// Event carries identities only, never file contents, keys or signed URLs.
type Event struct {
	Kind       string
	UserID     string
	TaskID     string
	Inode      int64
	Generation int64
}

func (e Event) valid() bool {
	if e.UserID == "" || len(e.UserID) > 128 {
		return false
	}
	switch e.Kind {
	case "task":
		return e.TaskID != "" && len(e.TaskID) <= 128
	case "file":
		return e.Inode > 0 && e.Generation > 0
	case "rendition":
		return e.Inode > 0
	case "dofs":
		return true
	}
	return false
}

// SocketPath shares the existing private runtime directory, not the DOFS
// control protocol or any public network listener.
func SocketPath(controlSocket string) string {
	if controlSocket == "" {
		return ""
	}
	return filepath.Join(filepath.Dir(controlSocket), "events.sock")
}

// Configure selects the endpoint for this process. An empty endpoint disables
// hints, for consumers that have not enabled the Domus integration.
func Configure(path string) { socketPath.Store(path) }

// Publish runs after a successful state write. A short, one-shot RPC cannot
// roll it back; a missing or busy receiver drops the hint without retrying.
func Publish(event Event) {
	path, _ := socketPath.Load().(string)
	if path == "" || !event.valid() {
		return
	}
	dialer := net.Dialer{Timeout: sendTimeout}
	conn, err := dialer.Dial("unix", path)
	if err == nil {
		defer conn.Close()
		_ = conn.SetDeadline(time.Now().Add(sendTimeout))
		client := rpc.NewClient(conn)
		defer client.Close()
		err = client.Call("Events.NotifyChange", event, &struct{}{})
	}
	if err != nil {
		log.Printf("[events] notification dropped (kind=%s)", event.Kind)
	}
}

type receiver struct{ receive func(Event) }

func (r *receiver) NotifyChange(event Event, _ *struct{}) error {
	if !event.valid() {
		return errors.New("invalid change hint")
	}
	r.receive(event)
	return nil
}

// Listen uses the standard RPC codec on an owner-only Unix socket. It does
// not retain messages. Existing state is read on view entry or reconnection.
func Listen(ctx context.Context, path string, receive func(Event)) (*net.UnixListener, error) {
	if path == "" || !filepath.IsAbs(path) {
		return nil, errors.New("event socket must be an absolute path")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return nil, err
	}
	if info, err := os.Lstat(path); err == nil {
		if info.Mode()&os.ModeSocket == 0 {
			return nil, errors.New("event socket path is occupied by a non-socket")
		}
		conn, dialErr := net.DialTimeout("unix", path, sendTimeout)
		if dialErr == nil {
			conn.Close()
			return nil, errors.New("event socket is already in use")
		}
		if !errors.Is(dialErr, syscall.ECONNREFUSED) && !errors.Is(dialErr, os.ErrNotExist) {
			return nil, fmt.Errorf("cannot inspect existing event socket: %w", dialErr)
		}
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return nil, err
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil {
		return nil, err
	}
	if err := os.Chmod(path, 0600); err != nil {
		listener.Close()
		return nil, err
	}
	server := rpc.NewServer()
	if err := server.RegisterName("Events", &receiver{receive: receive}); err != nil {
		listener.Close()
		return nil, err
	}
	done := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			listener.Close()
		case <-done:
		}
	}()
	go func() {
		defer close(done)
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			_ = conn.SetDeadline(time.Now().Add(2 * time.Second))
			go server.ServeConn(conn)
		}
	}()
	return listener, nil
}
