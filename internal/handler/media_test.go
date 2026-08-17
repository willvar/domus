package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"path"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gofiber/fiber/v2"
	"gorm.io/gorm"

	"domus/config"
	"domus/internal/model"
	workspaceRuntime "domus/internal/workspace"
)

type mediaWorkspaceFake struct {
	workspaceRuntime.Service
	mu       sync.Mutex
	commands [][]string
	exec     func(context.Context, workspaceRuntime.ExecRequest) (workspaceRuntime.ExecResult, error)
}

func (f *mediaWorkspaceFake) Exec(ctx context.Context, request workspaceRuntime.ExecRequest) (workspaceRuntime.ExecResult, error) {
	f.mu.Lock()
	f.commands = append(f.commands, append([]string(nil), request.Command...))
	f.mu.Unlock()
	return f.exec(ctx, request)
}

func newMediaTestHandler(t *testing.T) (*Handler, *model.Repos, *mediaWorkspaceFake, *model.User, model.FileRecord) {
	t.Helper()
	repos := model.NewMemRepos(nil)
	user, err := repos.Users.Create("alice", "password", "user", "")
	if err != nil {
		t.Fatal(err)
	}
	sourcePath := "alice/home/alice/photo.jpg"
	if err := repos.Files.Upsert(user.ID, sourcePath, "photo.jpg", false, 64, "image/jpeg", "", model.UpsertFileOpts{WrappedDEK: "source-dek"}); err != nil {
		t.Fatal(err)
	}
	source, err := repos.Files.Get(user.ID, sourcePath)
	if err != nil {
		t.Fatal(err)
	}
	fake := &mediaWorkspaceFake{}
	handler := &Handler{
		Config: &config.Config{Workspace: config.WorkspaceConfig{
			MaxSessionsPerUser: 4, ExecTimeoutSeconds: 5,
		}},
		Repos: repos, Workspace: fake,
	}
	fake.exec = func(_ context.Context, request workspaceRuntime.ExecRequest) (workspaceRuntime.ExecResult, error) {
		if len(request.Command) == 0 {
			return workspaceRuntime.ExecResult{}, errors.New("empty command")
		}
		switch request.Command[0] {
		case "mkdir", "rm", "test":
			return workspaceRuntime.ExecResult{ExitCode: 0}, nil
		case "mv":
			if len(request.Command) != 5 {
				return workspaceRuntime.ExecResult{ExitCode: 64}, nil
			}
			containerSource, containerOutput := request.Command[3], request.Command[4]
			logicalSource := user.Username + "/" + strings.TrimPrefix(containerSource, "/workspace/")
			logicalOutput := user.Username + "/" + strings.TrimPrefix(containerOutput, "/workspace/")
			if err := repos.Files.Move(user.ID, logicalSource, logicalOutput, path.Base(logicalOutput)); err != nil {
				return workspaceRuntime.ExecResult{}, err
			}
			return workspaceRuntime.ExecResult{ExitCode: 0}, nil
		case "/usr/local/bin/domus-preview":
			containerOutput := request.Command[2]
			logicalOutput := user.Username + "/" + strings.TrimPrefix(containerOutput, "/workspace/")
			if err := repos.Files.Upsert(user.ID, logicalOutput, path.Base(logicalOutput), false, 20, "image/jpeg", "", model.UpsertFileOpts{
				WrappedDEK: "thumbnail-dek", ObjectKey: ".dofs/objects/" + user.ID + "/preview",
			}); err != nil {
				return workspaceRuntime.ExecResult{}, err
			}
			return workspaceRuntime.ExecResult{ExitCode: 0}, nil
		case "/usr/local/bin/domus-transcode":
			containerOutput := request.Command[3]
			logicalOutput := user.Username + "/" + strings.TrimPrefix(containerOutput, "/workspace/")
			if err := repos.Files.Upsert(user.ID, logicalOutput, path.Base(logicalOutput), false, 30, "", "", model.UpsertFileOpts{
				WrappedDEK: "transcode-dek", ObjectKey: ".dofs/objects/" + user.ID + "/transcode",
			}); err != nil {
				return workspaceRuntime.ExecResult{}, err
			}
			return workspaceRuntime.ExecResult{ExitCode: 0}, nil
		case "ffprobe":
			return workspaceRuntime.ExecResult{ExitCode: 0, Stdout: []byte(`{"streams":[{"width":1920,"height":1080}],"format":{"duration":"2.5"}}`)}, nil
		default:
			return workspaceRuntime.ExecResult{ExitCode: 127, Stderr: []byte("unknown command")}, nil
		}
	}
	return handler, repos, fake, user, *source
}

func TestServerPreviewUsesWorkspaceAndPublishesEncryptedThumbnail(t *testing.T) {
	handler, repos, _, user, source := newMediaTestHandler(t)
	handler.enqueueServerPreview(workspaceRuntime.Identity{UserID: user.ID, Username: user.Username}, source)
	deadline := time.Now().Add(2 * time.Second)
	for {
		tasks, _ := repos.Tasks.ListRecent(user.ID)
		if len(tasks) > 0 && tasks[0].Status != "running" {
			if tasks[0].Type != "preview" || tasks[0].Status != "completed" {
				t.Fatalf("preview task = %+v", tasks[0])
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("preview task did not finish")
		}
		time.Sleep(5 * time.Millisecond)
	}
	updated, err := repos.Files.Get(user.ID, source.Path)
	if err != nil {
		t.Fatal(err)
	}
	if updated.ThumbnailKey != ".dofs/objects/"+user.ID+"/preview" || updated.ThumbnailWrappedDEK != "thumbnail-dek" {
		t.Fatalf("thumbnail metadata = %+v", updated)
	}
	if updated.MediaWidth != 1920 || updated.MediaHeight != 1080 || updated.MediaDuration != 2.5 {
		t.Fatalf("media metadata = %dx%d %.2f", updated.MediaWidth, updated.MediaHeight, updated.MediaDuration)
	}
}

func TestServerPreviewDoesNotAttachStaleGeneration(t *testing.T) {
	handler, repos, fake, user, source := newMediaTestHandler(t)
	baseExec := fake.exec
	var once sync.Once
	fake.exec = func(ctx context.Context, request workspaceRuntime.ExecRequest) (workspaceRuntime.ExecResult, error) {
		result, err := baseExec(ctx, request)
		if len(request.Command) > 0 && request.Command[0] == "/usr/local/bin/domus-preview" && err == nil {
			once.Do(func() {
				updated, updateErr := repos.Files.CommitGeneration(
					user.ID, source.ID, source.Generation, ".dofs/objects/"+user.ID+"/new-generation", 128,
				)
				if updateErr != nil || !updated {
					t.Errorf("CommitGeneration() = %v, %v", updated, updateErr)
				}
			})
		}
		return result, err
	}
	handler.enqueueServerPreview(workspaceRuntime.Identity{UserID: user.ID, Username: user.Username}, source)
	deadline := time.Now().Add(2 * time.Second)
	for {
		tasks, _ := repos.Tasks.ListRecent(user.ID)
		if len(tasks) > 0 && tasks[0].Status != "running" {
			if tasks[0].Status != "failed" {
				t.Fatalf("stale preview task = %+v", tasks[0])
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("stale preview task did not finish")
		}
		time.Sleep(5 * time.Millisecond)
	}
	updated, err := repos.Files.Get(user.ID, source.Path)
	if err != nil {
		t.Fatal(err)
	}
	if updated.Generation == source.Generation || updated.ThumbnailKey != "" || updated.ThumbnailWrappedDEK != "" {
		t.Fatalf("stale thumbnail was attached: %+v", updated)
	}
}

func TestTranscodeJobUsesFixedProfileAndFinalizesDOFSRecord(t *testing.T) {
	handler, repos, fake, user, source := newMediaTestHandler(t)
	taskID := "transcode-test"
	ctx, cancel, ok := handler.reserveMediaJob(user.ID, taskID)
	if !ok {
		t.Fatal("reserveMediaJob() rejected first job")
	}
	if err := repos.Tasks.Create(user.ID, taskID, "transcode", "photo.mp3"); err != nil {
		t.Fatal(err)
	}
	outputPath := "alice/home/alice/photo-test.mp3"
	handler.runTranscodeJob(ctx, cancel, taskID, workspaceRuntime.Identity{UserID: user.ID, Username: user.Username}, source, outputPath, transcodeProfiles["audio-mp3"])
	task, err := repos.Tasks.Get(taskID)
	if err != nil || task.Status != "completed" {
		t.Fatalf("transcode task = %+v, %v", task, err)
	}
	output, err := repos.Files.Get(user.ID, outputPath)
	if err != nil || output.ContentType != "audio/mpeg" {
		t.Fatalf("transcode output = %+v, %v", output, err)
	}
	fake.mu.Lock()
	defer fake.mu.Unlock()
	foundFixedProfile := false
	foundNoClobberPublish := false
	for _, command := range fake.commands {
		if len(command) == 4 && command[0] == "/usr/local/bin/domus-transcode" && command[1] == "audio-mp3" &&
			strings.Contains(command[3], "/.user/derived/transcode-"+taskID+".partial") {
			foundFixedProfile = true
		}
		if len(command) == 5 && command[0] == "mv" && command[1] == "--no-clobber" && command[4] == "/workspace/home/alice/photo-test.mp3" {
			foundNoClobberPublish = true
		}
	}
	if !foundFixedProfile || !foundNoClobberPublish {
		t.Fatalf("fixed transcode/publish commands not used: %v", fake.commands)
	}
}

func TestTranscodeHTTPEndpointAcceptsFixedProfile(t *testing.T) {
	handler, repos, _, user, source := newMediaTestHandler(t)
	if err := repos.Files.UpdateContentType(user.ID, source.Path, "video/mp4"); err != nil {
		t.Fatal(err)
	}
	app := fiber.New()
	app.Use(func(c *fiber.Ctx) error {
		c.Locals("session", &model.Session{UserID: user.ID, Username: user.Username, Role: "user"})
		return c.Next()
	})
	app.Post("/file/transcode", handler.handleTranscode)
	request := httptest.NewRequest("POST", "/file/transcode", bytes.NewBufferString(`{"path":"/home/alice/photo.jpg","profile":"video-720p"}`))
	request.Header.Set("Content-Type", "application/json")
	response, err := app.Test(request)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != fiber.StatusAccepted {
		t.Fatalf("status = %d, want %d", response.StatusCode, fiber.StatusAccepted)
	}
	var result struct {
		TaskID     string `json:"task_id"`
		OutputPath string `json:"output_path"`
		Profile    string `json:"profile"`
	}
	if err := json.NewDecoder(response.Body).Decode(&result); err != nil {
		t.Fatal(err)
	}
	if result.TaskID == "" || result.Profile != "video-720p" || !strings.HasPrefix(result.OutputPath, "/home/alice/photo-") || !strings.HasSuffix(result.OutputPath, ".720p.mp4") {
		t.Fatalf("response = %+v", result)
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		task, taskErr := repos.Tasks.Get(result.TaskID)
		if taskErr == nil && task.Status == "completed" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("transcode task did not complete: %+v, %v", task, taskErr)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestLogicalWorkspacePathCannotEscapeUserNamespace(t *testing.T) {
	identity := workspaceRuntime.Identity{UserID: "id", Username: "alice"}
	if got, err := logicalWorkspacePath(identity, "alice/home/alice/file.txt"); err != nil || got != "/workspace/home/alice/file.txt" {
		t.Fatalf("logicalWorkspacePath() = %q, %v", got, err)
	}
	if _, err := logicalWorkspacePath(identity, "bob/home/bob/file.txt"); err == nil {
		t.Fatal("cross-user logical path was accepted")
	}
}

func TestPreviewFailureLeavesOriginalReady(t *testing.T) {
	handler, repos, fake, user, source := newMediaTestHandler(t)
	fake.exec = func(_ context.Context, request workspaceRuntime.ExecRequest) (workspaceRuntime.ExecResult, error) {
		if len(request.Command) > 0 && request.Command[0] == "/usr/local/bin/domus-preview" {
			return workspaceRuntime.ExecResult{ExitCode: 1, Stderr: []byte("unsupported")}, nil
		}
		return workspaceRuntime.ExecResult{ExitCode: 0}, nil
	}
	handler.enqueueServerPreview(workspaceRuntime.Identity{UserID: user.ID, Username: user.Username}, source)
	deadline := time.Now().Add(2 * time.Second)
	for {
		tasks, _ := repos.Tasks.ListRecent(user.ID)
		if len(tasks) > 0 && tasks[0].Status == "failed" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("failed preview task did not finish")
		}
		time.Sleep(5 * time.Millisecond)
	}
	original, err := repos.Files.Get(user.ID, source.Path)
	if err != nil || original.Status != "ready" || original.ThumbnailKey != "" {
		t.Fatalf("original changed after preview failure: %+v, %v", original, err)
	}
}

func TestPreviewCleansOutputWhenDOFSMetadataIsIncomplete(t *testing.T) {
	handler, repos, fake, user, source := newMediaTestHandler(t)
	baseExec := fake.exec
	derivedPath := parentDirOf(source.Path) + ".user/derived/" + strconv.FormatInt(source.ID, 10) + "-g" + strconv.FormatInt(source.Generation, 10) + ".preview.jpg"
	fake.exec = func(ctx context.Context, request workspaceRuntime.ExecRequest) (workspaceRuntime.ExecResult, error) {
		if len(request.Command) > 0 && request.Command[0] == "/usr/local/bin/domus-preview" {
			containerOutput := request.Command[2]
			logicalOutput := user.Username + "/" + strings.TrimPrefix(containerOutput, "/workspace/")
			if err := repos.Files.Upsert(user.ID, logicalOutput, path.Base(logicalOutput), false, 20, "image/jpeg", ""); err != nil {
				return workspaceRuntime.ExecResult{}, err
			}
			return workspaceRuntime.ExecResult{ExitCode: 0}, nil
		}
		if len(request.Command) == 3 && request.Command[0] == "rm" && request.Command[1] == "-f" {
			logicalOutput := user.Username + "/" + strings.TrimPrefix(request.Command[2], "/workspace/")
			if err := repos.Files.Delete(user.ID, logicalOutput); err != nil {
				return workspaceRuntime.ExecResult{}, err
			}
			return workspaceRuntime.ExecResult{ExitCode: 0}, nil
		}
		return baseExec(ctx, request)
	}
	handler.enqueueServerPreview(workspaceRuntime.Identity{UserID: user.ID, Username: user.Username}, source)
	deadline := time.Now().Add(2 * time.Second)
	for {
		tasks, _ := repos.Tasks.ListRecent(user.ID)
		if len(tasks) > 0 && tasks[0].Status == "failed" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("incomplete preview task did not fail")
		}
		time.Sleep(5 * time.Millisecond)
	}
	if _, err := repos.Files.Get(user.ID, derivedPath); !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("incomplete derived output was not removed: %v", err)
	}
}

func TestMediaJobsReserveTerminalCapacity(t *testing.T) {
	handler, _, _, user, _ := newMediaTestHandler(t)
	handler.Config.Workspace.MaxSessionsPerUser = 1
	if ctx, cancel, ok := handler.reserveMediaJob(user.ID, "no-capacity"); ok {
		cancel()
		handler.releaseMediaJob(user.ID, "no-capacity")
		t.Fatalf("reserveMediaJob() = %v, want false when only the terminal slot remains", ctx)
	}
}

func TestShutdownMediaJobsCancelsAndWaits(t *testing.T) {
	handler, repos, fake, user, source := newMediaTestHandler(t)
	started := make(chan struct{})
	var once sync.Once
	fake.exec = func(ctx context.Context, request workspaceRuntime.ExecRequest) (workspaceRuntime.ExecResult, error) {
		if len(request.Command) > 0 && request.Command[0] == "/usr/local/bin/domus-preview" {
			once.Do(func() { close(started) })
			<-ctx.Done()
			return workspaceRuntime.ExecResult{}, ctx.Err()
		}
		return workspaceRuntime.ExecResult{ExitCode: 0}, nil
	}
	handler.enqueueServerPreview(workspaceRuntime.Identity{UserID: user.ID, Username: user.Username}, source)
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("preview job did not start")
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := handler.ShutdownMediaJobs(shutdownCtx); err != nil {
		t.Fatalf("ShutdownMediaJobs() error = %v", err)
	}
	tasks, err := repos.Tasks.ListRecent(user.ID)
	if err != nil || len(tasks) != 1 || tasks[0].Status != "cancelled" {
		t.Fatalf("tasks after shutdown = %+v, %v", tasks, err)
	}
	if _, _, ok := handler.reserveMediaJob(user.ID, "after-shutdown"); ok {
		t.Fatal("media job was accepted after shutdown")
	}
}
