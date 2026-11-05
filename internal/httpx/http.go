// Package httpx contains small helpers shared by the standard HTTP handlers.
package httpx

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net"
	"net/http"
	"strconv"
	"strings"
)

type Error struct {
	Code    int
	Message string
}

func (e *Error) Error() string                { return e.Message }
func NewError(code int, message string) error { return &Error{Code: code, Message: message} }

// Handler adds error reporting without replacing the standard request/writer.
type Handler func(http.ResponseWriter, *http.Request) error

func (h Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if err := h(w, r); err != nil {
		if r.Context().Err() != nil {
			return
		}
		if response, ok := w.(interface{ Written() bool }); ok && response.Written() {
			return
		}
		code, message := http.StatusInternalServerError, "internal_error"
		var responseError *Error
		if errors.As(err, &responseError) {
			code, message = responseError.Code, responseError.Message
		}
		var sizeError *http.MaxBytesError
		if errors.As(err, &sizeError) {
			code, message = http.StatusRequestEntityTooLarge, "request_too_large"
		}
		_ = JSON(w, code, map[string]any{"error": message})
	}
}

func JSON(w http.ResponseWriter, code int, value any) error {
	// Encode before committing headers so an encoding failure can be reported.
	body, err := json.Marshal(value)
	if err != nil {
		return err
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_, err = w.Write(body)
	return err
}

func Status(w http.ResponseWriter, code int) error { w.WriteHeader(code); return nil }

func DecodeJSON(r *http.Request, target any) error {
	contentType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || (contentType != "application/json" && !strings.HasSuffix(contentType, "+json")) {
		return errors.New("JSON content type required")
	}
	decoder := json.NewDecoder(r.Body)
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		if err != nil {
			return err
		}
		return errors.New("expected one JSON value")
	}
	return nil
}

// ReadBody preserves the bounded control message for subsequent JSON decoding.
func ReadBody(r *http.Request) ([]byte, error) {
	body, err := io.ReadAll(r.Body)
	r.Body = io.NopCloser(bytes.NewReader(body))
	return body, err
}

func Query(r *http.Request, key string, fallback ...string) string {
	value := r.URL.Query().Get(key)
	if value == "" && len(fallback) > 0 {
		return fallback[0]
	}
	return value
}

func QueryInt(r *http.Request, key string, fallback int) int {
	value, err := strconv.Atoi(r.URL.Query().Get(key))
	if err != nil {
		return fallback
	}
	return value
}

func ClientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err == nil {
		return host
	}
	return r.RemoteAddr
}

func Secure(r *http.Request) bool {
	if r.TLS != nil {
		return true
	}
	// Forwarded scheme is accepted only from the local reverse proxy.
	peer := net.ParseIP(ClientIP(r))
	return peer != nil && peer.IsLoopback() && strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https")
}
