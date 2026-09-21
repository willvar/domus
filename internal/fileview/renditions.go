package fileview

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/willvar/dofs"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"domus/internal/model"
)

// renditions manage server-transcoded playback derivatives. Rows reference
// source inode + generation like the thumbnail column does; object keys and
// wrapped DEKs are resolved from DOFS on demand.

func (r *Repo) CreateRendition(userID string, source *model.FileRecord, profile string) (*Rendition, error) {
	if source == nil || source.IsDir || source.Status != "ready" {
		return nil, gorm.ErrRecordNotFound
	}
	row := renditionRecord{
		UserID: userID, SourceInode: uint64(source.ID), Profile: profile,
		SourceGeneration: source.Generation, Status: "queued", Segments: "[]",
	}
	if err := r.db.Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "user_id"}, {Name: "source_inode"}, {Name: "profile"}},
		DoUpdates: clause.Assignments(map[string]any{
			"source_generation": row.SourceGeneration, "status": "queued", "task_id": "",
			"init_inode": 0, "codecs": "", "media_width": 0, "media_height": 0,
			"segments": "[]", "published_duration": 0, "error": "",
			"updated_at": row.UpdatedAt,
		}),
	}).Create(&row).Error; err != nil {
		return nil, err
	}
	return r.GetRendition(userID, uint64(source.ID), profile)
}

func (r *Repo) GetRendition(userID string, sourceInode uint64, profile string) (*Rendition, error) {
	var row renditionRecord
	if err := r.db.Where("user_id = ? AND source_inode = ? AND profile = ?", userID, sourceInode, profile).
		First(&row).Error; err != nil {
		return nil, err
	}
	return r.renditionFromRow(userID, row)
}

func (r *Repo) ListRenditions(userID string, sourceInode uint64) ([]Rendition, error) {
	var rows []renditionRecord
	if err := r.db.Where("user_id = ? AND source_inode = ?", userID, sourceInode).
		Order("created_at ASC").Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]Rendition, 0, len(rows))
	for _, row := range rows {
		rendition, err := r.renditionFromRow(userID, row)
		if err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				continue
			}
			return nil, err
		}
		out = append(out, *rendition)
	}
	return out, nil
}

func (r *Repo) renditionFromRow(userID string, row renditionRecord) (*Rendition, error) {
	rendition := &Rendition{
		SourceInode: int64(row.SourceInode), Profile: row.Profile, Status: row.Status,
		TaskID: row.TaskID, Codecs: row.Codecs, MediaWidth: row.MediaWidth,
		MediaHeight: row.MediaHeight, Error: row.Error, SourceGeneration: row.SourceGeneration,
	}
	if row.InitInode != 0 {
		artifact, err := r.renditionArtifact(userID, row.InitInode, 0)
		if err != nil {
			return nil, err
		}
		rendition.Init = artifact
	}
	var segments []renditionSegment
	if err := json.Unmarshal([]byte(row.Segments), &segments); err != nil {
		return nil, fmt.Errorf("decode rendition segments: %w", err)
	}
	rendition.Segments = make([]RenditionArtifact, 0, len(segments))
	for _, segment := range segments {
		artifact, err := r.renditionArtifact(userID, segment.Inode, segment.Duration)
		if err != nil {
			return nil, err
		}
		rendition.Segments = append(rendition.Segments, *artifact)
	}
	return rendition, nil
}

// renditionArtifact resolves a stored inode into the object reference the
// browser needs. Segments removed from DOFS make the whole rendition
// unreadable; surface that as not-found instead of a partial manifest.
func (r *Repo) renditionArtifact(userID string, inode uint64, duration float64) (*RenditionArtifact, error) {
	ctx, cancel := operationContext()
	defer cancel()
	node, err := r.runtime.Metadata.GetNode(ctx, userID, inode)
	if err != nil {
		return nil, mapNotFound(err)
	}
	if node.State != dofs.NodeStateReady || !node.IsFile() {
		return nil, gorm.ErrRecordNotFound
	}
	return &RenditionArtifact{
		Inode: inode, ObjectKey: node.ObjectKey,
		WrappedDEK: hex.EncodeToString(node.WrappedDEK), Size: node.Size, Duration: duration,
	}, nil
}

func (r *Repo) SetRenditionInit(userID string, sourceInode uint64, profile, codecs string, width, height int, initInode uint64) error {
	return r.db.Model(&renditionRecord{}).
		Where("user_id = ? AND source_inode = ? AND profile = ?", userID, sourceInode, profile).
		Updates(map[string]any{
			"init_inode": initInode, "codecs": codecs, "media_width": width, "media_height": height,
			"updated_at": time.Now(),
		}).Error
}

func (r *Repo) AppendRenditionSegment(userID string, sourceInode uint64, profile string, segmentInode uint64, duration float64) error {
	return r.db.Transaction(func(tx *gorm.DB) error {
		var row renditionRecord
		if err := tx.Where("user_id = ? AND source_inode = ? AND profile = ?", userID, sourceInode, profile).
			First(&row).Error; err != nil {
			return err
		}
		var segments []renditionSegment
		if err := json.Unmarshal([]byte(row.Segments), &segments); err != nil {
			return fmt.Errorf("decode rendition segments: %w", err)
		}
		segments = append(segments, renditionSegment{Inode: segmentInode, Duration: duration})
		encoded, err := json.Marshal(segments)
		if err != nil {
			return err
		}
		return tx.Model(&renditionRecord{}).
			Where("user_id = ? AND source_inode = ? AND profile = ?", userID, sourceInode, profile).
			Updates(map[string]any{
				"segments": string(encoded), "published_duration": row.PublishedDuration + duration,
				"updated_at": time.Now(),
			}).Error
	})
}

func (r *Repo) UpdateRenditionStatus(userID string, sourceInode uint64, profile, status, taskError string) error {
	return r.db.Model(&renditionRecord{}).
		Where("user_id = ? AND source_inode = ? AND profile = ?", userID, sourceInode, profile).
		Updates(map[string]any{"status": status, "error": taskError, "updated_at": time.Now()}).Error
}

// FailRendition marks a rendition failed and discards any partially published
// derived nodes so a later retry starts from a clean manifest.
func (r *Repo) FailRendition(userID string, sourceInode uint64, profile, taskError string) error {
	var row renditionRecord
	if err := r.db.Where("user_id = ? AND source_inode = ? AND profile = ?", userID, sourceInode, profile).
		First(&row).Error; err != nil {
		return err
	}
	r.removeRenditionArtifacts(userID, row)
	return r.db.Model(&renditionRecord{}).
		Where("user_id = ? AND source_inode = ? AND profile = ?", userID, sourceInode, profile).
		Updates(map[string]any{
			"status": "failed", "error": taskError, "segments": "[]", "published_duration": 0,
			"init_inode": 0, "updated_at": time.Now(),
		}).Error
}

// ResetRenditionsForTasks reclaims rendition rows left behind by interrupted
// worker runs: rows still transient (queued/running/cancelling) for the given
// task IDs lose their partial artifacts and return to queued state.
func (r *Repo) ResetRenditionsForTasks(userID string, taskIDs []string) error {
	if len(taskIDs) == 0 {
		return nil
	}
	var rows []renditionRecord
	if err := r.db.Where("user_id = ? AND task_id IN ? AND status IN ?", userID, taskIDs,
		[]string{"queued", "running", "cancelling"}).Find(&rows).Error; err != nil {
		return err
	}
	for _, row := range rows {
		r.removeRenditionArtifacts(userID, row)
	}
	return r.db.Model(&renditionRecord{}).
		Where("user_id = ? AND task_id IN ? AND status IN ?", userID, taskIDs,
			[]string{"queued", "running", "cancelling"}).
		Updates(map[string]any{
			"status": "queued", "segments": "[]", "published_duration": 0, "init_inode": 0,
			"updated_at": time.Now(),
		}).Error
}

func (r *Repo) SetRenditionTask(userID string, sourceInode uint64, profile, taskID string) error {
	return r.db.Model(&renditionRecord{}).
		Where("user_id = ? AND source_inode = ? AND profile = ?", userID, sourceInode, profile).
		Updates(map[string]any{"task_id": taskID, "updated_at": time.Now()}).Error
}

// SetRenditionCancelling flags a running rendition for cancellation by the
// worker, which observes the status between segment publishes.
func (r *Repo) SetRenditionCancelling(userID, taskID string) error {
	return r.db.Model(&renditionRecord{}).
		Where("user_id = ? AND task_id = ? AND status IN ?", userID, taskID, []string{"queued", "running"}).
		Update("status", "cancelling").Error
}

func (r *Repo) RenditionStatus(userID string, sourceInode uint64, profile string) (string, error) {
	var row renditionRecord
	if err := r.db.Select("status").Where("user_id = ? AND source_inode = ? AND profile = ?", userID, sourceInode, profile).
		First(&row).Error; err != nil {
		return "", err
	}
	return row.Status, nil
}

// ResetRendition discards a rendition's partial artifacts and returns it to
// queued state. Used when a worker run is interrupted or cancelled mid-flight.
func (r *Repo) ResetRendition(userID string, sourceInode uint64, profile string) error {
	var row renditionRecord
	if err := r.db.Where("user_id = ? AND source_inode = ? AND profile = ?", userID, sourceInode, profile).
		First(&row).Error; err != nil {
		return err
	}
	r.removeRenditionArtifacts(userID, row)
	return r.db.Model(&renditionRecord{}).
		Where("user_id = ? AND source_inode = ? AND profile = ?", userID, sourceInode, profile).
		Updates(map[string]any{
			"status": "queued", "segments": "[]", "published_duration": 0, "init_inode": 0,
			"updated_at": time.Now(),
		}).Error
}

// DeleteRendition removes the rendition row and deletes every derived DOFS
// node it published. Missing nodes are tolerated so a partially cleaned-up
// failure state can always be torn down.
func (r *Repo) DeleteRendition(userID string, sourceInode uint64, profile string) error {
	var row renditionRecord
	if err := r.db.Where("user_id = ? AND source_inode = ? AND profile = ?", userID, sourceInode, profile).
		First(&row).Error; err != nil {
		return err
	}
	inodes := []uint64{row.InitInode}
	var segments []renditionSegment
	if err := json.Unmarshal([]byte(row.Segments), &segments); err != nil {
		return fmt.Errorf("decode rendition segments: %w", err)
	}
	for _, segment := range segments {
		inodes = append(inodes, segment.Inode)
	}
	for _, inode := range inodes {
		if inode == 0 {
			continue
		}
		r.deleteRenditionNode(userID, inode)
	}
	return r.db.Where("user_id = ? AND source_inode = ? AND profile = ?", userID, sourceInode, profile).
		Delete(&renditionRecord{}).Error
}

// DeleteRenditionsBySource tears down every rendition of a source inode. Used
// when the source file itself is deleted or replaced.
func (r *Repo) DeleteRenditionsBySource(userID string, sourceInode uint64) error {
	var rows []renditionRecord
	if err := r.db.Where("user_id = ? AND source_inode = ?", userID, sourceInode).Find(&rows).Error; err != nil {
		return err
	}
	for _, row := range rows {
		if err := r.DeleteRendition(userID, sourceInode, row.Profile); err != nil {
			return err
		}
	}
	return nil
}

func (r *Repo) deleteRenditionNode(userID string, inode uint64) {
	ctx, cancel := operationContext()
	defer cancel()
	node, err := r.runtime.Metadata.GetNode(ctx, userID, inode)
	if err != nil {
		return
	}
	controller, err := r.controller(ctx, userID)
	if err != nil {
		return
	}
	defer controller.Close()
	_ = r.deleteNode(ctx, controller, userID, node)
}

// removeRenditionArtifacts deletes the derived DOFS nodes of one rendition row
// (init segment plus all media segments). Best-effort: nodes that are already
// gone are tolerated.
func (r *Repo) removeRenditionArtifacts(userID string, row renditionRecord) {
	inodes := []uint64{row.InitInode}
	var segments []renditionSegment
	if err := json.Unmarshal([]byte(row.Segments), &segments); err != nil {
		return
	}
	for _, segment := range segments {
		inodes = append(inodes, segment.Inode)
	}
	for _, inode := range inodes {
		if inode == 0 {
			continue
		}
		r.deleteRenditionNode(userID, inode)
	}
}
