//go:build linux

package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/google/uuid"
	dofscore "github.com/willvar/dofs"

	"domus/config"
	"domus/internal/auth"
	"domus/internal/dofs"
	"domus/internal/dofsbridge"
	"domus/internal/fileview"
	"domus/internal/model"
	"domus/internal/worker"
	"domus/shared/logger"
)

// workerCommand runs the optional content worker. It consumes DOFS FUSE
// mounts as ordinary filesystem paths, so it never handles encryption keys.
func workerCommand(configPath string, args []string) {
	cfg := loadConfig(configPath)
	if err := cfg.ValidateWorker(); err != nil {
		logger.Fatal("Invalid worker configuration: %v", err)
	}
	if err := logger.InitFromConfig(&cfg.Log); err != nil {
		logger.Fatal("Failed to initialize logger: %v", err)
	}

	once := hasFlag(args, "--once")
	userFilter := getFlagValue(args, "--user-id")
	limit := 0
	if raw := getFlagValue(args, "--limit"); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil || value < 0 {
			logger.Fatal("Invalid --limit value: %s", raw)
		}
		limit = value
	}
	interval := time.Duration(cfg.Worker.IntervalSeconds) * time.Second
	if raw := getFlagValue(args, "--interval"); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil || value < 0 {
			logger.Fatal("Invalid --interval value: %s", raw)
		}
		interval = time.Duration(value) * time.Second
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	serverKey, err := auth.ServerKeyFromSecret(cfg.Server.EncryptionSecret)
	if err != nil {
		logger.Fatal("Failed to load server encryption key: %v", err)
	}
	defer zeroSecret(serverKey)

	db, err := model.InitDB(cfg.Database)
	if err != nil {
		logger.Fatal("Failed to initialize database: %v", err)
	}
	repos := model.NewRepos(db, nil)
	runtime, err := dofsbridge.Open(ctx, db, cfg, serverKey, repos.Users)
	if err != nil {
		logger.Fatal("Failed to initialize DOFS runtime: %v", err)
	}
	defer runtime.Close()
	if err := runtime.Check(ctx); err != nil {
		logger.Fatal("DOFS runtime is unavailable: %v", err)
	}
	if err := runtime.EnsureAllUsers(ctx); err != nil {
		logger.Fatal("Failed to initialize DOFS namespaces: %v", err)
	}
	fileSystem, err := fileview.OpenExisting(db, runtime)
	if err != nil {
		logger.Fatal("Failed to initialize Domus file projection: %v", err)
	}

	// Transcode jobs from a previous worker process may be stuck mid-run.
	// Requeue them before the first pass so nothing is stranded as running.
	if stale, err := repos.Tasks.RequeueStaleTranscodes(); err != nil {
		logger.Info("worker: requeue stale transcodes failed: %v", err)
	} else if len(stale) > 0 {
		for _, task := range stale {
			_ = fileSystem.ResetRenditionsForTasks(task.UserID, []string{task.TaskID})
		}
		logger.Info("worker: requeued %d stale transcode task(s)", len(stale))
	}

	workDir := cfg.Worker.WorkDir
	if workDir == "" {
		workDir = filepath.Join(cfg.DOFS.StateRoot, "worker")
	}
	if err := os.MkdirAll(workDir, 0700); err != nil {
		logger.Fatal("Failed to create worker scratch directory: %v", err)
	}

	runner := &workerRunner{
		cfg: cfg, repos: repos, files: fileSystem,
		control: dofs.ControlClient{
			SocketPath: cfg.DOFS.ControlSocket,
			Timeout:    time.Duration(cfg.DOFS.MountTimeoutSeconds+10) * time.Second,
		},
		workDir: workDir, userFilter: strings.TrimSpace(userFilter), limit: limit,
	}

	runPass := func() {
		processed, failed, err := runner.pass(ctx)
		if err != nil {
			logger.Info("worker: pass finished with error: %v (processed=%d failed=%d)", err, processed, failed)
			return
		}
		logger.Info("worker: pass finished (processed=%d failed=%d)", processed, failed)
	}

	if once || interval <= 0 {
		runPass()
		return
	}
	logger.Info("worker: starting, interval %s", interval)
	runPass()
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			logger.Info("worker: stopped")
			return
		case <-ticker.C:
			runPass()
		}
	}
}

type workerRunner struct {
	cfg        *config.Config
	repos      *model.Repos
	files      *fileview.Repo
	control    dofs.ControlClient
	workDir    string
	userFilter string
	limit      int
}

type workerProbeBatch struct {
	userID, username, mountPoint string
	metadata, codecs             []model.FileRecord
}

func (w *workerRunner) pass(ctx context.Context) (processed, failed int, err error) {
	users, err := w.repos.Users.List()
	if err != nil {
		return 0, 0, fmt.Errorf("list users: %w", err)
	}
	var probes []workerProbeBatch
	for _, user := range users {
		if ctx.Err() != nil {
			return processed, failed, ctx.Err()
		}
		if w.userFilter != "" && user.ID != w.userFilter && user.Username != w.userFilter {
			continue
		}
		done, failures, userErr := w.processUser(ctx, user.ID, user.Username, &probes)
		processed += done
		failed += failures
		if userErr != nil {
			logger.Info("worker: user %s failed: %v", user.Username, userErr)
		}
		if w.limit > 0 && processed+failed >= w.limit {
			return processed, failed, nil
		}
	}
	return w.processProbes(ctx, probes, processed, failed)
}

func (w *workerRunner) processUser(ctx context.Context, userID, username string, probes *[]workerProbeBatch) (processed, failed int, err error) {
	status, err := w.control.Ensure(ctx, dofs.MountUserSelector{UserID: userID})
	if err != nil {
		return 0, 0, fmt.Errorf("ensure mount: %w", err)
	}
	if status.State != "mounted" {
		return 0, 0, fmt.Errorf("mount state %q", status.State)
	}
	mountPoint := filepath.Join(w.cfg.DOFS.MountRoot, userID)

	transcodeDone, transcodeFailed := w.processUserTranscodes(ctx, userID, username, mountPoint)
	processed += transcodeDone
	failed += transcodeFailed

	var thumbnailCandidates, codecCandidates, metaCandidates []model.FileRecord
	if err := w.collectCandidates(ctx, userID, "/", &thumbnailCandidates, &codecCandidates, &metaCandidates); err != nil {
		return processed, failed, err
	}

	for _, record := range thumbnailCandidates {
		if ctx.Err() != nil {
			return processed, failed, ctx.Err()
		}
		if w.limit > 0 && processed+failed >= w.limit {
			break
		}
		if err := w.processFile(ctx, userID, mountPoint, record); err != nil {
			failed++
			logger.Info("worker: thumbnail for %s failed: %v", record.Path, err)
			continue
		}
		processed++
		logger.Info("worker: thumbnail for %s ready (%s)", record.Path, username)
	}

	if len(metaCandidates) > 0 || len(codecCandidates) > 0 {
		*probes = append(*probes, workerProbeBatch{
			userID: userID, username: username, mountPoint: mountPoint,
			metadata: metaCandidates, codecs: codecCandidates,
		})
	}

	return processed, failed, nil
}

// Optional probes share the remaining pass budget only after every selected
// user's thumbnails have had priority. Keep the collected records, not a
// second namespace walk, so an unprobeable file cannot starve another user.
func (w *workerRunner) processProbes(ctx context.Context, batches []workerProbeBatch, processed, failed int) (int, int, error) {
	for _, batch := range batches {
		for _, record := range batch.metadata {
			if ctx.Err() != nil {
				return processed, failed, ctx.Err()
			}
			if w.limit > 0 && processed+failed >= w.limit {
				return processed, failed, nil
			}
			if err := w.probeFileMediaMeta(ctx, batch.userID, batch.mountPoint, record); err != nil {
				failed++
				logger.Info("worker: media meta probe for %s failed: %v", record.Path, err)
				continue
			}
			processed++
			logger.Info("worker: media meta for %s ready (%s)", record.Path, batch.username)
		}
		for _, record := range batch.codecs {
			if ctx.Err() != nil {
				return processed, failed, ctx.Err()
			}
			if w.limit > 0 && processed+failed >= w.limit {
				return processed, failed, nil
			}
			if err := w.probeFileCodecs(ctx, batch.userID, batch.mountPoint, record); err != nil {
				failed++
				logger.Info("worker: codec probe for %s failed: %v", record.Path, err)
				continue
			}
			processed++
			logger.Info("worker: codec probe for %s ready (%s)", record.Path, batch.username)
		}
	}
	return processed, failed, nil
}

func (w *workerRunner) collectCandidates(ctx context.Context, userID, parentPath string, thumbnailOut, codecOut, metaOut *[]model.FileRecord) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	children, err := w.files.ListDirectChildren(userID, parentPath)
	if err != nil {
		if errors.Is(err, dofscore.ErrNotFound) {
			return nil
		}
		return fmt.Errorf("list %s: %w", parentPath, err)
	}
	for _, child := range children {
		if child.IsDir {
			if child.Path == "/.domus/" || strings.HasPrefix(child.Path, "/.domus/") {
				continue
			}
			if err := w.collectCandidates(ctx, userID, child.Path, thumbnailOut, codecOut, metaOut); err != nil {
				return err
			}
			continue
		}
		if child.Status != "ready" {
			continue
		}
		// One walk serves all sweeps: thumbnails for image/video files that
		// lack one, codec strings for video files, and ffprobe metadata
		// summaries for any media file (video or audio) that lacks one.
		if child.ThumbnailKey == "" && isThumbnailMedia(child.ContentType) {
			*thumbnailOut = append(*thumbnailOut, child)
		}
		if child.MediaCodecs == "" && strings.HasPrefix(child.ContentType, "video/") {
			*codecOut = append(*codecOut, child)
		}
		if child.MediaMeta == "" && isProbeableMedia(child.ContentType) {
			*metaOut = append(*metaOut, child)
		}
	}
	return nil
}

// isProbeableMedia reports whether the content type benefits from an ffprobe
// metadata summary (container properties and recording tags).
func isProbeableMedia(contentType string) bool {
	return strings.HasPrefix(contentType, "video/") || strings.HasPrefix(contentType, "audio/")
}

func (w *workerRunner) processFile(ctx context.Context, userID, mountPoint string, record model.FileRecord) error {
	sourcePath := filepath.Join(mountPoint, filepath.FromSlash(strings.TrimPrefix(record.Path, "/")))
	if _, err := os.Stat(sourcePath); err != nil {
		return fmt.Errorf("source is not mounted: %w", err)
	}

	name := "thumb_" + uuid.NewString() + ".webp"
	scratchPath := filepath.Join(w.workDir, name)
	defer func() { _ = os.Remove(scratchPath) }()

	options := worker.ThumbnailOptions{
		FFmpegPath:   w.cfg.Worker.FFmpegPath,
		FFprobePath:  w.cfg.Worker.FFprobePath,
		MaxDimension: w.cfg.Worker.ThumbMaxDimension,
	}
	info, err := worker.GenerateThumbnail(ctx, options, sourcePath, scratchPath)
	if err != nil {
		return err
	}

	thumbnailData, err := os.ReadFile(scratchPath)
	if err != nil {
		return fmt.Errorf("read generated thumbnail: %w", err)
	}
	thumbnailMountPath := filepath.Join(mountPoint, ".domus", "thumbnails", name)
	if err := os.WriteFile(thumbnailMountPath, thumbnailData, 0600); err != nil {
		return fmt.Errorf("publish thumbnail into namespace: %w", err)
	}
	cleanupThumbnail := func() { _ = os.Remove(thumbnailMountPath) }

	namespacePath := "/.domus/thumbnails/" + name
	var thumbnailRecord *model.FileRecord
	for attempt := 0; attempt < 10; attempt++ {
		thumbnailRecord, err = w.files.Get(userID, namespacePath)
		if err == nil {
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	if err != nil {
		cleanupThumbnail()
		return fmt.Errorf("resolve published thumbnail: %w", err)
	}

	applied, err := w.files.UpdateThumbnailIfGeneration(
		userID, record.Path, record.ID, record.Generation,
		thumbnailRecord.StorageKey(), thumbnailRecord.WrappedDEK,
		info.Width, info.Height, info.Duration,
	)
	if err != nil {
		cleanupThumbnail()
		return fmt.Errorf("register thumbnail: %w", err)
	}
	if !applied {
		cleanupThumbnail()
		return nil
	}
	return nil
}

func isThumbnailMedia(contentType string) bool {
	return strings.HasPrefix(contentType, "image/") || strings.HasPrefix(contentType, "video/")
}

// ---------------------------------------------------------------------------
// Source media codec probing
// ---------------------------------------------------------------------------

// probeFileCodecs fills in the MSE codec string of one video source via the
// FUSE mount. Files are probed once: the stored media_codecs value excludes
// them from later sweeps.
func (w *workerRunner) probeFileCodecs(ctx context.Context, userID, mountPoint string, record model.FileRecord) error {
	sourcePath := filepath.Join(mountPoint, filepath.FromSlash(strings.TrimPrefix(record.Path, "/")))
	if _, err := os.Stat(sourcePath); err != nil {
		return fmt.Errorf("source is not mounted: %w", err)
	}
	codecs, err := worker.ProbeSourceCodecs(ctx, w.cfg.Worker.FFprobePath, sourcePath)
	if err != nil {
		return err
	}
	return w.files.UpdateMediaCodecs(userID, record.Path, uint64(record.ID), record.Generation, codecs)
}

// probeFileMediaMeta fills in the ffprobe metadata summary of one media
// source via the FUSE mount. Files are probed once: the stored media_meta
// value excludes them from later sweeps.
func (w *workerRunner) probeFileMediaMeta(ctx context.Context, userID, mountPoint string, record model.FileRecord) error {
	sourcePath := filepath.Join(mountPoint, filepath.FromSlash(strings.TrimPrefix(record.Path, "/")))
	if _, err := os.Stat(sourcePath); err != nil {
		return fmt.Errorf("source is not mounted: %w", err)
	}
	meta, err := worker.ProbeMediaMeta(ctx, w.cfg.Worker.FFprobePath, sourcePath)
	if err != nil {
		return err
	}
	if meta.IsEmpty() {
		return fmt.Errorf("worker: media meta is empty for %s", record.Path)
	}
	encoded, err := json.Marshal(meta)
	if err != nil {
		return err
	}
	return w.files.UpdateMediaMeta(userID, record.Path, uint64(record.ID), record.Generation, string(encoded))
}

// ---------------------------------------------------------------------------
// Transcode jobs
// ---------------------------------------------------------------------------

// processUserTranscodes claims and runs queued transcode tasks for one user.
// It stops claiming when the per-pass limit is reached; claimed-but-unrun
// tasks are recovered by the startup requeue on the next worker start.
func (w *workerRunner) processUserTranscodes(ctx context.Context, userID, username, mountPoint string) (processed, failed int) {
	for {
		if ctx.Err() != nil {
			return processed, failed
		}
		task, err := w.repos.Tasks.ClaimNextTranscode(userID)
		if err != nil {
			return processed, failed
		}
		if w.limit > 0 && processed+failed >= w.limit {
			return processed, failed
		}
		if err := w.processTranscode(ctx, userID, mountPoint, task); err != nil {
			failed++
			logger.Info("worker: transcode for %s failed: %v", task.SourcePath, err)
			continue
		}
		processed++
		logger.Info("worker: transcode for %s ready (%s)", task.SourcePath, username)
	}
}

// processTranscode runs one ffmpeg transcode for a claimed task. Artifacts
// are published incrementally (init segment, then each completed media
// segment) so playback can begin while the job is still running.
func (w *workerRunner) processTranscode(ctx context.Context, userID, mountPoint string, task *model.Task) error {
	if task.SourceInode <= 0 {
		return w.failTranscode(userID, task, 0, "transcode task has no source inode")
	}
	profile, ok := worker.FindTranscodeProfile(task.Profile)
	if !ok {
		return w.failTranscode(userID, task, task.SourceInode, "unknown transcode profile "+task.Profile)
	}
	rendition, err := w.files.GetRendition(userID, uint64(task.SourceInode), profile.ID)
	if err != nil {
		return w.failTranscode(userID, task, task.SourceInode, "rendition row is missing")
	}
	record, err := w.files.GetByID(userID, task.SourceInode)
	if err != nil {
		return w.failTranscode(userID, task, task.SourceInode, "source file is unavailable")
	}
	if record.Generation != rendition.SourceGeneration {
		return w.failTranscode(userID, task, task.SourceInode, "source file changed since the transcode was requested")
	}

	// A previous run of this task may have left partial artifacts behind.
	_ = w.files.DeleteRendition(userID, uint64(task.SourceInode), profile.ID)
	if _, err := w.files.CreateRendition(userID, record, profile.ID); err != nil {
		return w.failTranscode(userID, task, task.SourceInode, "rendition row could not be reset")
	}
	_ = w.files.UpdateRenditionStatus(userID, uint64(task.SourceInode), profile.ID, "running", "")
	_ = w.repos.Tasks.UpdateProgress(task.TaskID, 0, "transcoding")

	sourcePath := filepath.Join(mountPoint, filepath.FromSlash(strings.TrimPrefix(record.Path, "/")))
	info, err := worker.Probe(ctx, w.cfg.Worker.FFprobePath, sourcePath)
	if err != nil {
		return w.failTranscode(userID, task, task.SourceInode, "probe failed: "+err.Error())
	}

	scratchDir := filepath.Join(w.workDir, "transcode-"+task.TaskID)
	defer func() { _ = os.RemoveAll(scratchDir) }()
	run, err := worker.StartTranscode(ctx, worker.TranscodeOptions{
		FFmpegPath:  w.cfg.Worker.FFmpegPath,
		FFprobePath: w.cfg.Worker.FFprobePath,
		Profile:     profile,
	}, sourcePath, scratchDir)
	if err != nil {
		return w.failTranscode(userID, task, task.SourceInode, err.Error())
	}

	publisher := &transcodePublisher{
		runner: w, userID: userID, mountPoint: mountPoint,
		sourceInode: uint64(task.SourceInode), profile: profile.ID, scratchDir: scratchDir,
	}
	published := 0
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	exited := false
	for !exited {
		select {
		case <-ctx.Done():
			run.Cancel()
			_ = run.Wait()
			_ = w.files.ResetRendition(userID, uint64(task.SourceInode), profile.ID)
			_ = w.repos.Tasks.UpdateStatus(task.TaskID, "queued")
			return ctx.Err()
		case <-run.Done():
			exited = true
		case <-ticker.C:
		}
		status, statusErr := w.files.RenditionStatus(userID, uint64(task.SourceInode), profile.ID)
		if statusErr == nil && status == "cancelling" {
			run.Cancel()
		}
		if !publisher.initPublished {
			publisher.publishInit(ctx)
		}
		count, publishErr := publisher.publishPending(ctx)
		published += count
		if publishErr != nil {
			run.Cancel()
			_ = run.Wait()
			return w.failTranscode(userID, task, task.SourceInode, publishErr.Error())
		}
		publisher.updateProgress(task, info.Duration)
	}
	// The process has exited: publish the init segment (if it appeared after
	// the last tick) and any final playlist entries.
	if !publisher.initPublished {
		publisher.publishInit(ctx)
	}
	if _, publishErr := publisher.publishPending(ctx); publishErr != nil {
		return w.failTranscode(userID, task, task.SourceInode, publishErr.Error())
	}
	waitErr := run.Wait()
	status, _ := w.files.RenditionStatus(userID, uint64(task.SourceInode), profile.ID)
	if status == "cancelling" {
		_ = w.files.DeleteRendition(userID, uint64(task.SourceInode), profile.ID)
		return w.repos.Tasks.UpdateStatus(task.TaskID, "cancelled")
	}
	if waitErr != nil {
		return w.failTranscode(userID, task, task.SourceInode, "ffmpeg failed"+worker.CommandStderr(waitErr))
	}
	if published == 0 {
		return w.failTranscode(userID, task, task.SourceInode, "transcode produced no segments")
	}
	if !publisher.initPublished {
		return w.failTranscode(userID, task, task.SourceInode, "transcode never published an init segment")
	}
	_ = w.files.UpdateRenditionStatus(userID, uint64(task.SourceInode), profile.ID, "ready", "")
	return w.repos.Tasks.UpdateStatus(task.TaskID, "completed")
}

// failTranscode marks the task and its rendition failed.
func (w *workerRunner) failTranscode(userID string, task *model.Task, sourceInode int64, message string) error {
	_ = w.repos.Tasks.UpdateStatus(task.TaskID, "failed")
	if sourceInode > 0 {
		_ = w.files.FailRendition(userID, uint64(sourceInode), task.Profile, message)
	}
	return fmt.Errorf("%s", message)
}

// transcodePublisher publishes completed fMP4 artifacts into the user's
// namespace and appends them to the rendition manifest.
type transcodePublisher struct {
	runner      *workerRunner
	userID      string
	mountPoint  string
	sourceInode uint64
	profile     string
	scratchDir  string

	initPublished bool
	initPath      string
	published     int
	totalDuration float64
}

// verifyArtifact reads the file back and confirms it carries the full payload.
func verifyArtifact(path string, want int) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if len(data) != want {
		return fmt.Errorf("artifact writeback lost content: wrote %d bytes, read back %d", want, len(data))
	}
	return nil
}

// publishInit writes the fMP4 init segment once it exists and records the
// MSE codecs string probed from it.
func (p *transcodePublisher) publishInit(ctx context.Context) {
	sourcePath := filepath.Join(p.scratchDir, "init.mp4")
	data, err := os.ReadFile(sourcePath)
	if err != nil {
		return
	}
	if len(data) == 0 {
		// ffmpeg may create the file a moment before flushing the moov; retry
		// on the next tick rather than publishing an empty init segment.
		return
	}
	codecs, width, height, err := worker.ProbeTranscodeCodecs(ctx, p.runner.cfg.Worker.FFprobePath, sourcePath)
	if err != nil {
		logger.Info("worker: probe init segment: %v", err)
		return
	}
	mountPath, inode, err := p.writeArtifact(ctx, "init.mp4", data)
	if err != nil {
		logger.Info("worker: publish init segment: %v", err)
		return
	}
	if err := p.runner.files.SetRenditionInit(p.userID, p.sourceInode, p.profile, codecs, width, height, inode); err != nil {
		logger.Info("worker: register init segment: %v", err)
		_ = os.Remove(mountPath)
		return
	}
	p.initPublished = true
	p.initPath = mountPath
}

// publishPending publishes playlist entries that appeared since the last
// call. It returns the count of newly published segments.
func (p *transcodePublisher) publishPending(ctx context.Context) (int, error) {
	if !p.initPublished {
		return 0, nil
	}
	content, err := os.ReadFile(filepath.Join(p.scratchDir, "index.m3u8"))
	if err != nil {
		return 0, nil // the playlist appears shortly after the job starts
	}
	segments := worker.ParseTranscodePlaylist(string(content))
	publishedCount := 0
	for index := p.published; index < len(segments); index++ {
		segment := segments[index]
		data, err := os.ReadFile(filepath.Join(p.scratchDir, filepath.Base(segment.URI)))
		if err != nil {
			return publishedCount, fmt.Errorf("read segment %d: %w", index, err)
		}
		_, inode, err := p.writeArtifact(ctx, fmt.Sprintf("seg%03d.m4s", index), data)
		if err != nil {
			return publishedCount, err
		}
		if err := p.runner.files.AppendRenditionSegment(p.userID, p.sourceInode, p.profile, inode, segment.Duration); err != nil {
			return publishedCount, err
		}
		p.totalDuration += segment.Duration
		p.published = index + 1
		publishedCount++
	}
	return publishedCount, nil
}

// writeArtifact writes one derived artifact into the namespace through the
// FUSE mount and resolves its inode. FUSE writes can fail transiently while
// the writeback of a previously released file is still committing, so the
// write is retried with a short backoff.
func (p *transcodePublisher) writeArtifact(ctx context.Context, name string, data []byte) (mountPath string, inode uint64, err error) {
	namespacePath := fmt.Sprintf("/.domus/renditions/%d/%s/%s", p.sourceInode, p.profile, name)
	mountPath = filepath.Join(p.mountPoint, filepath.FromSlash(strings.TrimPrefix(namespacePath, "/")))
	if err := os.MkdirAll(filepath.Dir(mountPath), 0700); err != nil {
		return "", 0, fmt.Errorf("create rendition directory: %w", err)
	}
	for attempt := 0; ; attempt++ {
		err = os.WriteFile(mountPath, data, 0600)
		if err == nil {
			// Read back to make sure the content survived the FUSE writeback;
			// a lost write leaves a zero-length or truncated object behind.
			err = verifyArtifact(mountPath, len(data))
		}
		if err == nil || attempt >= 5 {
			break
		}
		time.Sleep(time.Duration(200*(attempt+1)) * time.Millisecond)
	}
	if err != nil {
		return "", 0, fmt.Errorf("publish rendition artifact: %w", err)
	}
	var record *model.FileRecord
	for attempt := 0; attempt < 10; attempt++ {
		record, err = p.runner.files.Get(p.userID, namespacePath)
		if err == nil {
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	if err != nil {
		_ = os.Remove(mountPath)
		return "", 0, fmt.Errorf("resolve published artifact: %w", err)
	}
	return mountPath, uint64(record.ID), nil
}

// updateProgress reports published playback time as task progress.
func (p *transcodePublisher) updateProgress(task *model.Task, sourceDuration float64) {
	if sourceDuration <= 0 || p.totalDuration <= 0 {
		return
	}
	progress := p.totalDuration / sourceDuration
	if progress > 0.99 {
		progress = 0.99
	}
	_ = p.runner.repos.Tasks.UpdateProgress(task.TaskID, progress, "transcoding")
}
