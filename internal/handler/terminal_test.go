package handler

import (
	"context"
	"encoding/json"
	"io"
	"testing"

	"domus/internal/terminal"
	workspaceRuntime "domus/internal/workspace"
	"domus/internal/ws"
)

type handlerTerminalService struct {
	workspaceRuntime.Service
	session workspaceRuntime.Session
}

func (s handlerTerminalService) OpenSession(context.Context, workspaceRuntime.SessionRequest) (workspaceRuntime.Session, error) {
	return s.session, nil
}

type handlerTerminalSession struct{}

func (handlerTerminalSession) Read([]byte) (int, error)                 { return 0, io.EOF }
func (handlerTerminalSession) Write(data []byte) (int, error)           { return len(data), nil }
func (handlerTerminalSession) Resize(context.Context, uint, uint) error { return nil }
func (handlerTerminalSession) Wait() (int, error)                       { return 0, nil }
func (handlerTerminalSession) Close() error                             { return nil }

func TestWSSessionCloseRequiresOwningConnection(t *testing.T) {
	manager := terminal.NewManager(handlerTerminalService{session: handlerTerminalSession{}}, 5)
	sessionID, err := manager.Open("user-1", "alice", "owner-connection", "")
	if err != nil {
		t.Fatal(err)
	}
	handler := &Handler{Terminal: manager}
	payload, err := json.Marshal(map[string]string{"session_id": sessionID})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := handler.wsSessionClose(&ws.Conn{ID: "other-connection"}, "", payload); err == nil || err.Error() != "forbidden" {
		t.Fatalf("foreign session close error = %v", err)
	}
	if manager.Get(sessionID) == nil {
		t.Fatal("foreign connection closed the session")
	}
	if _, err := handler.wsSessionClose(&ws.Conn{ID: "owner-connection"}, "", payload); err != nil {
		t.Fatalf("owner session close error = %v", err)
	}
	if manager.Get(sessionID) != nil {
		t.Fatal("owner session remained open")
	}
}
