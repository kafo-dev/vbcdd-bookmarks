package main

import (
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/BurntSushi/toml"
)

const (
	// appDirName is the directory the program owns under each XDG base
	// directory, from the configuration root to the cache root.
	appDirName     = "vbcdd/bookmarks"
	configFileName = "config.toml"
)

// Config names one bookmark file under one WebDAV directory. The field names
// are the ones the file uses, so no mapping sits between the two.
type Config struct {
	URL      string `toml:"url"`
	File     string `toml:"file"`
	User     string `toml:"user"`
	Password string `toml:"password"`
}

// configPath returns $XDG_CONFIG_HOME/vbcdd/bookmarks/config.toml.
func configPath() (string, error) {
	root, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("config: %w", err)
	}
	return filepath.Join(root, appDirName, configFileName), nil
}

// cachePath returns $XDG_CACHE_HOME/vbcdd/bookmarks/<name>, the local copy of
// the remote file.
func cachePath(remoteName string) (string, error) {
	root, err := os.UserCacheDir()
	if err != nil {
		return "", fmt.Errorf("cache: %w", err)
	}
	return filepath.Join(root, appDirName, filepath.Base(remoteName)), nil
}

func loadConfig(path string) (Config, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return Config{}, fmt.Errorf("config: %w", err)
	}
	var cfg Config
	if err = toml.Unmarshal(raw, &cfg); err != nil {
		return Config{}, fmt.Errorf("config %s: %w", path, err)
	}
	if err = cfg.validate(); err != nil {
		return Config{}, fmt.Errorf("config %s: %w", path, err)
	}
	return cfg.normalize(), nil
}

// validate reports the first field a writer could not use.
func (c Config) validate() error {
	fields := []struct {
		name  string
		value string
	}{
		{"url", c.URL},
		{"file", c.File},
		{"user", c.User},
		{"password", c.Password},
	}
	for _, field := range fields {
		if strings.TrimSpace(field.value) == "" {
			return fmt.Errorf("field %q must not be empty", field.name)
		}
	}
	parsed, err := url.Parse(c.URL)
	if err != nil {
		return fmt.Errorf("url %q: %w", c.URL, err)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return fmt.Errorf("url %q needs an http or https scheme", c.URL)
	}
	if parsed.Host == "" {
		return fmt.Errorf("url %q names no host", c.URL)
	}
	if parsed.RawQuery != "" || parsed.Fragment != "" {
		return fmt.Errorf("url %q must be a directory, without a query", c.URL)
	}
	if strings.HasPrefix(c.File, "/") {
		// Floccus rejects a leading slash, and a reader that accepted it
		// would address a different resource than Floccus does.
		return fmt.Errorf("file %q must be relative to the url", c.File)
	}
	return nil
}

// normalize returns the configuration in the shape the request builder
// expects: a trailing slash on the directory, no leading slash on the file.
func (c Config) normalize() Config {
	if !strings.HasSuffix(c.URL, "/") {
		c.URL += "/"
	}
	c.File = strings.TrimPrefix(c.File, "/")
	return c
}

// CachePath returns the local path that mirrors the remote file.
func (c Config) CachePath() (string, error) {
	return cachePath(c.File)
}
