package fileview

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/willvar/dofs"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"domus/internal/model"
)

func (r *Repo) PrepareDirectUpload(
	ctx context.Context,
	userID, uploadID, physical string,
	size int64,
	fileKey []byte,
	replace bool,
	ttl time.Duration,
) (dofs.ExternalUpload, error) {
	user, err := r.user(userID)
	if err != nil {
		return dofs.ExternalUpload{}, err
	}
	parent, name, err := r.resolveParent(ctx, user, physical)
	if err != nil {
		return dofs.ExternalUpload{}, err
	}
	controller, err := r.controller(ctx, userID)
	if err != nil {
		return dofs.ExternalUpload{}, err
	}
	defer controller.Close()
	return controller.PrepareDirectUpload(ctx, dofs.DirectUploadRequest{
		ID: uploadID, Parent: parent.Inode, Name: name, Mode: 0600,
		Size: size, FileKey: fileKey, Replace: replace, TTL: ttl,
	})
}

func (r *Repo) GetDirectUpload(ctx context.Context, userID, uploadID string) (dofs.ExternalUpload, error) {
	controller, err := r.controller(ctx, userID)
	if err != nil {
		return dofs.ExternalUpload{}, err
	}
	defer controller.Close()
	return controller.GetDirectUpload(ctx, uploadID)
}

func (r *Repo) RefreshDirectUpload(ctx context.Context, userID, uploadID string, ttl time.Duration) (dofs.ExternalUpload, error) {
	controller, err := r.controller(ctx, userID)
	if err != nil {
		return dofs.ExternalUpload{}, err
	}
	defer controller.Close()
	return controller.RefreshDirectUpload(ctx, uploadID, ttl)
}

func (r *Repo) CommitDirectUpload(ctx context.Context, userID, uploadID string) (dofs.Node, error) {
	controller, err := r.controller(ctx, userID)
	if err != nil {
		return dofs.Node{}, err
	}
	defer controller.Close()
	return controller.CommitDirectUpload(ctx, uploadID)
}

func (r *Repo) AbortDirectUpload(ctx context.Context, userID, uploadID string) error {
	controller, err := r.controller(ctx, userID)
	if err != nil {
		return err
	}
	defer controller.Close()
	if err := controller.AbortDirectUpload(ctx, uploadID); err != nil && !errors.Is(err, dofs.ErrNotFound) {
		return err
	}
	return r.db.Where("user_id = ? AND id = ?", userID, uploadID).Delete(&uploadRecord{}).Error
}

func (r *Repo) AcknowledgeDirectUpload(ctx context.Context, userID, uploadID string) error {
	controller, err := r.controller(ctx, userID)
	if err != nil {
		return err
	}
	defer controller.Close()
	return controller.AcknowledgeDirectUpload(ctx, uploadID)
}

func (r *Repo) copyMetadata(userID string, source, destination dofs.Node) error {
	var sourceMetadata metadataRecord
	err := r.db.Where("user_id = ? AND inode = ? AND generation = ?", userID, source.Inode, source.Generation).
		First(&sourceMetadata).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	sourceMetadata.Inode = destination.Inode
	sourceMetadata.Generation = destination.Generation
	sourceMetadata.CreatedAt = now
	sourceMetadata.UpdatedAt = now
	return r.db.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "user_id"}, {Name: "inode"}},
		UpdateAll: true,
	}).Create(&sourceMetadata).Error
}

func (r *Repo) copyFile(ctx context.Context, controller *dofs.Backend, userID string, source dofs.Node, parent uint64, name string) (dofs.Node, error) {
	upload, err := controller.PrepareCiphertextClone(ctx, "", parent, name, source.Mode, source, true, 15*time.Minute)
	if err != nil {
		return dofs.Node{}, err
	}
	if err := r.runtime.Objects.Copy(ctx, source.ObjectKey, upload.ObjectKey); err != nil {
		_ = controller.AbortDirectUpload(context.Background(), upload.ID)
		return dofs.Node{}, fmt.Errorf("copy encrypted DOFS generation: %w", err)
	}
	destination, err := controller.CommitDirectUpload(ctx, upload.ID)
	if err != nil {
		return dofs.Node{}, err
	}
	if err := r.copyMetadata(userID, source, destination); err != nil {
		return dofs.Node{}, err
	}
	if err := controller.AcknowledgeDirectUpload(ctx, upload.ID); err != nil {
		return dofs.Node{}, err
	}
	return destination, nil
}

func (r *Repo) destinationInsideSource(ctx context.Context, userID string, source, destinationParent uint64) (bool, error) {
	current := destinationParent
	for current != 0 {
		if current == source {
			return true, nil
		}
		node, err := r.runtime.Metadata.GetNode(ctx, userID, current)
		if err != nil {
			return false, err
		}
		current = node.Parent
	}
	return false, nil
}

func (r *Repo) copyTree(
	ctx context.Context,
	controller *dofs.Backend,
	userID string,
	source dofs.Node,
	destinationParent uint64,
	destinationName string,
	progress func(done, total int, current string),
	done *int,
	total int,
) error {
	if source.IsFile() {
		if _, err := r.copyFile(ctx, controller, userID, source, destinationParent, destinationName); err != nil {
			return err
		}
		*done = *done + 1
		if progress != nil {
			progress(*done, total, source.Name)
		}
		return nil
	}
	children, err := r.runtime.Metadata.List(ctx, userID, source.Inode)
	if err != nil {
		return err
	}
	destination, lookupErr := controller.Lookup(ctx, destinationParent, destinationName)
	if errors.Is(lookupErr, dofs.ErrNotFound) {
		destination, err = controller.CreateDirectory(ctx, destinationParent, destinationName, source.Mode)
	} else if lookupErr != nil {
		err = lookupErr
	} else if !destination.IsDir() {
		err = dofs.ErrTypeMismatch
	}
	if err != nil {
		return err
	}
	for _, child := range children {
		if err := r.copyTree(ctx, controller, userID, child, destination.Inode, child.Name, progress, done, total); err != nil {
			return err
		}
	}
	return nil
}

func (r *Repo) Copy(userID, sourcePath, destinationPath string, progress func(done, total int, current string)) error {
	ctx, cancel := operationContext()
	defer cancel()
	user, err := r.user(userID)
	if err != nil {
		return err
	}
	source, err := r.resolve(ctx, user, sourcePath)
	if err != nil {
		return err
	}
	destinationParent, destinationName, err := r.resolveParent(ctx, user, destinationPath)
	if err != nil {
		return err
	}
	if source.IsDir() {
		inside, err := r.destinationInsideSource(ctx, userID, source.Inode, destinationParent.Inode)
		if err != nil {
			return err
		}
		if inside {
			return errors.New("cannot copy a directory into itself")
		}
	}
	records := make([]model.FileRecord, 0)
	if err := r.collect(ctx, user, source, true, &records); err != nil {
		return err
	}
	total := 0
	for _, record := range records {
		if !record.IsDir {
			total++
		}
	}
	controller, err := r.controller(ctx, userID)
	if err != nil {
		return err
	}
	defer controller.Close()
	done := 0
	return r.copyTree(ctx, controller, userID, source, destinationParent.Inode, destinationName, progress, &done, total)
}
