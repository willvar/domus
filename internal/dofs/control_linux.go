//go:build linux

package dofs

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path"
	"strings"
	"sync"
	"time"

	dofscore "github.com/willvar/dofs"

	"golang.org/x/sys/unix"
)

type controlErrorBody struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

var controlSocketUmaskMu sync.Mutex

type controlErrorResponse struct {
	Error controlErrorBody `json:"error"`
}

type mountsResponse struct {
	Mounts []MountStatus `json:"mounts"`
}

// ControlAPIError is returned by ControlClient when the local manager rejects
// a request. The Unix socket permission boundary is the authentication layer;
// the API intentionally carries no reusable credential.
type ControlAPIError struct {
	StatusCode int
	Code       string
	Message    string
}

func (e *ControlAPIError) Error() string {
	return fmt.Sprintf("DOFS control API %s: %s", e.Code, e.Message)
}

func (m *Manager) ControlHandler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/health/live", func(writer http.ResponseWriter, _ *http.Request) {
		writeControlJSON(writer, http.StatusOK, map[string]string{"status": "live"})
	})
	mux.HandleFunc("GET /v1/health/ready", func(writer http.ResponseWriter, _ *http.Request) {
		health := m.Health()
		status := http.StatusOK
		if health.Status != "ready" {
			status = http.StatusServiceUnavailable
		}
		writeControlJSON(writer, status, health)
	})
	mux.HandleFunc("GET /v1/mounts", func(writer http.ResponseWriter, _ *http.Request) {
		writeControlJSON(writer, http.StatusOK, mountsResponse{Mounts: m.List()})
	})
	mux.HandleFunc("POST /v1/mounts", func(writer http.ResponseWriter, request *http.Request) {
		var selector MountUserSelector
		if err := decodeControlJSON(writer, request, &selector); err != nil {
			writeControlError(writer, err)
			return
		}
		status, err := m.Ensure(request.Context(), selector)
		if err != nil {
			writeControlError(writer, err)
			return
		}
		httpStatus := http.StatusOK
		if status.State == "pending" {
			httpStatus = http.StatusAccepted
		}
		writeControlJSON(writer, httpStatus, status)
	})
	mux.HandleFunc("POST /v1/reconcile", func(writer http.ResponseWriter, request *http.Request) {
		if err := m.ReconcileOnce(request.Context()); err != nil {
			writeControlError(writer, err)
			return
		}
		writeControlJSON(writer, http.StatusOK, m.Health())
	})
	mux.HandleFunc("GET /v1/mounts/{userID}", func(writer http.ResponseWriter, request *http.Request) {
		status, err := m.Status(request.PathValue("userID"))
		if err != nil {
			writeControlError(writer, err)
			return
		}
		writeControlJSON(writer, http.StatusOK, status)
	})
	mux.HandleFunc("DELETE /v1/mounts/{userID}", func(writer http.ResponseWriter, request *http.Request) {
		status, err := m.Unmount(request.Context(), request.PathValue("userID"))
		if err != nil {
			writeControlError(writer, err)
			return
		}
		writeControlJSON(writer, http.StatusOK, status)
	})
	return http.MaxBytesHandler(mux, 4096)
}

func decodeControlJSON(_ http.ResponseWriter, request *http.Request, destination any) error {
	decoder := json.NewDecoder(request.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return fmt.Errorf("invalid control request: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("invalid control request: multiple JSON values")
		}
		return fmt.Errorf("invalid control request: %w", err)
	}
	return nil
}

func writeControlJSON(writer http.ResponseWriter, status int, value any) {
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(value)
}

func writeControlError(writer http.ResponseWriter, err error) {
	status := http.StatusInternalServerError
	code := "internal_error"
	switch {
	case errors.Is(err, ErrInvalidUserSelector), strings.Contains(err.Error(), "invalid DOFS user ID"), strings.HasPrefix(err.Error(), "invalid control request"):
		status, code = http.StatusBadRequest, "invalid_request"
	case errors.Is(err, ErrMountNotManaged), errors.Is(err, ErrUserNotFound):
		status, code = http.StatusNotFound, "not_managed"
	case errors.Is(err, ErrForeignMount), errors.Is(err, ErrMountUnhealthy),
		errors.Is(err, dofscore.ErrWriterBusy), errors.Is(err, dofscore.ErrMountBusy):
		status, code = http.StatusConflict, "mount_conflict"
	case errors.Is(err, ErrManagerStopping), errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		status, code = http.StatusServiceUnavailable, "manager_unavailable"
	}
	writeControlJSON(writer, status, controlErrorResponse{Error: controlErrorBody{Code: code, Message: err.Error()}})
}

// ServeControl exposes the manager only through its permission-protected Unix
// socket. It starts reconciliation immediately and stops accepting requests
// when ctx is canceled; mount shutdown remains an explicit caller step so the
// caller can apply its configured deadline.
func (m *Manager) ServeControl(ctx context.Context) error {
	listener, err := m.listenControlSocket()
	if err != nil {
		return err
	}
	defer func() {
		_ = listener.Close()
		_ = removeSocketIfPresent(m.options.ControlSocket)
	}()

	timeout := m.options.MountTimeout + 10*time.Second
	server := &http.Server{
		Handler:           m.ControlHandler(),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      timeout,
		IdleTimeout:       30 * time.Second,
		MaxHeaderBytes:    8 * 1024,
	}
	reconcileContext, stopReconciler := context.WithCancel(ctx)
	defer stopReconciler()
	go m.RunReconciler(reconcileContext)
	if m.options.OnReady != nil {
		if err := m.options.OnReady(); err != nil {
			return fmt.Errorf("announce DOFS control readiness: %w", err)
		}
	}

	shutdownDone := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			shutdownContext, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			_ = server.Shutdown(shutdownContext)
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

func (m *Manager) listenControlSocket() (net.Listener, error) {
	if info, err := os.Lstat(m.options.ControlSocket); err == nil {
		if info.Mode()&os.ModeSymlink != 0 || info.Mode()&os.ModeSocket == 0 {
			return nil, errors.New("refusing to replace non-socket DOFS control path")
		}
		connection, dialErr := net.DialTimeout("unix", m.options.ControlSocket, 500*time.Millisecond)
		if dialErr == nil {
			_ = connection.Close()
			return nil, errors.New("DOFS control socket is already accepting connections")
		}
		if !errors.Is(dialErr, unix.ECONNREFUSED) && !errors.Is(dialErr, os.ErrNotExist) {
			return nil, fmt.Errorf("cannot prove existing DOFS control socket is stale: %w", dialErr)
		}
		if err := os.Remove(m.options.ControlSocket); err != nil {
			return nil, fmt.Errorf("remove stale DOFS control socket: %w", err)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("inspect DOFS control socket: %w", err)
	}

	controlSocketUmaskMu.Lock()
	previousUmask := unix.Umask(0077)
	listener, err := net.Listen("unix", m.options.ControlSocket)
	unix.Umask(previousUmask)
	controlSocketUmaskMu.Unlock()
	if err != nil {
		return nil, fmt.Errorf("listen on DOFS control socket: %w", err)
	}
	cleanup := func(cause error) (net.Listener, error) {
		_ = listener.Close()
		_ = removeSocketIfPresent(m.options.ControlSocket)
		return nil, cause
	}
	if err := os.Chmod(m.options.ControlSocket, m.options.SocketMode.Perm()); err != nil {
		return cleanup(fmt.Errorf("set DOFS control socket mode: %w", err))
	}
	if m.options.SocketGID >= 0 {
		if err := os.Chown(m.options.ControlSocket, -1, m.options.SocketGID); err != nil {
			return cleanup(fmt.Errorf("set DOFS control socket group: %w", err))
		}
	}
	m.logf("DOFS control socket listening at %s (mode %04o)", m.options.ControlSocket, m.options.SocketMode.Perm())
	return listener, nil
}

func removeSocketIfPresent(socketPath string) error {
	info, err := os.Lstat(socketPath)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 || info.Mode()&os.ModeSocket == 0 {
		return errors.New("refusing to remove non-socket control path")
	}
	return os.Remove(socketPath)
}

type ControlClient struct {
	SocketPath string
	Timeout    time.Duration
}

func (c ControlClient) httpClient() (*http.Client, error) {
	if strings.TrimSpace(c.SocketPath) == "" || !path.IsAbs(c.SocketPath) {
		return nil, errors.New("absolute DOFS control socket path is required")
	}
	timeout := c.Timeout
	if timeout <= 0 {
		timeout = 70 * time.Second
	}
	transport := &http.Transport{
		DisableKeepAlives: true,
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			var dialer net.Dialer
			return dialer.DialContext(ctx, "unix", c.SocketPath)
		},
	}
	return &http.Client{Transport: transport, Timeout: timeout}, nil
}

func (c ControlClient) request(ctx context.Context, method, requestPath string, input, output any) error {
	client, err := c.httpClient()
	if err != nil {
		return err
	}
	var body io.Reader
	if input != nil {
		encoded, err := json.Marshal(input)
		if err != nil {
			return err
		}
		body = bytes.NewReader(encoded)
	}
	request, err := http.NewRequestWithContext(ctx, method, "http://dofs"+requestPath, body)
	if err != nil {
		return err
	}
	if input != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := client.Do(request)
	if err != nil {
		return fmt.Errorf("call DOFS control socket %s: %w", c.SocketPath, err)
	}
	defer func() { _ = response.Body.Close() }()
	limited := io.LimitReader(response.Body, 1024*1024)
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		var failure controlErrorResponse
		if err := json.NewDecoder(limited).Decode(&failure); err != nil {
			return fmt.Errorf("DOFS control API returned HTTP %d", response.StatusCode)
		}
		return &ControlAPIError{
			StatusCode: response.StatusCode, Code: failure.Error.Code, Message: failure.Error.Message,
		}
	}
	if output == nil {
		_, _ = io.Copy(io.Discard, limited)
		return nil
	}
	if err := json.NewDecoder(limited).Decode(output); err != nil {
		return fmt.Errorf("decode DOFS control response: %w", err)
	}
	return nil
}

func (c ControlClient) Ensure(ctx context.Context, selector MountUserSelector) (MountStatus, error) {
	var status MountStatus
	err := c.request(ctx, http.MethodPost, "/v1/mounts", selector, &status)
	return status, err
}

func (c ControlClient) Status(ctx context.Context, userID string) (MountStatus, error) {
	var status MountStatus
	err := c.request(ctx, http.MethodGet, "/v1/mounts/"+url.PathEscape(userID), nil, &status)
	return status, err
}

func (c ControlClient) List(ctx context.Context) ([]MountStatus, error) {
	var response mountsResponse
	err := c.request(ctx, http.MethodGet, "/v1/mounts", nil, &response)
	return response.Mounts, err
}

func (c ControlClient) Unmount(ctx context.Context, userID string) (MountStatus, error) {
	var status MountStatus
	err := c.request(ctx, http.MethodDelete, "/v1/mounts/"+url.PathEscape(userID), nil, &status)
	return status, err
}

func (c ControlClient) Health(ctx context.Context, ready bool) (ManagerHealth, error) {
	endpoint := "/v1/health/live"
	if ready {
		endpoint = "/v1/health/ready"
	}
	var health ManagerHealth
	if !ready {
		var live map[string]string
		err := c.request(ctx, http.MethodGet, endpoint, nil, &live)
		if err == nil {
			health.Status = live["status"]
		}
		return health, err
	}
	client, err := c.httpClient()
	if err != nil {
		return ManagerHealth{}, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://dofs"+endpoint, nil)
	if err != nil {
		return ManagerHealth{}, err
	}
	response, err := client.Do(request)
	if err != nil {
		return ManagerHealth{}, fmt.Errorf("call DOFS control socket %s: %w", c.SocketPath, err)
	}
	defer func() { _ = response.Body.Close() }()
	if err := json.NewDecoder(io.LimitReader(response.Body, 1024*1024)).Decode(&health); err != nil {
		return ManagerHealth{}, fmt.Errorf("decode DOFS readiness response: %w", err)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		message := health.LastError
		if message == "" {
			message = "one or more desired mounts are not ready"
		}
		return health, &ControlAPIError{StatusCode: response.StatusCode, Code: "not_ready", Message: message}
	}
	return health, nil
}

func (c ControlClient) Reconcile(ctx context.Context) (ManagerHealth, error) {
	var health ManagerHealth
	err := c.request(ctx, http.MethodPost, "/v1/reconcile", nil, &health)
	return health, err
}
