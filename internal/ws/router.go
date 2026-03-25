package ws

import (
	"encoding/json"
	"errors"

	"zephyr/internal/model"
)

// ActionHandler processes a WebSocket action and returns a result or error.
type ActionHandler func(conn *Conn, reqID string, data json.RawMessage) (any, error)

type actionEntry struct {
	handler  ActionHandler
	perm     int64 // required permission bitmask (0 = auth only)
	rootOnly bool
}

// Router maps action names to handlers with permission checks.
type Router struct {
	actions map[string]*actionEntry
}

// NewRouter creates a new Router.
func NewRouter() *Router {
	return &Router{
		actions: make(map[string]*actionEntry),
	}
}

// Handle registers an action handler with a permission requirement.
// perm=0 means only authentication is required (no specific permission).
func (r *Router) Handle(action string, perm int64, handler ActionHandler) {
	r.actions[action] = &actionEntry{
		handler: handler,
		perm:    perm,
	}
}

// HandleRoot registers an action handler that requires root role.
func (r *Router) HandleRoot(action string, handler ActionHandler) {
	r.actions[action] = &actionEntry{
		handler:  handler,
		rootOnly: true,
	}
}

// Dispatch routes an action to its handler after permission checking.
func (r *Router) Dispatch(conn *Conn, reqID, action string, data json.RawMessage) (any, error) {
	entry, ok := r.actions[action]
	if !ok {
		return nil, errors.New("unknown_action")
	}

	// Check root-only
	if entry.rootOnly && conn.Session.Role != "root" {
		return nil, errors.New("forbidden")
	}

	// Check permissions (root bypasses)
	if entry.perm > 0 && conn.Session.Role != "root" {
		if conn.Session.Permissions&entry.perm != entry.perm {
			return nil, errors.New("forbidden")
		}
	}

	return entry.handler(conn, reqID, data)
}

// HasAction checks if an action is registered.
func (r *Router) HasAction(action string) bool {
	_, ok := r.actions[action]
	return ok
}

// PermRead etc are re-exported for convenience.
var (
	PermRead   = model.PermRead
	PermUpload = model.PermUpload
	PermEdit   = model.PermEdit
	PermDelete = model.PermDelete
)
