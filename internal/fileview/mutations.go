package fileview

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/willvar/dofs"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"domus/internal/model"
)

func (r *Repo) controller(ctx context.Context, userID string) (*dofs.Backend, error) {
	return r.runtime.OpenControl(ctx, userID)
}

func (r *Repo) ensureDirectoryPath(ctx context.Context, user *model.User, namespacePath string) (dofs.Node, error) {
	segments, _, err := splitNamespacePath(user, namespacePath)
	if err != nil {
		return dofs.Node{}, err
	}
	controller, err := r.controller(ctx, user.ID)
	if err != nil {
		return dofs.Node{}, err
	}
	defer controller.Close()
	current, err := r.runtime.Metadata.GetNode(ctx, user.ID, dofs.RootInode)
	if err != nil {
		return dofs.Node{}, err
	}
	for _, segment := range segments {
		next, lookupErr := controller.Lookup(ctx, current.Inode, segment)
		if lookupErr == nil {
			if !next.IsDir() {
				return dofs.Node{}, dofs.ErrTypeMismatch
			}
			current = next
			continue
		}
		if !errors.Is(lookupErr, dofs.ErrNotFound) {
			return dofs.Node{}, lookupErr
		}
		next, err = controller.CreateDirectory(ctx, current.Inode, segment, 0700)
		if errors.Is(err, dofs.ErrAlreadyExists) {
			next, err = controller.Lookup(ctx, current.Inode, segment)
		}
		if err != nil {
			return dofs.Node{}, err
		}
		current = next
	}
	return current, nil
}

func (r *Repo) upsertMetadata(userID string, inode uint64, generation int64, updates map[string]any) error {
	now := time.Now().UTC()
	values := map[string]any{
		"user_id": userID, "inode": inode, "generation": generation,
		"content_type": "", "content_hash": "", "thumbnail": uint64(0),
		"media_width": 0, "media_height": 0, "media_duration": float64(0),
		"media_codecs": "", "media_meta": "", "created_at": now, "updated_at": now,
	}
	assignments := make(clause.Set, 0, 10)
	for _, name := range []string{
		"content_type", "content_hash", "thumbnail", "media_width", "media_height",
		"media_duration", "media_codecs", "media_meta",
	} {
		incoming := clause.Column{Table: "excluded", Name: name}
		var value any = incoming
		if update, ok := updates[name]; ok {
			values[name] = update
		} else {
			// Preserve siblings only within the same generation. A new generation
			// resets every field not supplied by this update in the same statement.
			value = gorm.Expr("CASE WHEN ? = ? THEN ? ELSE ? END",
				clause.Column{Table: "domus_file_metadata", Name: "generation"},
				clause.Column{Table: "excluded", Name: "generation"},
				clause.Column{Table: "domus_file_metadata", Name: name}, incoming)
		}
		assignments = append(assignments, clause.Assignment{Column: clause.Column{Name: name}, Value: value})
	}
	for _, name := range []string{"generation", "updated_at"} {
		assignments = append(assignments, clause.Assignment{
			Column: clause.Column{Name: name}, Value: clause.Column{Table: "excluded", Name: name},
		})
	}
	return r.db.Model(&metadataRecord{}).Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "user_id"}, {Name: "inode"}},
		DoUpdates: assignments,
		// A delayed probe must not roll a newer projection back to its generation.
		Where: clause.Where{Exprs: []clause.Expression{clause.Lte{
			Column: clause.Column{Table: "domus_file_metadata", Name: "generation"},
			Value:  clause.Column{Table: "excluded", Name: "generation"},
		}}},
	}).Create(values).Error
}

func (r *Repo) Upsert(userID, namespacePath, name string, isDir bool, size int64, contentType, contentHash string, options ...model.UpsertFileOpts) error {
	ctx, cancel := operationContext()
	defer cancel()
	user, err := r.user(userID)
	if err != nil {
		return err
	}
	if isDir {
		_, err := r.ensureDirectoryPath(ctx, user, namespacePath)
		return err
	}
	node, err := r.resolve(ctx, user, namespacePath)
	if err != nil {
		return fmt.Errorf("file content must be published through DOFS before catalog update: %w", err)
	}
	if !node.IsFile() || node.Size != size {
		return dofs.ErrConflict
	}
	if len(options) > 0 {
		if options[0].ObjectKey != "" && options[0].ObjectKey != node.ObjectKey {
			return dofs.ErrConflict
		}
		if options[0].WrappedDEK != "" && options[0].WrappedDEK != hex.EncodeToString(node.WrappedDEK) {
			return dofs.ErrConflict
		}
	}
	return r.upsertMetadata(userID, node.Inode, node.Generation, map[string]any{
		"content_type": contentType, "content_hash": contentHash,
		// Product metadata is generation-scoped. A replacement must never show
		// dimensions, a thumbnail or codec strings generated from the previous
		// plaintext.
		"thumbnail": 0, "media_width": 0, "media_height": 0, "media_duration": 0,
		"media_codecs": "", "media_meta": "",
	})
}

func (r *Repo) deleteNode(ctx context.Context, controller *dofs.Backend, userID string, node dofs.Node) error {
	if node.IsDir() {
		children, err := r.runtime.Metadata.List(ctx, userID, node.Inode)
		if err != nil {
			return err
		}
		for _, child := range children {
			if err := r.deleteNode(ctx, controller, userID, child); err != nil {
				return err
			}
		}
	}
	if node.Inode == dofs.RootInode {
		return errors.New("refusing to remove DOFS namespace root")
	}
	if err := controller.Remove(ctx, node.Parent, node.Name, node.IsDir()); err != nil {
		return err
	}
	return r.deleteInodeProjection(userID, node.Inode)
}

func (r *Repo) deleteInodeProjection(userID string, inode uint64) error {
	return r.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("user_id = ? AND inode = ?", userID, inode).Delete(&metadataRecord{}).Error; err != nil {
			return err
		}
		// uploadRecord is transient resume/idempotency state, not file identity.
		// Once the inode is deleted, retaining it would make path fallback report
		// a ghost file and prevent the same logical name from being reused.
		return tx.Where("user_id = ? AND inode = ?", userID, inode).Delete(&uploadRecord{}).Error
	})
}

func (r *Repo) Delete(userID, namespacePath string) error {
	ctx, cancel := operationContext()
	defer cancel()
	user, err := r.user(userID)
	if err != nil {
		return err
	}
	node, err := r.resolve(ctx, user, namespacePath)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return r.db.Where("user_id = ? AND path = ?", userID, namespacePath).Delete(&uploadRecord{}).Error
	}
	if err != nil {
		return err
	}
	controller, err := r.controller(ctx, userID)
	if err != nil {
		return err
	}
	defer controller.Close()
	return r.deleteNode(ctx, controller, userID, node)
}

func (r *Repo) DeleteByPrefix(userID, prefix string) error {
	ctx, cancel := operationContext()
	defer cancel()
	user, err := r.user(userID)
	if err != nil {
		return err
	}
	node, err := r.resolve(ctx, user, prefix)
	if err != nil {
		return err
	}
	controller, err := r.controller(ctx, userID)
	if err != nil {
		return err
	}
	defer controller.Close()
	return r.deleteNode(ctx, controller, userID, node)
}

func (r *Repo) move(userID, oldPath, newPath string, noReplace bool) error {
	ctx, cancel := operationContext()
	defer cancel()
	user, err := r.user(userID)
	if err != nil {
		return err
	}
	source, err := r.resolve(ctx, user, oldPath)
	if err != nil {
		return err
	}
	parent, name, err := r.resolveParent(ctx, user, newPath)
	if err != nil {
		return err
	}
	var replacedInode uint64
	if destination, lookupErr := r.runtime.Metadata.Lookup(ctx, userID, parent.Inode, name); lookupErr == nil {
		replacedInode = destination.Inode
	}
	controller, err := r.controller(ctx, userID)
	if err != nil {
		return err
	}
	defer controller.Close()
	if _, err := controller.Rename(ctx, source.Inode, parent.Inode, name, noReplace); err != nil {
		return err
	}
	if replacedInode != 0 && replacedInode != source.Inode {
		if err := r.deleteInodeProjection(userID, replacedInode); err != nil {
			return err
		}
	}
	return nil
}

func (r *Repo) Move(userID, oldPath, newPath, _ string) error {
	return r.move(userID, oldPath, newPath, false)
}

func (r *Repo) MoveByPrefix(userID, oldPrefix, newPrefix string) error {
	return r.move(userID, oldPrefix, newPrefix, false)
}

func (r *Repo) MoveNoReplace(userID, oldPath, newPath, _ string) error {
	return r.move(userID, oldPath, newPath, true)
}

func (r *Repo) MoveByPrefixNoReplace(userID, oldPrefix, newPrefix string) error {
	return r.move(userID, oldPrefix, newPrefix, true)
}

func (r *Repo) thumbnailInode(userID, objectKey, wrapped string) (uint64, error) {
	if objectKey == "" {
		return 0, nil
	}
	record, err := r.GetByStorageKey(userID, objectKey)
	if err != nil {
		return 0, err
	}
	if wrapped != "" && wrapped != record.WrappedDEK {
		return 0, dofs.ErrConflict
	}
	return uint64(record.ID), nil
}

func (r *Repo) UpdateThumbnail(userID, namespacePath, thumbnailKey, thumbnailWrappedDEK string, width, height int, duration float64) error {
	record, err := r.Get(userID, namespacePath)
	if err != nil {
		return err
	}
	thumbnail, err := r.thumbnailInode(userID, thumbnailKey, thumbnailWrappedDEK)
	if err != nil {
		return err
	}
	return r.upsertMetadata(userID, uint64(record.ID), record.Generation, map[string]any{
		"thumbnail": thumbnail, "media_width": width, "media_height": height, "media_duration": duration,
	})
}

func (r *Repo) UpdateThumbnailIfGeneration(userID, namespacePath string, fileID, generation int64, thumbnailKey, thumbnailWrappedDEK string, width, height int, duration float64) (bool, error) {
	record, err := r.Get(userID, namespacePath)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if record.ID != fileID || record.Generation != generation || record.Status != "ready" {
		return false, nil
	}
	thumbnail, err := r.thumbnailInode(userID, thumbnailKey, thumbnailWrappedDEK)
	if err != nil {
		return false, err
	}
	// Stamp the generation that was actually probed. If a writer commits after
	// this check, recordFromNode ignores this now-stale row instead of attaching
	// an old thumbnail to the new contents.
	return true, r.upsertMetadata(userID, uint64(record.ID), generation, map[string]any{
		"thumbnail": thumbnail, "media_width": width,
		"media_height": height, "media_duration": duration,
	})
}

func (r *Repo) UpdateContentType(userID, namespacePath, contentType string) error {
	record, err := r.Get(userID, namespacePath)
	if err != nil {
		return err
	}
	return r.upsertMetadata(userID, uint64(record.ID), record.Generation, map[string]any{"content_type": contentType})
}

// UpdateMediaCodecs records the source media's MSE codec string, probed by
// the worker through the FUSE mount. The caller passes the inode and
// generation captured before the probe; if the file was replaced or moved in
// the meantime the stale result is discarded instead of being attached to the
// new contents.
func (r *Repo) UpdateMediaCodecs(userID, namespacePath string, fileID uint64, generation int64, codecs string) error {
	record, err := r.Get(userID, namespacePath)
	if err != nil {
		return err
	}
	if uint64(record.ID) != fileID || record.Generation != generation || record.Status != "ready" {
		return nil
	}
	return r.upsertMetadata(userID, fileID, generation, map[string]any{"media_codecs": codecs})
}

// UpdateMediaMeta records the JSON-encoded ffprobe summary of the source
// media. Like the codec string it carries the pre-probe inode/generation and
// is discarded when the contents changed during the probe.
func (r *Repo) UpdateMediaMeta(userID, namespacePath string, fileID uint64, generation int64, meta string) error {
	record, err := r.Get(userID, namespacePath)
	if err != nil {
		return err
	}
	if uint64(record.ID) != fileID || record.Generation != generation || record.Status != "ready" {
		return nil
	}
	return r.upsertMetadata(userID, fileID, generation, map[string]any{"media_meta": meta})
}

func (r *Repo) SearchFiles(userID, query string, limit int) ([]model.SearchFileResult, error) {
	if limit <= 0 {
		limit = 50
	}
	ctx, cancel := operationContext()
	defer cancel()
	user, err := r.user(userID)
	if err != nil {
		return nil, err
	}
	root, err := r.runtime.Metadata.GetNode(ctx, userID, dofs.RootInode)
	if err != nil {
		return nil, err
	}
	records := make([]model.FileRecord, 0)
	if err := r.collect(ctx, user, root, false, &records); err != nil {
		return nil, err
	}
	needle := strings.ToLower(strings.TrimSpace(query))
	result := make([]model.SearchFileResult, 0, min(limit, len(records)))
	for _, record := range records {
		if len(result) == limit {
			break
		}
		if strings.HasPrefix(record.Path, "/.domus/") || record.Path == "/.domus/" {
			continue
		}
		if strings.Contains(strings.ToLower(record.Name), needle) {
			result = append(result, model.SearchFileResult{FileRecord: record, Rank: 1})
		}
	}
	return result, nil
}
