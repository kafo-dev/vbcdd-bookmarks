package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	// Floccus treats a lock older than this as abandoned and syncs anyway,
	// so a writer may take it over at the same age.
	lockStaleAfter = 15 * time.Minute
	readCacheMode  = 0o600
	cacheDirMode   = 0o700
)

// LockState is what an existing lock file means for a writer.
type LockState string

const (
	LockFree  LockState = "free"
	LockBusy  LockState = "busy"
	LockStale LockState = "stale"
)

// Paths are the three resources one write touches, relative to the base URL.
type Paths struct {
	Bookmark string
	Lock     string
	Temp     string
}

func pathsFor(file string) Paths {
	if file == "" {
		panic("store: empty file path")
	}
	return Paths{Bookmark: file, Lock: file + ".lock", Temp: file + ".temp"}
}

// Store reads and writes one bookmark file, keeping the last fetched copy
// for an offline read.
type Store struct {
	cfg     Config
	paths   Paths
	client  *Client
	holding bool
}

func NewStore(cfg Config, client *Client) *Store {
	if client == nil {
		panic("store: nil client")
	}
	return &Store{cfg: cfg, paths: pathsFor(cfg.File), client: client}
}

// classifyLock decides whether a writer may pass an existing lock file. A
// lock the server cannot date counts as busy: a server that answered 200 to
// everything would otherwise hand the file to two writers.
func classifyLock(status int, modified *time.Time, now time.Time) (LockState, error) {
	if now.IsZero() {
		panic("store: zero clock")
	}
	switch status {
	case http.StatusNotFound:
		return LockFree, nil
	case http.StatusOK:
		if modified == nil {
			return LockBusy, nil
		}
		if now.Sub(*modified) < lockStaleAfter {
			return LockBusy, nil
		}
		return LockStale, nil
	default:
		return "", fmt.Errorf("store: the lock file answered %d", status)
	}
}

func (s *Store) LockState(ctx context.Context) (LockState, error) {
	response, err := s.client.Get(ctx, s.paths.Lock)
	if err != nil {
		return "", err
	}
	return classifyLock(response.Status, response.LastModified, time.Now())
}

// Fetch reads the file and checks it against the length the server reports.
func (s *Store) Fetch(ctx context.Context) (string, error) {
	response, err := s.client.Get(ctx, s.paths.Bookmark)
	if err != nil {
		return "", err
	}
	if response.Status == http.StatusNotFound {
		return emptyDocument(), nil
	}
	if response.Status != http.StatusOK {
		return "", fmt.Errorf("store: GET %s answered %d", s.paths.Bookmark, response.Status)
	}
	reported, known, err := s.client.Size(ctx, s.paths.Bookmark)
	if err != nil {
		return "", err
	}
	if known && reported != int64(len(response.Body)) {
		return "", fmt.Errorf("store: %s: the server reports %d bytes, the body holds %d",
			s.paths.Bookmark, reported, len(response.Body))
	}
	text := response.text()
	if len(text) == 0 {
		return "", fmt.Errorf("store: %s: the server sent an empty body", s.paths.Bookmark)
	}
	return text, nil
}

// FetchCached reads the file, falling back to the last copy that was read.
// The flag reports whether the text came off the network.
func (s *Store) FetchCached(ctx context.Context) (string, bool, error) {
	text, err := s.Fetch(ctx)
	if err == nil {
		if err = s.WriteCache(text); err != nil {
			return "", false, err
		}
		return text, true, nil
	}
	cached, cacheErr := s.ReadCache()
	if cacheErr != nil {
		return "", false, errors.Join(err, cacheErr)
	}
	if cached == "" {
		return "", false, err
	}
	assertCacheShape(cached)
	return cached, false, nil
}

// Commit uploads a temporary copy and renames it over the file, so a reader
// never sees a partial write.
func (s *Store) Commit(ctx context.Context, text string) error {
	if len(text) == 0 {
		return fmt.Errorf("store: refusing to write an empty file")
	}
	if _, err := ParseDoc(text); err != nil {
		return fmt.Errorf("store: refusing to write a file Floccus cannot read: %w", err)
	}
	body := []byte(text)
	if err := s.client.Put(ctx, s.paths.Temp, body, xbelContentType); err != nil {
		return err
	}
	if err := s.client.Move(ctx, s.paths.Temp, s.paths.Bookmark); err != nil {
		return err
	}
	return s.WriteCache(text)
}

// WithLock runs fn while holding the Floccus lock, and drops the lock on
// every exit path, including failure.
func (s *Store) WithLock(ctx context.Context, fn func() error) error {
	if s.holding {
		panic("store: the lock is not reentrant")
	}
	state, err := s.LockState(ctx)
	if err != nil {
		return err
	}
	if state == LockBusy {
		return fmt.Errorf(
			"store: %s is held by another writer; run `bookmarks unlock` when no writer runs",
			s.paths.Lock,
		)
	}
	if err = s.client.Put(ctx, s.paths.Lock, []byte(lockBody), lockContentType); err != nil {
		return err
	}
	s.holding = true
	// A lock left behind blocks Floccus for the whole stale window, so a
	// failed removal is reported even when the body succeeded.
	defer func() {
		if dropErr := s.DropLock(ctx); dropErr != nil {
			err = errors.Join(err, dropErr)
		}
	}()
	return fn()
}

// DropLock removes the lock file, whatever it holds.
func (s *Store) DropLock(ctx context.Context) error {
	s.holding = false
	if err := s.client.Delete(ctx, s.paths.Lock); err != nil {
		return err
	}
	return nil
}

// ReadCache returns the last fetched copy, or "" when nothing was fetched.
func (s *Store) ReadCache() (string, error) {
	path, err := s.cfg.CachePath()
	if err != nil {
		return "", err
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", fmt.Errorf("store: cache: %w", err)
	}
	return string(raw), nil
}

// WriteCache stores the fetched copy, through a temporary name so a killed
// process cannot leave a half-written cache behind.
func (s *Store) WriteCache(text string) error {
	if len(text) == 0 {
		return fmt.Errorf("store: refusing to cache an empty file")
	}
	path, err := s.cfg.CachePath()
	if err != nil {
		return err
	}
	if err = os.MkdirAll(filepath.Dir(path), cacheDirMode); err != nil {
		return fmt.Errorf("store: cache: %w", err)
	}
	temporary := path + ".tmp"
	if err = os.WriteFile(temporary, []byte(text), readCacheMode); err != nil {
		return fmt.Errorf("store: cache: %w", err)
	}
	if err = os.Rename(temporary, path); err != nil {
		if removeErr := os.Remove(temporary); removeErr != nil && !os.IsNotExist(removeErr) {
			return errors.Join(fmt.Errorf("store: cache: %w", err), removeErr)
		}
		return fmt.Errorf("store: cache: %w", err)
	}
	return nil
}

// emptyDocument is the file Floccus writes for a profile with no bookmarks:
// the declarations, the counter at zero, and no child.
func emptyDocument() string {
	return (&Doc{}).Render()
}

// assertCacheShape refuses a cached file that a crash or a stray editor left
// in a state the next write would carry onto the server.
func assertCacheShape(text string) {
	if !strings.Contains(text, xmlDeclaration) {
		panic("store: the cached copy lost its XML declaration")
	}
}
