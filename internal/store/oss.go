package store

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log"
	"os"
	"strings"
	"time"

	"zephyr/config"

	"github.com/aliyun/alibabacloud-oss-go-sdk-v2/oss"
	"github.com/aliyun/alibabacloud-oss-go-sdk-v2/oss/credentials"
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

// OSSClient implements FileStore using Aliyun OSS SDK V2
type OSSClient struct {
	client     *oss.Client
	bucketName string
}

func NewOSSClient(cfg config.OSSConfig) (FileStore, error) {
	provider := credentials.NewStaticCredentialsProvider(cfg.AccessKeyID, cfg.AccessKeySecret)
	ossCfg := oss.LoadDefaultConfig().
		WithCredentialsProvider(provider).
		WithRegion(cfg.Region).
		WithEndpoint(cfg.Endpoint)

	if cfg.CNAME {
		ossCfg = ossCfg.WithUseCName(true)
	}

	client := oss.NewClient(ossCfg)
	c := &OSSClient{client: client, bucketName: cfg.Bucket}
	c.ensureTempLifecycle()
	return c, nil
}

func (c *OSSClient) ctx() context.Context {
	return context.Background()
}

// ensureTempLifecycle configures an OSS lifecycle rule to auto-delete objects
// under _tmp/preview/ after 1 day, preventing plaintext residue if the
// application crashes before its in-process cleanup goroutine fires.
func (c *OSSClient) ensureTempLifecycle() {
	const ruleID = "zephyr-tmp-preview-cleanup"
	result, err := c.client.GetBucketLifecycle(c.ctx(), &oss.GetBucketLifecycleRequest{
		Bucket: oss.Ptr(c.bucketName),
	})
	if err == nil && result.LifecycleConfiguration != nil {
		for _, r := range result.LifecycleConfiguration.Rules {
			if oss.ToString(r.ID) == ruleID {
				return // already configured
			}
		}
	}

	rules := []oss.LifecycleRule{{
		ID:     oss.Ptr(ruleID),
		Prefix: oss.Ptr("_tmp/preview/"),
		Status: oss.Ptr("Enabled"),
		Expiration: &oss.LifecycleRuleExpiration{
			Days: oss.Ptr(int32(1)),
		},
	}}
	// Preserve existing rules
	if err == nil && result.LifecycleConfiguration != nil {
		rules = append(result.LifecycleConfiguration.Rules, rules...)
	}

	_, err = c.client.PutBucketLifecycle(c.ctx(), &oss.PutBucketLifecycleRequest{
		Bucket: oss.Ptr(c.bucketName),
		LifecycleConfiguration: &oss.LifecycleConfiguration{Rules: rules},
	})
	if err != nil {
		log.Printf("[WARN] failed to set _tmp/preview/ lifecycle rule: %v", err)
	}
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

	req := &oss.ListObjectsV2Request{
		Bucket:    oss.Ptr(c.bucketName),
		Prefix:    oss.Ptr(prefix),
		Delimiter: oss.Ptr("/"),
		MaxKeys:   int32(limit),
	}
	if marker != "" {
		req.ContinuationToken = oss.Ptr(marker)
	}

	result, err := c.client.ListObjectsV2(c.ctx(), req)
	if err != nil {
		return nil, err
	}

	var files []FileInfo

	// Directories (common prefixes)
	for _, dir := range result.CommonPrefixes {
		dirPrefix := oss.ToString(dir.Prefix)
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
		objKey := oss.ToString(obj.Key)
		name := strings.TrimPrefix(objKey, prefix)
		if name == "" || strings.HasSuffix(name, "/") {
			continue
		}
		fi := FileInfo{
			Name:  name,
			Path:  objKey,
			IsDir: false,
			Size:  obj.Size,
		}
		if obj.LastModified != nil {
			fi.LastModified = *obj.LastModified
		}
		if obj.Type != nil {
			fi.ContentType = *obj.Type
		}
		files = append(files, fi)
	}

	if files == nil {
		files = []FileInfo{}
	}

	nextMarker := ""
	if result.NextContinuationToken != nil {
		nextMarker = *result.NextContinuationToken
	}

	return &ListResult{
		Files:       files,
		NextMarker:  nextMarker,
		IsTruncated: result.IsTruncated,
	}, nil
}

// GetObjectInfo gets metadata for a single object
func (c *OSSClient) GetObjectInfo(key string) (*FileInfo, error) {
	result, err := c.client.HeadObject(c.ctx(), &oss.HeadObjectRequest{
		Bucket: oss.Ptr(c.bucketName),
		Key:    oss.Ptr(key),
	})
	if err != nil {
		return nil, err
	}

	name := key
	if idx := strings.LastIndex(strings.TrimSuffix(key, "/"), "/"); idx >= 0 {
		name = key[idx+1:]
	}

	fi := &FileInfo{
		Name:  name,
		Path:  key,
		IsDir: strings.HasSuffix(key, "/"),
		Size:  result.ContentLength,
	}
	if result.LastModified != nil {
		fi.LastModified = *result.LastModified
	}
	if result.ContentType != nil {
		fi.ContentType = *result.ContentType
	}

	return fi, nil
}

// CreateDirectory creates a directory marker object
func (c *OSSClient) CreateDirectory(key string) error {
	if !strings.HasSuffix(key, "/") {
		key += "/"
	}
	_, err := c.client.PutObject(c.ctx(), &oss.PutObjectRequest{
		Bucket: oss.Ptr(c.bucketName),
		Key:    oss.Ptr(key),
		Body:   strings.NewReader(""),
	})
	return err
}

// DeleteObject deletes a single object
func (c *OSSClient) DeleteObject(key string) error {
	_, err := c.client.DeleteObject(c.ctx(), &oss.DeleteObjectRequest{
		Bucket: oss.Ptr(c.bucketName),
		Key:    oss.Ptr(key),
	})
	return err
}

// DeleteObjects deletes multiple objects
func (c *OSSClient) DeleteObjects(keys []string) error {
	if len(keys) == 0 {
		return nil
	}
	objects := make([]oss.DeleteObject, len(keys))
	for i, k := range keys {
		objects[i] = oss.DeleteObject{Key: oss.Ptr(k)}
	}
	_, err := c.client.DeleteMultipleObjects(c.ctx(), &oss.DeleteMultipleObjectsRequest{
		Bucket:  oss.Ptr(c.bucketName),
		Objects: objects,
		Quiet:   true,
	})
	return err
}

// CopyObject copies a single object
func (c *OSSClient) CopyObject(srcKey, dstKey string) error {
	_, err := c.client.CopyObject(c.ctx(), &oss.CopyObjectRequest{
		Bucket:    oss.Ptr(c.bucketName),
		Key:       oss.Ptr(dstKey),
		SourceKey: oss.Ptr(srcKey),
	})
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
	var token *string

	for {
		req := &oss.ListObjectsV2Request{
			Bucket:  oss.Ptr(c.bucketName),
			Prefix:  oss.Ptr(prefix),
			MaxKeys: 1000,
		}
		if token != nil {
			req.ContinuationToken = token
		}

		result, err := c.client.ListObjectsV2(c.ctx(), req)
		if err != nil {
			return nil, err
		}

		for _, obj := range result.Contents {
			allObjects = append(allObjects, ObjectInfo{
				Key:  oss.ToString(obj.Key),
				Size: obj.Size,
			})
		}

		if !result.IsTruncated {
			break
		}
		token = result.NextContinuationToken
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

// GeneratePresignedURL generates a presigned download URL
func (c *OSSClient) GeneratePresignedURL(key string, expires time.Duration) (string, error) {
	result, err := c.client.Presign(c.ctx(), &oss.GetObjectRequest{
		Bucket: oss.Ptr(c.bucketName),
		Key:    oss.Ptr(key),
	}, oss.PresignExpires(expires))
	if err != nil {
		return "", err
	}
	return result.URL, nil
}

// GetObjectContent reads an object's content and returns it as a ReadCloser
func (c *OSSClient) GetObjectContent(key string) (io.ReadCloser, error) {
	result, err := c.client.GetObject(c.ctx(), &oss.GetObjectRequest{
		Bucket: oss.Ptr(c.bucketName),
		Key:    oss.Ptr(key),
	})
	if err != nil {
		return nil, err
	}
	return result.Body, nil
}

// GetObjectContentRange reads a byte range of an object's content
func (c *OSSClient) GetObjectContentRange(key string, start, end int64) (io.ReadCloser, error) {
	result, err := c.client.GetObject(c.ctx(), &oss.GetObjectRequest{
		Bucket: oss.Ptr(c.bucketName),
		Key:    oss.Ptr(key),
		Range:  oss.Ptr(fmt.Sprintf("bytes=%d-%d", start, end)),
	})
	if err != nil {
		return nil, err
	}
	return result.Body, nil
}

// PutObjectContent writes string content to an object
func (c *OSSClient) PutObjectContent(key, content string) error {
	_, err := c.client.PutObject(c.ctx(), &oss.PutObjectRequest{
		Bucket: oss.Ptr(c.bucketName),
		Key:    oss.Ptr(key),
		Body:   strings.NewReader(content),
	})
	return err
}

// PutObjectBytes writes binary data to an object
func (c *OSSClient) PutObjectBytes(key string, data []byte) error {
	_, err := c.client.PutObject(c.ctx(), &oss.PutObjectRequest{
		Bucket: oss.Ptr(c.bucketName),
		Key:    oss.Ptr(key),
		Body:   bytes.NewReader(data),
	})
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

// DownloadToFile downloads an object from OSS to a local file.
func (c *OSSClient) DownloadToFile(key, localPath string) error {
	_, err := c.client.GetObjectToFile(c.ctx(), &oss.GetObjectRequest{
		Bucket: oss.Ptr(c.bucketName),
		Key:    oss.Ptr(key),
	}, localPath)
	return err
}

// UploadFromFile uploads a local file to OSS.
func (c *OSSClient) UploadFromFile(key, localPath string) error {
	return c.UploadFromFileCtx(c.ctx(), key, localPath)
}

func (c *OSSClient) UploadFromFileCtx(ctx context.Context, key, localPath string) error {
	f, err := os.Open(localPath)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	_, err = c.client.PutObject(ctx, &oss.PutObjectRequest{
		Bucket: oss.Ptr(c.bucketName),
		Key:    oss.Ptr(key),
		Body:   f,
	})
	return err
}
