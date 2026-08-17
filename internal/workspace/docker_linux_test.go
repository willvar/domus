//go:build linux

package workspace

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/moby/moby/client"
)

func TestBoundedOutputSharesExactBudget(t *testing.T) {
	budget := &sharedOutputBudget{remaining: 5, limitReached: make(chan struct{})}
	stdout := &boundedOutput{budget: budget}
	stderr := &boundedOutput{budget: budget}
	if count, err := stdout.Write([]byte("abc")); err != nil || count != 3 {
		t.Fatalf("stdout.Write() = %d, %v", count, err)
	}
	if count, err := stderr.Write([]byte("def")); err != nil || count != 3 {
		t.Fatalf("stderr.Write() = %d, %v", count, err)
	}
	if string(stdout.Bytes()) != "abc" || string(stderr.Bytes()) != "de" || !budget.Truncated() {
		t.Fatalf("bounded output = stdout %q, stderr %q, truncated %v", stdout.Bytes(), stderr.Bytes(), budget.Truncated())
	}
	select {
	case <-budget.limitReached:
	default:
		t.Fatal("output budget did not signal the limit promptly")
	}
}

func TestDockerRuntimeIntegration(t *testing.T) {
	if os.Getenv("DOMUS_WORKSPACE_DOCKER_INTEGRATION") != "1" {
		t.Skip("set DOMUS_WORKSPACE_DOCKER_INTEGRATION=1 to run the real Docker test")
	}
	runtime, err := NewDockerRuntime("unix:///var/run/docker.sock")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = runtime.Close() }()
	ctx, cancel := context.WithTimeout(t.Context(), 90*time.Second)
	defer cancel()
	if err := runtime.Ping(ctx); err != nil {
		t.Fatalf("Docker Ping() error = %v", err)
	}
	imageName := os.Getenv("DOMUS_WORKSPACE_TEST_IMAGE")
	if imageName == "" {
		imageName = "domus-workspace:0.1.0"
	}
	image, err := runtime.ResolveImage(ctx, imageName, "never")
	if err != nil {
		t.Fatalf("resolve test image %q: %v", imageName, err)
	}
	mountpoint := t.TempDir()
	if err := os.Chmod(mountpoint, 0777); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(mountpoint, "home", "tester"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(mountpoint, "input"), []byte("from-dofs\n"), 0644); err != nil {
		t.Fatal(err)
	}
	identityDirectory := t.TempDir()
	passwdPath := filepath.Join(identityDirectory, "passwd")
	groupPath := filepath.Join(identityDirectory, "group")
	if err := os.WriteFile(passwdPath, []byte("root:x:0:0:root:/root:/sbin/nologin\ntester:x:1000:1000:Domus workspace:/workspace/home/tester:/bin/sh\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(groupPath, []byte("root:x:0:\ntester:x:1000:\n"), 0644); err != nil {
		t.Fatal(err)
	}
	suffix := strings.ReplaceAll(uuid.NewString(), "-", "")[:16]
	spec := ContainerSpec{
		Name: "domus-integration-" + suffix, Hostname: "domus-test", Image: image,
		UserID: "user-" + suffix, Username: "tester", ManagerID: uuid.NewString(),
		Mountpoint: mountpoint, PasswdPath: passwdPath, GroupPath: groupPath,
		MountID: uuid.NewString(), SpecHash: uuid.NewString(),
		UID: 1000, GID: 1000, NetworkMode: "none", ReadOnlyRootFS: true,
		MemoryBytes: 128 * 1024 * 1024, MemorySwap: 128 * 1024 * 1024,
		NanoCPUs: 500_000_000, PIDsLimit: 64, TmpfsSize: 8 * 1024 * 1024,
		ShmSize: 8 * 1024 * 1024, StopTimeout: 2,
		Keepalive: []string{"/bin/sh", "-c", "trap 'exit 0' TERM INT; while :; do sleep 3600 & wait $!; done"},
	}
	container, err := runtime.Create(ctx, spec)
	if err != nil {
		t.Fatal(err)
	}
	// The exact ID comes from this test's successful create call; cleanup never
	// uses a name, glob, label query, or environment-derived target.
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cleanupCancel()
		_ = runtime.Stop(cleanupCtx, container.ID, 1)
		_ = runtime.Remove(cleanupCtx, container.ID)
	})
	if err := runtime.Start(ctx, container.ID); err != nil {
		t.Fatal(err)
	}

	result, err := runtime.Exec(ctx, RuntimeExecRequest{
		ContainerID: container.ID,
		Command:     []string{"/bin/sh", "-c", "id -u; whoami; cat /workspace/input; printf written > /workspace/output"},
		WorkingDir:  "/workspace", UID: 1000, GID: 1000, OutputLimit: 1024 * 1024,
	})
	if err != nil {
		t.Fatalf("Exec() error = %v, stderr=%s", err, result.Stderr)
	}
	if result.ExitCode != 0 || !strings.Contains(string(result.Stdout), "1000\ntester\nfrom-dofs") {
		t.Fatalf("Exec() result = %+v", result)
	}
	written, err := os.ReadFile(filepath.Join(mountpoint, "output"))
	if err != nil || string(written) != "written" {
		t.Fatalf("bind write = %q, %v", written, err)
	}
	if imageName == "domus-workspace:0.1.0" {
		sshConfig, sshConfigErr := runtime.Exec(ctx, RuntimeExecRequest{
			ContainerID: container.ID,
			Command:     []string{"/bin/sh", "-c", "test \"$(getent passwd $(id -u) | cut -d: -f6)\" = /workspace/home/tester && ssh -G example.com 2>/dev/null | grep -q '^userknownhostsfile /workspace/home/tester/.ssh/known_hosts'"},
			WorkingDir:  "/workspace/home/tester", UID: 1000, GID: 1000, OutputLimit: 1024 * 1024,
		})
		if sshConfigErr != nil || sshConfig.ExitCode != 0 {
			t.Fatalf("dynamic Unix/OpenSSH home = %+v, %v", sshConfig, sshConfigErr)
		}
		if err := os.WriteFile(filepath.Join(mountpoint, "source.ppm"), []byte("P3\n2 2\n255\n255 0 0  0 255 0\n0 0 255  255 255 255\n"), 0644); err != nil {
			t.Fatal(err)
		}
		preview, previewErr := runtime.Exec(ctx, RuntimeExecRequest{
			ContainerID: container.ID,
			Command:     []string{"/usr/local/bin/domus-preview", "/workspace/source.ppm", "/workspace/preview.jpg"},
			WorkingDir:  "/workspace", UID: 1000, GID: 1000, OutputLimit: 1024 * 1024,
		})
		if previewErr != nil || preview.ExitCode != 0 {
			t.Fatalf("domus-preview = %+v, %v", preview, previewErr)
		}
		if info, err := os.Stat(filepath.Join(mountpoint, "preview.jpg")); err != nil || info.Size() == 0 {
			t.Fatalf("preview output was not created: %v", err)
		}
		if err := os.WriteFile(filepath.Join(mountpoint, "source.pdf"), minimalPDF(), 0644); err != nil {
			t.Fatal(err)
		}
		pdfPreview, pdfPreviewErr := runtime.Exec(ctx, RuntimeExecRequest{
			ContainerID: container.ID,
			Command:     []string{"/usr/local/bin/domus-preview", "/workspace/source.pdf", "/workspace/pdf-preview.jpg"},
			WorkingDir:  "/workspace", UID: 1000, GID: 1000, OutputLimit: 1024 * 1024,
		})
		if pdfPreviewErr != nil || pdfPreview.ExitCode != 0 {
			t.Fatalf("PDF domus-preview = %+v, %v", pdfPreview, pdfPreviewErr)
		}
		if info, err := os.Stat(filepath.Join(mountpoint, "pdf-preview.jpg")); err != nil || info.Size() == 0 {
			t.Fatalf("PDF preview output was not created: %v", err)
		}
		generated, generateErr := runtime.Exec(ctx, RuntimeExecRequest{
			ContainerID: container.ID,
			Command:     []string{"ffmpeg", "-nostdin", "-hide_banner", "-loglevel", "error", "-y", "-f", "lavfi", "-i", "sine=frequency=440:duration=0.1", "/workspace/source.wav"},
			WorkingDir:  "/workspace", UID: 1000, GID: 1000, OutputLimit: 1024 * 1024,
		})
		if generateErr != nil || generated.ExitCode != 0 {
			t.Fatalf("generate media fixture = %+v, %v", generated, generateErr)
		}
		transcoded, transcodeErr := runtime.Exec(ctx, RuntimeExecRequest{
			ContainerID: container.ID,
			Command:     []string{"/usr/local/bin/domus-transcode", "audio-mp3", "/workspace/source.wav", "/workspace/preview.mp3"},
			WorkingDir:  "/workspace", UID: 1000, GID: 1000, OutputLimit: 1024 * 1024,
		})
		if transcodeErr != nil || transcoded.ExitCode != 0 {
			t.Fatalf("domus-transcode = %+v, %v", transcoded, transcodeErr)
		}
		if info, err := os.Stat(filepath.Join(mountpoint, "preview.mp3")); err != nil || info.Size() == 0 {
			t.Fatalf("transcode output was not created: %v", err)
		}
	}

	inspected, err := runtime.client.ContainerInspect(ctx, container.ID, client.ContainerInspectOptions{})
	if err != nil {
		t.Fatal(err)
	}
	host := inspected.Container.HostConfig
	if host == nil || host.Privileged || !host.ReadonlyRootfs || host.Memory != spec.MemoryBytes ||
		host.MemorySwap != spec.MemorySwap || host.NanoCPUs != spec.NanoCPUs || host.PidsLimit == nil || *host.PidsLimit != spec.PIDsLimit {
		t.Fatalf("container hardening/resources were not applied: %+v", host)
	}
	if string(host.NetworkMode) == "host" || len(host.PortBindings) != 0 || len(host.Devices) != 0 {
		t.Fatalf("unsafe container connectivity/devices: %+v", host)
	}
	if len(host.Mounts) != 3 {
		t.Fatalf("unexpected bind mounts: %+v", host.Mounts)
	}
	mountsByTarget := make(map[string]struct {
		source   string
		readOnly bool
	}, len(host.Mounts))
	for _, configuredMount := range host.Mounts {
		mountsByTarget[configuredMount.Target] = struct {
			source   string
			readOnly bool
		}{configuredMount.Source, configuredMount.ReadOnly}
	}
	if mounted := mountsByTarget["/workspace"]; mounted.source != mountpoint || mounted.readOnly {
		t.Fatalf("unexpected workspace bind: %+v", mounted)
	}
	if mounted := mountsByTarget["/etc/passwd"]; mounted.source != passwdPath || !mounted.readOnly {
		t.Fatalf("unexpected passwd bind: %+v", mounted)
	}
	if mounted := mountsByTarget["/etc/group"]; mounted.source != groupPath || !mounted.readOnly {
		t.Fatalf("unexpected group bind: %+v", mounted)
	}
	if inspected.Container.Config == nil || len(inspected.Container.Config.ExposedPorts) != 0 {
		t.Fatalf("container exposed ports unexpectedly: %+v", inspected.Container.Config)
	}

	session, err := runtime.OpenSession(ctx, RuntimeSessionRequest{
		ContainerID: container.ID, Command: []string{"/bin/sh"}, WorkingDir: "/workspace",
		UID: 1000, GID: 1000, Columns: 80, Rows: 24,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = session.Close() }()
	if _, err := session.Write([]byte("printf tty-ok\\n\nexit\n")); err != nil {
		t.Fatal(err)
	}
	ttyOutput := make(chan []byte, 1)
	go func() {
		data, _ := io.ReadAll(session)
		ttyOutput <- data
	}()
	select {
	case data := <-ttyOutput:
		if !strings.Contains(string(data), "tty-ok") {
			t.Fatalf("TTY output = %q", data)
		}
	case <-ctx.Done():
		t.Fatal("timed out waiting for TTY output")
	}
	exitCode, err := session.Wait()
	if err != nil || exitCode != 0 {
		t.Fatalf("TTY Wait() = %d, %v", exitCode, err)
	}

	// An abandoned TTY must not leave a detached process behind. Closing the
	// attach stream either ends the exec via stdin EOF or stops the container
	// fail-closed when the command keeps running.
	abandoned, err := runtime.OpenSession(ctx, RuntimeSessionRequest{
		ContainerID: container.ID, Command: []string{"sleep", "30"}, WorkingDir: "/workspace",
		UID: 1000, GID: 1000, Columns: 80, Rows: 24,
	})
	if err != nil {
		t.Fatal(err)
	}
	abandonedProcess, ok := abandoned.(*dockerProcess)
	if !ok {
		t.Fatalf("OpenSession() process type = %T", abandoned)
	}
	if err := abandoned.Close(); err != nil {
		t.Fatalf("close abandoned TTY: %v", err)
	}
	abandonedState, err := runtime.client.ExecInspect(ctx, abandonedProcess.execID, client.ExecInspectOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if abandonedState.Running {
		t.Fatal("abandoned TTY process remained running")
	}
	containerState, err := runtime.client.ContainerInspect(ctx, container.ID, client.ContainerInspectOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if containerState.Container.State != nil && !containerState.Container.State.Running {
		if err := runtime.Start(ctx, container.ID); err != nil {
			t.Fatalf("restart workspace after abandoned TTY: %v", err)
		}
	}

	// Crossing the output budget must stop an unbounded producer immediately,
	// rather than letting it consume resources until the overall exec timeout.
	outputContext, cancelOutput := context.WithTimeout(context.Background(), 10*time.Second)
	outputResult, outputErr := runtime.Exec(outputContext, RuntimeExecRequest{
		ContainerID: container.ID, Command: []string{"yes", "domus"}, WorkingDir: "/workspace",
		UID: 1000, GID: 1000, OutputLimit: 1024,
	})
	cancelOutput()
	if !errors.Is(outputErr, ErrOutputLimit) || !outputResult.Truncated || len(outputResult.Stdout)+len(outputResult.Stderr) != 1024 {
		t.Fatalf("output-limited Exec() = %d bytes, truncated=%v, error=%v", len(outputResult.Stdout)+len(outputResult.Stderr), outputResult.Truncated, outputErr)
	}
	containerState, err = runtime.client.ContainerInspect(ctx, container.ID, client.ContainerInspectOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if containerState.Container.State == nil || containerState.Container.State.Running {
		t.Fatalf("container remained running after output limit: %+v", containerState.Container.State)
	}
	if err := runtime.Start(ctx, container.ID); err != nil {
		t.Fatalf("restart workspace after output limit: %v", err)
	}

	// Cancellation is fail-closed because Docker cannot signal one exec
	// process. The entire reusable container must stop so no detached command
	// can outlive the caller.
	cancelledContext, cancelExec := context.WithTimeout(context.Background(), 100*time.Millisecond)
	_, cancelledErr := runtime.Exec(cancelledContext, RuntimeExecRequest{
		ContainerID: container.ID, Command: []string{"sleep", "30"}, WorkingDir: "/workspace",
		UID: 1000, GID: 1000, OutputLimit: 1024,
	})
	cancelExec()
	if !errors.Is(cancelledErr, context.DeadlineExceeded) {
		t.Fatalf("cancelled Exec() error = %v, want deadline exceeded", cancelledErr)
	}
	containerState, err = runtime.client.ContainerInspect(ctx, container.ID, client.ContainerInspectOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if containerState.Container.State == nil || containerState.Container.State.Running {
		t.Fatalf("container remained running after cancelled exec: %+v", containerState.Container.State)
	}
}

func minimalPDF() []byte {
	content := "BT /F1 24 Tf 72 100 Td (Domus) Tj ET"
	objects := []string{
		"<< /Type /Catalog /Pages 2 0 R >>",
		"<< /Type /Pages /Kids [3 0 R] /Count 1 >>",
		"<< /Type /Page /Parent 2 0 R /MediaBox [0 0 300 200] /Resources << /Font << /F1 5 0 R >> >> /Contents 4 0 R >>",
		fmt.Sprintf("<< /Length %d >>\nstream\n%s\nendstream", len(content), content),
		"<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>",
	}
	var document strings.Builder
	document.WriteString("%PDF-1.4\n")
	offsets := make([]int, 0, len(objects))
	for index, object := range objects {
		offsets = append(offsets, document.Len())
		fmt.Fprintf(&document, "%d 0 obj\n%s\nendobj\n", index+1, object)
	}
	xrefOffset := document.Len()
	fmt.Fprintf(&document, "xref\n0 %d\n0000000000 65535 f \n", len(objects)+1)
	for _, offset := range offsets {
		fmt.Fprintf(&document, "%010d 00000 n \n", offset)
	}
	fmt.Fprintf(&document, "trailer\n<< /Size %d /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n", len(objects)+1, xrefOffset)
	return []byte(document.String())
}
