package config

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/openrelik/openrelik-go-client"
)

func TestConfig(t *testing.T) {
	// Setup temp dir for config
	tmpDir := t.TempDir()
	originalBaseDir := baseDir
	baseDir = tmpDir
	defer func() { baseDir = originalBaseDir }()

	t.Run("SaveAndLoadSettings", func(t *testing.T) {
		s := &Settings{ServerURL: "http://test-server"}
		if err := SaveSettings(s); err != nil {
			t.Fatalf("SaveSettings failed: %v", err)
		}

		loaded, err := LoadSettings()
		if err != nil {
			t.Fatalf("LoadSettings failed: %v", err)
		}

		if loaded.ServerURL != s.ServerURL {
			t.Errorf("expected ServerURL %q, got %q", s.ServerURL, loaded.ServerURL)
		}

		// Verify directory and file permissions
		dir, _ := GetConfigDir()
		info, err := os.Stat(dir)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != dirPerm {
			t.Errorf("expected dir perm %o, got %o", dirPerm, info.Mode().Perm())
		}

		info, err = os.Stat(filepath.Join(dir, settingsFile))
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != filePerm {
			t.Errorf("expected file perm %o, got %o", filePerm, info.Mode().Perm())
		}
	})

	t.Run("SaveAndLoadCredentials", func(t *testing.T) {
		c := &Credentials{APIKey: "test-api-key"}
		if err := SaveCredentials(c); err != nil {
			t.Fatalf("SaveCredentials failed: %v", err)
		}

		loaded, err := LoadCredentials()
		if err != nil {
			t.Fatalf("LoadCredentials failed: %v", err)
		}

		if loaded.APIKey != c.APIKey {
			t.Errorf("expected APIKey %q, got %q", c.APIKey, loaded.APIKey)
		}
	})

	t.Run("LoadMissingFiles", func(t *testing.T) {
		// Clean up files
		dir, _ := GetConfigDir()
		os.Remove(filepath.Join(dir, settingsFile))
		os.Remove(filepath.Join(dir, authCredsFile))

		_, err := LoadSettings()
		if err == nil {
			t.Error("expected error loading missing settings, got nil")
		}

		_, err = LoadCredentials()
		if err == nil {
			t.Error("expected error loading missing credentials, got nil")
		}
	})

	t.Run("LoadInvalidJSON", func(t *testing.T) {
		dir, _ := GetConfigDir()
		os.MkdirAll(dir, dirPerm)

		os.WriteFile(filepath.Join(dir, settingsFile), []byte("invalid json"), filePerm)
		_, err := LoadSettings()
		if err == nil {
			t.Error("expected error loading invalid settings, got nil")
		}

		os.WriteFile(filepath.Join(dir, authCredsFile), []byte("invalid json"), filePerm)
		_, err = LoadCredentials()
		if err == nil {
			t.Error("expected error loading invalid credentials, got nil")
		}
	})

	t.Run("EnsureConfigDirError", func(t *testing.T) {
		// Create a file where the directory should be
		tmpDir := t.TempDir()
		originalBaseDir := baseDir
		baseDir = tmpDir
		defer func() { baseDir = originalBaseDir }()

		dir, _ := GetConfigDir()
		os.WriteFile(dir, []byte("i am a file"), 0644)

		_, err := EnsureConfigDir()
		if err == nil {
			t.Error("expected error when config dir is a file, got nil")
		}
	})

	t.Run("GetConfigDirNoBase", func(t *testing.T) {
		originalBaseDir := baseDir
		baseDir = ""
		defer func() { baseDir = originalBaseDir }()

		dir, err := GetConfigDir()
		if err != nil {
			t.Fatalf("GetConfigDir failed: %v", err)
		}
		if !strings.Contains(dir, configDir) {
			t.Errorf("expected dir %q to contain %q", dir, configDir)
		}
	})

	t.Run("CredentialsMasking", func(t *testing.T) {
		c := Credentials{APIKey: "secret-key"}
		str := c.String()
		if str != "********" {
			t.Errorf("expected masked credentials, got %q", str)
		}
		if strings.Contains(str, "secret-key") {
			t.Error("masked string contains the secret key")
		}
	})

	t.Run("WorkersCacheMissing", func(t *testing.T) {
		_, err := LoadWorkersCache()
		if !errors.Is(err, ErrCacheMissing) {
			t.Errorf("expected ErrCacheMissing, got %v", err)
		}
	})

	t.Run("WorkersCacheSaveAndLoad", func(t *testing.T) {
		workers := []openrelik.Worker{{TaskName: "test-worker"}}
		if err := SaveWorkersCache(workers); err != nil {
			t.Fatalf("SaveWorkersCache failed: %v", err)
		}
		loaded, err := LoadWorkersCache()
		if err != nil {
			t.Fatalf("LoadWorkersCache failed: %v", err)
		}
		if len(loaded) != 1 || loaded[0].TaskName != "test-worker" {
			t.Errorf("unexpected workers: %+v", loaded)
		}
	})

	t.Run("WorkersCacheStale", func(t *testing.T) {
		dir, _ := EnsureConfigDir()
		// Write a cache entry with an old timestamp.
		entry := workersCacheEntry{
			Workers: []openrelik.Worker{{TaskName: "old-worker"}},
			SavedAt: time.Now().Add(-2 * time.Hour),
		}
		data, _ := json.MarshalIndent(entry, "", "  ")
		os.WriteFile(filepath.Join(dir, workersCacheFile), data, filePerm)

		_, err := LoadWorkersCache()
		if !errors.Is(err, ErrCacheStale) {
			t.Errorf("expected ErrCacheStale, got %v", err)
		}
	})

	t.Run("LoadOrRefreshWorkersCacheFromAPI", func(t *testing.T) {
		// Remove the cache file to simulate missing cache.
		dir, _ := GetConfigDir()
		os.Remove(filepath.Join(dir, workersCacheFile))

		refreshed := []openrelik.Worker{{TaskName: "fresh-worker"}}
		workers, err := LoadOrRefreshWorkersCache(context.Background(), func(_ context.Context) ([]openrelik.Worker, error) {
			return refreshed, nil
		})
		if err != nil {
			t.Fatalf("LoadOrRefreshWorkersCache failed: %v", err)
		}
		if len(workers) != 1 || workers[0].TaskName != "fresh-worker" {
			t.Errorf("unexpected workers: %+v", workers)
		}
		// Verify cache was saved.
		loaded, err := LoadWorkersCache()
		if err != nil {
			t.Fatalf("cache should be fresh after refresh, got: %v", err)
		}
		if len(loaded) != 1 || loaded[0].TaskName != "fresh-worker" {
			t.Errorf("unexpected cached workers: %+v", loaded)
		}
	})

	t.Run("WriteError", func(t *testing.T) {
		tmpDir := t.TempDir()
		originalBaseDir := baseDir
		baseDir = tmpDir
		defer func() { baseDir = originalBaseDir }()

		dir, _ := EnsureConfigDir()
		// Make directory read-only
		os.Chmod(dir, 0400)
		defer os.Chmod(dir, 0700)

		err := SaveSettings(&Settings{ServerURL: "test"})
		if err == nil {
			t.Error("expected error writing to read-only dir, got nil")
		}

		err = SaveCredentials(&Credentials{APIKey: "test"})
		if err == nil {
			t.Error("expected error writing to read-only dir, got nil")
		}
	})
}
