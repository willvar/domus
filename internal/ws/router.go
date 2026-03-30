package ws

import (
	"encoding/json"
	"errors"
)

// ActionHandler processes a WebSocket action and returns a result or error.
type ActionHandler func(conn *Conn, reqID string, data json.RawMessage) (any, error)

type actionEntry struct {
	handler  ActionHandler
	rootOnly bool
}

// Router maps action names to handlers with role checks.
type Router struct {
	actions map[string]*actionEntry
}

// NewRouter creates a new Router.
func NewRouter() *Router {
	return &Router{
		actions: make(map[string]*actionEntry),
	}
}

// Handle registers an action handler (authentication required, no role restriction).
func (r *Router) Handle(action string, handler ActionHandler) {
	r.actions[action] = &actionEntry{
		handler: handler,
	}
}

// HandleRoot registers an action handler that requires root role.
func (r *Router) HandleRoot(action string, handler ActionHandler) {
	r.actions[action] = &actionEntry{
		handler:  handler,
		rootOnly: true,
	}
}

// Dispatch routes an action to its handler after role checking.
func (r *Router) Dispatch(conn *Conn, reqID, action string, data json.RawMessage) (any, error) {
	entry, ok := r.actions[action]
	if !ok {
		return nil, errors.New("unknown_action")
	}

	// Check root-only
	if entry.rootOnly && conn.Session.Role != "root" {
		return nil, errors.New("forbidden")
	}

	return entry.handler(conn, reqID, data)
}

// HasAction checks if an action is registered.
func (r *Router) HasAction(action string) bool {
	_, ok := r.actions[action]
	return ok
}
