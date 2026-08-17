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
	"net/url"
	"path"
	"strings"
	"sync"
	"time"

	"github.com/fasthttp/websocket"
)

type ControlClient struct {
	SocketPath string
	Timeout    time.Duration
}

func (c ControlClient) effectiveTimeout() time.Duration {
	if c.Timeout > 0 {
		return c.Timeout
	}
	return 130 * time.Second
}

func (c ControlClient) validate() error {
	if strings.TrimSpace(c.SocketPath) == "" || !path.IsAbs(c.SocketPath) {
		return errors.New("absolute workspace control socket path is required")
	}
	return nil
}

func (c ControlClient) httpClient() (*http.Client, error) {
	if err := c.validate(); err != nil {
		return nil, err
	}
	transport := &http.Transport{
		DisableKeepAlives: true,
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			var dialer net.Dialer
			return dialer.DialContext(ctx, "unix", c.SocketPath)
		},
	}
	return &http.Client{Transport: transport, Timeout: c.effectiveTimeout()}, nil
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
	request, err := http.NewRequestWithContext(ctx, method, "http://workspace"+requestPath, body)
	if err != nil {
		return err
	}
	if input != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := client.Do(request)
	if err != nil {
		return fmt.Errorf("call workspace control socket %s: %w", c.SocketPath, err)
	}
	defer func() { _ = response.Body.Close() }()
	limited := io.LimitReader(response.Body, 32*1024*1024)
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		var failure controlErrorResponse
		if err := json.NewDecoder(limited).Decode(&failure); err != nil {
			return &APIError{StatusCode: response.StatusCode, Code: "http_error", Message: http.StatusText(response.StatusCode)}
		}
		return &APIError{StatusCode: response.StatusCode, Code: failure.Error.Code, Message: failure.Error.Message}
	}
	if output == nil {
		_, _ = io.Copy(io.Discard, limited)
		return nil
	}
	if err := json.NewDecoder(limited).Decode(output); err != nil {
		return fmt.Errorf("decode workspace control response: %w", err)
	}
	return nil
}

func (c ControlClient) Ensure(ctx context.Context, identity Identity) (Status, error) {
	var status Status
	err := c.request(ctx, http.MethodPost, "/v1/workspaces", EnsureRequest{Identity: identity}, &status)
	return status, err
}

func (c ControlClient) Status(ctx context.Context, userID string) (Status, error) {
	var status Status
	err := c.request(ctx, http.MethodGet, "/v1/workspaces/"+url.PathEscape(userID), nil, &status)
	return status, err
}

func (c ControlClient) List(ctx context.Context) ([]Status, error) {
	var response workspaceListResponse
	err := c.request(ctx, http.MethodGet, "/v1/workspaces", nil, &response)
	return response.Workspaces, err
}

func (c ControlClient) Stop(ctx context.Context, userID string) (Status, error) {
	var status Status
	err := c.request(ctx, http.MethodPost, "/v1/workspaces/"+url.PathEscape(userID)+"/stop", nil, &status)
	return status, err
}

func (c ControlClient) Remove(ctx context.Context, userID string) (Status, error) {
	var status Status
	err := c.request(ctx, http.MethodDelete, "/v1/workspaces/"+url.PathEscape(userID), nil, &status)
	return status, err
}

func (c ControlClient) Exec(ctx context.Context, request ExecRequest) (ExecResult, error) {
	var result ExecResult
	err := c.request(ctx, http.MethodPost, "/v1/workspaces/"+url.PathEscape(request.UserID)+"/exec", request, &result)
	if err == nil && result.Truncated {
		err = ErrOutputLimit
	}
	return result, err
}

func (c ControlClient) Health(ctx context.Context, ready bool) (Health, error) {
	endpoint := "/v1/health/live"
	if ready {
		endpoint = "/v1/health/ready"
	}
	client, err := c.httpClient()
	if err != nil {
		return Health{}, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://workspace"+endpoint, nil)
	if err != nil {
		return Health{}, err
	}
	response, err := client.Do(request)
	if err != nil {
		return Health{}, fmt.Errorf("call workspace control socket %s: %w", c.SocketPath, err)
	}
	defer func() { _ = response.Body.Close() }()
	var health Health
	if err := json.NewDecoder(io.LimitReader(response.Body, 1024*1024)).Decode(&health); err != nil {
		return Health{}, fmt.Errorf("decode workspace health: %w", err)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		message := health.LastError
		if message == "" {
			message = "workspace service is not ready"
		}
		return health, &APIError{StatusCode: response.StatusCode, Code: "not_ready", Message: message}
	}
	return health, nil
}

func (c ControlClient) Reconcile(ctx context.Context) (Health, error) {
	var health Health
	err := c.request(ctx, http.MethodPost, "/v1/reconcile", nil, &health)
	return health, err
}

func (c ControlClient) OpenSession(ctx context.Context, request SessionRequest) (Session, error) {
	if err := c.validate(); err != nil {
		return nil, err
	}
	encodedRequest, err := json.Marshal(request)
	if err != nil {
		return nil, fmt.Errorf("encode workspace session request: %w", err)
	}
	if len(encodedRequest) > maxSessionRequestBytes {
		return nil, fmt.Errorf("%w: workspace session request is too large", ErrInvalidRequest)
	}
	requestHeader := make(http.Header)
	requestHeader.Set(sessionRequestHeader, base64.RawURLEncoding.EncodeToString(encodedRequest))
	endpoint := "ws://workspace/v1/workspaces/" + url.PathEscape(request.UserID) + "/session"
	dialer := websocket.Dialer{
		HandshakeTimeout: c.effectiveTimeout(), ReadBufferSize: 4096, WriteBufferSize: 4096,
		Subprotocols: []string{workspaceSubprotocol}, EnableCompression: false,
		NetDialContext: func(dialCtx context.Context, _, _ string) (net.Conn, error) {
			var networkDialer net.Dialer
			return networkDialer.DialContext(dialCtx, "unix", c.SocketPath)
		},
	}
	connection, response, err := dialer.DialContext(ctx, endpoint, requestHeader)
	if err != nil {
		if response != nil && response.Body != nil {
			defer func() { _ = response.Body.Close() }()
			var failure controlErrorResponse
			if decodeErr := json.NewDecoder(io.LimitReader(response.Body, 1024*1024)).Decode(&failure); decodeErr == nil {
				return nil, &APIError{StatusCode: response.StatusCode, Code: failure.Error.Code, Message: failure.Error.Message}
			}
		}
		return nil, fmt.Errorf("open workspace session: %w", err)
	}
	if connection.Subprotocol() != workspaceSubprotocol {
		_ = connection.Close()
		return nil, errors.New("workspace session protocol negotiation failed")
	}
	// Server output chunks are 32 KiB before JSON/base64. Bound peer frames so
	// a malformed or compromised local daemon cannot allocate arbitrary memory
	// in the less-privileged web control plane.
	connection.SetReadLimit(128 * 1024)
	reader, writer := io.Pipe()
	session := &controlSession{
		connection: connection, reader: reader, writer: writer,
		waitDone: make(chan struct{}), exitCode: -1,
	}
	go session.readLoop()
	go func() {
		select {
		case <-ctx.Done():
			_ = session.Close()
		case <-session.waitDone:
		}
	}()
	return session, nil
}

type controlSession struct {
	connection *websocket.Conn
	reader     *io.PipeReader
	writer     *io.PipeWriter

	writeMu  sync.Mutex
	closeMu  sync.Once
	waitMu   sync.Mutex
	waitDone chan struct{}
	exitCode int
	waitErr  error
}

func (s *controlSession) readLoop() {
	defer close(s.waitDone)
	for {
		var message sessionMessage
		if err := s.connection.ReadJSON(&message); err != nil {
			s.waitMu.Lock()
			if !websocket.IsCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway) {
				s.waitErr = err
			}
			s.waitMu.Unlock()
			_ = s.writer.CloseWithError(err)
			return
		}
		switch message.Type {
		case "output":
			if _, err := s.writer.Write(message.Data); err != nil {
				return
			}
		case "exit":
			s.waitMu.Lock()
			s.exitCode = message.ExitCode
			if message.Error != "" {
				s.waitErr = errors.New(message.Error)
			}
			s.waitMu.Unlock()
			_ = s.writer.Close()
			return
		default:
			s.waitMu.Lock()
			s.waitErr = errors.New("workspace session sent an unknown message")
			s.waitMu.Unlock()
			_ = s.writer.CloseWithError(s.waitErr)
			return
		}
	}
}

func (s *controlSession) Read(buffer []byte) (int, error) {
	return s.reader.Read(buffer)
}

func (s *controlSession) Write(buffer []byte) (int, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if err := s.connection.WriteJSON(sessionMessage{Type: "input", Data: append([]byte(nil), buffer...)}); err != nil {
		return 0, err
	}
	return len(buffer), nil
}

func (s *controlSession) Resize(_ context.Context, columns, rows uint) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	return s.connection.WriteJSON(sessionMessage{Type: "resize", Columns: columns, Rows: rows})
}

func (s *controlSession) Wait() (int, error) {
	<-s.waitDone
	s.waitMu.Lock()
	defer s.waitMu.Unlock()
	return s.exitCode, s.waitErr
}

func (s *controlSession) Close() error {
	var closeErr error
	s.closeMu.Do(func() {
		s.writeMu.Lock()
		_ = s.connection.WriteJSON(sessionMessage{Type: "close"})
		closeErr = s.connection.Close()
		s.writeMu.Unlock()
		_ = s.reader.Close()
		_ = s.writer.Close()
	})
	return closeErr
}
