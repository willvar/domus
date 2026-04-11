package store

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"zephyr/config"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

// Custom types to decouple from SDK

type UploadPartInfo struct {
	PartNumber int    `json:"part_number"`
	ETag       string `json:"etag"`
}

type ObjectInfo struct {
	Key  string
	Size int64
}

// CompletePart represents a completed multipart upload part.
type CompletePart struct {
	PartNumber int    `json:"part_number"`
	ETag       string `json:"etag"`
}

// PartInfo describes a part that has been uploaded.
type PartInfo struct {
	PartNumber int    `json:"part_number"`
	Size       int64  `json:"size"`
	ETag       string `json:"etag"`
}

// HeadResult holds metadata returned by HeadObject.
type HeadResult struct {
	Size int64
	ETag string
}

// FileStore defines the interface for object storage operations
type FileStore interface {
	ListObjects(prefix, marker string, limit int) (*ListResult, error)
	GetObjectInfo(key string) (*FileInfo, error)
	CreateDirectory(key string) error
	DeleteObject(key string) error
	DeleteObjects(keys []string) error
	CopyObject(srcKey, dstKey string) error
	MoveObject(srcKey, dstKey string) error
	ListAllObjects(prefix string) ([]ObjectInfo, error)
	RecursiveCopy(srcPrefix, dstPrefix string, progress func(done, total int, current string)) error
	RecursiveMove(srcPrefix, dstPrefix string, progress func(done, total int, current string)) error
	RecursiveDelete(prefix string, progress func(done, total int, current string)) error
	GetTotalSize(prefix string) (int64, int, error)
	GeneratePresignedURL(key string, expires time.Duration) (string, error)
	GetObjectContent(key string) (io.ReadCloser, error)
	GetObjectContentRange(key string, start, end int64) (io.ReadCloser, error)
	PutObjectContent(key, content string) error
	PutObjectBytes(key string, data []byte) error
	RenameObject(oldKey, newKey string, isDir bool) error
	DownloadToFile(key, localPath string) error
	UploadFromFile(key, localPath string) error
	UploadFromFileCtx(ctx context.Context, key, localPath string) error
	UploadFromFileCtxProgress(ctx context.Context, key, localPath string, fn ProgressFn) error

	// Client-direct-upload operations (presigned URLs use clientUploadClient)
	PresignedPutObject(key string, expires time.Duration) (string, error)
	PresignedDeleteObject(key string, expires time.Duration) (string, error)
	CreateMultipartUpload(key string) (uploadID string, err error)
	PresignedUploadPart(key, uploadID string, partNumber int, expires time.Duration) (string, error)
	CompleteMultipartUpload(key, uploadID string, parts []CompletePart) error
	AbortMultipartUpload(key, uploadID string) error
	ListParts(key, uploadID string) ([]PartInfo, error)
	HeadObject(key string) (*HeadResult, error)
}

// ProgressFn reports upload progress: done bytes out of total bytes.
type ProgressFn func(done, total int64)

// progressReaderAt wraps an *os.File to report read progress while preserving
// io.ReaderAt so the MinIO SDK can use parallel multipart uploads.
type progressReaderAt struct {
	f     *os.File
	total int64
	done  atomic.Int64
	fn    ProgressFn
}

func (pr *progressReaderAt) Read(p []byte) (int, error) {
	n, err := pr.f.Read(p)
	if n > 0 {
		d := pr.done.Add(int64(n))
		if pr.fn != nil {
			pr.fn(d, pr.total)
		}
	}
	return n, err
}

func (pr *progressReaderAt) ReadAt(p []byte, off int64) (int, error) {
	n, err := pr.f.ReadAt(p, off)
	if n > 0 {
		d := pr.done.Add(int64(n))
		if pr.fn != nil {
			pr.fn(d, pr.total)
		}
	}
	return n, err
}

// FileInfo holds metadata for a file or directory
type FileInfo struct {
	Name          string    `json:"name"`
	Path          string    `json:"path"`
	IsDir         bool      `json:"is_dir"`
	Size          int64     `json:"size"`
	CreatedAt     time.Time `json:"created_at"`
	LastModified  time.Time `json:"last_modified"`
	ContentType   string    `json:"content_type,omitempty"`
	ThumbnailURL  string    `json:"thumbnail_url,omitempty"`
	ThumbnailDEK  string    `json:"thumbnail_dek,omitempty"`
	MediaWidth    int       `json:"media_width,omitempty"`
	MediaHeight   int       `json:"media_height,omitempty"`
	MediaDuration float64   `json:"media_duration,omitempty"`
	Status        string    `json:"status,omitempty"`
	JobID         string    `json:"job_id,omitempty"`
	JobProgress   float64   `json:"job_progress,omitempty"`
	JobPhase      string    `json:"job_phase,omitempty"`
}

// ListResult holds a paginated directory listing
type ListResult struct {
	Files       []FileInfo `json:"files"`
	NextMarker  string     `json:"next_marker"`
	IsTruncated bool       `json:"is_truncated"`
}

// OSSClient implements FileStore using MinIO S3-compatible SDK.
// It holds two MinIO clients:
//   - serverClient: for server-side operations (transcode, HeadObject, Delete, etc.)
//   - clientUploadClient: for generating presigned URLs that browsers will use for direct upload
type OSSClient struct {
	serverClient       *minio.Client
	serverCore         minio.Core
	clientUploadClient *minio.Client
	bucketName         string
	clientDownloadBase string // CDN or public download base URL (empty = presigned GET via clientUploadClient)
}

func NewOSSClient(cfg config.OSSConfig) (FileStore, error) {
	creds := credentials.NewStaticV4(cfg.AccessKeyID, cfg.AccessKeySecret, "")

	serverClient, err := minio.New(cfg.ServerEndpoint, &minio.Options{
		Creds: creds, Secure: true, Region: cfg.Region,
	})
	if err != nil {
		return nil, fmt.Errorf("server client: %w", err)
	}

	// clientUploadClient may share the same endpoint in dev/single-machine setups
	var uploadClient *minio.Client
	if cfg.ClientUploadEndpoint == cfg.ServerEndpoint {
		uploadClient = serverClient
	} else {
		uploadClient, err = minio.New(cfg.ClientUploadEndpoint, &minio.Options{
			Creds: creds, Secure: true, Region: cfg.Region,
		})
		if err != nil {
			return nil, fmt.Errorf("client upload client: %w", err)
		}
	}

	c := &OSSClient{
		serverClient:       serverClient,
		serverCore:         minio.Core{Client: serverClient},
		clientUploadClient: uploadClient,
		bucketName:         cfg.Bucket,
		clientDownloadBase: cfg.ClientDownloadEndpoint,
	}
	return c, nil
}

// ListObjects lists objects under a prefix (simulating directory listing)
func (c *OSSClient) ListObjects(prefix, marker string, limit int) (*ListResult, error) {
	if limit <= 0 {
		limit = 100
	}

	// Ensure prefix ends with /
	if prefix != "" && !strings.HasSuffix(prefix, "/") {
		prefix += "/"
	}

	result, err := c.serverCore.ListObjectsV2(c.bucketName, prefix, "", marker, "/", limit)
	if err != nil {
		return nil, err
	}

	var files []FileInfo

	// Directories (common prefixes)
	for _, dir := range result.CommonPrefixes {
		dirPrefix := dir.Prefix
		name := strings.TrimPrefix(dirPrefix, prefix)
		name = strings.TrimSuffix(name, "/")
		if name == "" || name == ".trash" {
			continue
		}
		files = append(files, FileInfo{
			Name:  name,
			Path:  dirPrefix,
			IsDir: true,
		})
	}

	// Files
	for _, obj := range result.Contents {
		name := strings.TrimPrefix(obj.Key, prefix)
		if name == "" || strings.HasSuffix(name, "/") {
			continue
		}
		fi := FileInfo{
			Name:         name,
			Path:         obj.Key,
			IsDir:        false,
			Size:         obj.Size,
			LastModified: obj.LastModified,
		}
		if obj.ContentType != "" {
			fi.ContentType = obj.ContentType
		}
		files = append(files, fi)
	}

	if files == nil {
		files = []FileInfo{}
	}

	return &ListResult{
		Files:       files,
		NextMarker:  result.NextContinuationToken,
		IsTruncated: result.IsTruncated,
	}, nil
}

// GetObjectInfo gets metadata for a single object
func (c *OSSClient) GetObjectInfo(key string) (*FileInfo, error) {
	info, err := c.serverClient.StatObject(context.Background(), c.bucketName, key, minio.StatObjectOptions{})
	if err != nil {
		return nil, err
	}

	name := key
	if idx := strings.LastIndex(strings.TrimSuffix(key, "/"), "/"); idx >= 0 {
		name = key[idx+1:]
	}

	fi := &FileInfo{
		Name:         name,
		Path:         key,
		IsDir:        strings.HasSuffix(key, "/"),
		Size:         info.Size,
		LastModified: info.LastModified,
		ContentType:  info.ContentType,
	}

	return fi, nil
}

// CreateDirectory creates a directory marker object
func (c *OSSClient) CreateDirectory(key string) error {
	if !strings.HasSuffix(key, "/") {
		key += "/"
	}
	_, err := c.serverClient.PutObject(context.Background(), c.bucketName, key, strings.NewReader(""), 0, minio.PutObjectOptions{})
	return err
}

// DeleteObject deletes a single object
func (c *OSSClient) DeleteObject(key string) error {
	return c.serverClient.RemoveObject(context.Background(), c.bucketName, key, minio.RemoveObjectOptions{})
}

// DeleteObjects deletes multiple objects
func (c *OSSClient) DeleteObjects(keys []string) error {
	if len(keys) == 0 {
		return nil
	}
	objectsCh := make(chan minio.ObjectInfo, len(keys))
	for _, k := range keys {
		objectsCh <- minio.ObjectInfo{Key: k}
	}
	close(objectsCh)

	for err := range c.serverClient.RemoveObjects(context.Background(), c.bucketName, objectsCh, minio.RemoveObjectsOptions{}) {
		if err.Err != nil {
			return err.Err
		}
	}
	return nil
}

// CopyObject copies a single object
func (c *OSSClient) CopyObject(srcKey, dstKey string) error {
	src := minio.CopySrcOptions{Bucket: c.bucketName, Object: srcKey}
	dst := minio.CopyDestOptions{Bucket: c.bucketName, Object: dstKey}
	_, err := c.serverClient.CopyObject(context.Background(), dst, src)
	return err
}

// MoveObject moves (copy + delete) a single object
func (c *OSSClient) MoveObject(srcKey, dstKey string) error {
	if err := c.CopyObject(srcKey, dstKey); err != nil {
		return err
	}
	return c.DeleteObject(srcKey)
}

// ListAllObjects lists all objects under a prefix recursively (no delimiter)
func (c *OSSClient) ListAllObjects(prefix string) ([]ObjectInfo, error) {
	var allObjects []ObjectInfo
	for obj := range c.serverClient.ListObjects(context.Background(), c.bucketName, minio.ListObjectsOptions{
		Prefix:    prefix,
		Recursive: true,
	}) {
		if obj.Err != nil {
			return nil, obj.Err
		}
		allObjects = append(allObjects, ObjectInfo{
			Key:  obj.Key,
			Size: obj.Size,
		})
	}
	return allObjects, nil
}

func (c *OSSClient) transferObjects(srcPrefix, dstPrefix string, deleteSource bool, progress func(done, total int, current string)) error {
	objects, err := c.ListAllObjects(srcPrefix)
	if err != nil {
		return err
	}

	total := len(objects)
	for i, obj := range objects {
		newKey := dstPrefix + strings.TrimPrefix(obj.Key, srcPrefix)
		if err := c.CopyObject(obj.Key, newKey); err != nil {
			return fmt.Errorf("copy %s: %w", obj.Key, err)
		}
		if deleteSource {
			if err := c.DeleteObject(obj.Key); err != nil {
				return fmt.Errorf("delete %s: %w", obj.Key, err)
			}
		}
		if progress != nil {
			progress(i+1, total, obj.Key)
		}
	}
	return nil
}

// RecursiveCopy copies all objects under srcPrefix to dstPrefix, calling progress callback
func (c *OSSClient) RecursiveCopy(srcPrefix, dstPrefix string, progress func(done, total int, current string)) error {
	return c.transferObjects(srcPrefix, dstPrefix, false, progress)
}

// RecursiveMove moves all objects under srcPrefix to dstPrefix
func (c *OSSClient) RecursiveMove(srcPrefix, dstPrefix string, progress func(done, total int, current string)) error {
	return c.transferObjects(srcPrefix, dstPrefix, true, progress)
}

// RecursiveDelete deletes all objects under a prefix
func (c *OSSClient) RecursiveDelete(prefix string, progress func(done, total int, current string)) error {
	objects, err := c.ListAllObjects(prefix)
	if err != nil {
		return err
	}

	total := len(objects)
	// Delete in batches of 1000
	batch := make([]string, 0, 1000)
	done := 0
	for _, obj := range objects {
		batch = append(batch, obj.Key)
		if len(batch) == 1000 {
			if err := c.DeleteObjects(batch); err != nil {
				return err
			}
			done += len(batch)
			if progress != nil {
				progress(done, total, obj.Key)
			}
			batch = batch[:0]
		}
	}
	if len(batch) > 0 {
		if err := c.DeleteObjects(batch); err != nil {
			return err
		}
		done += len(batch)
		if progress != nil {
			progress(done, total, "")
		}
	}
	return nil
}

// GetTotalSize calculates total size of all objects under a prefix
func (c *OSSClient) GetTotalSize(prefix string) (int64, int, error) {
	objects, err := c.ListAllObjects(prefix)
	if err != nil {
		return 0, 0, err
	}
	var total int64
	for _, obj := range objects {
		total += obj.Size
	}
	return total, len(objects), nil
}

// GeneratePresignedURL generates a presigned download URL.
// When client_download_endpoint is configured, returns a plain CDN URL
// (requires private bucket origin-pull enabled in the CDN console).
// Otherwise falls back to S3 presigned URL via the client upload endpoint.
func (c *OSSClient) GeneratePresignedURL(key string, expires time.Duration) (string, error) {
	if c.clientDownloadBase != "" {
		return "https://" + c.clientDownloadBase + "/" + key, nil
	}
	u, err := c.clientUploadClient.PresignedGetObject(context.Background(), c.bucketName, key, expires, url.Values{})
	if err != nil {
		return "", err
	}
	return u.String(), nil
}

// GetObjectContent reads an object's content and returns it as a ReadCloser
func (c *OSSClient) GetObjectContent(key string) (io.ReadCloser, error) {
	obj, err := c.serverClient.GetObject(context.Background(), c.bucketName, key, minio.GetObjectOptions{})
	if err != nil {
		return nil, err
	}
	return obj, nil
}

// GetObjectContentRange reads a byte range of an object's content
func (c *OSSClient) GetObjectContentRange(key string, start, end int64) (io.ReadCloser, error) {
	opts := minio.GetObjectOptions{}
	if err := opts.SetRange(start, end); err != nil {
		return nil, err
	}
	obj, err := c.serverClient.GetObject(context.Background(), c.bucketName, key, opts)
	if err != nil {
		return nil, err
	}
	return obj, nil
}

// PutObjectContent writes string content to an object
func (c *OSSClient) PutObjectContent(key, content string) error {
	r := strings.NewReader(content)
	_, err := c.serverClient.PutObject(context.Background(), c.bucketName, key, r, int64(len(content)), minio.PutObjectOptions{})
	return err
}

// PutObjectBytes writes binary data to an object
func (c *OSSClient) PutObjectBytes(key string, data []byte) error {
	r := bytes.NewReader(data)
	_, err := c.serverClient.PutObject(context.Background(), c.bucketName, key, r, int64(len(data)), minio.PutObjectOptions{})
	return err
}

// RenameObject renames (moves) a single object or directory
func (c *OSSClient) RenameObject(oldKey, newKey string, isDir bool) error {
	if isDir {
		if !strings.HasSuffix(oldKey, "/") {
			oldKey += "/"
		}
		if !strings.HasSuffix(newKey, "/") {
			newKey += "/"
		}
		return c.RecursiveMove(oldKey, newKey, nil)
	}
	return c.MoveObject(oldKey, newKey)
}

// DownloadToFile downloads an object to a local file.
func (c *OSSClient) DownloadToFile(key, localPath string) error {
	return c.serverClient.FGetObject(context.Background(), c.bucketName, key, localPath, minio.GetObjectOptions{})
}

// UploadFromFile uploads a local file.
func (c *OSSClient) UploadFromFile(key, localPath string) error {
	return c.UploadFromFileCtx(context.Background(), key, localPath)
}

func (c *OSSClient) UploadFromFileCtx(ctx context.Context, key, localPath string) error {
	return c.UploadFromFileCtxProgress(ctx, key, localPath, nil)
}

func (c *OSSClient) UploadFromFileCtxProgress(ctx context.Context, key, localPath string, fn ProgressFn) error {
	f, err := os.Open(localPath)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()

	info, err := f.Stat()
	if err != nil {
		return err
	}

	opts := minio.PutObjectOptions{
		PartSize:   uint64(config.UploadChunkSize),
		NumThreads: 4,
	}

	var body io.Reader = f
	if fn != nil {
		body = &progressReaderAt{f: f, total: info.Size(), fn: fn}
	}

	_, err = c.serverClient.PutObject(ctx, c.bucketName, key, body, info.Size(), opts)
	return err
}

// ---------------------------------------------------------------------------
// Client-direct-upload operations (presigned URLs signed by clientUploadClient)
// ---------------------------------------------------------------------------

// PresignedPutObject returns a presigned PUT URL for a single-object upload.
func (c *OSSClient) PresignedPutObject(key string, expires time.Duration) (string, error) {
	u, err := c.clientUploadClient.PresignedPutObject(context.Background(), c.bucketName, key, expires)
	if err != nil {
		return "", err
	}
	return u.String(), nil
}

// PresignedDeleteObject returns a presigned DELETE URL (e.g. for CORS probe cleanup).
func (c *OSSClient) PresignedDeleteObject(key string, expires time.Duration) (string, error) {
	u, err := c.clientUploadClient.Presign(context.Background(), "DELETE", c.bucketName, key, expires, nil)
	if err != nil {
		return "", err
	}
	return u.String(), nil
}

// CreateMultipartUpload initiates a multipart upload and returns the upload ID.
func (c *OSSClient) CreateMultipartUpload(key string) (string, error) {
	uploadID, err := c.serverCore.NewMultipartUpload(context.Background(), c.bucketName, key, minio.PutObjectOptions{})
	if err != nil {
		return "", err
	}
	return uploadID, nil
}

// PresignedUploadPart returns a presigned PUT URL for uploading a specific part.
func (c *OSSClient) PresignedUploadPart(key, uploadID string, partNumber int, expires time.Duration) (string, error) {
	params := make(url.Values)
	params.Set("partNumber", strconv.Itoa(partNumber))
	params.Set("uploadId", uploadID)
	u, err := c.clientUploadClient.Presign(context.Background(), "PUT", c.bucketName, key, expires, params)
	if err != nil {
		return "", err
	}
	return u.String(), nil
}

// CompleteMultipartUpload finalizes a multipart upload.
func (c *OSSClient) CompleteMultipartUpload(key, uploadID string, parts []CompletePart) error {
	completeParts := make([]minio.CompletePart, len(parts))
	for i, p := range parts {
		completeParts[i] = minio.CompletePart{
			PartNumber: p.PartNumber,
			ETag:       p.ETag,
		}
	}
	_, err := c.serverCore.CompleteMultipartUpload(context.Background(), c.bucketName, key, uploadID, completeParts, minio.PutObjectOptions{})
	return err
}

// AbortMultipartUpload cancels an in-progress multipart upload.
func (c *OSSClient) AbortMultipartUpload(key, uploadID string) error {
	return c.serverCore.AbortMultipartUpload(context.Background(), c.bucketName, key, uploadID)
}

// ListParts returns the parts that have been uploaded for a multipart upload.
func (c *OSSClient) ListParts(key, uploadID string) ([]PartInfo, error) {
	var allParts []PartInfo
	partMarker := 0
	for {
		result, err := c.serverCore.ListObjectParts(context.Background(), c.bucketName, key, uploadID, partMarker, 1000)
		if err != nil {
			return nil, err
		}
		for _, p := range result.ObjectParts {
			allParts = append(allParts, PartInfo{
				PartNumber: p.PartNumber,
				Size:       p.Size,
				ETag:       p.ETag,
			})
		}
		if !result.IsTruncated {
			break
		}
		partMarker = result.NextPartNumberMarker
	}
	return allParts, nil
}

// HeadObject returns the size and ETag of an object without downloading it.
func (c *OSSClient) HeadObject(key string) (*HeadResult, error) {
	info, err := c.serverClient.StatObject(context.Background(), c.bucketName, key, minio.StatObjectOptions{})
	if err != nil {
		return nil, err
	}
	return &HeadResult{Size: info.Size, ETag: info.ETag}, nil
}
