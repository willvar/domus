package store

import (
	"strings"
	"testing"
	"time"

	"domus/config"
)

func TestOSSClientMapsLogicalKeysIntoConfiguredPrefix(t *testing.T) {
	fileStore, err := NewOSSClientFromConfig(config.OSSConfig{
		ServerEndpoint: "s3.example.test", ClientUploadEndpoint: "s3.example.test",
		ClientDownloadEndpoint: "media.example.test", AccessKeyID: "access", AccessKeySecret: "secret",
		Bucket: "shared-bucket", Region: "test-1", Prefix: "/domus/development/",
	})
	if err != nil {
		t.Fatalf("NewOSSClientFromConfig() error = %v", err)
	}
	client := fileStore.(*OSSClient)

	physical, err := client.physicalKey(".dofs/v1/object")
	if err != nil {
		t.Fatalf("physicalKey() error = %v", err)
	}
	if physical != "domus/development/.dofs/v1/object" {
		t.Fatalf("physicalKey() = %q", physical)
	}
	listPrefix, err := client.physicalListPrefix("")
	if err != nil {
		t.Fatalf("physicalListPrefix() error = %v", err)
	}
	if listPrefix != "domus/development/" {
		t.Fatalf("physicalListPrefix() = %q", listPrefix)
	}
	logical, err := client.logicalKey(physical)
	if err != nil || logical != ".dofs/v1/object" {
		t.Fatalf("logicalKey() = %q, %v", logical, err)
	}

	downloadURL, err := client.GeneratePresignedURL(".dofs/v1/object", time.Minute)
	if err != nil {
		t.Fatalf("GeneratePresignedURL() error = %v", err)
	}
	if downloadURL != "https://media.example.test/domus/development/.dofs/v1/object" {
		t.Fatalf("download URL = %q", downloadURL)
	}
	putURL, err := client.PresignedPutObject(".dofs/v1/object", time.Minute)
	if err != nil {
		t.Fatalf("PresignedPutObject() error = %v", err)
	}
	if !strings.Contains(putURL, "/domus/development/.dofs/v1/object?") {
		t.Fatalf("presigned PUT URL does not contain physical prefix: %q", putURL)
	}
}

func TestOSSClientRejectsKeysThatCanEscapeURLPrefix(t *testing.T) {
	client := &OSSClient{objectPrefix: "domus/development"}
	for _, key := range []string{"", "/absolute", "../other-app/object", "media/../other-app", "media/./object"} {
		if _, err := client.physicalKey(key); err == nil {
			t.Fatalf("physicalKey(%q) should fail", key)
		}
	}
	if _, err := client.logicalKey("other-app/development/object"); err == nil {
		t.Fatal("logicalKey() accepted an object outside the configured prefix")
	}
}

func TestOSSClientEmptyPrefixPreservesLegacyKeys(t *testing.T) {
	client := &OSSClient{}
	physical, err := client.physicalKey("a//b")
	if err != nil || physical != "a//b" {
		t.Fatalf("physicalKey() = %q, %v", physical, err)
	}
	listPrefix, err := client.physicalListPrefix("")
	if err != nil || listPrefix != "" {
		t.Fatalf("physicalListPrefix() = %q, %v", listPrefix, err)
	}
}

func TestOSSClientFromConfigUsesPlainHTTPForLoopbackEndpoints(t *testing.T) {
	fileStore, err := NewOSSClientFromConfig(config.OSSConfig{
		ServerEndpoint: "127.0.0.1:8333", ClientUploadEndpoint: "127.0.0.1:8333",
		AccessKeyID: "access", AccessKeySecret: "secret",
		Bucket: "domus-dev", Region: "us-east-1",
	})
	if err != nil {
		t.Fatalf("NewOSSClientFromConfig() error = %v", err)
	}
	putURL, err := fileStore.(*OSSClient).PresignedPutObject(".dofs/v1/object", time.Minute)
	if err != nil {
		t.Fatalf("PresignedPutObject() error = %v", err)
	}
	if !strings.HasPrefix(putURL, "http://127.0.0.1:8333/") {
		t.Fatalf("presigned PUT URL = %q, want local HTTP endpoint", putURL)
	}
}
