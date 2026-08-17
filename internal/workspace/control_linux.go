//go:build linux

package workspace

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/fasthttp/websocket"
	"golang.org/x/sys/unix"
)

const (
	workspaceSubprotocol   = "domus.workspace.v1"
	sessionRequestHeader   = "X-Domus-Workspace-Session"
	maxSessionRequestBytes = 128 * 1024
	maxControlHeaderBytes  = 256 * 1024
)

type controlErrorBody struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type controlErrorResponse struct {
	Error controlErrorBody `json:"error"`
}

type workspaceListResponse struct {
	Workspaces []Status `json:"workspaces"`
}

type sessionMessage struct {
	Type     string `json:"type"`
	Data     []byte `json:"data,omitempty"`
	Columns  uint   `json:"columns,omitempty"`
	Rows     uint   `json:"rows,omitempty"`
	ExitCode int    `json:"exit_code,omitempty"`
	Error    string `json:"error,omitempty"`
}

func (m *Manager) ControlHandler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/health/live", func(writer http.ResponseWriter, request *http.Request) {
		health, _ := m.Health(request.Context(), false)
		writeControlJSON(writer, http.StatusOK, health)
	})
	mux.HandleFunc("GET /v1/health/ready", func(writer http.ResponseWriter, request *http.Request) {
		health, err := m.Health(request.Context(), true)
		statusCode := http.StatusOK
		if err != nil {
			statusCode = http.StatusServiceUnavailable
		}
		writeControlJSON(writer, statusCode, health)
	})
	mux.HandleFunc("GET /v1/workspaces", func(writer http.ResponseWriter, request *http.Request) {
		statuses, err := m.List(request.Context())
		if err != nil {
			writeControlError(writer, err)
			return
		}
		writeControlJSON(writer, http.StatusOK, workspaceListResponse{Workspaces: statuses})
	})
	mux.HandleFunc("POST /v1/workspaces", func(writer http.ResponseWriter, request *http.Request) {
		var input EnsureRequest
		if err := decodeControlJSON(request, &input); err != nil {
			writeControlError(writer, err)
			return
		}
		status, err := m.Ensure(request.Context(), input.Identity)
		if err != nil {
			writeControlError(writer, err)
			return
		}
		statusCode := http.StatusOK
		if status.State == "pending" {
			statusCode = http.StatusAccepted
		}
		writeControlJSON(writer, statusCode, status)
	})
	mux.HandleFunc("POST /v1/reconcile", func(writer http.ResponseWriter, request *http.Request) {
		if err := m.ReconcileOnce(request.Context()); err != nil {
			writeControlError(writer, err)
			return
		}
		health, _ := m.Health(request.Context(), false)
		writeControlJSON(writer, http.StatusOK, health)
	})
	mux.HandleFunc("GET /v1/workspaces/{userID}", func(writer http.ResponseWriter, request *http.Request) {
		status, err := m.Status(request.Context(), request.PathValue("userID"))
		if err != nil {
			writeControlError(writer, err)
			return
		}
		writeControlJSON(writer, http.StatusOK, status)
	})
	mux.HandleFunc("POST /v1/workspaces/{userID}/stop", func(writer http.ResponseWriter, request *http.Request) {
		status, err := m.Stop(request.Context(), request.PathValue("userID"))
		if err != nil {
			writeControlError(writer, err)
			return
		}
		writeControlJSON(writer, http.StatusOK, status)
	})
	mux.HandleFunc("DELETE /v1/workspaces/{userID}", func(writer http.ResponseWriter, request *http.Request) {
		status, err := m.Remove(request.Context(), request.PathValue("userID"))
		if err != nil {
			writeControlError(writer, err)
			return
		}
		writeControlJSON(writer, http.StatusOK, status)
	})
	mux.HandleFunc("POST /v1/workspaces/{userID}/exec", func(writer http.ResponseWriter, request *http.Request) {
		var input ExecRequest
		if err := decodeControlJSON(request, &input); err != nil {
			writeControlError(writer, err)
			return
		}
		if input.UserID != request.PathValue("userID") {
			writeControlError(writer, fmt.Errorf("%w: URL user ID does not match request", ErrInvalidRequest))
			return
		}
		result, err := m.Exec(request.Context(), input)
		if err != nil && !errors.Is(err, ErrOutputLimit) {
			writeControlError(writer, err)
			return
		}
		writeControlJSON(writer, http.StatusOK, result)
	})
	mux.HandleFunc("GET /v1/workspaces/{userID}/session", m.handleSession)
	return http.MaxBytesHandler(mux, 2*1024*1024)
}

func decodeControlJSON(request *http.Request, destination any) error {
	decoder := json.NewDecoder(request.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return fmt.Errorf("%w: decode JSON: %v", ErrInvalidRequest, err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return fmt.Errorf("%w: request must contain one JSON value", ErrInvalidRequest)
	}
	return nil
}

func writeControlJSON(writer http.ResponseWriter, statusCode int, value any) {
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(statusCode)
	_ = json.NewEncoder(writer).Encode(value)
}

func writeControlError(writer http.ResponseWriter, err error) {
	statusCode := http.StatusInternalServerError
	code := "internal_error"
	switch {
	case errors.Is(err, ErrInvalidRequest):
		statusCode, code = http.StatusBadRequest, "invalid_request"
	case errors.Is(err, ErrNotManaged):
		statusCode, code = http.StatusNotFound, "not_managed"
	case errors.Is(err, ErrConflict):
		statusCode, code = http.StatusConflict, "workspace_conflict"
	case errors.Is(err, ErrCapacity), errors.Is(err, ErrSessionLimit):
		statusCode, code = http.StatusTooManyRequests, "capacity_exhausted"
	case errors.Is(err, ErrUnavailable), errors.Is(err, ErrManagerStopping), errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		statusCode, code = http.StatusServiceUnavailable, "workspace_unavailable"
	case errors.Is(err, ErrOutputLimit):
		statusCode, code = http.StatusRequestEntityTooLarge, "output_limit"
	}
	writeControlJSON(writer, statusCode, controlErrorResponse{Error: controlErrorBody{Code: code, Message: err.Error()}})
}

func (m *Manager) handleSession(writer http.ResponseWriter, request *http.Request) {
	if websocket.Subprotocols(request) == nil || !containsString(websocket.Subprotocols(request), workspaceSubprotocol) {
		writeControlError(writer, fmt.Errorf("%w: required workspace subprotocol is missing", ErrInvalidRequest))
		return
	}
	sessionRequest, err := decodeSessionRequest(request.Header.Get(sessionRequestHeader))
	if err != nil {
		writeControlError(writer, err)
		return
	}
	if sessionRequest.UserID != request.PathValue("userID") {
		writeControlError(writer, fmt.Errorf("%w: URL user ID does not match session request", ErrInvalidRequest))
		return
	}
	session, err := m.OpenSession(request.Context(), sessionRequest)
	if err != nil {
		writeControlError(writer, err)
		return
	}
	upgrader := websocket.Upgrader{
		HandshakeTimeout: 10 * time.Second, ReadBufferSize: 4096, WriteBufferSize: 4096,
		Subprotocols: []string{workspaceSubprotocol}, EnableCompression: false,
		CheckOrigin: func(candidate *http.Request) bool { return candidate.Header.Get("Origin") == "" },
	}
	connection, err := upgrader.Upgrade(writer, request, nil)
	if err != nil {
		_ = session.Close()
		return
	}
	defer func() {
		_ = connection.Close()
		_ = session.Close()
	}()
	connection.SetReadLimit(128 * 1024)

	outputDone := make(chan struct{})
	go func() {
		defer close(outputDone)
		buffer := make([]byte, 32*1024)
		for {
			count, readErr := session.Read(buffer)
			if count > 0 {
				if err := connection.WriteJSON(sessionMessage{Type: "output", Data: append([]byte(nil), buffer[:count]...)}); err != nil {
					return
				}
			}
			if readErr != nil {
				break
			}
		}
		exitCode, waitErr := session.Wait()
		message := sessionMessage{Type: "exit", ExitCode: exitCode}
		if waitErr != nil && !errors.Is(waitErr, context.Canceled) {
			message.Error = waitErr.Error()
		}
		_ = connection.WriteJSON(message)
	}()

	for {
		var message sessionMessage
		if err := connection.ReadJSON(&message); err != nil {
			break
		}
		switch message.Type {
		case "input":
			if len(message.Data) > 64*1024 {
				_ = connection.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseMessageTooBig, "input is too large"), time.Now().Add(time.Second))
				return
			}
			if _, err := session.Write(message.Data); err != nil {
				return
			}
		case "resize":
			resizeCtx, cancel := context.WithTimeout(request.Context(), 5*time.Second)
			err := session.Resize(resizeCtx, message.Columns, message.Rows)
			cancel()
			if err != nil {
				return
			}
		case "close":
			return
		default:
			return
		}
	}
	_ = session.Close()
	select {
	case <-outputDone:
	case <-time.After(2 * time.Second):
	}
}

func decodeSessionRequest(encoded string) (SessionRequest, error) {
	if encoded == "" || len(encoded) > base64.RawURLEncoding.EncodedLen(maxSessionRequestBytes) {
		return SessionRequest{}, fmt.Errorf("%w: workspace session request is missing or too large", ErrInvalidRequest)
	}
	decoded, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil || len(decoded) > maxSessionRequestBytes {
		return SessionRequest{}, fmt.Errorf("%w: decode workspace session request", ErrInvalidRequest)
	}
	decoder := json.NewDecoder(bytes.NewReader(decoded))
	decoder.DisallowUnknownFields()
	var sessionRequest SessionRequest
	if err := decoder.Decode(&sessionRequest); err != nil {
		return SessionRequest{}, fmt.Errorf("%w: decode workspace session request: %v", ErrInvalidRequest, err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return SessionRequest{}, fmt.Errorf("%w: workspace session request must contain one JSON value", ErrInvalidRequest)
	}
	return sessionRequest, nil
}

func containsString(values []string, wanted string) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}

var controlSocketUmaskMu sync.Mutex

func (m *Manager) ServeControl(ctx context.Context) error {
	listener, socketLock, err := m.listenControlSocket()
	if err != nil {
		return err
	}
	defer func() {
		_ = listener.Close()
		_ = removeControlSocket(m.options.ControlSocket)
		_ = releaseManagerLock(socketLock)
	}()
	server := &http.Server{
		Handler: m.ControlHandler(), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second,
		WriteTimeout: max(m.options.OperationTimeout, m.options.ExecTimeout) + 10*time.Second,
		IdleTimeout:  30 * time.Second, MaxHeaderBytes: maxControlHeaderBytes,
	}
	reconcileCtx, stopReconciler := context.WithCancel(ctx)
	defer stopReconciler()
	go m.RunReconciler(reconcileCtx)
	if m.options.OnReady != nil {
		if err := m.options.OnReady(); err != nil {
			return fmt.Errorf("announce workspace readiness: %w", err)
		}
	}
	shutdownDone := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			_ = server.Shutdown(shutdownCtx)
			cancel()
		case <-shutdownDone:
		}
	}()
	err = server.Serve(listener)
	close(shutdownDone)
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

func (m *Manager) listenControlSocket() (net.Listener, *os.File, error) {
	parent := filepathDir(m.options.ControlSocket)
	if err := os.MkdirAll(parent, 0750); err != nil {
		return nil, nil, fmt.Errorf("create workspace socket directory: %w", err)
	}
	info, err := os.Lstat(parent)
	if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return nil, nil, errors.New("workspace socket parent must be a real directory")
	}
	resolvedParent, err := filepath.EvalSymlinks(parent)
	if err != nil || filepath.Clean(resolvedParent) != filepath.Clean(parent) {
		return nil, nil, errors.New("workspace socket parent must not contain symbolic-link components")
	}
	socketLock, err := acquireManagerLock(m.options.ControlSocket + ".lock")
	if err != nil {
		return nil, nil, err
	}
	fail := func(cause error) (net.Listener, *os.File, error) {
		_ = releaseManagerLock(socketLock)
		return nil, nil, cause
	}
	if info, err := os.Lstat(m.options.ControlSocket); err == nil {
		if info.Mode()&os.ModeSymlink != 0 || info.Mode()&os.ModeSocket == 0 {
			return fail(errors.New("refusing to replace non-socket workspace control path"))
		}
		connection, dialErr := net.DialTimeout("unix", m.options.ControlSocket, 500*time.Millisecond)
		if dialErr == nil {
			_ = connection.Close()
			return fail(errors.New("workspace control socket is already accepting connections"))
		}
		if !errors.Is(dialErr, unix.ECONNREFUSED) && !errors.Is(dialErr, os.ErrNotExist) {
			return fail(fmt.Errorf("cannot prove workspace control socket is stale: %w", dialErr))
		}
		if err := os.Remove(m.options.ControlSocket); err != nil {
			return fail(err)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return fail(err)
	}
	controlSocketUmaskMu.Lock()
	oldUmask := unix.Umask(0077)
	listener, err := net.Listen("unix", m.options.ControlSocket)
	unix.Umask(oldUmask)
	controlSocketUmaskMu.Unlock()
	if err != nil {
		return fail(err)
	}
	cleanup := func(cause error) (net.Listener, *os.File, error) {
		_ = listener.Close()
		_ = removeControlSocket(m.options.ControlSocket)
		return fail(cause)
	}
	if err := os.Chmod(m.options.ControlSocket, m.options.SocketMode.Perm()); err != nil {
		return cleanup(err)
	}
	if m.options.SocketGID >= 0 {
		if err := os.Chown(m.options.ControlSocket, -1, m.options.SocketGID); err != nil {
			return cleanup(err)
		}
	}
	m.logf("Workspace control socket listening at %s (mode %04o)", m.options.ControlSocket, m.options.SocketMode.Perm())
	return listener, socketLock, nil
}

func filepathDir(value string) string {
	index := strings.LastIndexByte(value, '/')
	if index <= 0 {
		return "/"
	}
	return value[:index]
}

func removeControlSocket(socketPath string) error {
	info, err := os.Lstat(socketPath)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 || info.Mode()&os.ModeSocket == 0 {
		return errors.New("refusing to remove non-socket workspace control path")
	}
	return os.Remove(socketPath)
}
