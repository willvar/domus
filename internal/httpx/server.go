package httpx

import (
	"bufio"
	"compress/gzip"
	"fmt"
	"math"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const MaxControlBody = 1 << 20

// ServerMiddleware preserves streaming and WebSocket capabilities. File bodies
// and SSE are not compressed: ranges and flushed events retain their semantics.
func ServerMiddleware(next http.Handler, origins string, record func(string, string, int, time.Duration)) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		started := time.Now()
		response := &responseWriter{ResponseWriter: w}
		defer func() {
			if recovered := recover(); recovered != nil {
				if recovered == http.ErrAbortHandler {
					panic(recovered)
				}
				if response.status == 0 {
					_ = JSON(response, 500, map[string]any{"error": "internal_error"})
				} else {
					panic(http.ErrAbortHandler)
				}
			}
			if response.compressor != nil {
				_ = response.compressor.Close()
			}
			if record != nil {
				status := response.status
				if status == 0 {
					status = http.StatusOK
				}
				record(r.Method, r.URL.Path, status, time.Since(started))
			}
		}()
		origin := r.Header.Get("Origin")
		if origins != "" {
			w.Header().Add("Vary", "Origin")
		}
		if AllowedOrigin(origin, origins) {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Access-Control-Allow-Credentials", "true")
			w.Header().Set("Access-Control-Expose-Headers", "ETag,Last-Modified,Accept-Ranges,Content-Range,DAV,Allow")
			if r.Method == http.MethodOptions && r.Header.Get("Access-Control-Request-Method") != "" {
				w.Header().Set("Access-Control-Allow-Methods", "GET,HEAD,POST,PUT,PATCH,DELETE,OPTIONS,PROPFIND")
				if r.URL.Path == "/dav" || strings.HasPrefix(r.URL.Path, "/dav/") {
					w.Header().Set("Access-Control-Allow-Methods", "GET,HEAD,OPTIONS,PROPFIND")
				}
				w.Header().Set("Access-Control-Allow-Headers", "Authorization,Content-Type,Depth,Range,If-Match,If-None-Match,If-Modified-Since,If-Unmodified-Since,If-Range")
				response.WriteHeader(http.StatusNoContent)
				return
			}
		}
		if r.ContentLength > MaxControlBody {
			_ = JSON(response, http.StatusRequestEntityTooLarge, map[string]any{"error": "request_too_large"})
			return
		}
		r.Body = http.MaxBytesReader(response, r.Body, MaxControlBody)
		response.compress = acceptsGzip(r.Header.Get("Accept-Encoding")) && r.Method != http.MethodHead &&
			!strings.HasPrefix(r.URL.Path, "/dav") && r.URL.Path != "/ws" && r.Header.Get("Accept") != "text/event-stream"
		next.ServeHTTP(response, r)
	})
}

func AllowedOrigin(origin, configured string) bool {
	if origin == "" {
		return false
	}
	for _, candidate := range strings.Split(configured, ",") {
		if strings.TrimSpace(candidate) == origin {
			return true
		}
	}
	return false
}

func acceptsGzip(header string) bool {
	for _, item := range strings.Split(header, ",") {
		parts := strings.Split(strings.TrimSpace(item), ";")
		if !strings.EqualFold(parts[0], "gzip") {
			continue
		}
		for _, parameter := range parts[1:] {
			if name, value, ok := strings.Cut(strings.TrimSpace(parameter), "="); ok && name == "q" {
				quality, err := strconv.ParseFloat(value, 64)
				if err != nil || math.IsNaN(quality) || quality <= 0 || quality > 1 {
					return false
				}
			}
		}
		return true
	}
	return false
}

type responseWriter struct {
	http.ResponseWriter
	status     int
	compress   bool
	compressor *gzip.Writer
}

func (w *responseWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }
func (w *responseWriter) Written() bool               { return w.status != 0 }
func (w *responseWriter) WriteHeader(code int) {
	if code >= 100 && code < 200 {
		w.ResponseWriter.WriteHeader(code)
		return
	}
	if w.status != 0 {
		return
	}
	w.status = code
	if w.compress && code != http.StatusNoContent && code != http.StatusNotModified && code != http.StatusPartialContent &&
		w.Header().Get("Content-Encoding") == "" && !strings.HasPrefix(w.Header().Get("Content-Type"), "text/event-stream") {
		w.Header().Set("Content-Encoding", "gzip")
		w.Header().Del("Content-Length")
		w.Header().Add("Vary", "Accept-Encoding")
		w.compressor = gzip.NewWriter(w.ResponseWriter)
	}
	w.ResponseWriter.WriteHeader(code)
}
func (w *responseWriter) Write(body []byte) (int, error) {
	if w.status == 0 {
		w.WriteHeader(http.StatusOK)
	}
	if w.compressor != nil {
		return w.compressor.Write(body)
	}
	return w.ResponseWriter.Write(body)
}
func (w *responseWriter) Flush() {
	_ = w.FlushError()
}
func (w *responseWriter) FlushError() error {
	if w.status == 0 {
		w.WriteHeader(http.StatusOK)
	}
	if w.compressor != nil {
		if err := w.compressor.Flush(); err != nil {
			return err
		}
	}
	return http.NewResponseController(w.ResponseWriter).Flush()
}
func (w *responseWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	if w.compressor != nil {
		return nil, nil, fmt.Errorf("cannot upgrade a compressed response")
	}
	connection, buffer, err := http.NewResponseController(w.ResponseWriter).Hijack()
	if err == nil {
		w.status = http.StatusSwitchingProtocols
	}
	return connection, buffer, err
}
