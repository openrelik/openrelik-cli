package config

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/openrelik/openrelik-go-client"
)

const (
	configDir        = ".openrelik"
	settingsFile     = "settings.json"
	credentialsFile  = "credentials.json"
	workersCacheFile = "workers_cache.json"
	dirPerm          = 0700
	filePerm         = 0600
	workersCacheTTL  = time.Hour
)

// ErrCacheMissing is returned when the workers cache file does not exist.
var ErrCacheMissing = errors.New("workers cache does not exist")

// ErrCacheStale is returned when the workers cache is older than the TTL.
var ErrCacheStale = errors.New("workers cache is stale")

// workersCacheEntry is the on-disk representation of the workers cache.
type workersCacheEntry struct {
	Workers []openrelik.Worker `json:"workers"`
	SavedAt time.Time          `json:"saved_at"`
}

type ServerConfig struct {
	URL string `json:"url"`
}

type Settings struct {
	ActiveServer string         `json:"active_server"`
	Servers      []ServerConfig `json:"servers"`

	// Deprecated: Use Servers and ActiveServer
	ServerURL string `json:"server_url,omitempty"`
}

type Credentials struct {
	APIKeys map[string]string `json:"api_keys"` // URL -> APIKey
}

func (c Credentials) String() string {
	return "********"
}

func (s *Settings) GetActiveServer() *ServerConfig {
	for i := range s.Servers {
		if s.Servers[i].URL == s.ActiveServer {
			return &s.Servers[i]
		}
	}
	if len(s.Servers) > 0 {
		return &s.Servers[0]
	}
	return nil
}

func (s *Settings) GetServerByURL(url string) *ServerConfig {
	for i := range s.Servers {
		if s.Servers[i].URL == url {
			return &s.Servers[i]
		}
	}
	return nil
}

var baseDir string

// SetBaseDir sets the base directory for configuration files (used for testing).
func SetBaseDir(dir string) {
	baseDir = dir
}

func GetConfigDir() (string, error) {
	if baseDir != "" {
		return filepath.Join(baseDir, configDir), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("could not get home directory: %w", err)
	}
	// Consider using os.UserConfigDir() in the future for better OS integration.
	return filepath.Join(home, configDir), nil
}

func EnsureConfigDir() (string, error) {
	dir, err := GetConfigDir()
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(dir, dirPerm); err != nil {
		return "", fmt.Errorf("could not create config directory: %w", err)
	}
	return dir, nil
}

func LoadSettings() (*Settings, error) {
	dir, err := GetConfigDir()
	if err != nil {
		return nil, err
	}
	path := filepath.Join(dir, settingsFile)
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read settings file %s: %w", path, err)
	}
	var s Settings
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, fmt.Errorf("failed to unmarshal settings file %s: %w", path, err)
	}

	// Migration for old settings format
	if s.ServerURL != "" && len(s.Servers) == 0 {
		s.Servers = []ServerConfig{{URL: s.ServerURL}}
		s.ActiveServer = s.ServerURL
		s.ServerURL = ""
	}

	return &s, nil
}

func SaveSettings(s *Settings) error {
	dir, err := EnsureConfigDir()
	if err != nil {
		return err
	}
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal settings: %w", err)
	}
	path := filepath.Join(dir, settingsFile)
	return saveAtomic(path, data)
}

func LoadCredentials() (*Credentials, error) {
	dir, err := GetConfigDir()
	if err != nil {
		return nil, err
	}
	path := filepath.Join(dir, credentialsFile)
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read credentials file %s: %w", path, err)
	}
	var c Credentials
	if err := json.Unmarshal(data, &c); err != nil {
		return nil, fmt.Errorf("failed to unmarshal credentials file %s: %w", path, err)
	}

	if c.APIKeys == nil {
		c.APIKeys = make(map[string]string)
	}

	return &c, nil
}


func SaveCredentials(c *Credentials) error {
	dir, err := EnsureConfigDir()
	if err != nil {
		return err
	}
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal credentials: %w", err)
	}
	path := filepath.Join(dir, credentialsFile)
	return saveAtomic(path, data)
}

// LoadWorkersCache reads the workers cache from disk. Returns ErrCacheMissing if the
// cache file does not exist, or ErrCacheStale if it is older than workersCacheTTL.
func LoadWorkersCache() ([]openrelik.Worker, error) {
	dir, err := GetConfigDir()
	if err != nil {
		return nil, err
	}
	path := filepath.Join(dir, workersCacheFile)
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, ErrCacheMissing
		}
		return nil, fmt.Errorf("failed to read workers cache file %s: %w", path, err)
	}
	var entry workersCacheEntry
	if err := json.Unmarshal(data, &entry); err != nil {
		return nil, fmt.Errorf("failed to unmarshal workers cache file %s: %w", path, err)
	}
	if time.Since(entry.SavedAt) > workersCacheTTL {
		return nil, ErrCacheStale
	}
	return entry.Workers, nil
}

// SaveWorkersCache writes workers to the cache file with the current timestamp.
func SaveWorkersCache(w []openrelik.Worker) error {
	dir, err := EnsureConfigDir()
	if err != nil {
		return err
	}
	entry := workersCacheEntry{Workers: w, SavedAt: time.Now()}
	data, err := json.MarshalIndent(entry, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal workers cache: %w", err)
	}
	path := filepath.Join(dir, workersCacheFile)
	return saveAtomic(path, data)
}

// LoadOrRefreshWorkersCache loads the workers cache. If the cache is missing or stale,
// it calls refresh to fetch fresh data, saves it, and returns it.
func LoadOrRefreshWorkersCache(ctx context.Context, refresh func(context.Context) ([]openrelik.Worker, error)) ([]openrelik.Worker, error) {
	workers, err := LoadWorkersCache()
	if err == nil {
		return workers, nil
	}
	if !errors.Is(err, ErrCacheMissing) && !errors.Is(err, ErrCacheStale) {
		return nil, err
	}
	workers, err = refresh(ctx)
	if err != nil {
		return nil, err
	}
	_ = SaveWorkersCache(workers) // best-effort; callers get the data regardless
	return workers, nil
}

// saveAtomic writes data to a temporary file and then renames it to the target path
// to ensure the write is atomic.
func saveAtomic(path string, data []byte) error {
	tmpFile := path + ".tmp"
	if err := os.WriteFile(tmpFile, data, filePerm); err != nil {
		return fmt.Errorf("failed to write temporary file %s: %w", tmpFile, err)
	}
	if err := os.Rename(tmpFile, path); err != nil {
		_ = os.Remove(tmpFile) // Best effort cleanup
		return fmt.Errorf("failed to rename %s to %s: %w", tmpFile, path, err)
	}
	return nil
}
