package httpx

import (
	"bufio"
	"compress/gzip"
	"context"
	"crypto/tls"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func TestMiddlewareCompressionAndErrors(t *testing.T) {
	endpoint := Handler(func(w http.ResponseWriter, r *http.Request) error {
		switch r.URL.Path {
		case "/panic":
			panic("not exposed")
		case "/body":
			_, err := ReadBody(r)
			return err
		case "/committed":
			_, _ = io.WriteString(w, "already sent")
			return errors.New("late write error")
		case "/sse":
			w.Header().Set("Content-Type", "text/event-stream")
			_, err := io.WriteString(w, "data: hello\n\n")
			return err
		default:
			return JSON(w, 200, map[string]string{"ok": "yes"})
		}
	})
	for _, test := range []struct {
		path, encoding string
		compressed     bool
		status         int
	}{
		{"/json", "gzip", true, 200}, {"/json", "br, gzip;q=0.7", true, 200},
		{"/json", "gzip;q=0.000", false, 200}, {"/json", "gzip;q=no", false, 200},
		{"/dav/file", "gzip", false, 200}, {"/sse", "gzip", false, 200},
		{"/panic", "gzip", true, 500}, {"/committed", "", false, 200},
	} {
		t.Run(test.path+test.encoding, func(t *testing.T) {
			r := httptest.NewRequest("GET", test.path, nil)
			r.Header.Set("Accept-Encoding", test.encoding)
			w := httptest.NewRecorder()
			ServerMiddleware(endpoint, "", nil).ServeHTTP(w, r)
			if w.Code != test.status {
				t.Fatalf("status = %d", w.Code)
			}
			if compressed := w.Header().Get("Content-Encoding") == "gzip"; compressed != test.compressed {
				t.Fatalf("gzip = %v", compressed)
			}
			var reader io.Reader = w.Body
			if test.compressed {
				decoded, err := gzip.NewReader(w.Body)
				if err != nil {
					t.Fatal(err)
				}
				defer decoded.Close()
				reader = decoded
			}
			body, err := io.ReadAll(reader)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(body), "not exposed") || strings.Contains(string(body), "late write error") {
				t.Fatal("private error exposed")
			}
			if test.path == "/committed" && string(body) != "already sent" {
				t.Fatalf("appended error: %s", body)
			}
		})
	}
	for _, knownLength := range []bool{true, false} {
		r := httptest.NewRequest("POST", "/body", strings.NewReader(strings.Repeat("x", MaxControlBody+1)))
		if !knownLength {
			r.ContentLength = -1
		}
		w := httptest.NewRecorder()
		ServerMiddleware(endpoint, "", nil).ServeHTTP(w, r)
		if w.Code != http.StatusRequestEntityTooLarge {
			t.Fatalf("known length %v status = %d", knownLength, w.Code)
		}
	}
}

func TestCORSAndJSONBoundary(t *testing.T) {
	endpoint := Handler(func(w http.ResponseWriter, r *http.Request) error { return JSON(w, 200, map[string]bool{"ok": true}) })
	for _, allowed := range []bool{true, false} {
		r := httptest.NewRequest("OPTIONS", "/dav/", nil)
		origin := "https://consumer.example"
		if !allowed {
			origin = "https://untrusted.example"
		}
		r.Header.Set("Origin", origin)
		r.Header.Set("Access-Control-Request-Method", "PROPFIND")
		w := httptest.NewRecorder()
		ServerMiddleware(endpoint, "https://consumer.example", nil).ServeHTTP(w, r)
		if allowed && (w.Code != 204 || w.Header().Get("Access-Control-Allow-Origin") != origin || !strings.Contains(w.Header().Get("Access-Control-Allow-Headers"), "If-Range")) {
			t.Fatalf("allowed preflight: %d %v", w.Code, w.Header())
		}
		if !allowed && w.Header().Get("Access-Control-Allow-Origin") != "" {
			t.Fatal("untrusted CORS origin allowed")
		}
	}
	for _, test := range []struct {
		contentType, body string
		valid             bool
	}{
		{"application/json", `{"ok":true}`, true}, {"application/problem+json", `{}`, true},
		{"text/plain", `{}`, false}, {"application/json", `{} {}`, false},
		{"application/json", `{} trailing`, false},
	} {
		r := httptest.NewRequest("POST", "/", strings.NewReader(test.body))
		r.Header.Set("Content-Type", test.contentType)
		var result map[string]any
		if err := DecodeJSON(r, &result); (err == nil) != test.valid {
			t.Fatalf("%s %s: %v", test.contentType, test.body, err)
		}
	}
}

func TestReadBodyPreservesJSONForEndpoint(t *testing.T) {
	const payload = `{"path":"/","file_name":"note.txt","file_size":4}`
	r := httptest.NewRequest(http.MethodPost, "/file/upload", strings.NewReader(payload))
	r.Header.Set("Content-Type", "application/json")
	// Control-plane middleware and upload dispatch inspect the same bytes.
	for range 2 {
		body, err := ReadBody(r)
		if err != nil || string(body) != payload {
			t.Fatalf("body inspection changed the control message: %q, %v", body, err)
		}
	}
	var body struct {
		Path     string `json:"path"`
		FileName string `json:"file_name"`
		FileSize int64  `json:"file_size"`
	}
	if err := DecodeJSON(r, &body); err != nil {
		t.Fatalf("endpoint could not decode the inspected body: %v", err)
	}
	if body.Path != "/" || body.FileName != "note.txt" || body.FileSize != 4 {
		t.Fatalf("endpoint received different metadata: %+v", body)
	}
	// Decoding retains standard streaming semantics; it is not a body cache.
	if err := DecodeJSON(r, &body); !errors.Is(err, io.EOF) {
		t.Fatalf("second stream decode = %v, want EOF", err)
	}
}

func TestSecureUsesOnlyLocalProxyScheme(t *testing.T) {
	for _, test := range []struct {
		peer, scheme string
		tls, secure  bool
	}{
		{"127.0.0.1:80", "https", false, true}, {"[::1]:80", "HTTPS", false, true},
		{"192.0.2.1:80", "https", false, false}, {"127.0.0.1:80", "https,http", false, false},
		{"192.0.2.1:80", "", true, true},
	} {
		r := httptest.NewRequest("GET", "/", nil)
		r.RemoteAddr = test.peer
		r.Header.Set("X-Forwarded-Proto", test.scheme)
		if test.tls {
			r.TLS = &tls.ConnectionState{}
		}
		if Secure(r) != test.secure {
			t.Fatalf("%+v", test)
		}
	}
}

func TestStreamingAndWebSocketSurviveMiddleware(t *testing.T) {
	cancelled := make(chan struct{})
	mux := http.NewServeMux()
	mux.HandleFunc("/events", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: ready\n\n")
		if err := http.NewResponseController(w).Flush(); err != nil {
			t.Error(err)
			return
		}
		<-r.Context().Done()
		close(cancelled)
	})
	mux.HandleFunc("/ws", func(w http.ResponseWriter, r *http.Request) {
		upgrader := websocket.Upgrader{}
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Error(err)
			return
		}
		defer conn.Close()
		kind, message, err := conn.ReadMessage()
		if err != nil {
			t.Error(err)
			return
		}
		_ = conn.WriteMessage(kind, message)
	})
	server := httptest.NewServer(ServerMiddleware(mux, "", nil))
	defer server.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	r, _ := http.NewRequestWithContext(ctx, "GET", server.URL+"/events", nil)
	r.Header.Set("Accept-Encoding", "gzip")
	response, err := server.Client().Do(r)
	if err != nil {
		t.Fatal(err)
	}
	reader := bufio.NewReader(response.Body)
	if event, err := reader.ReadString('\n'); err != nil || event != "data: ready\n" {
		t.Fatalf("event %q: %v", event, err)
	}
	if response.Header.Get("Content-Encoding") != "" {
		t.Fatal("SSE compressed")
	}
	_ = response.Body.Close()
	select {
	case <-cancelled:
	case <-ctx.Done():
		t.Fatal("disconnect did not cancel stream")
	}
	conn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http")+"/ws", http.Header{"Accept-Encoding": []string{"gzip"}})
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	if err := conn.WriteMessage(websocket.TextMessage, []byte("unchanged wire protocol")); err != nil {
		t.Fatal(err)
	}
	_, payload, err := conn.ReadMessage()
	if err != nil || string(payload) != "unchanged wire protocol" {
		t.Fatalf("echo %q: %v", payload, err)
	}
}
