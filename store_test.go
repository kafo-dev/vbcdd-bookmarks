package main

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestClassifyLock(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 10, 11, 12, 0, 0, 0, time.UTC)
	fresh := now.Add(-time.Minute)
	stale := now.Add(-lockStaleAfter - time.Minute)
	cases := []struct {
		name     string
		status   int
		modified *time.Time
		want     LockState
	}{
		{"no lock file", 404, nil, LockFree},
		{"fresh lock", 200, &fresh, LockBusy},
		{"undated lock", 200, nil, LockBusy},
		{"abandoned lock", 200, &stale, LockStale},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			got, err := classifyLock(test.status, test.modified, now)
			if err != nil {
				t.Fatalf("classifyLock: %v", err)
			}
			if got != test.want {
				t.Errorf("classifyLock = %q, want %q", got, test.want)
			}
		})
	}
	if _, err := classifyLock(500, nil, now); err == nil {
		t.Error("classifyLock accepted a 500 response")
	}
}

func TestFetchReportsAnEmptyTreeForAMissingFile(t *testing.T) {
	t.Parallel()
	dav := newFakeDAV()
	store, _ := newTestStore(t, dav)

	text, err := store.Fetch(context.Background())
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if text != emptyDocument() {
		t.Errorf("Fetch of a missing file returned %q", text)
	}
}

func TestFetchRefusesABodyThatDoesNotMatchThePublishedSize(t *testing.T) {
	t.Parallel()
	dav := newFakeDAV()
	dav.set("Documents/Bookmarks.xbel", sampleDoc)
	dav.sizeOverride["Documents/Bookmarks.xbel"] = int64(len(sampleDoc)) + 7
	store, _ := newTestStore(t, dav)

	if _, err := store.Fetch(context.Background()); err == nil {
		t.Fatal("Fetch accepted a body the server indexed at another length")
	}
}

func TestCommitUploadsThroughTheTemporaryPath(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	dav := newFakeDAV()
	store, _ := newTestStore(t, dav)

	if err := store.Commit(context.Background(), sampleDoc); err != nil {
		t.Fatalf("Commit: %v", err)
	}
	if !dav.sawRequest("PUT", "Documents/Bookmarks.xbel.temp") {
		t.Error("Commit did not write the temporary path first")
	}
	if !dav.sawRequest("MOVE", "Documents/Bookmarks.xbel.temp") {
		t.Error("Commit did not rename the temporary file")
	}
	if body, ok := dav.file("Documents/Bookmarks.xbel"); !ok || body != sampleDoc {
		t.Error("the renamed file does not hold what was uploaded")
	}
	if _, ok := dav.file("Documents/Bookmarks.xbel.temp"); ok {
		t.Error("the temporary file survived the rename")
	}
	cached, err := store.ReadCache()
	if err != nil {
		t.Fatalf("ReadCache: %v", err)
	}
	if cached != sampleDoc {
		t.Error("the cache does not hold the committed file")
	}
}

func TestCommitRefusesAFileFloccusCannotRead(t *testing.T) {
	t.Parallel()
	dav := newFakeDAV()
	store, _ := newTestStore(t, dav)

	if err := store.Commit(context.Background(), "<xbel/>"); err == nil {
		t.Fatal("Commit accepted a file without the XML declaration")
	}
	if dav.sawRequest("PUT", "Documents/Bookmarks.xbel.temp") {
		t.Error("Commit uploaded a file it had refused")
	}
}

func TestWithLockDropsTheLockOnFailure(t *testing.T) {
	t.Parallel()
	dav := newFakeDAV()
	store, _ := newTestStore(t, dav)
	want := errors.New("body failed")

	err := store.WithLock(context.Background(), func() error {
		if state, err := store.LockState(context.Background()); err != nil || state == LockFree {
			t.Errorf("inside the lock: state %q, err %v", state, err)
		}
		return want
	})
	if !errors.Is(err, want) {
		t.Fatalf("WithLock returned %v, want the body error", err)
	}
	if _, ok := dav.file("Documents/Bookmarks.xbel.lock"); ok {
		t.Error("the lock survived a failed body")
	}
}

func TestWithLockHoldsOffABusyLock(t *testing.T) {
	t.Parallel()
	dav := newFakeDAV()
	dav.set("Documents/Bookmarks.xbel.lock", lockBody)
	store, _ := newTestStore(t, dav)

	ran := false
	err := store.WithLock(context.Background(), func() error {
		ran = true
		return nil
	})
	if err == nil {
		t.Fatal("WithLock ran against a fresh lock")
	}
	if ran {
		t.Error("the body ran while another writer held the lock")
	}
	if !strings.Contains(err.Error(), "unlock") {
		t.Errorf("error %q does not name the recovery command", err)
	}
}

func TestWithLockTakesOverAnAbandonedLock(t *testing.T) {
	t.Parallel()
	dav := newFakeDAV()
	dav.set("Documents/Bookmarks.xbel.lock", lockBody)
	dav.age("Documents/Bookmarks.xbel.lock", lockStaleAfter+time.Minute)
	store, _ := newTestStore(t, dav)

	if err := store.WithLock(context.Background(), func() error { return nil }); err != nil {
		t.Fatalf("WithLock on a stale lock: %v", err)
	}
	if _, ok := dav.file("Documents/Bookmarks.xbel.lock"); ok {
		t.Error("the taken-over lock survived the write")
	}
}

func TestFetchCachedFallsBackToTheLastCopy(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	dav := newFakeDAV()
	dav.set("Documents/Bookmarks.xbel", sampleDoc)
	store, server := newTestStore(t, dav)

	if _, online, err := store.FetchCached(context.Background()); err != nil || !online {
		t.Fatalf("first read: online=%v err=%v", online, err)
	}
	server.Close()
	text, online, err := store.FetchCached(context.Background())
	if err != nil {
		t.Fatalf("FetchCached offline: %v", err)
	}
	if online {
		t.Error("FetchCached reported an online read while the server was down")
	}
	if text != sampleDoc {
		t.Error("the offline read did not come from the cache")
	}
}

func TestFetchCachedReportsTheNetworkErrorWithoutACache(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	dav := newFakeDAV()
	store, server := newTestStore(t, dav)
	server.Close()

	if _, _, err := store.FetchCached(context.Background()); err == nil {
		t.Fatal("FetchCached hid a failed read with no cache to fall back to")
	}
}

func TestDropLockIgnoresAnAbsentLock(t *testing.T) {
	t.Parallel()
	dav := newFakeDAV()
	store, _ := newTestStore(t, dav)

	if err := store.DropLock(context.Background()); err != nil {
		t.Errorf("DropLock without a lock: %v", err)
	}
}
