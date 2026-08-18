//go:build linux

package cmd

import (
	"os"
	"path/filepath"
	"testing"

	"domus/config"
)

func TestApplyDevRuntimeConfigUsesOnePrivateRuntimeTree(t *testing.T) {
	runtimeRoot := filepath.Join(t.TempDir(), "runtime")
	cfg := &config.Config{}
	applyDevRuntimeConfig(cfg, runtimeRoot, "workspace:test", 1234, 2345)

	if cfg.DOFS.MountRoot != filepath.Join(runtimeRoot, "dofs", "mounts") ||
		cfg.DOFS.StateRoot != filepath.Join(runtimeRoot, "dofs", "state") ||
		cfg.DOFS.ControlSocket != filepath.Join(runtimeRoot, "run", "dofs.sock") {
		t.Fatalf("unexpected DOFS dev layout: %+v", cfg.DOFS)
	}
	if cfg.Workspace.ControlSocket != filepath.Join(runtimeRoot, "run", "workspace.sock") ||
		cfg.Workspace.DOFSControlSocket != cfg.DOFS.ControlSocket ||
		cfg.Workspace.DOFSMountRoot != cfg.DOFS.MountRoot ||
		cfg.Workspace.StateRoot != filepath.Join(runtimeRoot, "workspace") {
		t.Fatalf("unexpected workspace dev layout: %+v", cfg.Workspace)
	}
	if cfg.DOFS.SocketGroup != "" || cfg.Workspace.SocketGroup != "" ||
		cfg.DOFS.UID != 1234 || cfg.DOFS.GID != 2345 ||
		cfg.Workspace.UID != 1234 || cfg.Workspace.GID != 2345 || !cfg.DOFS.AllowOther {
		t.Fatalf("unexpected development identity/socket policy: dofs=%+v workspace=%+v", cfg.DOFS, cfg.Workspace)
	}
	if cfg.Workspace.Image != "workspace:test" || cfg.Workspace.PullPolicy != "never" {
		t.Fatalf("unexpected development image policy: %+v", cfg.Workspace)
	}
	if cfg.Server.RootBootstrapPasswordFile != filepath.Join(runtimeRoot, "root-bootstrap-password") {
		t.Fatalf("unexpected development bootstrap path: %q", cfg.Server.RootBootstrapPasswordFile)
	}
}

func TestEnsureDevBootstrapPasswordCreatesAndReusesPrivateSecret(t *testing.T) {
	path := filepath.Join(t.TempDir(), "root-bootstrap-password")
	if err := ensureDevBootstrapPassword(path); err != nil {
		t.Fatal(err)
	}
	first, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(first) < 32 {
		t.Fatalf("generated bootstrap password is unexpectedly short: %d bytes", len(first))
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0600 {
		t.Fatalf("bootstrap password mode = %o", info.Mode().Perm())
	}
	if err := ensureDevBootstrapPassword(path); err != nil {
		t.Fatal(err)
	}
	second, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(second) != string(first) {
		t.Fatal("existing development bootstrap password was replaced")
	}
}

func TestEnsureDevBootstrapPasswordRejectsSymlink(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "target")
	if err := os.WriteFile(target, []byte("secret"), 0600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "root-bootstrap-password")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if err := ensureDevBootstrapPassword(link); err == nil {
		t.Fatal("expected symlink bootstrap password to be rejected")
	}
}

func TestEnsurePrivateDevRootRejectsSymlink(t *testing.T) {
	base := t.TempDir()
	target := filepath.Join(base, "target")
	if err := os.Mkdir(target, 0700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(base, "runtime")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if err := ensurePrivateDevRoot(link); err == nil {
		t.Fatal("expected symlink runtime root to be rejected")
	}
}

func TestEnsurePrivateDevRootRejectsSharedDirectory(t *testing.T) {
	runtimeRoot := filepath.Join(t.TempDir(), "runtime")
	if err := os.Mkdir(runtimeRoot, 0755); err != nil {
		t.Fatal(err)
	}
	if err := ensurePrivateDevRoot(runtimeRoot); err == nil {
		t.Fatal("expected group/other-accessible runtime root to be rejected")
	}
}
