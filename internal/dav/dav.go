// Package dav adapts the current visible DOFS namespace to read-only WebDAV.
package dav

import (
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"math"
	"mime"
	"net/http"
	"os"
	"path"
	"strings"
	"sync"
	"time"

	"github.com/willvar/dofs"
	"golang.org/x/net/webdav"

	"domus/internal/dofsbridge"
	"domus/internal/middleware"
)

const (
	Allow      = "OPTIONS, PROPFIND, GET, HEAD"
	readWindow = 1 << 20
)

func ServeHTTP(w http.ResponseWriter, r *http.Request, runtime *dofsbridge.Runtime, userID string) {
	w.Header().Set("Allow", Allow)
	w.Header().Set("DAV", "1") // No writable properties or locking support.
	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Security-Policy", "sandbox; default-src 'none'")
	switch r.Method {
	case http.MethodOptions:
		w.WriteHeader(http.StatusNoContent)
		return
	case "PROPFIND":
		// Deliberately disallow recursive walks; clients enumerate one directory
		// at a time rather than expanding an unbounded tree in one request.
		if depth := r.Header.Get("Depth"); depth != "0" && depth != "1" {
			w.Header().Set("Content-Type", "application/xml; charset=utf-8")
			w.WriteHeader(http.StatusForbidden)
			_, _ = io.WriteString(w, `<D:error xmlns:D="DAV:"><D:propfind-finite-depth/></D:error>`)
			return
		}
	case http.MethodGet, http.MethodHead:
		w.Header().Set("Content-Type", contentType(r.URL.Path))
		w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": path.Base(r.URL.Path)}))
	default:
		http.Error(w, "read-only WebDAV", http.StatusMethodNotAllowed)
		return
	}
	if runtime == nil {
		http.Error(w, "storage unavailable", http.StatusServiceUnavailable)
		return
	}
	backend, err := dofs.NewBackend(r.Context(), userID, runtime.Metadata, runtime.Objects, runtime.Keys, dofs.BackendOptions{})
	if err != nil {
		http.Error(w, "storage unavailable", http.StatusServiceUnavailable)
		return
	}
	defer backend.Close()
	handler := webdav.Handler{Prefix: "/dav", FileSystem: &filesystem{backend: backend}, LockSystem: webdav.NewMemLS()}
	handler.ServeHTTP(w, r)
}

type filesystem struct{ backend *dofs.Backend }

var _ webdav.FileSystem = (*filesystem)(nil)

func (*filesystem) Mkdir(context.Context, string, os.FileMode) error { return os.ErrPermission }
func (*filesystem) RemoveAll(context.Context, string) error          { return os.ErrPermission }
func (*filesystem) Rename(context.Context, string, string) error     { return os.ErrPermission }

func (fs *filesystem) lookup(ctx context.Context, name string) (dofs.Node, error) {
	if strings.ContainsAny(name, "\x00\\") {
		return dofs.Node{}, os.ErrNotExist
	}
	for _, component := range strings.Split(name, "/") {
		if component == "." || component == ".." {
			return dofs.Node{}, os.ErrNotExist
		}
	}
	clean, err := middleware.ResolveApplicationPath(name)
	if err != nil {
		return dofs.Node{}, os.ErrNotExist
	}
	node, err := fs.backend.GetNode(ctx, dofs.RootInode)
	if err != nil {
		return dofs.Node{}, fileError(err)
	}
	for _, component := range strings.Split(strings.Trim(clean, "/"), "/") {
		if component == "" {
			continue
		}
		if !visible(node) || !node.IsDir() {
			return dofs.Node{}, os.ErrNotExist
		}
		node, err = fs.backend.Lookup(ctx, node.Inode, component)
		if err != nil {
			return dofs.Node{}, fileError(err)
		}
	}
	if !visible(node) {
		return dofs.Node{}, os.ErrNotExist
	}
	return node, nil
}

func visible(node dofs.Node) bool {
	return node.State == dofs.NodeStateReady && node.DeletedAt == nil &&
		(node.IsDir() || (node.IsFile() && node.Generation > 0 && node.ObjectKey != ""))
}

func fileError(err error) error {
	if errors.Is(err, dofs.ErrNotFound) || errors.Is(err, dofs.ErrTypeMismatch) {
		return os.ErrNotExist
	}
	return err
}

func (fs *filesystem) Stat(ctx context.Context, name string) (os.FileInfo, error) {
	node, err := fs.lookup(ctx, name)
	if err != nil {
		return nil, err
	}
	return info{node: node}, nil
}

func (fs *filesystem) OpenFile(ctx context.Context, name string, flag int, _ os.FileMode) (webdav.File, error) {
	if flag != os.O_RDONLY {
		return nil, os.ErrPermission
	}
	node, err := fs.lookup(ctx, name)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(ctx)
	return &file{ctx: ctx, cancel: cancel, fs: fs, name: name, node: node}, nil
}

type info struct{ node dofs.Node }

func (i info) Name() string {
	if i.node.Inode == dofs.RootInode {
		return "/"
	}
	return i.node.Name
}
func (i info) Size() int64        { return i.node.Size }
func (i info) ModTime() time.Time { return i.node.ModifiedAt }
func (i info) IsDir() bool        { return i.node.IsDir() }
func (i info) Sys() any           { return nil }
func (i info) Mode() os.FileMode {
	if i.IsDir() {
		return os.ModeDir | 0555
	}
	return 0444
}
func (i info) ETag(context.Context) (string, error) {
	return fmt.Sprintf(`"%s-%x-%x"`, i.node.NamespaceID, i.node.Inode, i.node.Generation), nil
}
func (i info) ContentType(context.Context) (string, error) { return contentType(i.node.Name), nil }

func contentType(name string) string {
	if value := mime.TypeByExtension(path.Ext(name)); value != "" {
		return value
	}
	return "application/octet-stream"
}

type file struct {
	mu            sync.Mutex
	ctx           context.Context
	cancel        context.CancelFunc
	fs            *filesystem
	name          string
	node          dofs.Node // Immutable generation snapshot: never mix versions mid-read.
	position      int64
	window        []byte
	windowOffset  int64
	entries       []os.FileInfo
	directoryRead bool
	closed        bool
}

var _ webdav.File = (*file)(nil)

// Use WebDAV's property extension point rather than rewriting XML responses:
// the library's default supportedlock property otherwise advertises write locks.
func (*file) DeadProps() (map[xml.Name]webdav.Property, error) {
	name := xml.Name{Space: "DAV:", Local: "supportedlock"}
	return map[xml.Name]webdav.Property{name: {XMLName: name}}, nil
}
func (*file) Patch([]webdav.Proppatch) ([]webdav.Propstat, error) { return nil, os.ErrPermission }

func (f *file) Close() error {
	// ServeContent's multipart producer may still be in Read on disconnect.
	// Cancel before taking the lock, then wait for that read before clearing
	// plaintext or allowing the request's deferred backend.Close to run.
	f.cancel()
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.closed {
		return nil
	}
	f.closed = true
	dofs.Clear(f.window)
	f.window, f.entries = nil, nil
	return nil
}
func (*file) Write([]byte) (int, error) { return 0, os.ErrPermission }
func (f *file) Stat() (os.FileInfo, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.closed {
		return nil, os.ErrClosed
	}
	return info{node: f.node}, nil
}

func (f *file) Read(destination []byte) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.closed {
		return 0, os.ErrClosed
	}
	if err := f.ctx.Err(); err != nil {
		return 0, err
	}
	if f.node.IsDir() {
		return 0, os.ErrInvalid
	}
	if len(destination) == 0 {
		return 0, nil
	}
	if f.position >= f.node.Size {
		return 0, io.EOF
	}
	if len(f.window) == 0 || f.position < f.windowOffset || f.position >= f.windowOffset+int64(len(f.window)) {
		dofs.Clear(f.window)
		f.window = nil
		f.windowOffset = f.position / readWindow * readWindow
		var err error
		// Fixed-size DOFS range reads bound plaintext memory independently of file
		// size. There is no adapter-owned pipe/goroutine or full-file temp/cache.
		f.window, err = f.fs.backend.ReadAt(f.ctx, f.node, f.windowOffset, readWindow)
		if err != nil {
			return 0, err
		}
		if len(f.window) == 0 {
			return 0, io.ErrUnexpectedEOF
		}
	}
	n := copy(destination, f.window[f.position-f.windowOffset:])
	f.position += int64(n)
	return n, nil
}

func (f *file) Seek(offset int64, whence int) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.closed {
		return 0, os.ErrClosed
	}
	base := int64(0)
	switch whence {
	case io.SeekStart:
	case io.SeekCurrent:
		base = f.position
	case io.SeekEnd:
		base = f.node.Size
	default:
		return 0, os.ErrInvalid
	}
	if (offset > 0 && base > math.MaxInt64-offset) || (offset < 0 && offset < -base) {
		return 0, os.ErrInvalid
	}
	f.position = base + offset
	return f.position, nil
}

func (f *file) Readdir(count int) ([]os.FileInfo, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.closed {
		return nil, os.ErrClosed
	}
	if err := f.ctx.Err(); err != nil {
		return nil, err
	}
	if !f.node.IsDir() {
		return nil, os.ErrInvalid
	}
	if !f.directoryRead {
		nodes, err := f.fs.backend.List(f.ctx, f.node.Inode)
		if err != nil {
			return nil, err
		}
		// DOFS currently lists a directory as one metadata slice. This is O(n)
		// in this directory only; add metadata pagination if that becomes large.
		f.entries = make([]os.FileInfo, 0, len(nodes))
		for _, node := range nodes {
			if !visible(node) {
				continue
			}
			if _, err := middleware.ResolveApplicationPath(path.Join(f.name, node.Name)); err != nil {
				continue
			}
			f.entries = append(f.entries, info{node: node})
		}
		f.directoryRead = true
	}
	if count <= 0 {
		result := f.entries
		f.entries = nil
		return result, nil
	}
	if len(f.entries) == 0 {
		return nil, io.EOF
	}
	if count > len(f.entries) {
		count = len(f.entries)
	}
	result := f.entries[:count]
	f.entries = f.entries[count:]
	return result, nil
}
