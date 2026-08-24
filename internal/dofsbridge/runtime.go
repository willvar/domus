package dofsbridge

import (
	"bytes"
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/willvar/dofs"
	dofspostgres "github.com/willvar/dofs/metadata/postgres"
	dofssqlite "github.com/willvar/dofs/metadata/sqlite"
	dofss3 "github.com/willvar/dofs/object/s3"
	"gorm.io/gorm"

	"domus/config"
	"domus/internal/model"
)

// Runtime is the shared Domus integration surface for independent DOFS.
// PostgreSQL and object storage are owned by the surrounding process; Runtime
// owns only plaintext key-provider state and clears it on Close.
type Runtime struct {
	Metadata       dofs.MetadataStore
	Objects        dofs.ObjectStore
	Keys           *dofs.EnvelopeKeyProvider
	Users          model.UserRepo
	metadataCloser io.Closer
}

func Open(ctx context.Context, database *gorm.DB, cfg *config.Config, serverKey []byte, users model.UserRepo) (*Runtime, error) {
	if database == nil || cfg == nil || users == nil {
		return nil, errors.New("domus DOFS bridge requires database, config and user repository")
	}
	metadata, metadataCloser, err := openMetadata(ctx, database, cfg)
	if err != nil {
		return nil, err
	}
	objects, err := dofss3.New(dofss3.Config{
		Endpoint:  cfg.OSS.ServerEndpoint,
		AccessKey: cfg.OSS.AccessKeyID,
		SecretKey: cfg.OSS.AccessKeySecret,
		Bucket:    cfg.OSS.Bucket,
		Region:    cfg.OSS.Region,
		Prefix:    cfg.OSS.Prefix,
		Secure:    true,
	})
	if err != nil {
		if metadataCloser != nil {
			_ = metadataCloser.Close()
		}
		return nil, fmt.Errorf("open DOFS object store: %w", err)
	}
	keys, err := dofs.NewEnvelopeKeyProvider(serverKey)
	if err != nil {
		if metadataCloser != nil {
			_ = metadataCloser.Close()
		}
		return nil, fmt.Errorf("open DOFS key provider: %w", err)
	}
	return &Runtime{
		Metadata: metadata, Objects: objects, Keys: keys, Users: users,
		metadataCloser: metadataCloser,
	}, nil
}

func openMetadata(ctx context.Context, database *gorm.DB, cfg *config.Config) (dofs.MetadataStore, io.Closer, error) {
	switch cfg.DOFS.Metadata.Driver {
	case "sqlite":
		path := cfg.DOFS.Metadata.SQLite.Path
		if path == "" {
			path = filepath.Join(cfg.DOFS.StateRoot, "metadata.sqlite")
		}
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			return nil, nil, fmt.Errorf("create DOFS SQLite directory: %w", err)
		}
		metadata, err := dofssqlite.Open(ctx, dofssqlite.Config{
			Path:               path,
			BusyTimeout:        time.Duration(cfg.DOFS.Metadata.SQLite.BusyTimeoutSeconds) * time.Second,
			MaxOpenConnections: cfg.DOFS.Metadata.SQLite.MaxOpenConnections,
		})
		if err != nil {
			return nil, nil, fmt.Errorf("open DOFS SQLite metadata: %w", err)
		}
		if err := metadata.Migrate(ctx); err != nil {
			_ = metadata.Close()
			return nil, nil, fmt.Errorf("migrate DOFS SQLite metadata: %w", err)
		}
		return metadata, metadata, nil
	case "postgres":
		sqlDatabase, err := database.DB()
		if err != nil {
			return nil, nil, fmt.Errorf("open DOFS PostgreSQL handle: %w", err)
		}
		metadata, err := dofspostgres.New(sqlDatabase)
		if err != nil {
			return nil, nil, err
		}
		if err := metadata.Migrate(ctx); err != nil {
			return nil, nil, fmt.Errorf("migrate DOFS PostgreSQL metadata: %w", err)
		}
		return metadata, nil, nil
	default:
		return nil, nil, fmt.Errorf("unsupported Domus DOFS metadata driver %q", cfg.DOFS.Metadata.Driver)
	}
}

func (r *Runtime) Close() {
	if r == nil {
		return
	}
	if r.Keys != nil {
		r.Keys.Close()
	}
	if r.metadataCloser != nil {
		_ = r.metadataCloser.Close()
		r.metadataCloser = nil
	}
}

func (r *Runtime) Check(ctx context.Context) error {
	if r == nil || r.Metadata == nil || r.Objects == nil || r.Keys == nil {
		return errors.New("Domus DOFS runtime is incomplete")
	}
	if checker, ok := r.Objects.(interface{ Check(context.Context) error }); ok {
		if err := checker.Check(ctx); err != nil {
			return err
		}
	}
	_, err := r.Metadata.ListNamespaces(ctx)
	return err
}

func (r *Runtime) Usage(ctx context.Context, userID string) (int64, int, error) {
	namespace, err := r.Metadata.GetNamespace(ctx, userID)
	if err != nil {
		return 0, 0, err
	}
	nodes, err := r.Metadata.ListState(ctx, userID, dofs.NodeStateReady)
	if err != nil {
		return 0, 0, err
	}
	files := 0
	for _, node := range nodes {
		if node.IsFile() {
			files++
		}
	}
	return namespace.UsedBytes, files, nil
}

// ReclaimNamespace performs one idempotent physical-cleanup pass. Deleted
// inode tombstones remain durable retry work when object storage is
// unavailable, an upload URL is still live, or a FUSE mount holds the
// namespace open.
func (r *Runtime) ReclaimNamespace(ctx context.Context, userID string) (dofs.ReclaimResult, error) {
	if r == nil || r.Metadata == nil || r.Objects == nil {
		return dofs.ReclaimResult{NamespaceID: userID}, errors.New("Domus DOFS runtime is incomplete")
	}
	return dofs.ReclaimDeleted(ctx, r.Metadata, r.Objects, userID)
}

// DeleteNamespace removes the authoritative metadata first, making every
// ciphertext generation unreachable, then reclaims the namespace's exact
// object prefix. A failed object cleanup leaves only encrypted garbage and is
// safe to retry with DeleteNamespaceObjects.
func (r *Runtime) DeleteNamespace(ctx context.Context, userID string) error {
	return dofs.DeleteNamespace(ctx, r.Metadata, r.Objects, userID)
}

func (r *Runtime) DeleteNamespaceObjects(ctx context.Context, userID string) error {
	root, err := dofs.ObjectRoot(userID)
	if err != nil {
		return err
	}
	if err := r.Objects.DeletePrefix(ctx, root); err != nil && !errors.Is(err, dofs.ErrObjectNotFound) {
		return fmt.Errorf("delete DOFS namespace objects: %w", err)
	}
	return nil
}

func (r *Runtime) EnsureAllUsers(ctx context.Context) error {
	users, err := r.Users.List()
	if err != nil {
		return fmt.Errorf("list Domus users for DOFS: %w", err)
	}
	for index := range users {
		if err := r.EnsureUser(ctx, &users[index]); err != nil {
			return err
		}
	}
	return nil
}

func (r *Runtime) EnsureUser(ctx context.Context, user *model.User) error {
	if user == nil {
		return errors.New("Domus DOFS user is required")
	}
	if err := dofs.ValidateNamespaceID(user.ID); err != nil {
		return fmt.Errorf("invalid Domus user ID for DOFS: %w", err)
	}
	wrappedKEK, err := hex.DecodeString(user.WrappedKEK)
	if err != nil || len(wrappedKEK) == 0 {
		return fmt.Errorf("decode DOFS namespace key for user %s", user.ID)
	}
	namespace, err := r.Metadata.GetNamespace(ctx, user.ID)
	if errors.Is(err, dofs.ErrNotFound) {
		namespace, err = r.Metadata.CreateNamespace(ctx, dofs.Namespace{
			ID: user.ID, Label: user.Username, WrappedKEK: wrappedKEK,
		})
		if errors.Is(err, dofs.ErrAlreadyExists) {
			namespace, err = r.Metadata.GetNamespace(ctx, user.ID)
		}
	}
	if err != nil {
		return fmt.Errorf("ensure DOFS namespace for user %s: %w", user.ID, err)
	}
	if !bytes.Equal(namespace.WrappedKEK, wrappedKEK) {
		return fmt.Errorf("DOFS namespace key differs from Domus user %s", user.ID)
	}
	controller, err := r.OpenControl(ctx, user.ID)
	if err != nil {
		return err
	}
	defer controller.Close()
	internal, err := ensureDirectory(ctx, controller, dofs.RootInode, ".domus", 0700)
	if err != nil {
		return fmt.Errorf("ensure Domus private root for %s: %w", user.ID, err)
	}
	for _, name := range []string{"trash", "thumbnails", "user"} {
		if _, err := ensureDirectory(ctx, controller, internal.Inode, name, 0700); err != nil {
			return fmt.Errorf("ensure Domus private directory %s for %s: %w", name, user.ID, err)
		}
	}
	return nil
}

func ensureDirectory(ctx context.Context, controller *dofs.Backend, parent uint64, name string, mode uint32) (dofs.Node, error) {
	node, err := controller.Lookup(ctx, parent, name)
	if err == nil {
		if !node.IsDir() {
			return dofs.Node{}, dofs.ErrTypeMismatch
		}
		return node, nil
	}
	if !errors.Is(err, dofs.ErrNotFound) {
		return dofs.Node{}, err
	}
	node, err = controller.CreateDirectory(ctx, parent, name, mode)
	if errors.Is(err, dofs.ErrAlreadyExists) {
		return controller.Lookup(ctx, parent, name)
	}
	return node, err
}

func (r *Runtime) OpenControl(ctx context.Context, userID string) (*dofs.Backend, error) {
	return dofs.NewBackend(ctx, userID, r.Metadata, r.Objects, r.Keys, dofs.BackendOptions{ControlPlane: true})
}
