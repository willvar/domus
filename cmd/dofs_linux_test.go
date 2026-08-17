//go:build linux

package cmd

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestValidateDOFSMountpoint(t *testing.T) {
	t.Run("accepts empty directory", func(t *testing.T) {
		directory := t.TempDir()
		got, err := validateDOFSMountpoint(directory)
		if err != nil {
			t.Fatalf("validate mountpoint: %v", err)
		}
		want, _ := filepath.Abs(directory)
		if got != want {
			t.Fatalf("got %q, want %q", got, want)
		}
	})

	t.Run("rejects nonempty directory", func(t *testing.T) {
		directory := t.TempDir()
		if err := os.WriteFile(filepath.Join(directory, "keep"), []byte("data"), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := validateDOFSMountpoint(directory); err == nil {
			t.Fatal("expected nonempty directory rejection")
		}
	})

	t.Run("rejects file", func(t *testing.T) {
		file := filepath.Join(t.TempDir(), "mountpoint")
		if err := os.WriteFile(file, nil, 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := validateDOFSMountpoint(file); err == nil {
			t.Fatal("expected file rejection")
		}
	})

	t.Run("rejects symlink", func(t *testing.T) {
		directory := t.TempDir()
		link := filepath.Join(t.TempDir(), "mountpoint")
		if err := os.Symlink(directory, link); err != nil {
			t.Fatal(err)
		}
		if _, err := validateDOFSMountpoint(link); err == nil {
			t.Fatal("expected symlink rejection")
		}
	})

	t.Run("rejects symlink parent", func(t *testing.T) {
		realParent := t.TempDir()
		mountpoint := filepath.Join(realParent, "mountpoint")
		if err := os.Mkdir(mountpoint, 0700); err != nil {
			t.Fatal(err)
		}
		link := filepath.Join(t.TempDir(), "parent")
		if err := os.Symlink(realParent, link); err != nil {
			t.Fatal(err)
		}
		if _, err := validateDOFSMountpoint(filepath.Join(link, "mountpoint")); err == nil {
			t.Fatal("expected symlink-parent rejection")
		}
	})
}

func TestResolveDOFSSocketAccess(t *testing.T) {
	gid, mode, err := resolveDOFSSocketAccess("")
	if err != nil || gid != -1 || mode != 0600 {
		t.Fatalf("owner-only socket access = gid %d mode %04o err %v", gid, mode, err)
	}
	gid, mode, err = resolveDOFSSocketAccess("1234")
	if err != nil || gid != 1234 || mode != 0660 {
		t.Fatalf("group socket access = gid %d mode %04o err %v", gid, mode, err)
	}
	if _, _, err := resolveDOFSSocketAccess("-1"); err == nil {
		t.Fatal("negative socket GID was accepted")
	}
}

func TestNotifySystemdDOFSReady(t *testing.T) {
	tests := []struct {
		name       string
		listenName string
	}{
		{name: "filesystem", listenName: filepath.Join(t.TempDir(), "notify.sock")},
		{
			name:       "abstract",
			listenName: "\x00domus-dofs-test-" + fmt.Sprint(os.Getpid(), "-", time.Now().UnixNano()),
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			listener, err := net.ListenUnixgram("unixgram", &net.UnixAddr{Name: test.listenName, Net: "unixgram"})
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = listener.Close() }()
			envName := test.listenName
			if strings.HasPrefix(envName, "\x00") {
				envName = "@" + strings.TrimPrefix(envName, "\x00")
			}
			t.Setenv("NOTIFY_SOCKET", envName)
			if err := notifySystemdDOFSReady(); err != nil {
				t.Fatalf("notifySystemdDOFSReady() error = %v", err)
			}
			if err := listener.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
				t.Fatal(err)
			}
			buffer := make([]byte, 256)
			read, _, err := listener.ReadFromUnix(buffer)
			if err != nil {
				t.Fatal(err)
			}
			if got := string(buffer[:read]); got != "READY=1\nSTATUS=DOFS control socket is ready" {
				t.Fatalf("systemd notification = %q", got)
			}
		})
	}

	t.Setenv("NOTIFY_SOCKET", "relative.sock")
	if err := notifySystemdDOFSReady(); err == nil {
		t.Fatal("relative NOTIFY_SOCKET was accepted")
	}
}
