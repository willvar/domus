package config

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestSaveAtomicallyReplacesConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	cfg := validConfig()
	if err := Save(path, cfg); err != nil {
		t.Fatal(err)
	}
	previous, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	reader, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()

	cfg.Server.Port++
	if err := Save(path, cfg); err != nil {
		t.Fatal(err)
	}
	// A reader that already opened the previous configuration must retain a
	// complete snapshot, rather than seeing the inode truncated and rewritten.
	snapshot, err := io.ReadAll(reader)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(snapshot, previous) {
		t.Fatal("Save rewrote the configuration underneath an existing reader")
	}
	current, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if current.Server.Port != cfg.Server.Port || current.Server.EncryptionSecret != cfg.Server.EncryptionSecret {
		t.Fatal("the new configuration was not published completely")
	}
}

func TestSaveConcurrentReadersSeeCompleteConfigs(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	first := validConfig()
	second := *first
	second.Server.Port++
	firstBytes, err := yaml.Marshal(first)
	if err != nil {
		t.Fatal(err)
	}
	secondBytes, err := yaml.Marshal(&second)
	if err != nil {
		t.Fatal(err)
	}
	if err := Save(path, first); err != nil {
		t.Fatal(err)
	}

	stop := make(chan struct{})
	failures := make(chan error, 1)
	var readers sync.WaitGroup
	for range 4 {
		readers.Go(func() {
			for {
				select {
				case <-stop:
					return
				default:
				}
				data, err := os.ReadFile(path)
				if err == nil && !bytes.Equal(data, firstBytes) && !bytes.Equal(data, secondBytes) {
					err = fmt.Errorf("reader observed an incomplete configuration (%d bytes)", len(data))
				}
				if err != nil {
					select {
					case failures <- err:
					default:
					}
					return
				}
			}
		})
	}
	for index := range 100 {
		cfg := first
		if index%2 == 0 {
			cfg = &second
		}
		if err := Save(path, cfg); err != nil {
			t.Error(err)
			break
		}
	}
	close(stop)
	readers.Wait()
	select {
	case err := <-failures:
		t.Fatal(err)
	default:
	}
}

func TestSaveMakesExistingConfigPrivate(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte("server: {}\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := Save(path, validConfig()); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0600 {
		t.Fatalf("saved configuration mode = %04o, want 0600", info.Mode().Perm())
	}
}

func TestSaveFailureLeavesTargetAndNoTemporaryFiles(t *testing.T) {
	parent := t.TempDir()
	path := filepath.Join(parent, "config.yaml")
	if err := os.Mkdir(path, 0700); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(path, "keep")
	if err := os.WriteFile(marker, []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := Save(path, validConfig()); err == nil {
		t.Fatal("Save unexpectedly replaced a directory")
	}
	if data, err := os.ReadFile(marker); err != nil || string(data) != "keep" {
		t.Fatal("Save changed the existing target on failure")
	}
	entries, err := os.ReadDir(parent)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != "config.yaml" {
		t.Fatal("Save left temporary configuration files behind")
	}
}

func TestSavePreservesExplicitSymlink(t *testing.T) {
	parent := t.TempDir()
	path := filepath.Join(parent, "instance.yaml")
	link := filepath.Join(parent, "config.yaml")
	if err := Save(path, validConfig()); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("instance.yaml", link); err != nil {
		t.Fatal(err)
	}
	cfg := validConfig()
	cfg.Server.Port++
	if err := Save(link, cfg); err != nil {
		t.Fatal(err)
	}
	info, err := os.Lstat(link)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		t.Fatal("Save replaced the explicit configuration symlink")
	}
	current, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if current.Server.Port != cfg.Server.Port {
		t.Fatal("Save did not publish to the symlink target")
	}
}
