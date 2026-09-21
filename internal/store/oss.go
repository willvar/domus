package store

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"path"
	"strconv"
	"strings"
	"time"

	"domus/config"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

// Custom types to decouple from SDK

type ObjectInfo struct {
	Key  string
	Size int64
}

// FileInfo is the browser-facing file listing projection assembled from DOFS
// metadata. It is not populated by listing object-storage paths.
type FileInfo struct {
	Inode         int64     `json:"inode"`
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
	TaskID        string    `json:"task_id,omitempty"`
	TaskProgress  float64   `json:"task_progress,omitempty"`
	TaskPhase     string    `json:"task_phase,omitempty"`
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

// ControlStore is the only object-storage capability available to Domus HTTP
// handlers. It can issue browser-direct URLs and finalize multipart uploads,
// but deliberately cannot read or write object bodies. User file bytes must
// stay on browser/worker-to-object-storage connections.
type ControlStore interface {
	GeneratePresignedURL(key string, expires time.Duration) (string, error)
	PresignedPutObject(key string, expires time.Duration) (string, error)
	PresignedDeleteObject(key string, expires time.Duration) (string, error)
	CreateMultipartUpload(key string) (uploadID string, err error)
	PresignedUploadPart(key, uploadID string, partNumber int, expires time.Duration) (string, error)
	CompleteMultipartUpload(key, uploadID string, parts []CompletePart) error
	AbortMultipartUpload(key, uploadID string) error
	ListParts(key, uploadID string) ([]PartInfo, error)
	HeadObject(key string) (*HeadResult, error)
}

// FileStore adds offline administrative backup/reset capabilities. It is kept
// out of Handler so user upload bytes cannot accidentally be proxied by a
// future HTTP endpoint.
type FileStore interface {
	ControlStore
	Check(context.Context) error
	GetObjectContent(key string) (io.ReadCloser, error)
	CreateDirectory(key string) error
	ListAllObjects(prefix string) ([]ObjectInfo, error)
	DeleteAllObjects(progress func(done, total int, current string)) error
	PutObject(key string, reader io.Reader, size int64) error
}

// OSSClient implements FileStore using MinIO S3-compatible SDK.
// It holds two MinIO clients:
//   - serverClient: for server-side operations (HeadObject, Delete, etc.)
//   - clientUploadClient: for generating presigned URLs that browsers will use for direct upload
type OSSClient struct {
	serverClient       *minio.Client
	serverCore         minio.Core
	clientUploadClient *minio.Client
	bucketName         string
	objectPrefix       string
	clientDownloadBase string // CDN or public download base URL (empty = presigned GET via clientUploadClient)
}

// OSSOptions is the transport-level object-store configuration shared by
// production and protocol integration tests. Endpoint URLs must be absolute
// HTTP(S) URLs; the production config adapter below always supplies HTTPS.
type OSSOptions struct {
	ServerEndpointURL       string
	ClientUploadEndpointURL string
	ClientDownloadBaseURL   string
	AccessKeyID             string
	AccessKeySecret         string
	Bucket                  string
	Region                  string
	Prefix                  string
}

type ossEndpoint struct {
	host   string
	secure bool
}

// NewOSSClient constructs the same S3-compatible client used in production.
// Keeping transport URLs here, rather than an insecure flag in Config, lets a
// disposable local S3 service exercise this constructor without weakening the
// production configuration contract.
func NewOSSClient(options OSSOptions) (*OSSClient, error) {
	if strings.TrimSpace(options.ServerEndpointURL) == "" ||
		strings.TrimSpace(options.ClientUploadEndpointURL) == "" ||
		options.AccessKeyID == "" || options.AccessKeySecret == "" ||
		strings.TrimSpace(options.Bucket) == "" || strings.TrimSpace(options.Region) == "" {
		return nil, errors.New("object store configuration is incomplete")
	}

	serverEndpoint, err := parseOSSEndpoint(options.ServerEndpointURL)
	if err != nil {
		return nil, fmt.Errorf("server endpoint: %w", err)
	}
	uploadEndpoint, err := parseOSSEndpoint(options.ClientUploadEndpointURL)
	if err != nil {
		return nil, fmt.Errorf("client upload endpoint: %w", err)
	}
	downloadBase := ""
	if strings.TrimSpace(options.ClientDownloadBaseURL) != "" {
		downloadEndpoint, err := parseOSSEndpoint(options.ClientDownloadBaseURL)
		if err != nil {
			return nil, fmt.Errorf("client download endpoint: %w", err)
		}
		downloadBase = downloadEndpoint.url()
	}
	objectPrefix := strings.Trim(strings.TrimSpace(options.Prefix), "/")
	if objectPrefix != "" {
		if clean := path.Clean(objectPrefix); clean != objectPrefix || clean == "." || clean == ".." || strings.HasPrefix(clean, "../") || strings.Contains(clean, "\x00") {
			return nil, errors.New("object store prefix is invalid")
		}
	}

	creds := credentials.NewStaticV4(options.AccessKeyID, options.AccessKeySecret, "")

	serverClient, err := minio.New(serverEndpoint.host, &minio.Options{
		Creds: creds, Secure: serverEndpoint.secure, Region: options.Region,
	})
	if err != nil {
		return nil, fmt.Errorf("server client: %w", err)
	}

	// clientUploadClient may share the same endpoint in dev/single-machine setups
	var uploadClient *minio.Client
	if uploadEndpoint == serverEndpoint {
		uploadClient = serverClient
	} else {
		uploadClient, err = minio.New(uploadEndpoint.host, &minio.Options{
			Creds: creds, Secure: uploadEndpoint.secure, Region: options.Region,
		})
		if err != nil {
			return nil, fmt.Errorf("client upload client: %w", err)
		}
	}

	c := &OSSClient{
		serverClient:       serverClient,
		serverCore:         minio.Core{Client: serverClient},
		clientUploadClient: uploadClient,
		bucketName:         options.Bucket,
		objectPrefix:       objectPrefix,
		clientDownloadBase: downloadBase,
	}
	return c, nil
}

// NewOSSClientFromConfig maps Domus' host-only endpoint configuration onto the
// production HTTPS transport. Loopback hosts (local development object stores)
// are the only endpoints reached over plain HTTP.
func NewOSSClientFromConfig(cfg config.OSSConfig) (FileStore, error) {
	downloadBaseURL := ""
	if strings.TrimSpace(cfg.ClientDownloadEndpoint) != "" {
		downloadBaseURL = config.ClientEndpointURL(cfg.ClientDownloadEndpoint, cfg.ClientEndpointInsecure)
	}
	return NewOSSClient(OSSOptions{
		ServerEndpointURL:       config.EndpointURL(cfg.ServerEndpoint),
		ClientUploadEndpointURL: config.ClientEndpointURL(cfg.ClientUploadEndpoint, cfg.ClientEndpointInsecure),
		ClientDownloadBaseURL:   downloadBaseURL,
		AccessKeyID:             cfg.AccessKeyID,
		AccessKeySecret:         cfg.AccessKeySecret,
		Bucket:                  cfg.Bucket,
		Region:                  cfg.Region,
		Prefix:                  cfg.Prefix,
	})
}

func parseOSSEndpoint(raw string) (ossEndpoint, error) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Host == "" || parsed.User != nil ||
		(parsed.Scheme != "http" && parsed.Scheme != "https") ||
		(parsed.Path != "" && parsed.Path != "/") || parsed.RawQuery != "" || parsed.Fragment != "" {
		return ossEndpoint{}, errors.New("must be an absolute HTTP(S) URL without path, query, fragment, or credentials")
	}
	return ossEndpoint{host: parsed.Host, secure: parsed.Scheme == "https"}, nil
}

func (e ossEndpoint) url() string {
	scheme := "http"
	if e.secure {
		scheme = "https"
	}
	return scheme + "://" + e.host
}

// Check verifies that the configured bucket exists and the administrative
// credentials can reach it. Destructive offline commands call this before
// changing PostgreSQL or local DOFS metadata.
func (c *OSSClient) Check(ctx context.Context) error {
	exists, err := c.serverClient.BucketExists(ctx, c.bucketName)
	if err != nil {
		return fmt.Errorf("check bucket %q: %w", c.bucketName, err)
	}
	if !exists {
		return fmt.Errorf("bucket %q does not exist or is not accessible", c.bucketName)
	}
	return nil
}

func (c *OSSClient) physicalKey(key string) (string, error) {
	if key == "" || strings.HasPrefix(key, "/") || strings.Contains(key, "\x00") {
		return "", fmt.Errorf("invalid object key %q", key)
	}
	base := strings.TrimSuffix(key, "/")
	if base == "" {
		return "", fmt.Errorf("invalid object key %q", key)
	}
	for _, segment := range strings.Split(base, "/") {
		if segment == "." || segment == ".." {
			return "", fmt.Errorf("invalid object key %q", key)
		}
	}
	if c.objectPrefix == "" {
		return key, nil
	}
	return c.objectPrefix + "/" + key, nil
}

func (c *OSSClient) physicalListPrefix(prefix string) (string, error) {
	if prefix == "" {
		if c.objectPrefix == "" {
			return "", nil
		}
		return c.objectPrefix + "/", nil
	}
	return c.physicalKey(prefix)
}

func (c *OSSClient) logicalKey(key string) (string, error) {
	if c.objectPrefix == "" {
		return key, nil
	}
	root := c.objectPrefix + "/"
	if !strings.HasPrefix(key, root) {
		return "", fmt.Errorf("object key %q is outside configured prefix %q", key, c.objectPrefix)
	}
	return strings.TrimPrefix(key, root), nil
}

// CreateDirectory creates a directory marker object
func (c *OSSClient) CreateDirectory(key string) error {
	if !strings.HasSuffix(key, "/") {
		key += "/"
	}
	physical, err := c.physicalKey(key)
	if err != nil {
		return err
	}
	_, err = c.serverClient.PutObject(context.Background(), c.bucketName, physical, strings.NewReader(""), 0, minio.PutObjectOptions{})
	return err
}

func (c *OSSClient) deleteObjects(keys []string) error {
	if len(keys) == 0 {
		return nil
	}
	physicalKeys := make([]string, 0, len(keys))
	for _, key := range keys {
		physical, err := c.physicalKey(key)
		if err != nil {
			return err
		}
		physicalKeys = append(physicalKeys, physical)
	}
	objectsCh := make(chan minio.ObjectInfo, len(physicalKeys))
	for _, key := range physicalKeys {
		objectsCh <- minio.ObjectInfo{Key: key}
	}
	close(objectsCh)

	for err := range c.serverClient.RemoveObjects(context.Background(), c.bucketName, objectsCh, minio.RemoveObjectsOptions{}) {
		if err.Err != nil {
			return err.Err
		}
	}
	return nil
}

// ListAllObjects lists all objects under a prefix recursively (no delimiter)
func (c *OSSClient) ListAllObjects(prefix string) ([]ObjectInfo, error) {
	physicalPrefix, err := c.physicalListPrefix(prefix)
	if err != nil {
		return nil, err
	}
	var allObjects []ObjectInfo
	for obj := range c.serverClient.ListObjects(context.Background(), c.bucketName, minio.ListObjectsOptions{
		Prefix:    physicalPrefix,
		Recursive: true,
	}) {
		if obj.Err != nil {
			return nil, obj.Err
		}
		logical, err := c.logicalKey(obj.Key)
		if err != nil {
			return nil, err
		}
		if logical == "" {
			continue
		}
		allObjects = append(allObjects, ObjectInfo{
			Key:  logical,
			Size: obj.Size,
		})
	}
	return allObjects, nil
}

func (c *OSSClient) deleteObjectsWithPrefix(prefix string, progress func(done, total int, current string)) error {
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
			if err := c.deleteObjects(batch); err != nil {
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
		if err := c.deleteObjects(batch); err != nil {
			return err
		}
		done += len(batch)
		if progress != nil {
			progress(done, total, "")
		}
	}
	return nil
}

// DeleteAllObjects deletes every object inside the configured object prefix.
// An empty configured prefix preserves the legacy whole-bucket behavior.
func (c *OSSClient) DeleteAllObjects(progress func(done, total int, current string)) error {
	return c.deleteObjectsWithPrefix("", progress)
}

// GeneratePresignedURL generates a presigned download URL.
// When client_download_endpoint is configured, returns a plain CDN URL
// (requires private bucket origin-pull enabled in the CDN console).
// Otherwise falls back to S3 presigned URL via the client upload endpoint.
func (c *OSSClient) GeneratePresignedURL(key string, expires time.Duration) (string, error) {
	physical, err := c.physicalKey(key)
	if err != nil {
		return "", err
	}
	if c.clientDownloadBase != "" {
		return c.clientDownloadBase + "/" + physical, nil
	}
	u, err := c.clientUploadClient.PresignedGetObject(context.Background(), c.bucketName, physical, expires, url.Values{})
	if err != nil {
		return "", err
	}
	return u.String(), nil
}

// GetObjectContent reads an object's content and returns it as a ReadCloser
func (c *OSSClient) GetObjectContent(key string) (io.ReadCloser, error) {
	physical, err := c.physicalKey(key)
	if err != nil {
		return nil, err
	}
	obj, err := c.serverClient.GetObject(context.Background(), c.bucketName, physical, minio.GetObjectOptions{})
	if err != nil {
		return nil, err
	}
	return obj, nil
}

// PutObject is restricted to the offline backup/restore command path. Domus
// HTTP handlers receive ControlStore, which does not expose this method.
func (c *OSSClient) PutObject(key string, reader io.Reader, size int64) error {
	if size < 0 {
		return fmt.Errorf("invalid object size %d", size)
	}
	physical, err := c.physicalKey(key)
	if err != nil {
		return err
	}
	_, err = c.serverClient.PutObject(
		context.Background(), c.bucketName, physical, reader, size, minio.PutObjectOptions{},
	)
	return err
}

// ---------------------------------------------------------------------------
// Client-direct-upload operations (presigned URLs signed by clientUploadClient)
// ---------------------------------------------------------------------------

// PresignedPutObject returns a presigned PUT URL for a single-object upload.
func (c *OSSClient) PresignedPutObject(key string, expires time.Duration) (string, error) {
	physical, err := c.physicalKey(key)
	if err != nil {
		return "", err
	}
	u, err := c.clientUploadClient.PresignedPutObject(context.Background(), c.bucketName, physical, expires)
	if err != nil {
		return "", err
	}
	return u.String(), nil
}

// PresignedDeleteObject returns a presigned DELETE URL (e.g. for CORS probe cleanup).
func (c *OSSClient) PresignedDeleteObject(key string, expires time.Duration) (string, error) {
	physical, err := c.physicalKey(key)
	if err != nil {
		return "", err
	}
	u, err := c.clientUploadClient.Presign(context.Background(), "DELETE", c.bucketName, physical, expires, nil)
	if err != nil {
		return "", err
	}
	return u.String(), nil
}

// CreateMultipartUpload initiates a multipart upload and returns the upload ID.
func (c *OSSClient) CreateMultipartUpload(key string) (string, error) {
	physical, err := c.physicalKey(key)
	if err != nil {
		return "", err
	}
	uploadID, err := c.serverCore.NewMultipartUpload(context.Background(), c.bucketName, physical, minio.PutObjectOptions{})
	if err != nil {
		return "", err
	}
	return uploadID, nil
}

// PresignedUploadPart returns a presigned PUT URL for uploading a specific part.
func (c *OSSClient) PresignedUploadPart(key, uploadID string, partNumber int, expires time.Duration) (string, error) {
	physical, err := c.physicalKey(key)
	if err != nil {
		return "", err
	}
	params := make(url.Values)
	params.Set("partNumber", strconv.Itoa(partNumber))
	params.Set("uploadId", uploadID)
	u, err := c.clientUploadClient.Presign(context.Background(), "PUT", c.bucketName, physical, expires, params)
	if err != nil {
		return "", err
	}
	return u.String(), nil
}

// CompleteMultipartUpload finalizes a multipart upload.
func (c *OSSClient) CompleteMultipartUpload(key, uploadID string, parts []CompletePart) error {
	physical, err := c.physicalKey(key)
	if err != nil {
		return err
	}
	completeParts := make([]minio.CompletePart, len(parts))
	for i, p := range parts {
		completeParts[i] = minio.CompletePart{
			PartNumber: p.PartNumber,
			ETag:       p.ETag,
		}
	}
	_, err = c.serverCore.CompleteMultipartUpload(context.Background(), c.bucketName, physical, uploadID, completeParts, minio.PutObjectOptions{})
	return err
}

// AbortMultipartUpload cancels an in-progress multipart upload.
func (c *OSSClient) AbortMultipartUpload(key, uploadID string) error {
	physical, err := c.physicalKey(key)
	if err != nil {
		return err
	}
	return c.serverCore.AbortMultipartUpload(context.Background(), c.bucketName, physical, uploadID)
}

// ListParts returns the parts that have been uploaded for a multipart upload.
func (c *OSSClient) ListParts(key, uploadID string) ([]PartInfo, error) {
	physical, err := c.physicalKey(key)
	if err != nil {
		return nil, err
	}
	var allParts []PartInfo
	partMarker := 0
	for {
		result, err := c.serverCore.ListObjectParts(context.Background(), c.bucketName, physical, uploadID, partMarker, 1000)
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
	physical, err := c.physicalKey(key)
	if err != nil {
		return nil, err
	}
	info, err := c.serverClient.StatObject(context.Background(), c.bucketName, physical, minio.StatObjectOptions{})
	if err != nil {
		return nil, err
	}
	return &HeadResult{Size: info.Size, ETag: info.ETag}, nil
}
