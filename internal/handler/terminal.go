package handler

import (
	"encoding/json"
	"errors"

	"domus/internal/ws"
)

func (h *Handler) wsSessionOpen(conn *ws.Conn, _ string, data json.RawMessage) (any, error) {
	var request struct {
		Cwd string `json:"cwd"`
	}
	if len(data) != 0 {
		_ = json.Unmarshal(data, &request)
	}

	id, err := h.Terminal.Open(conn.UserID, conn.Username, conn.ID, request.Cwd)
	if err != nil {
		return nil, err
	}
	session := h.Terminal.Get(id)
	cwd := "/"
	if session != nil {
		cwd = session.Cwd
	}
	return map[string]any{
		"session_id": id,
		"cwd":        cwd,
		"user":       conn.Username,
	}, nil
}

// wsSessionInput forwards raw TTY bytes synchronously so WebSocket message
// order is preserved from the browser through Workspace Manager.
func (h *Handler) wsSessionInput(conn *ws.Conn, _ string, data json.RawMessage) (any, error) {
	var request struct {
		SessionID string `json:"session_id"`
		Data      string `json:"data"`
	}
	if err := json.Unmarshal(data, &request); err != nil || request.SessionID == "" {
		return nil, errors.New("invalid_request")
	}
	session := h.Terminal.Get(request.SessionID)
	if session == nil {
		return nil, errors.New("session_not_found")
	}
	if session.ConnID != conn.ID {
		return nil, errors.New("forbidden")
	}
	if err := h.Terminal.Input(request.SessionID, request.Data); err != nil {
		return nil, err
	}
	return map[string]any{}, nil
}

func (h *Handler) wsSessionResize(conn *ws.Conn, _ string, data json.RawMessage) (any, error) {
	var request struct {
		SessionID string `json:"session_id"`
		Cols      int    `json:"cols"`
		Rows      int    `json:"rows"`
	}
	if err := json.Unmarshal(data, &request); err != nil || request.SessionID == "" {
		return nil, errors.New("invalid_request")
	}
	session := h.Terminal.Get(request.SessionID)
	if session == nil {
		return nil, errors.New("session_not_found")
	}
	if session.ConnID != conn.ID {
		return nil, errors.New("forbidden")
	}
	if err := h.Terminal.Resize(request.SessionID, request.Cols, request.Rows); err != nil {
		return nil, err
	}
	return map[string]any{}, nil
}

func (h *Handler) wsSessionClose(conn *ws.Conn, _ string, data json.RawMessage) (any, error) {
	var request struct {
		SessionID string `json:"session_id"`
	}
	if err := json.Unmarshal(data, &request); err != nil || request.SessionID == "" {
		return nil, errors.New("invalid_request")
	}
	session := h.Terminal.Get(request.SessionID)
	if session == nil {
		return nil, errors.New("session_not_found")
	}
	if session.ConnID != conn.ID {
		return nil, errors.New("forbidden")
	}
	h.Terminal.Close(request.SessionID)
	return map[string]any{}, nil
}
