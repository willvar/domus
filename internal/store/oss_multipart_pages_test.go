package store

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"domus/config"
	"github.com/minio/minio-go/v7"
)

func TestOSSListPartsPagination(t *testing.T) {
	for _, test := range []struct {
		name      string
		boundary  string
		next      int
		wantError bool
	}{
		{name: "exclusive marker", next: 2},
		{name: "inclusive marker", boundary: `<Part><PartNumber>2</PartNumber><Size>7</Size><ETag>two</ETag></Part>`, next: 2},
		{name: "conflicting size", boundary: `<Part><PartNumber>2</PartNumber><Size>8</Size><ETag>two</ETag></Part>`, next: 2, wantError: true},
		{name: "conflicting etag", boundary: `<Part><PartNumber>2</PartNumber><Size>7</Size><ETag>changed</ETag></Part>`, next: 2, wantError: true},
		{name: "non advancing marker", next: 0, wantError: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			requests := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests++
				w.Header().Set("Content-Type", "application/xml")
				if requests > 2 {
					http.Error(w, "pagination loop", http.StatusBadRequest)
					return
				}
				if r.URL.Query().Get("part-number-marker") == "" || r.URL.Query().Get("part-number-marker") == "0" {
					fmt.Fprintf(w, `<ListPartsResult><IsTruncated>true</IsTruncated><NextPartNumberMarker>%d</NextPartNumberMarker><Part><PartNumber>1</PartNumber><Size>7</Size><ETag>one</ETag></Part><Part><PartNumber>2</PartNumber><Size>7</Size><ETag>two</ETag></Part></ListPartsResult>`, test.next)
				} else {
					fmt.Fprintf(w, `<ListPartsResult><IsTruncated>false</IsTruncated>%s<Part><PartNumber>3</PartNumber><Size>7</Size><ETag>three</ETag></Part></ListPartsResult>`, test.boundary)
				}
			}))
			defer server.Close()
			client, err := NewOSSClient(OSSOptions{ServerEndpointURL: server.URL, ClientUploadEndpointURL: server.URL, AccessKeyID: "test", AccessKeySecret: "test", Bucket: "test-bucket", Region: "us-east-1"})
			if err != nil {
				t.Fatal(err)
			}
			parts, err := client.ListParts("test.bin", "test-upload")
			if test.wantError {
				if err == nil {
					t.Fatal("expected invalid pagination to fail")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if len(parts) != 3 {
				t.Fatalf("got %d parts, want 3", len(parts))
			}
			for i, part := range parts {
				if part.PartNumber != i+1 || part.Size != 7 {
					t.Fatalf("invalid part at index %d: %+v", i, part)
				}
			}
		})
	}
}

// Opt-in protocol test against a configured development store. It creates only
// a unique multipart fixture and aborts it on exit, including failed assertions.
func TestOSSListPartsAcrossPages(t *testing.T) {
	path := os.Getenv("DOMUS_TEST_OSS_CONFIG")
	if path == "" {
		t.Skip("set DOMUS_TEST_OSS_CONFIG to a development configuration")
	}
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	store, err := NewOSSClientFromConfig(cfg.OSS)
	if err != nil {
		t.Fatal(err)
	}
	c := store.(*OSSClient)
	key := fmt.Sprintf("multipart-test/%d", time.Now().UnixNano())
	physical, err := c.physicalKey(key)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	uploadID, err := c.serverCore.NewMultipartUpload(ctx, c.bucketName, physical, minio.PutObjectOptions{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := c.AbortMultipartUpload(key, uploadID); err != nil {
			t.Error(err)
		}
	})
	const count = 1003
	for part := 1; part <= count; part++ {
		_, err := c.serverCore.PutObjectPart(ctx, c.bucketName, physical, uploadID, part, bytes.NewReader([]byte("fixture")), 7, minio.PutObjectPartOptions{})
		if err != nil {
			t.Fatalf("part %d: %v", part, err)
		}
	}
	for _, marker := range []int{0, 1000} {
		result, err := c.serverCore.ListObjectParts(ctx, c.bucketName, physical, uploadID, marker, 1000)
		if err != nil {
			t.Fatal(err)
		}
		first, last := 0, 0
		if len(result.ObjectParts) > 0 {
			first = result.ObjectParts[0].PartNumber
			last = result.ObjectParts[len(result.ObjectParts)-1].PartNumber
		}
		t.Logf("marker=%d count=%d first=%d last=%d truncated=%t next=%d", marker, len(result.ObjectParts), first, last, result.IsTruncated, result.NextPartNumberMarker)
	}
	parts, err := c.ListParts(key, uploadID)
	if err != nil {
		t.Fatal(err)
	}
	if len(parts) != count {
		t.Fatalf("listed %d parts; want %d", len(parts), count)
	}
	for i, part := range parts {
		if part.PartNumber != i+1 || part.Size != 7 {
			t.Fatalf("part at index %d: number=%d size=%d", i, part.PartNumber, part.Size)
		}
	}
}
