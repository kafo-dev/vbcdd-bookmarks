package main

import (
	"path/filepath"
	"strings"
	"testing"
)

func writeConfig(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := writeFile(path, body); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return path
}

func TestLoadConfigNormalizesTheURL(t *testing.T) {
	t.Parallel()
	path := writeConfig(t, `url = "https://webdav.example.org"
file = "Documents/Bookmarks.xbel"
user = "example"
password = "secret"
`)
	cfg, err := loadConfig(path)
	if err != nil {
		t.Fatalf("loadConfig: %v", err)
	}
	if cfg.URL != "https://webdav.example.org/" {
		t.Errorf("URL = %q, want a trailing slash", cfg.URL)
	}
	if !strings.HasSuffix(cfg.URL, "/") || strings.HasPrefix(cfg.File, "/") {
		t.Error("the normalized configuration cannot be addressed")
	}
}

func TestLoadConfigRejects(t *testing.T) {
	t.Parallel()
	cases := map[string]string{
		"missing field":  "url = \"https://x.example/\"\nfile = \"\"\nuser = \"example\"\npassword = \"secret\"\n",
		"broken toml":    "url = \"https://x.example/\n",
		"no scheme":      "url = \"webdav.example.org\"\nfile = \"f.xbel\"\nuser = \"example\"\npassword = \"secret\"\n",
		"absolute file":  "url = \"https://x.example/\"\nfile = \"/f.xbel\"\nuser = \"example\"\npassword = \"secret\"\n",
		"query on url":   "url = \"https://x.example/?a=1\"\nfile = \"f.xbel\"\nuser = \"example\"\npassword = \"secret\"\n",
		"no host":        "url = \"https:///dir\"\nfile = \"f.xbel\"\nuser = \"example\"\npassword = \"secret\"\n",
		"not a table":    "[url]\nfile = \"f.xbel\"\n",
		"empty password": "url = \"https://x.example/\"\nfile = \"f.xbel\"\nuser = \"example\"\npassword = \"\"\n",
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if _, err := loadConfig(writeConfig(t, body)); err == nil {
				t.Error("loadConfig accepted the file")
			}
		})
	}
}

func TestCachePathFollowsTheRemoteName(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	cfg := Config{URL: "https://x.example/", File: "Documents/Bookmarks.xbel"}
	path, err := cfg.CachePath()
	if err != nil {
		t.Fatalf("CachePath: %v", err)
	}
	if !strings.HasSuffix(path, filepath.Join("vbcdd", "bookmarks", "Bookmarks.xbel")) {
		t.Errorf("CachePath = %q, want the remote file name below the cache directory", path)
	}
}

func TestConfigPathUsesTheXDGDirectory(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	path, err := configPath()
	if err != nil {
		t.Fatalf("configPath: %v", err)
	}
	if !strings.HasSuffix(path, filepath.Join("vbcdd", "bookmarks", configFileName)) {
		t.Errorf("configPath = %q, want the documented location", path)
	}
}
