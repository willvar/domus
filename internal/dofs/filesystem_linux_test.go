//go:build linux

package dofs

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
)

// This test is opt-in because many CI containers do not expose /dev/fuse.
// Run locally with: DOFS_FUSE_INTEGRATION=1 go test ./internal/dofs -run FUSE
func TestFUSEMountReadOnly(t *testing.T) {
	if os.Getenv("DOFS_FUSE_INTEGRATION") != "1" {
		t.Skip("set DOFS_FUSE_INTEGRATION=1 to exercise the kernel FUSE mount")
	}

	want := bytes.Repeat([]byte("dofs-integration\n"), 9000)
	backend, _, _ := newTestBackend(t, want)
	mountpoint := t.TempDir()
	server, err := Mount(mountpoint, backend, MountOptions{
		UID: uint32(os.Getuid()),
		GID: uint32(os.Getgid()),
	})
	if err != nil {
		t.Fatalf("mount DOFS: %v", err)
	}
	defer func() {
		_ = server.Unmount()
		server.Wait()
	}()
	mountInfo, err := inspectLinuxMount(mountpoint)
	if err != nil {
		t.Fatalf("inspect mounted DOFS: %v", err)
	}
	if !isDOFSMount(mountInfo) || mountInfo.Source != "dofs:"+backend.UserID() {
		t.Fatalf("unexpected managed mount identity: %+v", mountInfo)
	}

	path := filepath.Join(mountpoint, "home", "alice", "data.bin")
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read mounted file: %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("mounted plaintext mismatch: got %d bytes, want %d", len(got), len(want))
	}
	if err := os.WriteFile(path, []byte("overwrite"), 0600); err == nil {
		t.Fatal("read-only mount unexpectedly accepted a write")
	}
}

func TestFUSEMountWritableGeneration(t *testing.T) {
	if os.Getenv("DOFS_FUSE_INTEGRATION") != "1" {
		t.Skip("set DOFS_FUSE_INTEGRATION=1 to exercise the kernel FUSE mount")
	}

	backend, objects, record := newTestBackend(t, []byte("old contents"))
	repository := enableTestWriteback(t, backend, objects, t.TempDir())
	mountpoint := t.TempDir()
	server, err := Mount(mountpoint, backend, MountOptions{
		UID:      uint32(os.Getuid()),
		GID:      uint32(os.Getgid()),
		Writable: true,
	})
	if err != nil {
		t.Fatalf("mount writable DOFS: %v", err)
	}
	defer func() {
		_ = server.Unmount()
		server.Wait()
	}()

	path := filepath.Join(mountpoint, "home", "alice", "data.bin")
	want := bytes.Repeat([]byte("new-generation\n"), 9000)
	if err := os.WriteFile(path, want, 0600); err != nil {
		t.Fatalf("write mounted file: %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read committed file: %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("mounted plaintext mismatch: got %d bytes, want %d", len(got), len(want))
	}
	current, err := repository.GetByID(backend.UserID(), record.ID)
	if err != nil {
		t.Fatal(err)
	}
	if current.Generation <= record.Generation || current.StorageKey() == record.StorageKey() {
		t.Fatalf("generation was not switched: %+v", current)
	}
}

func TestFUSEMountWritableNamespaceLifecycle(t *testing.T) {
	if os.Getenv("DOFS_FUSE_INTEGRATION") != "1" {
		t.Skip("set DOFS_FUSE_INTEGRATION=1 to exercise the kernel FUSE mount")
	}

	backend, objects, _ := newTestBackend(t, []byte("base"))
	enableTestWriteback(t, backend, objects, t.TempDir())
	mountpoint := t.TempDir()
	server, err := Mount(mountpoint, backend, MountOptions{
		UID: uint32(os.Getuid()), GID: uint32(os.Getgid()), Writable: true,
	})
	if err != nil {
		t.Fatalf("mount writable DOFS: %v", err)
	}
	defer func() {
		_ = server.Unmount()
		server.Wait()
	}()

	home := filepath.Join(mountpoint, "home", "alice")
	projects := filepath.Join(home, "projects")
	if err := os.Mkdir(projects, 0700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	note := filepath.Join(projects, "note.txt")
	if err := os.WriteFile(note, []byte("created through FUSE"), 0600); err != nil {
		t.Fatalf("create file: %v", err)
	}
	archive := filepath.Join(home, "archive")
	if err := os.Rename(projects, archive); err != nil {
		t.Fatalf("rename directory: %v", err)
	}
	note = filepath.Join(archive, "note.txt")
	if got, err := os.ReadFile(note); err != nil || string(got) != "created through FUSE" {
		t.Fatalf("read renamed file: %q, %v", got, err)
	}

	handle, err := os.OpenFile(note, os.O_RDWR, 0)
	if err != nil {
		t.Fatalf("open before unlink: %v", err)
	}
	if err := os.Remove(note); err != nil {
		_ = handle.Close()
		t.Fatalf("unlink open file: %v", err)
	}
	if _, err := handle.WriteAt([]byte("OPEN"), 0); err != nil {
		_ = handle.Close()
		t.Fatalf("write unlinked handle: %v", err)
	}
	buffer := make([]byte, 4)
	if _, err := handle.ReadAt(buffer, 0); err != nil {
		_ = handle.Close()
		t.Fatalf("read unlinked handle: %v", err)
	}
	if string(buffer) != "OPEN" {
		t.Fatalf("unlinked handle contents = %q", buffer)
	}
	if err := handle.Close(); err != nil {
		t.Fatalf("close unlinked handle: %v", err)
	}
	if err := os.Remove(archive); err != nil {
		t.Fatalf("rmdir: %v", err)
	}
}

// TestFUSEBindMountIntoDocker proves the production handoff boundary: Docker
// receives only an existing plaintext bind mount, while OSS credentials and
// /dev/fuse remain in the host DOFS process. It is opt-in because it needs a
// local Docker daemon, FUSE, and a pre-pulled image.
func TestFUSEBindMountIntoDocker(t *testing.T) {
	if os.Getenv("DOFS_DOCKER_INTEGRATION") != "1" {
		t.Skip("set DOFS_DOCKER_INTEGRATION=1 to exercise a real Docker bind mount")
	}
	if _, err := exec.LookPath("docker"); err != nil {
		t.Skip("docker CLI is unavailable")
	}
	if err := ValidateHostRequirements(true); err != nil {
		t.Skipf("host is not configured for Docker-visible FUSE mounts: %v", err)
	}
	image := os.Getenv("DOFS_DOCKER_IMAGE")
	if image == "" {
		image = "domus-workspace:0.1.0"
	}
	if output, err := exec.Command("docker", "image", "inspect", image).CombinedOutput(); err != nil {
		t.Skipf("container image %q is not present (test never pulls): %v: %s", image, err, output)
	}

	backend, objects, _ := newTestBackend(t, []byte("before-container"))
	enableTestWriteback(t, backend, objects, t.TempDir())
	mountpoint := t.TempDir()
	server, err := Mount(mountpoint, backend, MountOptions{
		UID: uint32(os.Getuid()), GID: uint32(os.Getgid()), Writable: true, AllowOther: true,
	})
	if err != nil {
		t.Fatalf("mount writable DOFS: %v", err)
	}
	defer func() {
		_ = server.Unmount()
		server.Wait()
	}()

	target := "/workspace/home/alice/data.bin"
	want := "written-inside-user-container"
	mountArgument := fmt.Sprintf("type=bind,src=%s,dst=/workspace", mountpoint)
	identity := strconv.Itoa(os.Getuid()) + ":" + strconv.Itoa(os.Getgid())
	command := exec.Command(
		"docker", "run", "--rm", "--network=none", "--read-only",
		"--cap-drop=ALL", "--security-opt=no-new-privileges", "--user", identity,
		"--mount", mountArgument, image, "sh", "-c", "printf '%s' \"$1\" > \"$2\" && cat \"$2\"", "sh", want, target,
	)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("run isolated Docker bind-mount probe: %v: %s", err, output)
	}
	if string(output) != want {
		t.Fatalf("container readback = %q, want %q", output, want)
	}
	hostPath := filepath.Join(mountpoint, "home", "alice", "data.bin")
	if hostData, err := os.ReadFile(hostPath); err != nil || string(hostData) != want {
		t.Fatalf("host read after container write = %q, err=%v", hostData, err)
	}
}
