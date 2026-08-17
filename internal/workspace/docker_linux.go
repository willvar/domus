//go:build linux

package workspace

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"path"
	"strconv"
	"strings"
	"sync"
	"time"

	cerrdefs "github.com/containerd/errdefs"
	"github.com/moby/moby/api/pkg/stdcopy"
	containertypes "github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/mount"
	"github.com/moby/moby/client"
)

type DockerRuntime struct {
	client *client.Client
}

func NewDockerRuntime(host string) (*DockerRuntime, error) {
	host = strings.TrimSpace(host)
	if !strings.HasPrefix(host, "unix://") || !path.IsAbs(strings.TrimPrefix(host, "unix://")) {
		return nil, errors.New("workspace Docker host must be an absolute unix socket")
	}
	dockerClient, err := client.New(client.WithHost(host))
	if err != nil {
		return nil, fmt.Errorf("initialize Docker client: %w", err)
	}
	return &DockerRuntime{client: dockerClient}, nil
}

func (r *DockerRuntime) Ping(ctx context.Context) error {
	ping, err := r.client.Ping(ctx, client.PingOptions{NegotiateAPIVersion: true})
	if err != nil {
		return err
	}
	if ping.OSType != "" && ping.OSType != "linux" {
		return fmt.Errorf("docker engine OS is %q, want linux", ping.OSType)
	}
	return nil
}

func (r *DockerRuntime) ResolveImage(ctx context.Context, reference, pullPolicy string) (RuntimeImage, error) {
	inspect := func() (RuntimeImage, error) {
		result, err := r.client.ImageInspect(ctx, reference)
		if err != nil {
			return RuntimeImage{}, err
		}
		return RuntimeImage{ID: result.ID, Reference: reference}, nil
	}
	if pullPolicy != "always" {
		image, err := inspect()
		if err == nil {
			return image, nil
		}
		if pullPolicy == "never" || !cerrdefs.IsNotFound(err) {
			return RuntimeImage{}, err
		}
	}
	pull, err := r.client.ImagePull(ctx, reference, client.ImagePullOptions{})
	if err != nil {
		return RuntimeImage{}, err
	}
	defer func() { _ = pull.Close() }()
	if err := pull.Wait(ctx); err != nil {
		return RuntimeImage{}, err
	}
	return inspect()
}

func (r *DockerRuntime) FindByName(ctx context.Context, name string) (RuntimeContainer, bool, error) {
	result, err := r.client.ContainerList(ctx, client.ContainerListOptions{
		All: true, Filters: make(client.Filters).Add("name", "^/"+name+"$"),
	})
	if err != nil {
		return RuntimeContainer{}, false, err
	}
	for _, candidate := range result.Items {
		for _, candidateName := range candidate.Names {
			if strings.TrimPrefix(candidateName, "/") == name {
				return RuntimeContainer{
					ID: candidate.ID, Name: name, Running: candidate.State == containertypes.StateRunning,
					State: string(candidate.State), Labels: cloneLabels(candidate.Labels),
				}, true, nil
			}
		}
	}
	return RuntimeContainer{}, false, nil
}

func (r *DockerRuntime) ListManaged(ctx context.Context) ([]RuntimeContainer, error) {
	result, err := r.client.ContainerList(ctx, client.ContainerListOptions{
		All: true, Filters: make(client.Filters).Add("label", labelManaged+"=true"),
	})
	if err != nil {
		return nil, err
	}
	containers := make([]RuntimeContainer, 0, len(result.Items))
	for _, candidate := range result.Items {
		name := ""
		if len(candidate.Names) > 0 {
			name = strings.TrimPrefix(candidate.Names[0], "/")
		}
		containers = append(containers, RuntimeContainer{
			ID: candidate.ID, Name: name, Running: candidate.State == containertypes.StateRunning,
			State: string(candidate.State), Labels: cloneLabels(candidate.Labels),
		})
	}
	return containers, nil
}

func cloneLabels(input map[string]string) map[string]string {
	output := make(map[string]string, len(input))
	for key, value := range input {
		output[key] = value
	}
	return output
}

func (r *DockerRuntime) Create(ctx context.Context, spec ContainerSpec) (RuntimeContainer, error) {
	if !path.IsAbs(spec.Mountpoint) || !path.IsAbs(spec.PasswdPath) || !path.IsAbs(spec.GroupPath) {
		return RuntimeContainer{}, errors.New("workspace bind sources must be absolute")
	}
	initProcess := true
	pidsLimit := spec.PIDsLimit
	identity := strconv.FormatUint(uint64(spec.UID), 10) + ":" + strconv.FormatUint(uint64(spec.GID), 10)
	home := path.Join("/workspace/home", spec.Username)
	labels := map[string]string{
		labelManaged: "true", labelManagerID: spec.ManagerID, labelUserID: spec.UserID,
		labelUsername: spec.Username, labelMountID: spec.MountID, labelSpecHash: spec.SpecHash,
	}
	created, err := r.client.ContainerCreate(ctx, client.ContainerCreateOptions{
		Name: spec.Name,
		Config: &containertypes.Config{
			Hostname: spec.Hostname, User: identity, Image: spec.Image.ID,
			Env: []string{"HOME=" + home, "USER=" + spec.Username, "LOGNAME=" + spec.Username, "DOMUS_USER_ID=" + spec.UserID},
			Cmd: append([]string(nil), spec.Keepalive...), WorkingDir: home, Labels: labels,
			StopTimeout: &spec.StopTimeout,
		},
		HostConfig: &containertypes.HostConfig{
			NetworkMode:   containertypes.NetworkMode(spec.NetworkMode),
			RestartPolicy: containertypes.RestartPolicy{Name: containertypes.RestartPolicyDisabled},
			LogConfig:     containertypes.LogConfig{Type: "json-file", Config: map[string]string{"max-size": "10m", "max-file": "3"}},
			CapDrop:       []string{"ALL"}, Privileged: false, PublishAllPorts: false,
			ReadonlyRootfs: spec.ReadOnlyRootFS, SecurityOpt: []string{"no-new-privileges:true"},
			Tmpfs: map[string]string{
				"/tmp": "rw,nosuid,nodev,size=" + strconv.FormatInt(spec.TmpfsSize, 10),
				"/run": "rw,nosuid,nodev,noexec,size=" + strconv.FormatInt(spec.TmpfsSize, 10),
			},
			ShmSize: spec.ShmSize, Init: &initProcess,
			Resources: containertypes.Resources{
				Memory: spec.MemoryBytes, MemorySwap: spec.MemorySwap, NanoCPUs: spec.NanoCPUs, PidsLimit: &pidsLimit,
			},
			Mounts: []mount.Mount{
				{
					Type: mount.TypeBind, Source: spec.Mountpoint, Target: "/workspace", ReadOnly: false,
					BindOptions: &mount.BindOptions{Propagation: mount.PropagationRPrivate, CreateMountpoint: false},
				},
				{
					Type: mount.TypeBind, Source: spec.PasswdPath, Target: "/etc/passwd", ReadOnly: true,
					BindOptions: &mount.BindOptions{Propagation: mount.PropagationRPrivate, CreateMountpoint: false},
				},
				{
					Type: mount.TypeBind, Source: spec.GroupPath, Target: "/etc/group", ReadOnly: true,
					BindOptions: &mount.BindOptions{Propagation: mount.PropagationRPrivate, CreateMountpoint: false},
				},
			},
		},
	})
	if err != nil {
		return RuntimeContainer{}, err
	}
	return RuntimeContainer{ID: created.ID, Name: spec.Name, State: "created", Labels: labels}, nil
}

func (r *DockerRuntime) Start(ctx context.Context, containerID string) error {
	_, err := r.client.ContainerStart(ctx, containerID, client.ContainerStartOptions{})
	if cerrdefs.IsNotModified(err) {
		return nil
	}
	return err
}

func (r *DockerRuntime) Stop(ctx context.Context, containerID string, timeoutSeconds int) error {
	_, err := r.client.ContainerStop(ctx, containerID, client.ContainerStopOptions{Timeout: &timeoutSeconds})
	if cerrdefs.IsNotModified(err) || cerrdefs.IsNotFound(err) {
		return nil
	}
	return err
}

func (r *DockerRuntime) Remove(ctx context.Context, containerID string) error {
	_, err := r.client.ContainerRemove(ctx, containerID, client.ContainerRemoveOptions{RemoveVolumes: true})
	if cerrdefs.IsNotFound(err) {
		return nil
	}
	return err
}

func (r *DockerRuntime) Exec(ctx context.Context, request RuntimeExecRequest) (ExecResult, error) {
	startedAt := time.Now()
	created, err := r.client.ExecCreate(ctx, request.ContainerID, client.ExecCreateOptions{
		User: runtimeIdentity(request.UID, request.GID), AttachStdin: len(request.Stdin) > 0,
		AttachStdout: true, AttachStderr: true, TTY: false, Privileged: false,
		Env: request.Environment, WorkingDir: request.WorkingDir, Cmd: request.Command,
	})
	if err != nil {
		return ExecResult{}, err
	}
	attached, err := r.client.ExecAttach(ctx, created.ID, client.ExecAttachOptions{TTY: false})
	if err != nil {
		return ExecResult{}, err
	}
	defer attached.Close()
	combined := &sharedOutputBudget{remaining: request.OutputLimit, limitReached: make(chan struct{})}
	stdout := boundedOutput{budget: combined}
	stderr := boundedOutput{budget: combined}
	copyDone := make(chan error, 1)
	go func() {
		_, copyErr := stdcopy.StdCopy(&stdout, &stderr, attached.Reader)
		copyDone <- copyErr
	}()
	if len(request.Stdin) > 0 {
		if deadline, ok := ctx.Deadline(); ok {
			_ = attached.Conn.SetWriteDeadline(deadline)
		}
		interruptWrite := context.AfterFunc(ctx, func() {
			_ = attached.Conn.SetWriteDeadline(time.Now())
		})
		_, writeErr := attached.Conn.Write(request.Stdin)
		interruptWrite()
		_ = attached.Conn.SetWriteDeadline(time.Time{})
		if writeErr != nil {
			attached.Close()
			stopErr := r.stopAfterExecFailure(request.ContainerID)
			waitForExecCopy(copyDone)
			if ctx.Err() != nil {
				return execPartialResult(startedAt, &stdout, &stderr, combined), errors.Join(ctx.Err(), stopErr)
			}
			return execPartialResult(startedAt, &stdout, &stderr, combined), errors.Join(writeErr, stopErr)
		}
		_ = attached.CloseWrite()
	}
	select {
	case <-ctx.Done():
		attached.Close()
		stopErr := r.stopAfterExecFailure(request.ContainerID)
		waitForExecCopy(copyDone)
		return execPartialResult(startedAt, &stdout, &stderr, combined), errors.Join(ctx.Err(), stopErr)
	case <-combined.limitReached:
		attached.Close()
		stopErr := r.stopAfterExecFailure(request.ContainerID)
		waitForExecCopy(copyDone)
		return execPartialResult(startedAt, &stdout, &stderr, combined), errors.Join(ErrOutputLimit, stopErr)
	case copyErr := <-copyDone:
		if copyErr != nil && !errors.Is(copyErr, io.EOF) {
			attached.Close()
			stopErr := r.stopAfterExecFailure(request.ContainerID)
			return execPartialResult(startedAt, &stdout, &stderr, combined), errors.Join(copyErr, stopErr)
		}
	}
	inspected, err := r.client.ExecInspect(ctx, created.ID, client.ExecInspectOptions{})
	if err != nil {
		stopErr := r.stopAfterExecFailure(request.ContainerID)
		return execPartialResult(startedAt, &stdout, &stderr, combined), errors.Join(err, stopErr)
	}
	if inspected.Running {
		stopErr := r.stopAfterExecFailure(request.ContainerID)
		return execPartialResult(startedAt, &stdout, &stderr, combined), errors.Join(errors.New("workspace exec stream closed while process was still running"), stopErr)
	}
	result := ExecResult{
		ExitCode: inspected.ExitCode, Stdout: stdout.Bytes(), Stderr: stderr.Bytes(),
		Duration: time.Since(startedAt), Truncated: combined.Truncated(),
	}
	if result.Truncated {
		return result, ErrOutputLimit
	}
	return result, nil
}

func (r *DockerRuntime) stopAfterExecFailure(containerID string) error {
	stopCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err := r.client.ContainerStop(stopCtx, containerID, client.ContainerStopOptions{Timeout: intPointer(1)})
	if cerrdefs.IsNotModified(err) || cerrdefs.IsNotFound(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("stop workspace container after exec failure: %w", err)
	}
	return nil
}

func waitForExecCopy(copyDone <-chan error) {
	select {
	case <-copyDone:
	case <-time.After(2 * time.Second):
	}
}

func execPartialResult(startedAt time.Time, stdout, stderr *boundedOutput, budget *sharedOutputBudget) ExecResult {
	return ExecResult{
		Stdout: stdout.Bytes(), Stderr: stderr.Bytes(), Duration: time.Since(startedAt), Truncated: budget.Truncated(),
	}
}

type sharedOutputBudget struct {
	mu           sync.Mutex
	remaining    int64
	truncated    bool
	limitOnce    sync.Once
	limitReached chan struct{}
}

type boundedOutput struct {
	mu     sync.Mutex
	buffer bytes.Buffer
	budget *sharedOutputBudget
}

func (output *boundedOutput) Write(data []byte) (int, error) {
	originalLength := len(data)
	output.budget.mu.Lock()
	allowed := int64(len(data))
	if allowed > output.budget.remaining {
		allowed = output.budget.remaining
		output.budget.truncated = true
		if output.budget.limitReached != nil {
			output.budget.limitOnce.Do(func() { close(output.budget.limitReached) })
		}
	}
	if allowed < 0 {
		allowed = 0
	}
	output.budget.remaining -= allowed
	output.budget.mu.Unlock()
	if allowed > 0 {
		output.mu.Lock()
		_, _ = output.buffer.Write(data[:allowed])
		output.mu.Unlock()
	}
	return originalLength, nil
}

func (output *boundedOutput) Bytes() []byte {
	output.mu.Lock()
	defer output.mu.Unlock()
	return append([]byte(nil), output.buffer.Bytes()...)
}

func (budget *sharedOutputBudget) Truncated() bool {
	budget.mu.Lock()
	defer budget.mu.Unlock()
	return budget.truncated
}

func intPointer(value int) *int { return &value }

func runtimeIdentity(uid, gid uint32) string {
	return strconv.FormatUint(uint64(uid), 10) + ":" + strconv.FormatUint(uint64(gid), 10)
}

func (r *DockerRuntime) OpenSession(ctx context.Context, request RuntimeSessionRequest) (RuntimeProcess, error) {
	created, err := r.client.ExecCreate(ctx, request.ContainerID, client.ExecCreateOptions{
		User: runtimeIdentity(request.UID, request.GID), AttachStdin: true, AttachStdout: true, AttachStderr: true,
		TTY: true, Privileged: false, ConsoleSize: client.ConsoleSize{Height: request.Rows, Width: request.Columns},
		Env: request.Environment, WorkingDir: request.WorkingDir, Cmd: request.Command,
	})
	if err != nil {
		return nil, err
	}
	attached, err := r.client.ExecAttach(ctx, created.ID, client.ExecAttachOptions{
		TTY: true, ConsoleSize: client.ConsoleSize{Height: request.Rows, Width: request.Columns},
	})
	if err != nil {
		return nil, err
	}
	processCtx, cancel := context.WithCancel(context.Background())
	process := &dockerProcess{
		client: r.client, execID: created.ID, containerID: request.ContainerID, response: attached.HijackedResponse,
		ctx: processCtx, cancel: cancel, waitDone: make(chan struct{}), exitCode: -1,
	}
	go process.pollExit()
	return process, nil
}

type dockerProcess struct {
	client      *client.Client
	execID      string
	containerID string
	response    client.HijackedResponse
	ctx         context.Context
	cancel      context.CancelFunc

	writeMu  sync.Mutex
	closeMu  sync.Once
	closeErr error
	waitDone chan struct{}
	waitMu   sync.Mutex
	exitCode int
	waitErr  error
}

func (p *dockerProcess) Read(buffer []byte) (int, error) {
	return p.response.Reader.Read(buffer)
}

func (p *dockerProcess) Write(buffer []byte) (int, error) {
	p.writeMu.Lock()
	defer p.writeMu.Unlock()
	return p.response.Conn.Write(buffer)
}

func (p *dockerProcess) Resize(ctx context.Context, columns, rows uint) error {
	_, err := p.client.ExecResize(ctx, p.execID, client.ExecResizeOptions{Width: columns, Height: rows})
	return err
}

func (p *dockerProcess) pollExit() {
	defer close(p.waitDone)
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		inspectCtx, cancel := context.WithTimeout(p.ctx, 5*time.Second)
		inspected, err := p.client.ExecInspect(inspectCtx, p.execID, client.ExecInspectOptions{})
		cancel()
		if err == nil && !inspected.Running {
			p.waitMu.Lock()
			p.exitCode = inspected.ExitCode
			p.waitMu.Unlock()
			return
		}
		if err != nil && p.ctx.Err() == nil && cerrdefs.IsNotFound(err) {
			p.waitMu.Lock()
			p.waitErr = ErrContainerStopped
			p.waitMu.Unlock()
			return
		}
		select {
		case <-p.ctx.Done():
			p.waitMu.Lock()
			p.waitErr = p.ctx.Err()
			p.waitMu.Unlock()
			return
		case <-ticker.C:
		}
	}
}

func (p *dockerProcess) Wait() (int, error) {
	<-p.waitDone
	p.waitMu.Lock()
	defer p.waitMu.Unlock()
	return p.exitCode, p.waitErr
}

func (p *dockerProcess) Close() error {
	p.closeMu.Do(func() {
		p.closeErr = p.response.CloseWrite()
		p.response.Close()
		// Closing stdin normally makes an interactive shell exit. If the exec is
		// still alive (for example, a foreground program ignores stdin), Docker
		// has no API to signal that one exec process. Stop the reusable container
		// fail-closed so a disconnected terminal cannot leave detached commands.
		select {
		case <-p.waitDone:
		case <-time.After(time.Second):
			stopCtx, stopCancel := context.WithTimeout(context.Background(), 5*time.Second)
			_, stopErr := p.client.ContainerStop(stopCtx, p.containerID, client.ContainerStopOptions{Timeout: intPointer(1)})
			stopCancel()
			if cerrdefs.IsNotModified(stopErr) || cerrdefs.IsNotFound(stopErr) {
				stopErr = nil
			}
			if stopErr != nil {
				p.closeErr = errors.Join(p.closeErr, fmt.Errorf("stop workspace container after abandoned session: %w", stopErr))
			}
		}
		p.cancel()
	})
	return p.closeErr
}

func (r *DockerRuntime) Close() error {
	return r.client.Close()
}
