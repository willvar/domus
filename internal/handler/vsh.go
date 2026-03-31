package handler

import (
	"encoding/json"
	"errors"
	"strings"

	"zephyr/internal/ws"
)

func (h *Handler) wsSessionOpen(conn *ws.Conn, _ string, data json.RawMessage) (any, error) {
	var req struct {
		Cwd string `json:"cwd"`
	}
	_ = json.Unmarshal(data, &req)

	encKey, _ := h.getFileEncryptionKey(conn.Session)

	id, err := h.Vsh.Open(
		conn.UserID,
		conn.Username,
		conn.ID,
		req.Cwd,
		encKey,
	)
	if err != nil {
		return nil, err
	}

	s := h.Vsh.Get(id)
	cwd := "/"
	if s != nil {
		cwd = s.Cwd
	}

	history := h.Vsh.LoadHistory(s)

	return map[string]any{
		"session_id": id,
		"cwd":        cwd,
		"user":       conn.Username,
		"history":    history,
	}, nil
}

// wsSessionInput handles input from the frontend.
// In vsh mode: data is a command line. In SSH mode: data is raw bytes.
// Returns immediately — output is pushed via session.output events.
func (h *Handler) wsSessionInput(conn *ws.Conn, _ string, data json.RawMessage) (any, error) {
	var req struct {
		SessionID string `json:"session_id"`
		Data      string `json:"data"`
	}
	if err := json.Unmarshal(data, &req); err != nil || req.SessionID == "" {
		return nil, errors.New("invalid_request")
	}

	s := h.Vsh.Get(req.SessionID)
	if s == nil {
		return nil, errors.New("session_not_found")
	}
	if s.ConnID != conn.ID {
		return nil, errors.New("forbidden")
	}

	// Persist vsh commands to history (not SSH raw input)
	if s.Mode == "vsh" {
		cmd := strings.TrimSpace(req.Data)
		if cmd != "" && cmd != "exit" {
			go h.Vsh.AppendHistory(s, cmd)
		}
	}

	// Fire-and-forget: Input() pushes output via callbacks
	go h.Vsh.Input(req.SessionID, req.Data)

	return map[string]any{}, nil
}

func (h *Handler) wsSessionResize(conn *ws.Conn, _ string, data json.RawMessage) (any, error) {
	var req struct {
		SessionID string `json:"session_id"`
		Cols      int    `json:"cols"`
		Rows      int    `json:"rows"`
	}
	if err := json.Unmarshal(data, &req); err != nil || req.SessionID == "" {
		return nil, errors.New("invalid_request")
	}

	s := h.Vsh.Get(req.SessionID)
	if s == nil {
		return nil, errors.New("session_not_found")
	}
	if s.ConnID != conn.ID {
		return nil, errors.New("forbidden")
	}

	h.Vsh.Resize(req.SessionID, req.Cols, req.Rows)
	return map[string]any{}, nil
}

func (h *Handler) wsSessionClose(conn *ws.Conn, _ string, data json.RawMessage) (any, error) {
	var req struct {
		SessionID string `json:"session_id"`
	}
	if err := json.Unmarshal(data, &req); err != nil || req.SessionID == "" {
		return nil, errors.New("invalid_request")
	}

	h.Vsh.Close(req.SessionID)
	return map[string]any{}, nil
}

func (h *Handler) wsSessionComplete(conn *ws.Conn, _ string, data json.RawMessage) (any, error) {
	var req struct {
		SessionID string `json:"session_id"`
		Line      string `json:"line"`
	}
	if err := json.Unmarshal(data, &req); err != nil || req.SessionID == "" {
		return nil, errors.New("invalid_request")
	}

	s := h.Vsh.Get(req.SessionID)
	if s == nil {
		return nil, errors.New("session_not_found")
	}
	if s.ConnID != conn.ID {
		return nil, errors.New("forbidden")
	}

	matches, prefix := h.Vsh.Complete(req.SessionID, req.Line)

	return map[string]any{
		"matches": matches,
		"prefix":  prefix,
	}, nil
}
