package terminal

import (
	"bytes"
	"context"
	"io"
	"sync"
	"testing"
	"time"

	workspaceRuntime "domus/internal/workspace"
)

type testWorkspaceService struct {
	workspaceRuntime.Service
	session workspaceRuntime.Session
	request workspaceRuntime.SessionRequest
}

func (s *testWorkspaceService) OpenSession(_ context.Context, request workspaceRuntime.SessionRequest) (workspaceRuntime.Session, error) {
	s.request = request
	return s.session, nil
}

type testSession struct {
	reader *io.PipeReader
	writer *io.PipeWriter
	mu     sync.Mutex
	input  bytes.Buffer
	closed bool
}

func newTestSession() *testSession {
	reader, writer := io.Pipe()
	return &testSession{reader: reader, writer: writer}
}

func (s *testSession) Read(buffer []byte) (int, error) { return s.reader.Read(buffer) }
func (s *testSession) Write(data []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.input.Write(data)
}
func (s *testSession) Resize(context.Context, uint, uint) error { return nil }
func (s *testSession) Wait() (int, error)                       { return 0, nil }
func (s *testSession) Close() error {
	s.mu.Lock()
	s.closed = true
	s.mu.Unlock()
	_ = s.writer.Close()
	return s.reader.Close()
}

func TestManagerUsesRawWorkspaceTTY(t *testing.T) {
	runtimeSession := newTestSession()
	service := &testWorkspaceService{session: runtimeSession}
	manager := NewManager(service, 5)
	output := make(chan []byte, 1)
	exited := make(chan string, 1)
	manager.OnPushBytes = func(_, _ string, data []byte) { output <- append([]byte(nil), data...) }
	manager.OnPushExit = func(_, _, reason string) { exited <- reason }

	sessionID, err := manager.Open("user-1", "alice", "connection-1", "/home/alice/project/")
	if err != nil {
		t.Fatal(err)
	}
	if service.request.WorkingDir != "/workspace/home/alice/project" {
		t.Fatalf("working directory = %q", service.request.WorkingDir)
	}
	if err := manager.Input(sessionID, "printf ok\n"); err != nil {
		t.Fatal(err)
	}
	runtimeSession.mu.Lock()
	input := runtimeSession.input.String()
	runtimeSession.mu.Unlock()
	if input != "printf ok\n" {
		t.Fatalf("raw input = %q", input)
	}
	wantOutput := []byte{0xff, 'o', 'k', '\r', '\n'}
	if _, err := runtimeSession.writer.Write(wantOutput); err != nil {
		t.Fatal(err)
	}
	_ = runtimeSession.writer.Close()
	select {
	case data := <-output:
		if !bytes.Equal(data, wantOutput) {
			t.Fatalf("raw output = %x, want %x", data, wantOutput)
		}
	case <-time.After(time.Second):
		t.Fatal("workspace output was not pushed")
	}
	select {
	case reason := <-exited:
		if reason != "exit" {
			t.Fatalf("exit reason = %q", reason)
		}
	case <-time.After(time.Second):
		t.Fatal("workspace exit was not pushed")
	}
}

func TestManagerRequiresWorkspace(t *testing.T) {
	manager := NewManager(nil, 5)
	if _, err := manager.Open("user-1", "alice", "connection-1", ""); err == nil || err.Error() != "workspace_unavailable" {
		t.Fatalf("Open() error = %v", err)
	}
}

func TestManagerKeepsClientWorkingDirectoryInsideWorkspace(t *testing.T) {
	runtimeSession := newTestSession()
	service := &testWorkspaceService{session: runtimeSession}
	manager := NewManager(service, 5)
	id, err := manager.Open("user-1", "alice", "connection-1", "../../etc")
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close(id)
	if service.request.WorkingDir != "/workspace/etc" {
		t.Fatalf("working directory escaped workspace: %q", service.request.WorkingDir)
	}
}

func TestManagerSessionLimitIsAtomic(t *testing.T) {
	service := &queuedWorkspaceService{}
	for index := 0; index < 20; index++ {
		service.sessions = append(service.sessions, newTestSession())
	}
	manager := NewManager(service, 5)
	const attempts = 20
	start := make(chan struct{})
	results := make(chan string, attempts)
	var workers sync.WaitGroup
	for index := 0; index < attempts; index++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			<-start
			if id, err := manager.Open("user-1", "alice", "connection-1", ""); err == nil {
				results <- id
			}
		}()
	}
	close(start)
	workers.Wait()
	close(results)
	var opened []string
	for id := range results {
		opened = append(opened, id)
	}
	if len(opened) != 5 {
		t.Fatalf("concurrent sessions = %d, want 5", len(opened))
	}
	for _, id := range opened {
		manager.Close(id)
	}
	if _, err := manager.Open("user-1", "alice", "connection-1", ""); err != nil {
		t.Fatalf("session capacity was not released: %v", err)
	}
}

type queuedWorkspaceService struct {
	workspaceRuntime.Service
	mu       sync.Mutex
	sessions []workspaceRuntime.Session
}

func (s *queuedWorkspaceService) OpenSession(context.Context, workspaceRuntime.SessionRequest) (workspaceRuntime.Session, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	session := s.sessions[0]
	s.sessions = s.sessions[1:]
	return session, nil
}

type blockingSession struct {
	started chan<- struct{}
	release <-chan struct{}
}

func (s *blockingSession) Read([]byte) (int, error)                 { return 0, io.EOF }
func (s *blockingSession) Write(data []byte) (int, error)           { return len(data), nil }
func (s *blockingSession) Resize(context.Context, uint, uint) error { return nil }
func (s *blockingSession) Wait() (int, error)                       { return 0, nil }
func (s *blockingSession) Close() error {
	s.started <- struct{}{}
	<-s.release
	return nil
}

func TestManagerClosesConnectionSessionsConcurrently(t *testing.T) {
	started := make(chan struct{}, 2)
	release := make(chan struct{})
	service := &queuedWorkspaceService{sessions: []workspaceRuntime.Session{
		&blockingSession{started: started, release: release},
		&blockingSession{started: started, release: release},
	}}
	manager := NewManager(service, 5)
	first, err := manager.Open("user-1", "alice", "connection-1", "")
	if err != nil {
		t.Fatal(err)
	}
	second, err := manager.Open("user-1", "alice", "connection-1", "")
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() {
		manager.CloseByConn("connection-1")
		close(done)
	}()
	for index := 0; index < 2; index++ {
		select {
		case <-started:
		case <-time.After(time.Second):
			t.Fatal("session closes were serialized")
		}
	}
	close(release)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("CloseByConn did not finish")
	}
	if manager.Get(first) != nil || manager.Get(second) != nil {
		t.Fatal("closed sessions remained registered")
	}
}
