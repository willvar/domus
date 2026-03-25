package stats

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"time"
)

// SocketServer Unix Socket 统计服务端
type SocketServer struct {
	listener  net.Listener
	collector Collector
	path      string
}

// NewSocketServer 创建 Unix Socket 服务端
func NewSocketServer(socketPath string, collector Collector) *SocketServer {
	return &SocketServer{
		collector: collector,
		path:      socketPath,
	}
}

// Start 启动 Socket 服务
func (s *SocketServer) Start() error {
	_ = os.Remove(s.path)

	listener, err := net.Listen("unix", s.path)
	if err != nil {
		return fmt.Errorf("listen unix socket: %w", err)
	}

	s.listener = listener

	go s.acceptLoop()

	return nil
}

func (s *SocketServer) acceptLoop() {
	for {
		conn, err := s.listener.Accept()
		if err != nil {
			return
		}

		go s.handleConn(conn)
	}
}

func (s *SocketServer) handleConn(conn net.Conn) {
	defer func() { _ = conn.Close() }()

	snapshot := s.collector.GetSnapshot()
	data := snapshot.ToMap()

	encoder := json.NewEncoder(conn)
	_ = encoder.Encode(data)
}

// Close 关闭 Socket 服务
func (s *SocketServer) Close() error {
	if s.listener != nil {
		if err := s.listener.Close(); err != nil {
			return err
		}
	}
	_ = os.Remove(s.path)
	return nil
}

// Path 获取 socket 路径
func (s *SocketServer) Path() string {
	return s.path
}

// FetchStats 从 Unix Socket 获取统计信息
func FetchStats(socketPath string, timeout time.Duration) (map[string]any, error) {
	conn, err := net.DialTimeout("unix", socketPath, timeout)
	if err != nil {
		return nil, fmt.Errorf("connect to socket: %w", err)
	}
	defer func() { _ = conn.Close() }()

	if err := conn.SetReadDeadline(time.Now().Add(timeout)); err != nil {
		return nil, fmt.Errorf("set read deadline: %w", err)
	}

	var data map[string]any
	decoder := json.NewDecoder(conn)
	if err := decoder.Decode(&data); err != nil {
		return nil, fmt.Errorf("decode stats: %w", err)
	}

	return data, nil
}
