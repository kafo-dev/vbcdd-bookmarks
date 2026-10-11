package main

import (
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

// The credentials every test uses.
const (
	fakeUser   = "example"
	fakeSecret = "secret"
)

// fakeDAV is a WebDAV directory in a map. It answers the five methods the
// store uses, refuses a request without credentials, and records every
// request so a test can assert the protocol rather than only the outcome.
type fakeDAV struct {
	mu sync.Mutex

	files    map[string][]byte
	modified map[string]time.Time
	requests []string

	// sizeOverride makes PROPFIND lie about one path, which is what a
	// server with a stale index does.
	sizeOverride map[string]int64
}

func newFakeDAV() *fakeDAV {
	return &fakeDAV{
		files:        map[string][]byte{},
		modified:     map[string]time.Time{},
		sizeOverride: map[string]int64{},
	}
}

// authorization is the header every test request must carry; the fake
// answers 401 to anything else, which is what a wrong password gets.
func (f *fakeDAV) authorization() string {
	token := base64.StdEncoding.EncodeToString([]byte(fakeUser + ":" + fakeSecret))
	return "Basic " + token
}

func (f *fakeDAV) set(path, body string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.files[path] = []byte(body)
	f.modified[path] = time.Now()
}

func (f *fakeDAV) age(path string, age time.Duration) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.modified[path] = time.Now().Add(-age)
}

func (f *fakeDAV) file(path string) (string, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	body, ok := f.files[path]
	return string(body), ok
}

func (f *fakeDAV) sawRequest(method, path string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Contains(f.requests, method+" "+path)
}

func (f *fakeDAV) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/")
	f.mu.Lock()
	defer f.mu.Unlock()
	f.requests = append(f.requests, r.Method+" "+path)
	if r.Header.Get("Authorization") != f.authorization() {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	switch r.Method {
	case http.MethodGet:
		f.handleGet(w, path)
	case http.MethodPut:
		f.handlePut(w, r, path)
	case http.MethodDelete:
		f.handleDelete(w, path)
	case "MOVE":
		f.handleMove(w, r, path)
	case "PROPFIND":
		f.handlePropfind(w, path)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func (f *fakeDAV) handleGet(w http.ResponseWriter, path string) {
	body, ok := f.files[path]
	if !ok {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	w.Header().Set("Last-Modified", f.modified[path].UTC().Format(http.TimeFormat))
	writeBody(w, body)
}

func (f *fakeDAV) handlePut(w http.ResponseWriter, r *http.Request, path string) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	f.files[path] = body
	f.modified[path] = time.Now()
	w.WriteHeader(http.StatusCreated)
}

func (f *fakeDAV) handleDelete(w http.ResponseWriter, path string) {
	if _, ok := f.files[path]; !ok {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	delete(f.files, path)
	delete(f.modified, path)
	w.WriteHeader(http.StatusNoContent)
}

func (f *fakeDAV) handleMove(w http.ResponseWriter, r *http.Request, path string) {
	if r.Header.Get("Overwrite") != "T" {
		http.Error(w, "no overwrite", http.StatusPreconditionFailed)
		return
	}
	destination, err := url.Parse(r.Header.Get("Destination"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	body, ok := f.files[path]
	if !ok {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	target := strings.TrimPrefix(destination.Path, "/")
	f.files[target] = body
	f.modified[target] = time.Now()
	delete(f.files, path)
	w.WriteHeader(http.StatusNoContent)
}

func (f *fakeDAV) handlePropfind(w http.ResponseWriter, path string) {
	body, ok := f.files[path]
	if !ok {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	size := int64(len(body))
	if override, published := f.sizeOverride[path]; published {
		size = override
	}
	payload := fmt.Sprintf(
		`<?xml version="1.0" encoding="UTF-8"?><D:multistatus xmlns:D="DAV:"><D:response>`+
			`<D:href>/%s</D:href><D:propstat><D:prop><D:getcontentlength>%d</D:getcontentlength>`+
			`</D:prop><D:status>HTTP/1.1 200 OK</D:status></D:propstat></D:response></D:multistatus>`,
		path, size,
	)
	w.Header().Set("Content-Type", "application/xml")
	w.WriteHeader(http.StatusMultiStatus)
	writeBody(w, []byte(payload))
}

// writeBody fails the test loudly rather than dropping a write error.
func writeBody(w http.ResponseWriter, body []byte) {
	if _, err := w.Write(body); err != nil {
		panic(fmt.Sprintf("fake DAV: %v", err))
	}
}

// newTestStore points a store at a fake directory. A test that reads or
// writes the local copy sets `XDG_CACHE_HOME` itself.
func newTestStore(t *testing.T, dav *fakeDAV) (*Store, *httptest.Server) {
	t.Helper()
	server := httptest.NewServer(dav)
	t.Cleanup(server.Close)
	cfg := Config{
		URL:      server.URL + "/",
		File:     "Documents/Bookmarks.xbel",
		User:     fakeUser,
		Password: fakeSecret,
	}
	return NewStore(cfg, NewClient(cfg.URL, cfg.User, cfg.Password)), server
}

// writeConfigFile puts a configuration file where `--config` expects it.
func writeConfigFile(t *testing.T, server *httptest.Server) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.toml")
	body := fmt.Sprintf(
		"url = %q\nfile = \"Documents/Bookmarks.xbel\"\nuser = %q\npassword = %q\n",
		server.URL, fakeUser, fakeSecret,
	)
	if err := writeFile(path, body); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return path
}
