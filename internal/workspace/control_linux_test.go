//go:build linux

package workspace

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"
)

func TestUnixControlClientEndToEnd(t *testing.T) {
	manager, _, runtime, identity := newTestManager(t, nil)
	serveContext, stopServe := context.WithCancel(context.Background())
	serveDone := make(chan error, 1)
	go func() { serveDone <- manager.ServeControl(serveContext) }()
	t.Cleanup(func() {
		stopServe()
		select {
		case <-serveDone:
		case <-time.After(2 * time.Second):
		}
	})
	deadline := time.Now().Add(2 * time.Second)
	for {
		if _, err := os.Lstat(manager.options.ControlSocket); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("workspace control socket was not created")
		}
		time.Sleep(5 * time.Millisecond)
	}
	client := ControlClient{SocketPath: manager.options.ControlSocket, Timeout: 5 * time.Second}
	health, err := client.Health(t.Context(), false)
	if err != nil || health.Status != "live" || health.ProtocolVersion != ControlProtocolVersion {
		t.Fatalf("Health() = %+v, %v", health, err)
	}
	status, err := client.Ensure(t.Context(), identity)
	if err != nil || status.State != "running" || status.ContainerID == "" {
		t.Fatalf("Ensure() = %+v, %v", status, err)
	}
	result, err := client.Exec(t.Context(), ExecRequest{Identity: identity, Command: []string{"printf", "ok"}})
	if err != nil || result.ExitCode != 0 || string(result.Stdout) != "ok\n" {
		t.Fatalf("Exec() = %+v, %v", result, err)
	}
	runtime.mu.Lock()
	runtime.execFunc = func(context.Context, RuntimeExecRequest) (ExecResult, error) {
		return ExecResult{ExitCode: 0, Stdout: []byte("partial"), Truncated: true}, ErrOutputLimit
	}
	runtime.mu.Unlock()
	result, err = client.Exec(t.Context(), ExecRequest{Identity: identity, Command: []string{"printf", "large"}})
	if !errors.Is(err, ErrOutputLimit) || !result.Truncated || string(result.Stdout) != "partial" {
		t.Fatalf("truncated Exec() = %+v, %v", result, err)
	}
	runtime.mu.Lock()
	runtime.execFunc = nil
	runtime.mu.Unlock()
	listed, err := client.List(t.Context())
	if err != nil || len(listed) != 1 || listed[0].UserID != identity.UserID {
		t.Fatalf("List() = %+v, %v", listed, err)
	}

	sessionContext, cancelSession := context.WithCancel(context.Background())
	session, err := client.OpenSession(sessionContext, SessionRequest{
		Identity: identity, Command: []string{"/bin/bash", "-l"}, WorkingDir: "/workspace/home/alice/project",
		Environment: map[string]string{"DOMUS_TEST": "session"}, Columns: 93, Rows: 31,
	})
	if err != nil {
		cancelSession()
		t.Fatalf("OpenSession() error = %v", err)
	}
	buffer := make([]byte, 1)
	if _, err := session.Read(buffer); err == nil {
		t.Fatalf("session.Read() error = %v, want EOF/closed", err)
	}
	exitCode, waitErr := session.Wait()
	if waitErr != nil || exitCode != 0 {
		t.Fatalf("session.Wait() = %d, %v", exitCode, waitErr)
	}
	_ = session.Close()
	cancelSession()
	runtime.mu.Lock()
	sessionRequest := runtime.sessionRequest
	runtime.mu.Unlock()
	if sessionRequest.WorkingDir != "/workspace/home/alice/project" || sessionRequest.Columns != 93 || sessionRequest.Rows != 31 ||
		len(sessionRequest.Command) != 2 || sessionRequest.Command[1] != "-l" || !containsString(sessionRequest.Environment, "DOMUS_TEST=session") {
		t.Fatalf("runtime session request = %+v", sessionRequest)
	}

	removed, err := client.Remove(t.Context(), identity.UserID)
	if err != nil || removed.State != "stopped" {
		t.Fatalf("Remove() = %+v, %v", removed, err)
	}
}
