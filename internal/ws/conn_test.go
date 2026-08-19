package ws

import (
	"encoding/json"
	"testing"
	"time"
)

func TestReliableQueueAppliesBackpressureWithoutDropping(t *testing.T) {
	hub := NewHub()
	connection := &Conn{
		ID: "connection-1", hub: hub, send: make(chan []byte, 1), done: make(chan struct{}),
		subs: make(map[string]struct{}),
	}
	hub.register(connection)
	if !connection.enqueue([]byte("first"), true) {
		t.Fatal("first reliable message was rejected")
	}
	queued := make(chan bool, 1)
	go func() { queued <- connection.enqueue([]byte("second"), true) }()
	select {
	case <-queued:
		t.Fatal("second reliable message did not wait for queue capacity")
	case <-time.After(20 * time.Millisecond):
	}
	if got := string(<-connection.send); got != "first" {
		t.Fatalf("first queued message = %q", got)
	}
	select {
	case ok := <-queued:
		if !ok {
			t.Fatal("second reliable message was rejected")
		}
	case <-time.After(time.Second):
		t.Fatal("second reliable message remained blocked")
	}
	if got := string(<-connection.send); got != "second" {
		t.Fatalf("second queued message = %q", got)
	}
	connection.Close()
	if connection.enqueue([]byte("after-close"), true) {
		t.Fatal("message was accepted after close")
	}
}

func TestDirectorySubscriptionsAreIsolatedByUser(t *testing.T) {
	hub := NewHub()
	userA := &Conn{
		ID: "connection-a", UserID: "user-a", hub: hub, send: make(chan []byte, 1),
		done: make(chan struct{}), subs: make(map[string]struct{}),
	}
	userB := &Conn{
		ID: "connection-b", UserID: "user-b", hub: hub, send: make(chan []byte, 1),
		done: make(chan struct{}), subs: make(map[string]struct{}),
	}
	userA.Subscribe("/")
	userB.Subscribe("/")

	hub.PushDirChanged("user-a", "/", "/", "refresh")
	select {
	case payload := <-userA.send:
		var event map[string]any
		if err := json.Unmarshal(payload, &event); err != nil || event["event"] != "dir.changed" {
			t.Fatalf("user A event = %s, %v", payload, err)
		}
	case <-time.After(time.Second):
		t.Fatal("user A did not receive its root notification")
	}
	select {
	case payload := <-userB.send:
		t.Fatalf("user B received user A's root notification: %s", payload)
	default:
	}
}

func TestBestEffortQueueDoesNotBlockWhenFull(t *testing.T) {
	connection := &Conn{send: make(chan []byte, 1), done: make(chan struct{})}
	if !connection.enqueue([]byte("first"), false) {
		t.Fatal("first best-effort message was rejected")
	}
	started := time.Now()
	if connection.enqueue([]byte("second"), false) {
		t.Fatal("full best-effort queue accepted another message")
	}
	if time.Since(started) > 100*time.Millisecond {
		t.Fatal("best-effort enqueue blocked")
	}
}

func TestHubCloseAllClosesConnectionsConcurrently(t *testing.T) {
	hub := NewHub()
	started := make(chan struct{}, 2)
	release := make(chan struct{})
	hub.OnConnClose = func(string) {
		started <- struct{}{}
		<-release
	}
	for _, id := range []string{"connection-1", "connection-2"} {
		hub.register(&Conn{
			ID: id, UserID: "user-1", hub: hub, send: make(chan []byte, 1),
			done: make(chan struct{}), subs: make(map[string]struct{}),
		})
	}
	done := make(chan struct{})
	go func() {
		hub.CloseAll()
		close(done)
	}()
	for index := 0; index < 2; index++ {
		select {
		case <-started:
		case <-time.After(time.Second):
			t.Fatal("connection closes were serialized")
		}
	}
	close(release)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("CloseAll did not finish")
	}
}
