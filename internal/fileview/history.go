package fileview

import (
	"context"
	"encoding/hex"
	"errors"
	"sort"
	"time"

	"github.com/willvar/dofs"
	"gorm.io/gorm"

	"domus/internal/model"
)

// HistoryVersion is the Domus-facing projection of one immutable DOFS
// generation. Object keys and wrapped keys deliberately stay server-side.
type HistoryVersion struct {
	Generation int64     `json:"generation"`
	Size       int64     `json:"size"`
	CreatedAt  time.Time `json:"created_at"`
	Current    bool      `json:"current"`
}

func validateGeneration(namespaceID string, inode uint64, generation dofs.Generation) error {
	if generation.NamespaceID != namespaceID || generation.Inode != inode ||
		generation.Number <= 0 || generation.Size < 0 {
		return errors.New("dofs: invalid historical generation metadata")
	}
	return dofs.ValidateGenerationObjectKey(namespaceID, inode, generation.ObjectKey)
}

// ListHistory resolves a visible file and returns its immutable generation
// metadata. Product metadata is intentionally not copied into this projection.
func (r *Repo) ListHistory(ctx context.Context, userID, namespacePath string) (model.FileRecord, []HistoryVersion, error) {
	user, err := r.user(userID)
	if err != nil {
		return model.FileRecord{}, nil, err
	}
	node, err := r.resolve(ctx, user, namespacePath)
	if err != nil {
		return model.FileRecord{}, nil, err
	}
	if !node.IsFile() {
		return model.FileRecord{}, nil, dofs.ErrTypeMismatch
	}
	record, err := r.recordFromNode(ctx, user, node, namespacePath)
	if err != nil {
		return model.FileRecord{}, nil, err
	}
	generations, err := r.runtime.Metadata.ListGenerations(ctx, userID, node.Inode)
	if err != nil {
		return model.FileRecord{}, nil, err
	}
	versions := make([]HistoryVersion, 0, len(generations))
	for _, generation := range generations {
		if err := validateGeneration(userID, node.Inode, generation); err != nil {
			return model.FileRecord{}, nil, err
		}
		versions = append(versions, HistoryVersion{
			Generation: generation.Number,
			Size:       generation.Size,
			CreatedAt:  generation.CreatedAt,
			Current:    generation.Number == node.Generation,
		})
	}
	sort.Slice(versions, func(i, j int) bool {
		return versions[i].Generation > versions[j].Generation
	})
	return record, versions, nil
}

// GenerationAccess returns a file projection whose object reference and wrapped
// DEK point at one immutable generation. Historical key material remains owned
// by DOFS and is not persisted in Domus.
func (r *Repo) GenerationAccess(ctx context.Context, userID, namespacePath string, number int64) (model.FileRecord, error) {
	user, err := r.user(userID)
	if err != nil {
		return model.FileRecord{}, err
	}
	node, err := r.resolve(ctx, user, namespacePath)
	if err != nil {
		return model.FileRecord{}, err
	}
	if !node.IsFile() {
		return model.FileRecord{}, dofs.ErrTypeMismatch
	}
	generations, err := r.runtime.Metadata.ListGenerations(ctx, userID, node.Inode)
	if err != nil {
		return model.FileRecord{}, err
	}
	var selected *dofs.Generation
	for index := range generations {
		if generations[index].Number == number {
			selected = &generations[index]
			break
		}
	}
	if selected == nil {
		return model.FileRecord{}, gorm.ErrRecordNotFound
	}
	if err := validateGeneration(userID, node.Inode, *selected); err != nil {
		return model.FileRecord{}, err
	}
	record, err := r.recordFromNode(ctx, user, node, namespacePath)
	if err != nil {
		return model.FileRecord{}, err
	}
	record.ObjectKey = selected.ObjectKey
	record.Size = selected.Size
	record.Generation = selected.Number
	wrappedDEK := selected.WrappedDEK
	if len(wrappedDEK) == 0 && selected.Number == node.Generation {
		wrappedDEK = node.WrappedDEK
	}
	if len(wrappedDEK) == 0 {
		return model.FileRecord{}, errors.New("dofs: historical generation has no wrapped data key")
	}
	record.WrappedDEK = hex.EncodeToString(wrappedDEK)
	// Current-generation product projections do not describe a historical
	// plaintext object. Keep only the filename-derived content type and key.
	record.ContentHash = ""
	record.ContentType = ""
	record.ThumbnailKey = ""
	record.ThumbnailWrappedDEK = ""
	record.MediaWidth = 0
	record.MediaHeight = 0
	record.MediaDuration = 0
	record.MediaCodecs = ""
	record.MediaMeta = ""
	return record, nil
}

// RestoreGeneration asks DOFS to copy the immutable source object and publish
// it as a new current generation with a CAS against expectedCurrent.
func (r *Repo) RestoreGeneration(ctx context.Context, userID, namespacePath string, number, expectedCurrent int64) (model.FileRecord, error) {
	user, err := r.user(userID)
	if err != nil {
		return model.FileRecord{}, err
	}
	node, err := r.resolve(ctx, user, namespacePath)
	if err != nil {
		return model.FileRecord{}, err
	}
	if !node.IsFile() {
		return model.FileRecord{}, dofs.ErrTypeMismatch
	}
	controller, err := r.controller(ctx, userID)
	if err != nil {
		return model.FileRecord{}, err
	}
	defer controller.Close()
	restored, err := controller.RestoreGeneration(ctx, node.Inode, number, expectedCurrent)
	if err != nil {
		return model.FileRecord{}, err
	}
	if restored.Inode != node.Inode || restored.State != dofs.NodeStateReady {
		return model.FileRecord{}, errors.New("dofs: restored node is invalid")
	}
	return r.recordFromNode(ctx, user, restored, namespacePath)
}
