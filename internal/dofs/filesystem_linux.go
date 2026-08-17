//go:build linux

package dofs

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/hanwen/go-fuse/v2/fs"
	"github.com/hanwen/go-fuse/v2/fuse"
	"golang.org/x/sys/unix"

	"domus/internal/model"
)

const (
	rootInode         = 1
	readOnlyDirPerms  = 0500
	readOnlyFilePerms = 0400
	writableDirPerms  = 0700
	writableFilePerms = 0600
)

type MountOptions struct {
	Debug      bool
	AllowOther bool
	UID        uint32
	GID        uint32
	Writable   bool
}

// Node is an inode in a single-user DOFS mount. A nil record is the synthetic
// mount root; all other nodes correspond to an existing file row.
type Node struct {
	fs.Inode
	backend   *Backend
	recordMu  sync.RWMutex
	record    *model.FileRecord
	mountedAt time.Time
}

func NewRoot(backend *Backend) *Node {
	return &Node{backend: backend, mountedAt: time.Now()}
}

func (n *Node) isDirectory() bool {
	record := n.recordSnapshot()
	return record == nil || record.IsDir
}

func (n *Node) directoryKey() string {
	record := n.recordSnapshot()
	if record == nil {
		return n.backend.RootKey()
	}
	return record.Path
}

func (n *Node) recordSnapshot() *model.FileRecord {
	n.recordMu.RLock()
	defer n.recordMu.RUnlock()
	if n.record == nil {
		return nil
	}
	record := *n.record
	return &record
}

func (n *Node) setRecord(record *model.FileRecord) {
	n.recordMu.Lock()
	defer n.recordMu.Unlock()
	copy := *record
	n.record = &copy
}

func stableAttr(record *model.FileRecord) fs.StableAttr {
	if record == nil {
		return fs.StableAttr{Mode: fuse.S_IFDIR, Ino: rootInode}
	}
	mode := uint32(fuse.S_IFREG)
	if record.IsDir {
		mode = fuse.S_IFDIR
	}
	return fs.StableAttr{Mode: mode, Ino: uint64(record.ID) + 2, Gen: 1}
}

func fillAttr(out *fuse.Attr, record *model.FileRecord, mountedAt time.Time, writable bool) {
	directoryPerms := uint32(readOnlyDirPerms)
	filePerms := uint32(readOnlyFilePerms)
	if writable {
		directoryPerms = writableDirPerms
		filePerms = writableFilePerms
	}
	if record == nil {
		out.Mode = fuse.S_IFDIR | directoryPerms
		out.Nlink = 2
		out.SetTimes(&mountedAt, &mountedAt, &mountedAt)
		return
	}

	createdAt := record.CreatedAt
	updatedAt := record.UpdatedAt
	if record.IsDir {
		out.Mode = fuse.S_IFDIR | directoryPerms
		out.Nlink = 2
	} else {
		out.Mode = fuse.S_IFREG | filePerms
		out.Nlink = 1
		out.Size = uint64(record.Size)
	}
	out.SetTimes(&createdAt, &updatedAt, &updatedAt)
}

func (n *Node) Getattr(ctx context.Context, handle fs.FileHandle, out *fuse.AttrOut) syscall.Errno {
	if file, ok := handle.(*fileHandle); ok {
		return file.Getattr(ctx, out)
	}
	fillAttr(&out.Attr, n.recordSnapshot(), n.mountedAt, n.backend.Writable())
	return 0
}

func (n *Node) Lookup(ctx context.Context, name string, out *fuse.EntryOut) (*fs.Inode, syscall.Errno) {
	if !n.isDirectory() {
		return nil, syscall.ENOTDIR
	}
	record, err := n.backend.LookupChild(n.directoryKey(), name)
	if err != nil {
		return nil, errno(err)
	}

	if existing := n.GetChild(name); existing != nil {
		if operations, ok := existing.Operations().(*Node); ok {
			operations.setRecord(record)
			fillAttr(&out.Attr, record, operations.mountedAt, n.backend.Writable())
		}
		return existing, 0
	}

	childNode := &Node{backend: n.backend, record: record, mountedAt: n.mountedAt}
	child := n.NewInode(ctx, childNode, stableAttr(record))
	fillAttr(&out.Attr, record, n.mountedAt, n.backend.Writable())
	return child, 0
}

func (n *Node) Readdir(_ context.Context) (fs.DirStream, syscall.Errno) {
	if !n.isDirectory() {
		return nil, syscall.ENOTDIR
	}
	records, err := n.backend.ListChildren(n.directoryKey())
	if err != nil {
		return nil, errno(err)
	}

	entries := make([]fuse.DirEntry, 0, len(records))
	for i := range records {
		record := &records[i]
		attributes := stableAttr(record)
		entries = append(entries, fuse.DirEntry{
			Name: record.Name,
			Mode: attributes.Mode,
			Ino:  attributes.Ino,
		})
	}
	return fs.NewListDirStream(entries), 0
}

func (n *Node) Mkdir(ctx context.Context, name string, _ uint32, out *fuse.EntryOut) (*fs.Inode, syscall.Errno) {
	if !n.backend.Writable() {
		return nil, syscall.EROFS
	}
	if !n.isDirectory() {
		return nil, syscall.ENOTDIR
	}
	record, err := n.backend.CreateDirectory(n.directoryKey(), name)
	if err != nil {
		return nil, errno(err)
	}
	childNode := &Node{backend: n.backend, record: record, mountedAt: n.mountedAt}
	child := n.NewInode(ctx, childNode, stableAttr(record))
	fillAttr(&out.Attr, record, n.mountedAt, true)
	return child, 0
}

func (n *Node) Create(ctx context.Context, name string, flags uint32, _ uint32, out *fuse.EntryOut) (*fs.Inode, fs.FileHandle, uint32, syscall.Errno) {
	if !n.backend.Writable() {
		return nil, nil, 0, syscall.EROFS
	}
	if !n.isDirectory() {
		return nil, nil, 0, syscall.ENOTDIR
	}
	record, err := n.backend.CreateFile(n.directoryKey(), name)
	if err != nil {
		return nil, nil, 0, errno(err)
	}
	childNode := &Node{backend: n.backend, record: record, mountedAt: n.mountedAt}
	child := n.NewInode(ctx, childNode, stableAttr(record))
	if err := n.backend.retainFile(record.ID); err != nil {
		_ = n.backend.RemoveChild(n.directoryKey(), name, false)
		return nil, nil, 0, errno(err)
	}
	handle := &fileHandle{
		backend: n.backend, node: childNode, record: *record,
		writable: flags&syscall.O_ACCMODE != syscall.O_RDONLY,
		retained: true,
	}
	fillAttr(&out.Attr, record, n.mountedAt, true)
	return child, handle, 0, 0
}

func (n *Node) Unlink(_ context.Context, name string) syscall.Errno {
	if !n.backend.Writable() {
		return syscall.EROFS
	}
	if !n.isDirectory() {
		return syscall.ENOTDIR
	}
	return errno(n.backend.RemoveChild(n.directoryKey(), name, false))
}

func (n *Node) Rmdir(_ context.Context, name string) syscall.Errno {
	if !n.backend.Writable() {
		return syscall.EROFS
	}
	if !n.isDirectory() {
		return syscall.ENOTDIR
	}
	return errno(n.backend.RemoveChild(n.directoryKey(), name, true))
}

func (n *Node) Rename(_ context.Context, name string, newParent fs.InodeEmbedder, newName string, flags uint32) syscall.Errno {
	if !n.backend.Writable() {
		return syscall.EROFS
	}
	if !n.isDirectory() {
		return syscall.ENOTDIR
	}
	if flags & ^uint32(unix.RENAME_NOREPLACE) != 0 {
		return syscall.ENOTSUP
	}
	newParentNode, ok := newParent.(*Node)
	if !ok || !newParentNode.isDirectory() || newParentNode.backend != n.backend {
		return syscall.EXDEV
	}
	oldPrefix := ""
	var cachedSource *Node
	if inode := n.GetChild(name); inode != nil {
		cachedSource, _ = inode.Operations().(*Node)
		if cachedSource != nil {
			if snapshot := cachedSource.recordSnapshot(); snapshot != nil && snapshot.IsDir {
				oldPrefix = snapshot.Path
			}
		}
	}
	renamed, err := n.backend.RenameChild(
		n.directoryKey(), name, newParentNode.directoryKey(), newName,
		flags&uint32(unix.RENAME_NOREPLACE) != 0,
	)
	if err != nil {
		return errno(err)
	}
	if cachedSource != nil {
		cachedSource.setRecord(renamed)
		if renamed.IsDir && oldPrefix != "" && oldPrefix != renamed.Path {
			cachedSource.rewriteDescendantPaths(oldPrefix, renamed.Path)
		}
	}
	return 0
}

func (n *Node) rewriteDescendantPaths(oldPrefix, newPrefix string) {
	for _, child := range n.Children() {
		operations, ok := child.Operations().(*Node)
		if !ok {
			continue
		}
		record := operations.recordSnapshot()
		if record != nil {
			if strings.HasPrefix(record.Path, oldPrefix) {
				record.Path = newPrefix + strings.TrimPrefix(record.Path, oldPrefix)
			}
			if strings.HasPrefix(record.Parent, oldPrefix) {
				record.Parent = newPrefix + strings.TrimPrefix(record.Parent, oldPrefix)
			}
			operations.setRecord(record)
		}
		operations.rewriteDescendantPaths(oldPrefix, newPrefix)
	}
}

func (n *Node) Open(_ context.Context, flags uint32) (fs.FileHandle, uint32, syscall.Errno) {
	if n.isDirectory() {
		return nil, 0, syscall.EISDIR
	}
	recordPtr := n.recordSnapshot()
	if recordPtr == nil {
		return nil, 0, syscall.EIO
	}
	record := *recordPtr
	wantsWrite := flags&syscall.O_ACCMODE != syscall.O_RDONLY
	if wantsWrite && !n.backend.Writable() {
		return nil, 0, syscall.EROFS
	}
	if err := n.backend.retainFile(record.ID); err != nil {
		return nil, 0, errno(err)
	}
	handle := &fileHandle{backend: n.backend, node: n, record: record, writable: wantsWrite, retained: true}
	if !wantsWrite {
		return handle, 0, 0
	}
	if flags&syscall.O_TRUNC != 0 {
		writeSession, err := handle.ensureWrite()
		if err != nil {
			_ = n.backend.releaseFile(record.ID)
			return nil, 0, errno(err)
		}
		if err := writeSession.Truncate(0); err != nil {
			writeSession.abort()
			_ = n.backend.releaseFile(record.ID)
			return nil, 0, errno(err)
		}
	}
	return handle, 0, 0
}

type fileHandle struct {
	backend    *Backend
	node       *Node
	record     model.FileRecord
	writable   bool
	writeMu    sync.Mutex
	write      *WriteSession
	retained   bool
	release    sync.Once
	releaseErr syscall.Errno
}

func (h *fileHandle) ensureWrite() (*WriteSession, error) {
	h.writeMu.Lock()
	defer h.writeMu.Unlock()
	if h.write != nil {
		return h.write, nil
	}
	if !h.writable {
		return nil, ErrReadOnly
	}
	record := h.record
	if h.node != nil {
		if latest := h.node.recordSnapshot(); latest != nil {
			record = *latest
		}
	}
	var onCommit func(*model.FileRecord)
	if h.node != nil {
		onCommit = h.node.setRecord
	}
	writeSession, err := h.backend.OpenWrite(&record, onCommit)
	if err != nil {
		return nil, err
	}
	h.record = record
	h.write = writeSession
	return writeSession, nil
}

func (h *fileHandle) writeSession() *WriteSession {
	h.writeMu.Lock()
	defer h.writeMu.Unlock()
	return h.write
}

func (h *fileHandle) Read(_ context.Context, dest []byte, offset int64) (fuse.ReadResult, syscall.Errno) {
	writeSession := h.writeSession()
	if h.writable && writeSession == nil {
		var err error
		writeSession, err = h.ensureWrite()
		if err != nil {
			return nil, errno(err)
		}
	}
	if writeSession != nil {
		read, err := writeSession.ReadAt(dest, offset)
		if err != nil && !errors.Is(err, io.EOF) {
			return nil, errno(err)
		}
		return fuse.ReadResultData(dest[:read]), 0
	}
	data, err := h.backend.ReadAt(&h.record, offset, len(dest))
	if err != nil {
		return nil, errno(err)
	}
	return fuse.ReadResultData(data), 0
}

func (h *fileHandle) Write(_ context.Context, data []byte, offset int64) (uint32, syscall.Errno) {
	writeSession, err := h.ensureWrite()
	if err != nil {
		return 0, errno(err)
	}
	written, err := writeSession.WriteAt(data, offset)
	if err != nil {
		return uint32(written), errno(err)
	}
	return uint32(written), 0
}

func (h *fileHandle) Flush(_ context.Context) syscall.Errno {
	writeSession := h.writeSession()
	if writeSession == nil {
		return 0
	}
	return errno(writeSession.Commit())
}

func (h *fileHandle) Fsync(_ context.Context, _ uint32) syscall.Errno {
	return h.Flush(context.Background())
}

func (h *fileHandle) Release(_ context.Context) syscall.Errno {
	h.release.Do(func() {
		if writeSession := h.writeSession(); writeSession != nil {
			h.releaseErr = errno(writeSession.Close())
		}
		if h.retained {
			if releaseErr := errno(h.backend.releaseFile(h.record.ID)); h.releaseErr == 0 {
				h.releaseErr = releaseErr
			}
			h.retained = false
		}
	})
	return h.releaseErr
}

func (h *fileHandle) Getattr(_ context.Context, out *fuse.AttrOut) syscall.Errno {
	record := h.record
	if h.node != nil {
		if latest := h.node.recordSnapshot(); latest != nil {
			record = *latest
		}
	}
	mountedAt := time.Now()
	if h.node != nil {
		mountedAt = h.node.mountedAt
	}
	fillAttr(&out.Attr, &record, mountedAt, h.backend.Writable())
	if writeSession := h.writeSession(); writeSession != nil {
		size, err := writeSession.Size()
		if err != nil {
			return errno(err)
		}
		out.Size = uint64(size)
	}
	return 0
}

func (h *fileHandle) Setattr(_ context.Context, input *fuse.SetAttrIn, out *fuse.AttrOut) syscall.Errno {
	if input.Valid & ^uint32(fuse.FATTR_SIZE|fuse.FATTR_FH|fuse.FATTR_LOCKOWNER|fuse.FATTR_KILL_SUIDGID) != 0 {
		return syscall.EPERM
	}
	size, ok := input.GetSize()
	if !ok {
		return syscall.EPERM
	}
	writeSession, err := h.ensureWrite()
	if err != nil {
		return errno(err)
	}
	if err := writeSession.Truncate(int64(size)); err != nil {
		return errno(err)
	}
	return h.Getattr(context.Background(), out)
}

func (n *Node) Setattr(ctx context.Context, handle fs.FileHandle, input *fuse.SetAttrIn, out *fuse.AttrOut) syscall.Errno {
	if input.Valid & ^uint32(fuse.FATTR_SIZE|fuse.FATTR_FH|fuse.FATTR_LOCKOWNER|fuse.FATTR_KILL_SUIDGID) != 0 {
		return syscall.EPERM
	}
	if file, ok := handle.(*fileHandle); ok {
		return file.Setattr(ctx, input, out)
	}
	size, ok := input.GetSize()
	if !ok {
		return syscall.EPERM
	}
	if !n.backend.Writable() || n.isDirectory() {
		return syscall.EROFS
	}
	record := n.recordSnapshot()
	if record == nil {
		return syscall.EIO
	}
	if err := n.backend.retainFile(record.ID); err != nil {
		return errno(err)
	}
	defer func() { _ = n.backend.releaseFile(record.ID) }()
	session, err := n.backend.OpenWrite(record, n.setRecord)
	if err != nil {
		return errno(err)
	}
	if err := session.Truncate(int64(size)); err != nil {
		session.abort()
		return errno(err)
	}
	if err := session.Close(); err != nil {
		return errno(err)
	}
	fillAttr(&out.Attr, n.recordSnapshot(), n.mountedAt, true)
	return 0
}

func errno(err error) syscall.Errno {
	switch {
	case err == nil:
		return 0
	case errors.Is(err, ErrNotFound), errors.Is(err, ErrNotReady):
		return syscall.ENOENT
	case errors.Is(err, ErrInvalidName):
		return syscall.EINVAL
	case errors.Is(err, model.ErrFileExists):
		return syscall.EEXIST
	case errors.Is(err, model.ErrDirectoryNotEmpty):
		return syscall.ENOTEMPTY
	case errors.Is(err, ErrNotDirectory):
		return syscall.ENOTDIR
	case errors.Is(err, model.ErrNodeTypeMismatch):
		return syscall.EINVAL
	case errors.Is(err, ErrIsDirectory):
		return syscall.EISDIR
	case errors.Is(err, ErrReadOnly):
		return syscall.EROFS
	case errors.Is(err, ErrWriteConflict):
		return syscall.ESTALE
	case errors.Is(err, ErrHandleClosed):
		return syscall.EBADF
	default:
		return syscall.EIO
	}
}

// Mount exposes backend at mountpoint and returns after the kernel mount is
// ready. The caller owns server.Wait/Unmount and the backend lifecycle.
func Mount(mountpoint string, backend *Backend, options MountOptions) (*fuse.Server, error) {
	if backend == nil {
		return nil, errors.New("DOFS backend is required")
	}
	if options.Writable && !backend.Writable() {
		return nil, errors.New("DOFS writeback is not enabled")
	}
	oneSecond := time.Second
	mountOptions := []string{"default_permissions", "nosuid", "nodev"}
	if !options.Writable {
		mountOptions = append(mountOptions, "ro")
	}
	fsOptions := &fs.Options{
		MountOptions: fuse.MountOptions{
			AllowOther:    options.AllowOther,
			Options:       mountOptions,
			FsName:        fmt.Sprintf("dofs:%s", backend.UserID()),
			Name:          "dofs",
			Debug:         options.Debug,
			DisableXAttrs: true,
			MaxWrite:      128 * 1024,
			MaxReadAhead:  128 * 1024,
		},
		EntryTimeout:    &oneSecond,
		AttrTimeout:     &oneSecond,
		NegativeTimeout: &oneSecond,
		UID:             options.UID,
		GID:             options.GID,
		RootStableAttr:  &fs.StableAttr{Ino: rootInode},
	}
	return fs.Mount(mountpoint, NewRoot(backend), fsOptions)
}

var (
	_ fs.NodeGetattrer = (*Node)(nil)
	_ fs.NodeSetattrer = (*Node)(nil)
	_ fs.NodeLookuper  = (*Node)(nil)
	_ fs.NodeReaddirer = (*Node)(nil)
	_ fs.NodeOpener    = (*Node)(nil)
	_ fs.NodeMkdirer   = (*Node)(nil)
	_ fs.NodeCreater   = (*Node)(nil)
	_ fs.NodeUnlinker  = (*Node)(nil)
	_ fs.NodeRmdirer   = (*Node)(nil)
	_ fs.NodeRenamer   = (*Node)(nil)
	_ fs.FileReader    = (*fileHandle)(nil)
	_ fs.FileWriter    = (*fileHandle)(nil)
	_ fs.FileFlusher   = (*fileHandle)(nil)
	_ fs.FileFsyncer   = (*fileHandle)(nil)
	_ fs.FileReleaser  = (*fileHandle)(nil)
	_ fs.FileGetattrer = (*fileHandle)(nil)
	_ fs.FileSetattrer = (*fileHandle)(nil)
)
