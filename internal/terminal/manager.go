package terminal

import (
	"context"
	"errors"
	"fmt"
	"path"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	workspaceRuntime "domus/internal/workspace"
)

// Session is one browser-owned raw TTY attached to a user's reusable
// workspace container.
type Session struct {
	ID       string
	UserID   string
	Username string
	ConnID   string
	Cwd      string

	runtime   workspaceRuntime.Session
	cancel    context.CancelFunc
	pumpOnce  sync.Once
	closeOnce sync.Once
	exitOnce  sync.Once
	pushBytes func(string, []byte)
	pushExit  func(string, string)
}

// Manager owns the browser-facing terminal sessions. Workspace Manager is the
// authoritative container/session admission boundary; this local limit keeps
// the web process from over-admitting requests before they reach it.
type Manager struct {
	sessions   sync.Map // session ID -> *Session
	workspace  workspaceRuntime.Service
	maxPerUser int

	countMu    sync.Mutex
	userCounts map[string]int

	OnPushBytes func(connID, sessionID string, data []byte)
	OnPushExit  func(connID, sessionID, reason string)
}

func NewManager(workspace workspaceRuntime.Service, maxSessionsPerUser int) *Manager {
	return &Manager{
		workspace: workspace, maxPerUser: maxSessionsPerUser,
		userCounts: make(map[string]int),
	}
}

func (m *Manager) reserve(userID string) bool {
	m.countMu.Lock()
	defer m.countMu.Unlock()
	if m.maxPerUser <= 0 || m.userCounts[userID] >= m.maxPerUser {
		return false
	}
	m.userCounts[userID]++
	return true
}

func (m *Manager) release(userID string) {
	m.countMu.Lock()
	defer m.countMu.Unlock()
	if m.userCounts[userID] <= 1 {
		delete(m.userCounts, userID)
		return
	}
	m.userCounts[userID]--
}

func (m *Manager) remove(session *Session) {
	if session == nil {
		return
	}
	if _, loaded := m.sessions.LoadAndDelete(session.ID); loaded {
		m.release(session.UserID)
	}
}

// Open creates a real shell TTY in the user's reusable workspace container.
func (m *Manager) Open(userID, username, connID, cwd string) (string, error) {
	if m.workspace == nil {
		return "", errors.New("workspace_unavailable")
	}
	if !m.reserve(userID) {
		return "", errors.New("too_many_sessions")
	}
	reserved := true
	defer func() {
		if reserved {
			m.release(userID)
		}
	}()

	if cwd == "" {
		cwd = "/home/" + username + "/"
	}
	if len(cwd) > 4096 || strings.IndexByte(cwd, 0) >= 0 {
		return "", errors.New("invalid_working_directory")
	}
	// Treat even a malformed relative client path as rooted in the user's DOFS
	// namespace. path.Join must never be allowed to resolve above /workspace.
	cwd = path.Clean("/" + strings.TrimPrefix(cwd, "/"))
	workingDirectory := path.Join("/workspace", strings.TrimPrefix(cwd, "/"))
	if cwd == "/" {
		workingDirectory = "/workspace"
	}

	sessionContext, cancel := context.WithCancel(context.Background())
	runtimeSession, err := m.workspace.OpenSession(sessionContext, workspaceRuntime.SessionRequest{
		Identity:   workspaceRuntime.Identity{UserID: userID, Username: username},
		WorkingDir: workingDirectory,
		Columns:    80,
		Rows:       24,
	})
	if err != nil {
		cancel()
		return "", fmt.Errorf("workspace_unavailable: %w", err)
	}

	id := uuid.NewString()
	session := &Session{
		ID: id, UserID: userID, Username: username, ConnID: connID, Cwd: cwd,
		runtime: runtimeSession, cancel: cancel,
		pushBytes: func(sessionID string, data []byte) {
			if m.OnPushBytes != nil {
				m.OnPushBytes(connID, sessionID, data)
			}
		},
		pushExit: func(sessionID, reason string) {
			if m.OnPushExit != nil {
				m.OnPushExit(connID, sessionID, reason)
			}
		},
	}
	m.sessions.Store(id, session)
	reserved = false
	return id, nil
}

func (m *Manager) startPump(session *Session) {
	session.pumpOnce.Do(func() {
		go func() {
			buffer := make([]byte, 32*1024)
			for {
				count, err := session.runtime.Read(buffer)
				if count > 0 {
					session.pushBytes(session.ID, append([]byte(nil), buffer[:count]...))
				}
				if err != nil {
					break
				}
			}
			exitCode, waitErr := session.runtime.Wait()
			reason := "exit"
			if waitErr != nil {
				reason = "workspace_error"
			} else if exitCode != 0 {
				reason = fmt.Sprintf("exit_%d", exitCode)
			}
			m.remove(session)
			if session.cancel != nil {
				session.cancel()
			}
			session.exitOnce.Do(func() { session.pushExit(session.ID, reason) })
		}()
	})
}

func (m *Manager) Get(id string) *Session {
	value, ok := m.sessions.Load(id)
	if !ok {
		return nil
	}
	return value.(*Session)
}

// Input forwards TTY bytes exactly once and in caller order.
func (m *Manager) Input(sessionID, data string) error {
	session := m.Get(sessionID)
	if session == nil {
		return errors.New("session_not_found")
	}
	m.startPump(session)
	if _, err := session.runtime.Write([]byte(data)); err != nil {
		session.exitOnce.Do(func() { session.pushExit(session.ID, "workspace_error") })
		m.closeSession(session)
		return fmt.Errorf("workspace_input_failed: %w", err)
	}
	return nil
}

func (m *Manager) Resize(sessionID string, columns, rows int) error {
	session := m.Get(sessionID)
	if session == nil {
		return errors.New("session_not_found")
	}
	if columns <= 0 || rows <= 0 {
		return errors.New("invalid_terminal_size")
	}
	resizeContext, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	err := session.runtime.Resize(resizeContext, uint(columns), uint(rows))
	cancel()
	if err != nil {
		return fmt.Errorf("workspace_resize_failed: %w", err)
	}
	m.startPump(session)
	return nil
}

func (m *Manager) Close(id string) {
	if session := m.Get(id); session != nil {
		m.closeSession(session)
	}
}

func (m *Manager) closeSession(session *Session) {
	if session == nil {
		return
	}
	session.closeOnce.Do(func() {
		_ = session.runtime.Close()
		if session.cancel != nil {
			session.cancel()
		}
	})
	m.remove(session)
}

// CloseByConn closes all sessions for a disconnected browser concurrently so
// one slow local control connection cannot serialize the rest of the cleanup.
func (m *Manager) CloseByConn(connID string) {
	var sessions []*Session
	m.sessions.Range(func(_, value any) bool {
		session := value.(*Session)
		if session.ConnID == connID {
			sessions = append(sessions, session)
		}
		return true
	})
	var wait sync.WaitGroup
	wait.Add(len(sessions))
	for _, session := range sessions {
		go func(session *Session) {
			defer wait.Done()
			m.closeSession(session)
		}(session)
	}
	wait.Wait()
}
