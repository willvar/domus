package fileview

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/willvar/dofs"
	"gorm.io/gorm"

	"domus/internal/dofsbridge"
	"domus/internal/filekind"
	"domus/internal/model"
)

const operationTimeout = 30 * time.Second

// Repo presents DOFS inode metadata through Domus's existing file view while
// persisting only application-specific catalog and upload-task fields.
type Repo struct {
	db      *gorm.DB
	runtime *dofsbridge.Runtime
}

func Open(database *gorm.DB, runtime *dofsbridge.Runtime) (*Repo, error) {
	if database == nil || runtime == nil {
		return nil, errors.New("Domus file view requires database and DOFS runtime")
	}
	if err := database.AutoMigrate(&metadataRecord{}, &uploadRecord{}, &renditionRecord{}); err != nil {
		return nil, fmt.Errorf("migrate Domus file projection: %w", err)
	}
	return &Repo{db: database, runtime: runtime}, nil
}

func operationContext() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), operationTimeout)
}

func mapNotFound(err error) error {
	if errors.Is(err, dofs.ErrNotFound) {
		return gorm.ErrRecordNotFound
	}
	return err
}

func (r *Repo) user(userID string) (*model.User, error) {
	user, err := r.runtime.Users.GetByID(userID)
	if err != nil {
		return nil, err
	}
	if err := dofs.ValidateNamespaceID(user.ID); err != nil {
		return nil, err
	}
	return user, nil
}

func splitNamespacePath(user *model.User, namespacePath string) ([]string, bool, error) {
	if user == nil || namespacePath == "" || !strings.HasPrefix(namespacePath, "/") || strings.Contains(namespacePath, "\x00") {
		return nil, false, gorm.ErrRecordNotFound
	}
	directory := namespacePath == "/" || strings.HasSuffix(namespacePath, "/")
	trimmed := strings.Trim(namespacePath, "/")
	if trimmed == "" {
		return nil, true, nil
	}
	segments := strings.Split(trimmed, "/")
	for _, segment := range segments {
		if err := dofs.ValidateName(segment); err != nil {
			return nil, false, gorm.ErrRecordNotFound
		}
	}
	return segments, directory, nil
}

func (r *Repo) resolve(ctx context.Context, user *model.User, namespacePath string) (dofs.Node, error) {
	segments, wantsDirectory, err := splitNamespacePath(user, namespacePath)
	if err != nil {
		return dofs.Node{}, err
	}
	node, err := r.runtime.Metadata.GetNode(ctx, user.ID, dofs.RootInode)
	if err != nil {
		return dofs.Node{}, mapNotFound(err)
	}
	for _, segment := range segments {
		node, err = r.runtime.Metadata.Lookup(ctx, user.ID, node.Inode, segment)
		if err != nil {
			return dofs.Node{}, mapNotFound(err)
		}
	}
	if node.State != dofs.NodeStateReady || (wantsDirectory && !node.IsDir()) {
		return dofs.Node{}, gorm.ErrRecordNotFound
	}
	return node, nil
}

func (r *Repo) resolveParent(ctx context.Context, user *model.User, namespacePath string) (dofs.Node, string, error) {
	segments, _, err := splitNamespacePath(user, namespacePath)
	if err != nil || len(segments) == 0 {
		return dofs.Node{}, "", gorm.ErrRecordNotFound
	}
	name := segments[len(segments)-1]
	parent := dofs.Node{Inode: dofs.RootInode, NamespaceID: user.ID, Kind: dofs.NodeKindDirectory, State: dofs.NodeStateReady}
	if len(segments) > 1 {
		parentPath := "/" + strings.Join(segments[:len(segments)-1], "/") + "/"
		parent, err = r.resolve(ctx, user, parentPath)
		if err != nil {
			return dofs.Node{}, "", err
		}
	}
	if !parent.IsDir() {
		return dofs.Node{}, "", dofs.ErrNotDirectory
	}
	return parent, name, nil
}

func (r *Repo) pathForNode(ctx context.Context, user *model.User, node dofs.Node) (string, error) {
	if node.NamespaceID != user.ID || node.Inode == 0 {
		return "", gorm.ErrRecordNotFound
	}
	if node.Inode == dofs.RootInode {
		return "/", nil
	}
	segments := make([]string, 0, 8)
	current := node
	for current.Inode != dofs.RootInode {
		if current.Parent == 0 || len(segments) > 4096 {
			return "", errors.New("corrupt DOFS parent chain")
		}
		segments = append(segments, current.Name)
		var err error
		current, err = r.runtime.Metadata.GetNode(ctx, user.ID, current.Parent)
		if err != nil {
			return "", mapNotFound(err)
		}
	}
	for left, right := 0, len(segments)-1; left < right; left, right = left+1, right-1 {
		segments[left], segments[right] = segments[right], segments[left]
	}
	namespacePath := "/" + strings.Join(segments, "/")
	if node.IsDir() {
		namespacePath += "/"
	}
	return namespacePath, nil
}

func namespaceParent(namespacePath string) string {
	trimmed := strings.TrimSuffix(namespacePath, "/")
	index := strings.LastIndex(trimmed, "/")
	if index < 0 {
		return ""
	}
	return trimmed[:index+1]
}

func (r *Repo) recordFromNode(ctx context.Context, user *model.User, node dofs.Node, namespacePath string) (model.FileRecord, error) {
	if node.Inode > math.MaxInt64 || node.State != dofs.NodeStateReady {
		return model.FileRecord{}, gorm.ErrRecordNotFound
	}
	if namespacePath == "" {
		var err error
		namespacePath, err = r.pathForNode(ctx, user, node)
		if err != nil {
			return model.FileRecord{}, err
		}
	}
	record := model.FileRecord{
		ID: int64(node.Inode), UserID: user.ID, Path: namespacePath, Parent: namespaceParent(namespacePath),
		Name: node.Name, IsDir: node.IsDir(), Size: node.Size,
		ObjectKey: node.ObjectKey, Generation: node.Generation,
		Status: "ready", CreatedAt: node.CreatedAt, UpdatedAt: node.ModifiedAt,
	}
	if node.Inode == dofs.RootInode {
		record.Name = "/"
		record.Parent = ""
	}
	if len(node.WrappedDEK) > 0 {
		record.WrappedDEK = hex.EncodeToString(node.WrappedDEK)
	}
	var metadata metadataRecord
	err := r.db.Where("user_id = ? AND inode = ?", user.ID, node.Inode).First(&metadata).Error
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return model.FileRecord{}, err
	}
	if err == nil && (node.IsDir() || metadata.Generation == node.Generation) {
		record.ContentType = metadata.ContentType
		record.ContentHash = metadata.ContentHash
		record.MediaWidth = metadata.MediaWidth
		record.MediaHeight = metadata.MediaHeight
		record.MediaDuration = metadata.MediaDuration
		if metadata.Thumbnail != 0 {
			thumbnail, thumbnailErr := r.runtime.Metadata.GetNode(ctx, user.ID, metadata.Thumbnail)
			if thumbnailErr == nil && thumbnail.State == dofs.NodeStateReady && thumbnail.IsFile() {
				record.ThumbnailKey = thumbnail.ObjectKey
				record.ThumbnailWrappedDEK = hex.EncodeToString(thumbnail.WrappedDEK)
			}
		}
	}
	if record.ContentType == "" && !record.IsDir {
		record.ContentType = filekind.ContentType(record.Name, "")
	}
	return record, nil
}

func (r *Repo) recordFromUpload(ctx context.Context, user *model.User, upload uploadRecord) (model.FileRecord, error) {
	if upload.Inode > math.MaxInt64 {
		return model.FileRecord{}, errors.New("DOFS inode does not fit Domus file ID")
	}
	record := model.FileRecord{
		ID: int64(upload.Inode), UserID: upload.UserID, Path: upload.Path, Parent: upload.Parent,
		Name: upload.Name, Size: upload.Size, ContentType: upload.ContentType, Status: upload.Status,
		UploadID: upload.ID, TaskID: upload.TaskID, OSSUploadID: upload.OSSUploadID,
		ClientInstanceID: upload.ClientInstanceID, LastSeenAt: upload.LastSeenAt,
		CreatedAt: upload.CreatedAt, UpdatedAt: upload.UpdatedAt,
	}
	node, nodeErr := r.runtime.Metadata.GetNode(ctx, user.ID, upload.Inode)
	if nodeErr == nil {
		record.Generation = node.Generation
		record.ObjectKey = node.ObjectKey
		if len(node.WrappedDEK) > 0 {
			record.WrappedDEK = hex.EncodeToString(node.WrappedDEK)
		}
	}
	if direct, directErr := r.runtime.Metadata.GetExternalUpload(ctx, user.ID, upload.ID); directErr == nil {
		record.ObjectKey = direct.ObjectKey
		record.WrappedDEK = hex.EncodeToString(direct.WrappedDEK)
	}
	return record, nil
}

func (r *Repo) Get(userID, namespacePath string) (*model.FileRecord, error) {
	ctx, cancel := operationContext()
	defer cancel()
	user, err := r.user(userID)
	if err != nil {
		return nil, err
	}
	node, err := r.resolve(ctx, user, namespacePath)
	if err == nil {
		record, err := r.recordFromNode(ctx, user, node, namespacePath)
		return &record, err
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	}
	var upload uploadRecord
	// Only an unpublished upload may stand in for a missing DOFS node. Ready
	// rows are retained briefly so a lost completion response can be retried by
	// upload ID, but they must never resurrect a renamed or deleted pathname.
	if uploadErr := r.db.Where(
		"user_id = ? AND path = ? AND status IN ?",
		userID, namespacePath, []string{"uploading", "processing"},
	).
		Order("created_at DESC").First(&upload).Error; uploadErr != nil {
		return nil, uploadErr
	}
	record, err := r.recordFromUpload(ctx, user, upload)
	return &record, err
}

func (r *Repo) GetByID(userID string, id int64) (*model.FileRecord, error) {
	if id <= 0 {
		return nil, gorm.ErrRecordNotFound
	}
	ctx, cancel := operationContext()
	defer cancel()
	user, err := r.user(userID)
	if err != nil {
		return nil, err
	}
	node, err := r.runtime.Metadata.GetNode(ctx, userID, uint64(id))
	if err != nil {
		return nil, mapNotFound(err)
	}
	if node.State != dofs.NodeStateReady {
		// A reserved direct-upload inode exists in metadata before it is
		// publishable. Preserve the FileRepo contract: absence is an error,
		// never a misleading (nil, nil) result.
		return nil, gorm.ErrRecordNotFound
	}
	record, err := r.recordFromNode(ctx, user, node, "")
	return &record, err
}

func (r *Repo) GetByStorageKey(userID, objectKey string) (*model.FileRecord, error) {
	root, err := dofs.ObjectRoot(userID)
	if err != nil || !strings.HasPrefix(objectKey, root) {
		return nil, gorm.ErrRecordNotFound
	}
	relative := strings.TrimPrefix(objectKey, root)
	component, _, found := strings.Cut(relative, "/")
	if !found {
		return nil, gorm.ErrRecordNotFound
	}
	inode, err := strconv.ParseUint(component, 10, 64)
	if err != nil || inode == 0 || inode > math.MaxInt64 {
		return nil, gorm.ErrRecordNotFound
	}
	record, err := r.GetByID(userID, int64(inode))
	if err != nil || record.ObjectKey != objectKey {
		return nil, gorm.ErrRecordNotFound
	}
	return record, nil
}

func (r *Repo) ListDirectChildren(userID, parentPath string) ([]model.FileRecord, error) {
	ctx, cancel := operationContext()
	defer cancel()
	user, err := r.user(userID)
	if err != nil {
		return nil, err
	}
	parent, err := r.resolve(ctx, user, parentPath)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		// Directory listing has historically been an empty-set query. Preserve
		// that contract for optional product directories (for example the
		// thumbnail directory before its first artifact exists).
		return []model.FileRecord{}, nil
	}
	if err != nil {
		return nil, err
	}
	if !parent.IsDir() {
		return nil, dofs.ErrNotDirectory
	}
	nodes, err := r.runtime.Metadata.List(ctx, userID, parent.Inode)
	if err != nil {
		return nil, err
	}
	var uploads []uploadRecord
	if err := r.db.Where("user_id = ? AND parent = ? AND status IN ?", userID, parentPath, []string{"uploading", "processing"}).Find(&uploads).Error; err != nil {
		return nil, err
	}
	activeInodes := make(map[uint64]struct{}, len(uploads))
	for _, upload := range uploads {
		activeInodes[upload.Inode] = struct{}{}
	}
	records := make([]model.FileRecord, 0, len(nodes))
	for _, node := range nodes {
		// Replacement uploads reserve the existing inode. Show one uploading
		// projection rather than both the old ready generation and a duplicate.
		if _, replacing := activeInodes[node.Inode]; replacing {
			continue
		}
		namespacePath := parentPath + node.Name
		if node.IsDir() {
			namespacePath += "/"
		}
		record, err := r.recordFromNode(ctx, user, node, namespacePath)
		if err != nil {
			return nil, err
		}
		records = append(records, record)
	}
	for _, upload := range uploads {
		record, err := r.recordFromUpload(ctx, user, upload)
		if err != nil {
			return nil, err
		}
		records = append(records, record)
	}
	sort.Slice(records, func(first, second int) bool {
		if records[first].IsDir != records[second].IsDir {
			return records[first].IsDir
		}
		return records[first].Name < records[second].Name
	})
	return records, nil
}

func (r *Repo) ListAllChildren(userID, parent string) ([]model.FileRecord, error) {
	return r.ListDirectChildren(userID, parent)
}

func (r *Repo) collect(ctx context.Context, user *model.User, node dofs.Node, includeSelf bool, result *[]model.FileRecord) error {
	if includeSelf {
		record, err := r.recordFromNode(ctx, user, node, "")
		if err != nil {
			return err
		}
		*result = append(*result, record)
	}
	if !node.IsDir() {
		return nil
	}
	children, err := r.runtime.Metadata.List(ctx, user.ID, node.Inode)
	if err != nil {
		return err
	}
	for _, child := range children {
		if err := r.collect(ctx, user, child, true, result); err != nil {
			return err
		}
	}
	return nil
}

func (r *Repo) ListByPrefix(userID, prefix string) ([]model.FileRecord, error) {
	ctx, cancel := operationContext()
	defer cancel()
	user, err := r.user(userID)
	if err != nil {
		return nil, err
	}
	node, err := r.resolve(ctx, user, prefix)
	if err != nil {
		return nil, err
	}
	result := make([]model.FileRecord, 0)
	if err := r.collect(ctx, user, node, node.Inode != dofs.RootInode, &result); err != nil {
		return nil, err
	}
	return result, nil
}

var _ model.FileRepo = (*Repo)(nil)
