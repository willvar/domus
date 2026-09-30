//go:build linux

package cmd

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"domus/config"
)

// These child processes only expose fixture health endpoints. They never open
// a real database, object store or FUSE mount, or signal an existing service.
func TestDevServiceHelper(t *testing.T) {
	if os.Getenv("DOMUS_DEV_TEST_HELPER") != "1" {
		return
	}
	separator := 0
	for index, arg := range os.Args {
		if arg == "--" {
			separator = index + 1
			break
		}
	}
	args := os.Args[separator:]
	name := map[string]string{"start": "Web", "dofs": "DOFS", "worker": "worker"}[args[0]]
	cfg, err := config.Load(getFlagValue(args, "-c"))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Server.SessionSecret != "current-instance" || cfg.Server.EncryptionSecret != strings.Repeat("11", 32) {
		t.Fatal("child loaded a stale or incomplete configuration")
	}
	events := os.Getenv("DOMUS_DEV_TEST_EVENTS")
	record := func(event string) {
		file, err := os.OpenFile(events, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600)
		if err != nil {
			t.Fatal(err)
		}
		_, err = fmt.Fprintln(file, name+":"+event)
		_ = file.Close()
		if err != nil {
			t.Fatal(err)
		}
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM)
	defer stop()
	record("start")
	if os.Getenv("DOMUS_DEV_TEST_FAIL") == name {
		os.Exit(23)
	}
	if name == "worker" {
		<-ctx.Done()
		record("stop")
		return
	}
	network, address := "tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(cfg.Server.Port))
	if name == "DOFS" {
		network, address = "unix", cfg.DOFS.ControlSocket
	}
	listener, err := net.Listen(network, address)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	var ready, waiting sync.Once
	server := &http.Server{Handler: http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if os.Getenv("DOMUS_DEV_TEST_HOLD") == name {
			if _, err := os.Stat(events + ".release"); errors.Is(err, os.ErrNotExist) {
				waiting.Do(func() { record("waiting") })
				writer.WriteHeader(http.StatusServiceUnavailable)
				return
			}
		}
		ready.Do(func() { record("ready") })
		_, _ = writer.Write([]byte(`{"status":"live"}`))
	})}
	go func() { _ = server.Serve(listener) }()
	if name == "Web" && os.Getenv("DOMUS_DEV_TEST_FAIL") == "Web-during-DOFS" {
		go func() {
			for ctx.Err() == nil {
				data, _ := os.ReadFile(events)
				if strings.Contains(string(data), "DOFS:waiting") {
					os.Exit(23)
				}
				time.Sleep(10 * time.Millisecond)
			}
		}()
	}
	<-ctx.Done()
	_ = server.Close()
	record("stop")
}

func TestRunDevServices(t *testing.T) {
	cases := []struct {
		name, hold, failure, wantError string
		withWorker, cancelWhileWaiting bool
	}{
		{name: "web-only"},
		{name: "full-stack", withWorker: true},
		{name: "Web must serve HTTP before DOFS", withWorker: true, hold: "Web"},
		{name: "DOFS must serve health before worker", withWorker: true, hold: "DOFS"},
		{name: "DOFS failure stops Web", withWorker: true, failure: "DOFS", wantError: "DOFS exited:"},
		{name: "worker failure stops dependencies", withWorker: true, failure: "worker", wantError: "worker exited:"},
		{name: "Web exit aborts DOFS readiness", withWorker: true, hold: "DOFS", failure: "Web-during-DOFS", wantError: "Web exited:"},
		{name: "cancel during DOFS readiness", withWorker: true, hold: "DOFS", cancelWhileWaiting: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Keep Unix socket paths short even for descriptive subtest names.
			root, err := os.MkdirTemp(os.TempDir(), "domus-dev-test-")
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = os.RemoveAll(root) })
			binary, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			t.Setenv("DOMUS_DEV_TEST_HELPER", "1")
			t.Setenv("DOMUS_DEV_TEST_BINARY", binary)
			t.Setenv("DOMUS_DEV_TEST_EVENTS", filepath.Join(root, "events"))
			t.Setenv("DOMUS_DEV_TEST_HOLD", tc.hold)
			t.Setenv("DOMUS_DEV_TEST_FAIL", tc.failure)
			executable := filepath.Join(root, "service")
			if err := os.WriteFile(executable, []byte("#!/bin/sh\nexec \"$DOMUS_DEV_TEST_BINARY\" -test.run='^TestDevServiceHelper$' -- \"$@\"\n"), 0700); err != nil {
				t.Fatal(err)
			}
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			port := listener.Addr().(*net.TCPAddr).Port
			_ = listener.Close()
			cfg := &config.Config{
				Server: config.ServerConfig{Port: port, SessionSecret: "current-instance", EncryptionSecret: strings.Repeat("11", 32)},
				DOFS:   config.DOFSConfig{ControlSocket: filepath.Join(root, "dofs.sock")},
			}
			path := filepath.Join(root, "config.yaml")
			if err := os.WriteFile(path, []byte("server: {}\n"), 0600); err != nil {
				t.Fatal(err)
			}
			if err := config.Save(path, cfg); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			done := make(chan error, 1)
			finished := make(chan struct{})
			go func() {
				defer close(finished)
				done <- runDevServices(ctx, executable, path, cfg, tc.withWorker)
			}()
			t.Cleanup(func() { cancel(); <-finished })
			readEvents := func() string {
				data, _ := os.ReadFile(filepath.Join(root, "events"))
				return string(data)
			}
			waitEvent := func(event string) {
				t.Helper()
				for !strings.Contains(readEvents(), event) {
					select {
					case err := <-done:
						t.Fatalf("supervisor exited before %s: %v", event, err)
					case <-ctx.Done():
						t.Fatalf("timed out waiting for %s", event)
					case <-time.After(10 * time.Millisecond):
					}
				}
			}
			if tc.hold != "" {
				waitEvent(tc.hold + ":waiting")
				next := "worker"
				if tc.hold == "Web" {
					next = "DOFS"
				}
				if strings.Contains(readEvents(), next+":start") {
					t.Fatalf("%s started before %s readiness", next, tc.hold)
				}
				if tc.cancelWhileWaiting {
					cancel()
				} else if tc.failure == "" {
					if err := os.WriteFile(filepath.Join(root, "events.release"), nil, 0600); err != nil {
						t.Fatal(err)
					}
				}
			}
			if tc.failure == "" && !tc.cancelWhileWaiting {
				last := "Web:ready"
				if tc.withWorker {
					last = "worker:start"
				}
				waitEvent(last)
				cancel()
			}
			err = <-done
			if tc.wantError != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantError) {
					t.Fatalf("supervisor error = %v, want %s", err, tc.wantError)
				}
			} else if !errors.Is(err, context.Canceled) {
				t.Fatalf("supervisor cancellation = %v", err)
			}
			events := readEvents()
			if !strings.Contains(events, "Web:stop") && tc.failure != "Web-during-DOFS" {
				t.Fatal("Web child was not cleaned up")
			}
			if tc.withWorker && tc.failure != "DOFS" && !strings.Contains(events, "DOFS:stop") {
				t.Fatal("DOFS child was not cleaned up")
			}
			if !tc.withWorker && strings.Contains(events, "DOFS:start") {
				t.Fatal("web-only dev unexpectedly started DOFS")
			}
			if tc.withWorker && tc.failure == "" && !tc.cancelWhileWaiting {
				order := []string{"Web:ready", "DOFS:start", "DOFS:ready", "worker:start", "worker:stop", "DOFS:stop", "Web:stop"}
				previous := -1
				for _, event := range order {
					index := strings.Index(events, event)
					if index <= previous {
						t.Fatalf("unexpected service order at %s: %s", event, events)
					}
					previous = index
				}
			}
			if (tc.cancelWhileWaiting || tc.failure == "DOFS" || tc.failure == "Web-during-DOFS") && strings.Contains(events, "worker:start") {
				t.Fatal("worker started without healthy dependencies")
			}
		})
	}
}

func TestMakeDevDelegatesWorkerStartup(t *testing.T) {
	command := exec.Command("make", "--just-print", "dev")
	command.Dir = ".."
	output, err := command.Output()
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(string(output), "go run .") != 1 || !strings.Contains(string(output), "--with-worker") {
		t.Fatal("make dev must delegate all backend services to one supervisor")
	}
}
