package ws

import (
	"encoding/json"
	"strings"
	"sync"

	"github.com/fasthttp/websocket"

	"domus/internal/model"
	"domus/shared/logger"
)

// Hub manages all active WebSocket connections.
type Hub struct {
	// conns maps connID -> *Conn
	conns sync.Map
	// userConns maps userID -> *sync.Map[connID -> *Conn]
	userConns sync.Map
	// dirSubs maps a user-scoped namespace path -> *sync.Map[connID -> *Conn]
	dirSubs sync.Map

	router *Router

	// OnConnClose is called when a connection is unregistered, before cleanup.
	// Can be used to clean up resources tied to a connection (e.g. terminal sessions).
	OnConnClose func(connID string)
}

// NewHub creates a new Hub.
func NewHub() *Hub {
	return &Hub{
		router: NewRouter(),
	}
}

// Router returns the hub's message router for action registration.
func (h *Hub) Router() *Router {
	return h.router
}

// HandleConnection is called when a new WebSocket connection is established.
// It registers the connection and starts the read/write pumps.
func (h *Hub) HandleConnection(raw *websocket.Conn, session *model.Session, sessionID string) {
	conn := newConn(h, raw, session, sessionID)
	h.register(conn)

	go conn.writePump()
	conn.readPump() // blocks until connection closes
}

func (h *Hub) register(conn *Conn) {
	h.conns.Store(conn.ID, conn)

	// Add to user's connection set
	actual, _ := h.userConns.LoadOrStore(conn.UserID, &sync.Map{})
	actual.(*sync.Map).Store(conn.ID, conn)
}

func (h *Hub) unregister(conn *Conn) {
	if h.OnConnClose != nil {
		h.OnConnClose(conn.ID)
	}

	h.conns.Delete(conn.ID)

	// Remove from user's connection set
	if v, ok := h.userConns.Load(conn.UserID); ok {
		m := v.(*sync.Map)
		m.Delete(conn.ID)
	}

	// Remove all directory subscriptions
	conn.subsMu.Lock()
	subs := make([]string, 0, len(conn.subs))
	for path := range conn.subs {
		subs = append(subs, path)
	}
	conn.subs = make(map[string]struct{})
	conn.subsMu.Unlock()

	for _, path := range subs {
		h.removeDirSub(path, conn)
	}
}

func (h *Hub) addDirSub(path string, conn *Conn) {
	actual, _ := h.dirSubs.LoadOrStore(directorySubscriptionKey(conn.UserID, path), &sync.Map{})
	actual.(*sync.Map).Store(conn.ID, conn)
}

func (h *Hub) removeDirSub(path string, conn *Conn) {
	if v, ok := h.dirSubs.Load(directorySubscriptionKey(conn.UserID, path)); ok {
		v.(*sync.Map).Delete(conn.ID)
	}
}

// dispatch routes an inbound message to the router.
func (h *Hub) dispatch(conn *Conn, raw []byte) {
	var msg struct {
		ID     string          `json:"id"`
		Action string          `json:"action"`
		Data   json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(raw, &msg); err != nil {
		conn.WriteJSON(map[string]any{
			"id":    "",
			"ok":    false,
			"error": "invalid_message",
		})
		return
	}

	result, err := h.router.Dispatch(conn, msg.ID, msg.Action, msg.Data)
	if err != nil {
		conn.WriteJSON(map[string]any{
			"id":     msg.ID,
			"action": msg.Action,
			"ok":     false,
			"error":  err.Error(),
		})
		return
	}

	conn.WriteJSON(map[string]any{
		"id":     msg.ID,
		"action": msg.Action,
		"ok":     true,
		"data":   result,
	})
}

// --- Send helpers ---

// SendToUser sends a message to all connections of a user.
func (h *Hub) SendToUser(userID string, msg any) {
	data, err := json.Marshal(msg)
	if err != nil {
		return
	}
	if v, ok := h.userConns.Load(userID); ok {
		v.(*sync.Map).Range(func(_, val any) bool {
			val.(*Conn).enqueue(data, false)
			return true
		})
	}
}

// SendToConn sends a message to a specific connection.
func (h *Hub) SendToConn(connID string, msg any) {
	data, err := json.Marshal(msg)
	if err != nil {
		return
	}
	if v, ok := h.conns.Load(connID); ok {
		v.(*Conn).enqueue(data, true)
	}
}

func directorySubscriptionKey(userID, namespacePath string) string {
	return strings.TrimSpace(userID) + "\x00" + namespacePath
}

// NotifyDirectory sends a push event only to this user's connections that are
// subscribed to the namespace-relative directory path.
func (h *Hub) NotifyDirectory(userID, resolvedPath string, msg any) {
	data, err := json.Marshal(msg)
	if err != nil {
		return
	}
	if v, ok := h.dirSubs.Load(directorySubscriptionKey(userID, resolvedPath)); ok {
		v.(*sync.Map).Range(func(_, val any) bool {
			val.(*Conn).enqueue(data, false)
			return true
		})
	}
}

// DisconnectUser force-closes all connections for a user.
func (h *Hub) DisconnectUser(userID string) {
	var connections []*Conn
	if v, ok := h.userConns.Load(userID); ok {
		v.(*sync.Map).Range(func(_, val any) bool {
			connections = append(connections, val.(*Conn))
			return true
		})
	}
	closeConnections(connections)
}

// CloseAll gracefully closes all connections.
func (h *Hub) CloseAll() {
	logger.Info("[ws] closing all connections")
	var connections []*Conn
	h.conns.Range(func(_, val any) bool {
		connections = append(connections, val.(*Conn))
		return true
	})
	closeConnections(connections)
}

func closeConnections(connections []*Conn) {
	var wait sync.WaitGroup
	wait.Add(len(connections))
	for _, connection := range connections {
		go func() {
			defer wait.Done()
			connection.Close()
		}()
	}
	wait.Wait()
}
