package store

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"io"
	"os"
	"slices"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/minio/minio-go/v7"
)

func TestSeaweedFSPrefixIsolation(t *testing.T) {
	if os.Getenv("DOMUS_SEAWEEDFS_INTEGRATION") != "1" {
		t.Skip("set DOMUS_SEAWEEDFS_INTEGRATION=1 to exercise OSS prefix isolation")
	}
	endpoint := strings.TrimSpace(os.Getenv("DOMUS_TEST_S3_ENDPOINT"))
	if endpoint == "" {
		t.Fatal("DOMUS_TEST_S3_ENDPOINT is required")
	}
	accessKey := environmentOr("DOMUS_TEST_S3_ACCESS_KEY", "domus-test-access")
	secretKey := environmentOr("DOMUS_TEST_S3_SECRET_KEY", "domus-test-secret")
	bucket := "domus-prefix-" + randomTestHex(t, 8)
	testRoot := "prefix-contract/" + randomTestHex(t, 8)
	prefixA := testRoot + "/a"
	prefixB := testRoot + "/b"
	outsideKey := testRoot + "/outside/sentinel.bin"
	clientA := newIntegrationOSSClient(t, endpoint, accessKey, secretKey, bucket, prefixA)
	client := clientA.serverClient
	if err := client.MakeBucket(t.Context(), bucket, minio.MakeBucketOptions{Region: "us-east-1"}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		for object := range client.ListObjects(ctx, bucket, minio.ListObjectsOptions{Recursive: true}) {
			if object.Err != nil {
				t.Errorf("list cleanup objects: %v", object.Err)
				continue
			}
			if err := client.RemoveObject(ctx, bucket, object.Key, minio.RemoveObjectOptions{}); err != nil {
				t.Errorf("remove cleanup object %q: %v", object.Key, err)
			}
		}
		if err := client.RemoveBucket(ctx, bucket); err != nil {
			t.Errorf("remove test bucket %q: %v", bucket, err)
		}
	})

	clientB := newIntegrationOSSClient(t, endpoint, accessKey, secretKey, bucket, prefixB)
	for name, scopedClient := range map[string]*OSSClient{"prefix A": clientA, "prefix B": clientB} {
		if err := scopedClient.Check(t.Context()); err != nil {
			t.Fatalf("%s bucket check: %v", name, err)
		}
	}

	if err := clientA.PutObject("nested/file.bin", bytes.NewReader([]byte("prefix-a")), int64(len("prefix-a"))); err != nil {
		t.Fatal(err)
	}
	if err := clientB.PutObject("sentinel.bin", bytes.NewReader([]byte("prefix-b")), int64(len("prefix-b"))); err != nil {
		t.Fatal(err)
	}
	if _, err := client.PutObject(
		t.Context(), bucket, outsideKey, bytes.NewReader([]byte("outside")), int64(len("outside")), minio.PutObjectOptions{},
	); err != nil {
		t.Fatal(err)
	}

	objects, err := clientA.ListAllObjects("")
	if err != nil {
		t.Fatal(err)
	}
	if len(objects) != 1 || objects[0].Key != "nested/file.bin" {
		t.Fatalf("prefix A logical listing = %#v", objects)
	}
	if _, err := client.StatObject(t.Context(), bucket, prefixA+"/nested/file.bin", minio.StatObjectOptions{}); err != nil {
		t.Fatalf("prefix A physical object is unavailable: %v", err)
	}

	if err := clientA.DeleteAllObjects(nil); err != nil {
		t.Fatal(err)
	}
	objects, err = clientA.ListAllObjects("")
	if err != nil {
		t.Fatal(err)
	}
	if len(objects) != 0 {
		t.Fatalf("prefix A still contains objects after deletion: %#v", objects)
	}
	if _, err := client.StatObject(t.Context(), bucket, prefixA+"/nested/file.bin", minio.StatObjectOptions{}); err == nil {
		t.Fatal("prefix A physical object survived DeleteAllObjects")
	}

	assertStoredContent(t, clientB, "sentinel.bin", "prefix-b")
	object, err := client.GetObject(t.Context(), bucket, outsideKey, minio.GetObjectOptions{})
	if err != nil {
		t.Fatalf("open outside sentinel: %v", err)
	}
	content, readErr := io.ReadAll(object)
	closeErr := object.Close()
	if readErr != nil || closeErr != nil || string(content) != "outside" {
		t.Fatalf("outside sentinel = %q, read=%v, close=%v", content, readErr, closeErr)
	}

	remaining, err := rawObjectKeys(t.Context(), client, bucket)
	if err != nil {
		t.Fatal(err)
	}
	wantRemaining := []string{outsideKey, prefixB + "/sentinel.bin"}
	sort.Strings(wantRemaining)
	if !slices.Equal(remaining, wantRemaining) {
		t.Fatalf("remaining physical objects = %v, want %v", remaining, wantRemaining)
	}
}

func newIntegrationOSSClient(t *testing.T, endpoint, accessKey, secretKey, bucket, prefix string) *OSSClient {
	t.Helper()
	client, err := NewOSSClient(OSSOptions{
		ServerEndpointURL: endpoint, ClientUploadEndpointURL: endpoint,
		AccessKeyID: accessKey, AccessKeySecret: secretKey,
		Bucket: bucket, Region: "us-east-1", Prefix: prefix,
	})
	if err != nil {
		t.Fatalf("create integration client: %v", err)
	}
	return client
}

func assertStoredContent(t *testing.T, client *OSSClient, key, want string) {
	t.Helper()
	object, err := client.GetObjectContent(key)
	if err != nil {
		t.Fatal(err)
	}
	content, readErr := io.ReadAll(object)
	closeErr := object.Close()
	if readErr != nil || closeErr != nil || string(content) != want {
		t.Fatalf("object %q = %q, read=%v, close=%v", key, content, readErr, closeErr)
	}
}

func rawObjectKeys(ctx context.Context, client *minio.Client, bucket string) ([]string, error) {
	var keys []string
	for object := range client.ListObjects(ctx, bucket, minio.ListObjectsOptions{Recursive: true}) {
		if object.Err != nil {
			return nil, object.Err
		}
		keys = append(keys, object.Key)
	}
	sort.Strings(keys)
	return keys, nil
}

func randomTestHex(t *testing.T, size int) string {
	t.Helper()
	buffer := make([]byte, size)
	if _, err := rand.Read(buffer); err != nil {
		t.Fatal(err)
	}
	return hex.EncodeToString(buffer)
}

func environmentOr(name, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(name)); value != "" {
		return value
	}
	return fallback
}
