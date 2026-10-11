package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const addedURL = "https://example.net/saved-from-the-phone"

func TestRunAddCreatesThePhoneFolder(t *testing.T) {
	dav := newFakeDAV()
	dav.set("Documents/Bookmarks.xbel", sampleDoc)
	_, server := newTestStore(t, dav)
	t.Setenv("XDG_CACHE_HOME", t.TempDir())

	code := run([]string{
		"bookmarks", "add", "--config", writeConfigFile(t, server),
		"--title", "Saved", addedURL,
	})
	if code != 0 {
		t.Fatalf("run exited %d", code)
	}
	body, ok := dav.file("Documents/Bookmarks.xbel")
	if !ok {
		t.Fatal("the write left no file on the server")
	}
	doc, err := ParseDoc(body)
	if err != nil {
		t.Fatalf("the server holds a file Floccus cannot read: %v", err)
	}
	folder := doc.ChildFolder(phoneFolderTitle)
	if folder == nil {
		t.Fatal("the writer did not create the Phone folder")
	}
	if folder.ParentID != 0 {
		t.Errorf("the Phone folder sits under id %d, not under <xbel>", folder.ParentID)
	}
	if doc.Children[len(doc.Children)-1] != Node(folder) {
		t.Error("the Phone folder did not append to the end of the root child list")
	}
	bookmark := doc.FindBookmark(addedURL, "Saved")
	if bookmark == nil {
		t.Fatal("the shared link did not reach the file")
	}
	if folder.ID != 13 {
		t.Errorf("the Phone folder took id %d, want 13 above the counter", folder.ID)
	}
	if bookmark.ID != 14 {
		t.Errorf("the new bookmark took id %d, want 14 above the folder", bookmark.ID)
	}
	if doc.HighestID != 14 {
		t.Errorf("HighestID = %d, want 14", doc.HighestID)
	}
	if _, held := dav.file("Documents/Bookmarks.xbel.lock"); held {
		t.Error("the writer left the lock behind")
	}
}

func TestRunAddReusesAnExistingPhoneFolder(t *testing.T) {
	dav := newFakeDAV()
	doc, err := ParseDoc(sampleDoc)
	if err != nil {
		t.Fatalf("ParseDoc: %v", err)
	}
	if _, err = doc.NewFolder(phoneFolderTitle, nil); err != nil {
		t.Fatalf("NewFolder: %v", err)
	}
	dav.set("Documents/Bookmarks.xbel", doc.Render())
	_, server := newTestStore(t, dav)
	t.Setenv("XDG_CACHE_HOME", t.TempDir())

	if code := run([]string{
		"bookmarks", "add", "--config", writeConfigFile(t, server),
		"--title", "Second", addedURL,
	}); code != 0 {
		t.Fatalf("run exited %d", code)
	}
	body, _ := dav.file("Documents/Bookmarks.xbel")
	updated, err := ParseDoc(body)
	if err != nil {
		t.Fatalf("ParseDoc: %v", err)
	}
	folders := 0
	for _, child := range updated.Children {
		if folder, ok := child.(*Folder); ok && folder.Title == phoneFolderTitle {
			folders++
		}
	}
	if folders != 1 {
		t.Errorf("the file holds %d Phone folders, want 1", folders)
	}
}

func TestRunAddStartsFromAnEmptyServer(t *testing.T) {
	dav := newFakeDAV()
	_, server := newTestStore(t, dav)
	t.Setenv("XDG_CACHE_HOME", t.TempDir())

	if code := run([]string{
		"bookmarks", "add", "--config", writeConfigFile(t, server),
		"--title", "First", addedURL,
	}); code != 0 {
		t.Fatalf("run exited %d", code)
	}
	body, ok := dav.file("Documents/Bookmarks.xbel")
	if !ok {
		t.Fatal("a 404 read stopped the writer from creating the file")
	}
	doc, err := ParseDoc(body)
	if err != nil {
		t.Fatalf("ParseDoc: %v", err)
	}
	folder := doc.ChildFolder(phoneFolderTitle)
	if folder == nil || folder.ID != 1 {
		t.Fatalf("the first folder took %v, want id 1", folder)
	}
	if doc.FindBookmark(addedURL, "First") == nil {
		t.Error("the link did not reach the new file")
	}
}

func TestRunAddRefusesABusyLock(t *testing.T) {
	dav := newFakeDAV()
	dav.set("Documents/Bookmarks.xbel", sampleDoc)
	dav.set("Documents/Bookmarks.xbel.lock", lockBody)
	_, server := newTestStore(t, dav)
	t.Setenv("XDG_CACHE_HOME", t.TempDir())

	code := run([]string{
		"bookmarks", "add", "--config", writeConfigFile(t, server),
		"--title", "Saved", addedURL,
	})
	if code == 0 {
		t.Fatal("the writer ran while another writer held the lock")
	}
	body, _ := dav.file("Documents/Bookmarks.xbel")
	if body != sampleDoc {
		t.Error("the file changed under a lock held by another writer")
	}
	if _, ok := dav.file("Documents/Bookmarks.xbel.lock"); !ok {
		t.Error("the writer removed a lock it did not take")
	}
}

func TestRunAddRejectsAShareWithoutALink(t *testing.T) {
	t.Parallel()
	dav := newFakeDAV()
	_, server := newTestStore(t, dav)

	if code := run([]string{
		"bookmarks", "add", "--config", writeConfigFile(t, server),
		"no link in this text",
	}); code == 0 {
		t.Error("run accepted a share without a URL")
	}
}

func TestDispatchNamesTheInstalledCommands(t *testing.T) {
	t.Parallel()
	cases := []struct {
		argv    []string
		command string
	}{
		{[]string{"/data/data/com.termux/files/home/bin/bookmarks-add", "u"}, "add"},
		{[]string{"/data/data/com.termux/files/home/bin/termux-url-opener", "u"}, "add"},
		{[]string{"bookmarks-pick"}, "pick"},
		{[]string{"bookmarks", "pick", "--delete"}, "pick"},
		{[]string{"bookmarks", "unlock"}, "unlock"},
		{[]string{"bookmarks", "--help"}, "help"},
	}
	for _, test := range cases {
		command, _ := dispatch(test.argv)
		if command != test.command {
			t.Errorf("dispatch(%q) = %q, want %q", test.argv[0], command, test.command)
		}
	}
}

func TestRunRejectsAnUnknownCommand(t *testing.T) {
	t.Parallel()
	if code := run([]string{"bookmarks", "frobnicate"}); code != exitUsage {
		t.Errorf("run exited %d, want %d", code, exitUsage)
	}
	if code := run([]string{"bookmarks"}); code != exitUsage {
		t.Errorf("run with no command exited %d, want %d", code, exitUsage)
	}
}

func TestRunVersion(t *testing.T) {
	t.Parallel()
	if code := run([]string{"bookmarks", "version"}); code != 0 {
		t.Errorf("run version exited %d", code)
	}
	if !strings.HasPrefix(version, "0.") {
		t.Errorf("version %q does not start at 0.", version)
	}
}

// recordArgument is the Termux opener a phone has: it reports the link it
// was handed through the file named in BOOKMARKS_TEST_OUT.
const recordArgument = `#!/bin/sh
printf '%s' "$1" > "$BOOKMARKS_TEST_OUT"
`

func TestRunPickOpensTheChosenLink(t *testing.T) {
	dav := newFakeDAV()
	dav.set("Documents/Bookmarks.xbel", sampleDoc)
	_, server := newTestStore(t, dav)
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	digest := filepath.Join(t.TempDir(), "opened")
	t.Setenv("BOOKMARKS_TEST_OUT", digest)
	dir := t.TempDir()
	stubCommand(t, dir, "fzf", chooseFirst)
	stubCommand(t, dir, "termux-open-url", recordArgument)
	t.Setenv("PATH", stubPath(dir))

	if code := run([]string{"bookmarks", "pick", "--config", writeConfigFile(t, server)}); code != 0 {
		t.Fatalf("run pick exited %d", code)
	}
	opened, err := os.ReadFile(digest)
	if err != nil {
		t.Fatalf("the opener left no report: %v", err)
	}
	if string(opened) != "https://example.org/?a=1&b=2" {
		t.Errorf("the opener received %q, want the chosen href", opened)
	}
}

func TestRunPickCancelsQuietly(t *testing.T) {
	dav := newFakeDAV()
	dav.set("Documents/Bookmarks.xbel", sampleDoc)
	_, server := newTestStore(t, dav)
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	dir := t.TempDir()
	stubCommand(t, dir, "fzf", cancelFZF)
	t.Setenv("PATH", stubPath(dir))

	if code := run([]string{"bookmarks", "pick", "--config", writeConfigFile(t, server)}); code != 0 {
		t.Errorf("run pick exited %d after a canceled picker, want 0", code)
	}
}

// shortenFetch shrinks the read bound, so a test can watch the picker
// outlive it.
func shortenFetch(t *testing.T, bound time.Duration) {
	t.Helper()
	previous := fetchTimeout
	fetchTimeout = bound
	t.Cleanup(func() { fetchTimeout = previous })
}

// TestRunPickOutlivesTheReadBound pins the bug where the picker inherited
// the read deadline: fzf died with "signal: killed" ten seconds in.
func TestRunPickOutlivesTheReadBound(t *testing.T) {
	dav := newFakeDAV()
	dav.set("Documents/Bookmarks.xbel", sampleDoc)
	_, server := newTestStore(t, dav)
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	digest := filepath.Join(t.TempDir(), "opened")
	t.Setenv("BOOKMARKS_TEST_OUT", digest)
	dir := t.TempDir()
	stubCommand(t, dir, "fzf", slowChooseFirst)
	stubCommand(t, dir, "termux-open-url", recordArgument)
	t.Setenv("PATH", stubPath(dir))
	shortenFetch(t, 50*time.Millisecond)

	code := run([]string{"bookmarks", "pick", "--config", writeConfigFile(t, server)})
	if code != 0 {
		t.Fatalf("run pick exited %d after the read bound expired", code)
	}
	opened, err := os.ReadFile(digest)
	if err != nil {
		t.Fatalf("the opener left no report: %v", err)
	}
	if string(opened) != "https://example.org/?a=1&b=2" {
		t.Errorf("the opener received %q, want the chosen href", opened)
	}
}
