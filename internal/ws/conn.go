package ws

import (
	"encoding/json"
	"sync"
	"sync/atomic"
	"time"

	"github.com/fasthttp/websocket"
	"github.com/google/uuid"

	"zephyr/internal/model"
)

const (
	writeWait      = 10 * time.Second
	pongWait       = 40 * time.Second
	pingPeriod     = 30 * time.Second
	maxMessageSize = 256 * 1024
	sendBufSize    = 256
)

// Conn wraps a raw WebSocket connection with session info and subscription state.
type Conn struct {
	ID        string
	UserID    string
	Username  string
	Session   *model.Session
	SessionID string

	hub  *Hub
	raw  *websocket.Conn
	send chan []byte

	subs   map[string]struct{} // subscribed directory paths (resolved OSS paths)
	subsMu sync.Mutex

	closed int32
}

func newConn(hub *Hub, raw *websocket.Conn, session *model.Session, sessionID string) *Conn {
	return &Conn{
		ID:        uuid.New().String(),
		UserID:    session.UserID,
		Username:  session.Username,
		Session:   session,
		SessionID: sessionID,
		hub:       hub,
		raw:       raw,
		send:      make(chan []byte, sendBufSize),
		subs:      make(map[string]struct{}),
	}
}

// WriteJSON marshals v and queues it for sending. Non-blocking; drops if buffer full.
func (c *Conn) WriteJSON(v any) {
	if atomic.LoadInt32(&c.closed) != 0 {
		return
	}
	data, err := json.Marshal(v)
	if err != nil {
		return
	}
	select {
	case c.send <- data:
	default:
		// Buffer full, drop message
	}
}

// Subscribe adds a directory path to this connection's subscriptions.
func (c *Conn) Subscribe(path string) {
	c.subsMu.Lock()
	c.subs[path] = struct{}{}
	c.subsMu.Unlock()
	c.hub.addDirSub(path, c)
}

// Unsubscribe removes a directory path from this connection's subscriptions.
func (c *Conn) Unsubscribe(path string) {
	c.subsMu.Lock()
	delete(c.subs, path)
	c.subsMu.Unlock()
	c.hub.removeDirSub(path, c)
}

// Close closes the connection and cleans up.
func (c *Conn) Close() {
	if !atomic.CompareAndSwapInt32(&c.closed, 0, 1) {
		return
	}
	close(c.send)
	_ = c.raw.Close()
	c.hub.unregister(c)
}

// readPump reads messages from the WebSocket and dispatches them.
func (c *Conn) readPump() {
	defer c.Close()

	c.raw.SetReadLimit(maxMessageSize)
	_ = c.raw.SetReadDeadline(time.Now().Add(pongWait))
	c.raw.SetPongHandler(func(string) error {
		_ = c.raw.SetReadDeadline(time.Now().Add(pongWait))
		return nil
	})

	for {
		_, message, err := c.raw.ReadMessage()
		if err != nil {
			return
		}
		c.hub.dispatch(c, message)
	}
}

// writePump writes messages from the send channel to the WebSocket.
func (c *Conn) writePump() {
	ticker := time.NewTicker(pingPeriod)
	defer func() {
		ticker.Stop()
		c.Close()
	}()

	for {
		select {
		case msg, ok := <-c.send:
			if !ok {
				// Channel closed
				_ = c.raw.WriteMessage(websocket.CloseMessage, nil)
				return
			}
			_ = c.raw.SetWriteDeadline(time.Now().Add(writeWait))
			if err := c.raw.WriteMessage(websocket.TextMessage, msg); err != nil {
				return
			}
		case <-ticker.C:
			_ = c.raw.SetWriteDeadline(time.Now().Add(writeWait))
			if err := c.raw.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		}
	}
}
