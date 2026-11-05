package dav

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/willvar/dofs"
	metadatamemory "github.com/willvar/dofs/metadata/memory"
	objectmemory "github.com/willvar/dofs/object/memory"

	"domus/internal/dofsbridge"
	"domus/internal/httpx"
)

func fixture(t *testing.T) (*dofsbridge.Runtime, *dofs.Backend, []byte) {
	t.Helper()
	keys, err := dofs.NewEnvelopeKeyProvider(bytes.Repeat([]byte{0x42}, dofs.KeySize))
	if err != nil {
		t.Fatal(err)
	}
	wrapped, err := keys.WrapNewNamespaceKey()
	if err != nil {
		t.Fatal(err)
	}
	metadata := metadatamemory.New()
	for _, id := range []string{"alice", "bob"} {
		if _, err := metadata.CreateNamespace(t.Context(), dofs.Namespace{ID: id, WrappedKEK: wrapped}); err != nil {
			t.Fatal(err)
		}
	}
	runtime := &dofsbridge.Runtime{Metadata: metadata, Objects: objectmemory.New(), Keys: keys}
	t.Cleanup(runtime.Close)
	control, err := runtime.OpenControl(t.Context(), "alice")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = control.Close() })
	plaintext := bytes.Repeat([]byte("original file bytes; not ciphertext\n"), 100003)
	putFile(t, runtime, control, dofs.RootInode, "文档 & notes.txt", plaintext, false)
	private, err := control.CreateDirectory(t.Context(), dofs.RootInode, ".domus", 0700)
	if err != nil {
		t.Fatal(err)
	}
	putFile(t, runtime, control, private.Inode, "secret.txt", []byte("internal secret"), false)
	if _, err := control.CreateDirectory(t.Context(), dofs.RootInode, "__trash__", 0755); err != nil {
		t.Fatal(err)
	}
	if _, err := control.PrepareDirectUpload(t.Context(), dofs.DirectUploadRequest{ID: "pending", Parent: dofs.RootInode, Name: "pending.txt", Size: 3, FileKey: bytes.Repeat([]byte{0x33}, dofs.KeySize)}); err != nil {
		t.Fatal(err)
	}
	return runtime, control, plaintext
}

func putFile(t *testing.T, runtime *dofsbridge.Runtime, control *dofs.Backend, parent uint64, name string, plaintext []byte, replace bool) dofs.Node {
	t.Helper()
	key := bytes.Repeat([]byte{0x33}, dofs.KeySize)
	upload, err := control.PrepareDirectUpload(t.Context(), dofs.DirectUploadRequest{ID: uuid.NewString(), Parent: parent, Name: name, Size: int64(len(plaintext)), FileKey: key, Replace: replace})
	if err != nil {
		t.Fatal(err)
	}
	var ciphertext bytes.Buffer
	if err := dofs.EncryptStream(key, bytes.NewReader(plaintext), &ciphertext); err != nil {
		t.Fatal(err)
	}
	if err := runtime.Objects.Put(t.Context(), upload.ObjectKey, bytes.NewReader(ciphertext.Bytes()), int64(ciphertext.Len())); err != nil {
		t.Fatal(err)
	}
	node, err := control.CommitDirectUpload(t.Context(), upload.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := control.AcknowledgeDirectUpload(t.Context(), upload.ID); err != nil {
		t.Fatal(err)
	}
	return node
}

func request(t *testing.T, runtime *dofsbridge.Runtime, method, target string, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(method, target, nil)
	for key, value := range headers {
		r.Header.Set(key, value)
	}
	w := httptest.NewRecorder()
	httpx.ServerMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { ServeHTTP(w, r, runtime, "alice") }), "", nil).ServeHTTP(w, r)
	return w
}

func TestReadOnlyDAVAndCurrentGeneration(t *testing.T) {
	runtime, control, plaintext := fixture(t)
	name := "/dav/%E6%96%87%E6%A1%A3%20%26%20notes.txt"
	w := request(t, runtime, "GET", name, map[string]string{"Accept-Encoding": "gzip"})
	if w.Code != 200 || !bytes.Equal(w.Body.Bytes(), plaintext) {
		t.Fatalf("GET status %d; bytes %d", w.Code, w.Body.Len())
	}
	etag := w.Header().Get("ETag")
	modified := w.Header().Get("Last-Modified")
	if etag == "" || modified == "" || w.Header().Get("Content-Encoding") != "" {
		t.Fatalf("headers %v", w.Header())
	}
	for _, test := range []struct {
		method  string
		headers map[string]string
		status  int
		body    []byte
	}{
		{"HEAD", nil, 200, nil},
		{"GET", map[string]string{"Range": "bytes=65530-65570"}, 206, plaintext[65530:65571]},
		{"GET", map[string]string{"Range": "bytes=-13"}, 206, plaintext[len(plaintext)-13:]},
		{"GET", map[string]string{"Range": "bytes=99999999-"}, 416, nil},
		{"GET", map[string]string{"If-None-Match": etag}, 304, nil},
		{"GET", map[string]string{"If-Modified-Since": modified}, 304, nil},
		{"GET", map[string]string{"If-Match": `"wrong"`}, 412, nil},
		{"GET", map[string]string{"Range": "bytes=0-3", "If-Range": etag}, 206, plaintext[:4]},
		{"GET", map[string]string{"Range": "bytes=0-3", "If-Range": `"wrong"`}, 200, plaintext},
	} {
		t.Run(test.method+test.headers["Range"]+test.headers["If-None-Match"]+test.headers["If-Match"]+test.headers["If-Range"], func(t *testing.T) {
			w := request(t, runtime, test.method, name, test.headers)
			if w.Code != test.status {
				t.Fatalf("status %d: %s", w.Code, w.Body)
			}
			if test.body != nil && !bytes.Equal(w.Body.Bytes(), test.body) {
				t.Fatalf("body %d != %d", w.Body.Len(), len(test.body))
			}
			if (test.method == "HEAD" || test.status == 304) && w.Body.Len() != 0 {
				t.Fatal("unexpected body")
			}
		})
	}
	for _, method := range []string{"PUT", "POST", "PATCH", "DELETE", "MKCOL", "COPY", "MOVE", "LOCK", "UNLOCK", "PROPPATCH"} {
		w := request(t, runtime, method, name, nil)
		if w.Code != 405 || w.Header().Get("Allow") != Allow {
			t.Fatalf("%s: %d %v", method, w.Code, w.Header())
		}
	}
	w = request(t, runtime, "OPTIONS", "/dav/", nil)
	if w.Code != 204 || w.Header().Get("DAV") != "1" || w.Header().Get("Allow") != Allow {
		t.Fatalf("OPTIONS %d %v", w.Code, w.Header())
	}
	w = request(t, runtime, "PROPFIND", "/dav/", map[string]string{"Depth": "1"})
	if w.Code != 207 || !strings.Contains(w.Body.String(), "文档 &amp; notes.txt") || strings.Contains(w.Body.String(), ".domus") || strings.Contains(w.Body.String(), "pending.txt") || !strings.Contains(w.Body.String(), "__trash__") {
		t.Fatalf("PROPFIND %d: %s", w.Code, w.Body)
	}
	if strings.Contains(w.Body.String(), "lockentry") {
		t.Fatal("read-only properties advertise writable locks")
	}
	w = request(t, runtime, "PROPFIND", "/dav/", map[string]string{"Depth": "infinity"})
	if w.Code != 403 {
		t.Fatalf("unbounded PROPFIND %d", w.Code)
	}
	for _, target := range []string{"/dav/.domus/secret.txt", "/dav/.user/secret.txt", "/dav/%2Edomus/secret.txt", "/dav/pending.txt", "/dav/missing.txt"} {
		w := request(t, runtime, "GET", target, nil)
		if w.Code != 404 {
			t.Fatalf("private or missing %q: %d", target, w.Code)
		}
	}
	putFile(t, runtime, control, dofs.RootInode, "文档 & notes.txt", []byte("new generation"), true)
	w = request(t, runtime, "GET", name, map[string]string{"If-None-Match": etag})
	if w.Code != 200 || w.Body.String() != "new generation" || w.Header().Get("ETag") == etag {
		t.Fatalf("new generation %d %s", w.Code, w.Body)
	}
	private, err := control.Lookup(t.Context(), dofs.RootInode, ".domus")
	if err != nil {
		t.Fatal(err)
	}
	node, err := control.Lookup(t.Context(), dofs.RootInode, "文档 & notes.txt")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := control.Rename(t.Context(), node.Inode, private.Inode, "trashed.txt", true); err != nil {
		t.Fatal(err)
	}
	if w := request(t, runtime, "GET", name, nil); w.Code != 404 {
		t.Fatal("trashed file remained reachable")
	}
}

func TestFilesystemBoundaryAndNoContentReadsForMetadata(t *testing.T) {
	runtime, _, _ := fixture(t)
	objects := &observedObjects{ObjectStore: runtime.Objects}
	runtime.Objects = objects
	for _, method := range []string{"HEAD", "PROPFIND"} {
		w := request(t, runtime, method, "/dav/%E6%96%87%E6%A1%A3%20%26%20notes.txt", map[string]string{"Depth": "0"})
		if w.Code != 200 && w.Code != 207 {
			t.Fatalf("%s: %d", method, w.Code)
		}
	}
	if objects.reads.Load() != 0 {
		t.Fatal("metadata or HEAD decrypted content")
	}
	backend, err := dofs.NewBackend(t.Context(), "alice", runtime.Metadata, runtime.Objects, runtime.Keys, dofs.BackendOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer backend.Close()
	if backend.CanMutate() {
		t.Fatal("DAV backend can mutate")
	}
	fs := &filesystem{backend: backend}
	for _, name := range []string{"/.domus/secret.txt", "/.domus/../文档 & notes.txt", "/../文档 & notes.txt", "/.user", "/a\\b"} {
		if _, err := fs.Stat(t.Context(), name); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("path %q: %v", name, err)
		}
	}
	if err := fs.Mkdir(t.Context(), "/new", 0700); !errors.Is(err, os.ErrPermission) {
		t.Fatal(err)
	}
	if err := fs.RemoveAll(t.Context(), "/"); !errors.Is(err, os.ErrPermission) {
		t.Fatal(err)
	}
	if err := fs.Rename(t.Context(), "/a", "/b"); !errors.Is(err, os.ErrPermission) {
		t.Fatal(err)
	}
	if _, err := fs.OpenFile(t.Context(), "/文档 & notes.txt", os.O_RDWR, 0); !errors.Is(err, os.ErrPermission) {
		t.Fatal(err)
	}
	opened, err := fs.OpenFile(t.Context(), "/文档 & notes.txt", os.O_RDONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := opened.Write([]byte("overwrite")); !errors.Is(err, os.ErrPermission) {
		t.Fatal(err)
	}
	if _, err := opened.Seek(-1, io.SeekStart); !errors.Is(err, os.ErrInvalid) {
		t.Fatal(err)
	}
	buffer := make([]byte, readWindow*2)
	if n, err := opened.Read(buffer); err != nil || n > readWindow || objects.maxRange.Load() > readWindow+2048 {
		t.Fatalf("read %d max range %d: %v", n, objects.maxRange.Load(), err)
	}
	if err := opened.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := opened.Read(buffer); !errors.Is(err, os.ErrClosed) {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/dav/文档%20%26%20notes.txt", nil)
	ServeHTTP(w, r, runtime, "bob")
	if w.Code != 404 {
		t.Fatal("cross-user file visible")
	}
}

type observedObjects struct {
	dofs.ObjectStore
	reads    atomic.Int64
	active   atomic.Int64
	maxRange atomic.Int64
	block    atomic.Bool
	entered  chan struct{}
	once     sync.Once
}

func (o *observedObjects) GetRange(ctx context.Context, key string, start, end int64) (io.ReadCloser, error) {
	o.reads.Add(1)
	length := end - start + 1
	for previous := o.maxRange.Load(); length > previous; previous = o.maxRange.Load() {
		if o.maxRange.CompareAndSwap(previous, length) {
			break
		}
	}
	if o.block.Load() && start > readWindow {
		o.once.Do(func() { close(o.entered) })
		<-ctx.Done()
		return nil, ctx.Err()
	}
	reader, err := o.ObjectStore.GetRange(ctx, key, start, end)
	if err != nil {
		return nil, err
	}
	o.active.Add(1)
	return &observedReader{ReadCloser: reader, active: &o.active}, nil
}

type observedReader struct {
	io.ReadCloser
	active *atomic.Int64
}

func (r *observedReader) Close() error { r.active.Add(-1); return r.ReadCloser.Close() }

func TestClientDisconnectCancelsDOFSRangeAndClosesReaders(t *testing.T) {
	for _, requestRange := range []string{"", "bytes=0-1048575,2097152-3145727"} {
		t.Run(requestRange, func(t *testing.T) {
			runtime, _, _ := fixture(t)
			objects := &observedObjects{ObjectStore: runtime.Objects, entered: make(chan struct{})}
			objects.block.Store(true)
			runtime.Objects = objects
			done := make(chan struct{})
			server := httptest.NewServer(httpx.ServerMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				defer close(done)
				ServeHTTP(w, r, runtime, "alice")
			}), "", nil))
			defer server.Close()
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			r, _ := http.NewRequestWithContext(ctx, "GET", server.URL+"/dav/%E6%96%87%E6%A1%A3%20%26%20notes.txt", nil)
			if requestRange != "" {
				r.Header.Set("Range", requestRange)
			}
			response, err := server.Client().Do(r)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := io.CopyN(io.Discard, response.Body, readWindow); err != nil {
				t.Fatal(err)
			}
			select {
			case <-objects.entered:
			case <-ctx.Done():
				t.Fatal("second bounded range was not requested")
			}
			_ = response.Body.Close()
			select {
			case <-done:
			case <-ctx.Done():
				t.Fatal("client disconnect did not cancel DOFS read")
			}
			if objects.active.Load() != 0 {
				t.Fatalf("unclosed object readers: %d", objects.active.Load())
			}
		})
	}
}

func TestFileCloseCancelsAndWaitsForPendingRange(t *testing.T) {
	runtime, _, _ := fixture(t)
	objects := &observedObjects{ObjectStore: runtime.Objects, entered: make(chan struct{})}
	objects.block.Store(true)
	runtime.Objects = objects
	backend, err := dofs.NewBackend(t.Context(), "alice", runtime.Metadata, runtime.Objects, runtime.Keys, dofs.BackendOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer backend.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	opened, err := (&filesystem{backend: backend}).OpenFile(ctx, "/文档 & notes.txt", os.O_RDONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := opened.Seek(2*readWindow, io.SeekStart); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { _, err := opened.Read(make([]byte, 4096)); done <- err }()
	select {
	case <-objects.entered:
	case <-ctx.Done():
		t.Fatal("range read did not start")
	}
	if err := opened.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("pending read after Close: %v", err)
		}
	case <-ctx.Done():
		t.Fatal("Close left a pending read behind")
	}
}
