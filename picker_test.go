package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// stubCommand writes an executable of this name into dir, so the picker and
// the opener run a script a test can predict. The test puts dir on PATH.
func stubCommand(t *testing.T, dir, name, script string) {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("stub %s: %v", name, err)
	}
}

// stubPath returns the PATH value that reaches the stubs in dir.
func stubPath(dir string) string {
	return dir + string(os.PathListSeparator) + os.Getenv("PATH")
}

func TestEntryLineSurvivesATabInATitle(t *testing.T) {
	t.Parallel()
	entry := Entry{
		Title:  "Cats\tand\nDogs",
		Folder: "Phone",
		Host:   "example.org",
		Href:   "https://example.org/",
	}
	line := entry.line()
	if strings.Count(line, fieldSeparator) != 3 {
		t.Fatalf("line %q does not hold exactly three separators", line)
	}
	parsed, err := parseEntry(line)
	if err != nil {
		t.Fatalf("parseEntry: %v", err)
	}
	if parsed.Host != "example.org" || parsed.Href != entry.Href {
		t.Errorf("parseEntry lost a field: %+v", parsed)
	}
	if strings.Contains(parsed.Title, "\t") || strings.Contains(parsed.Title, "\n") {
		t.Errorf("the sanitized title still holds a control character: %q", parsed.Title)
	}
}

func TestParseEntryRefusesAShortRow(t *testing.T) {
	t.Parallel()
	if _, err := parseEntry("title\thttps://example.org/"); err == nil {
		t.Error("parseEntry accepted a row without the folder and host fields")
	}
}

func TestCollectEntriesNamesTheHoldingFolder(t *testing.T) {
	t.Parallel()
	doc, err := ParseDoc(sampleDoc)
	if err != nil {
		t.Fatalf("ParseDoc: %v", err)
	}
	phone, err := doc.NewFolder(phoneFolderTitle, nil)
	if err != nil {
		t.Fatalf("NewFolder: %v", err)
	}
	if _, err = doc.NewBookmark(phone, "https://example.net/saved", "Saved on the phone"); err != nil {
		t.Fatalf("NewBookmark: %v", err)
	}

	entries := collectEntries(doc)
	if len(entries) != 2 {
		t.Fatalf("collectEntries returned %d rows, want 2", len(entries))
	}
	if entries[0].Folder != "Bookmarks Bar" || entries[0].Host != "example.org" {
		t.Errorf("first row = %+v, want the folder and host of the sample bookmark", entries[0])
	}
	if entries[1].Folder != phoneFolderTitle {
		t.Errorf("second row sits in %q, want %q", entries[1].Folder, phoneFolderTitle)
	}
}

func TestFirstURL(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name  string
		input string
		want  string
		found bool
	}{
		{"bare", "https://example.org/a?b=1", "https://example.org/a?b=1", true},
		{"trailing dot", "https://example.org/a.", "https://example.org/a", true},
		{"in a sentence", "Look at https://example.org/x) now", "https://example.org/x", true},
		{"with a title", "Example — https://example.org/x", "https://example.org/x", true},
		{"text only", "no link in this share", "", false},
		{"other scheme", "magnet:?xt=urn:btih:x", "", false},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			got, found := firstURL(test.input)
			if found != test.found {
				t.Fatalf("found = %v, want %v", found, test.found)
			}
			if got != test.want {
				t.Errorf("firstURL = %q, want %q", got, test.want)
			}
		})
	}
}

func TestHostOf(t *testing.T) {
	t.Parallel()
	if got := hostOf("https://example.org:8443/a"); got != "example.org:8443" {
		t.Errorf("hostOf = %q, want the host with its port", got)
	}
	if got := hostOf("not a url at all"); got != "" {
		t.Errorf("hostOf = %q for a non-URL, want an empty host", got)
	}
}

// chooseFirst is the fzf a phone runs: it takes the first row it is given.
const chooseFirst = `#!/bin/sh
head -n 1
`

// chooseFirstAndReportKey answers the delete key on the first row.
const chooseFirstAndReportKey = `#!/bin/sh
printf 'ctrl-d\n'
head -n 1
`

const cancelFZF = `#!/bin/sh
exit 130
`

// slowChooseFirst reads the list at a human pace: long enough to outlive a
// phase ceiling that a caller might hand to the picker by mistake.
const slowChooseFirst = `#!/bin/sh
sleep 0.4
head -n 1
`

func TestPickReturnsTheChosenRow(t *testing.T) {
	dir := t.TempDir()
	stubCommand(t, dir, "fzf", chooseFirst)
	t.Setenv("PATH", stubPath(dir))
	entries := []Entry{
		{Title: "First", Folder: "Phone", Host: "example.org", Href: "https://example.org/1"},
		{Title: "Second", Folder: "Phone", Host: "example.org", Href: "https://example.org/2"},
	}
	chosen, key, err := pick(entries, false, "2 bookmarks")
	if err != nil {
		t.Fatalf("pick: %v", err)
	}
	if chosen.Href != "https://example.org/1" {
		t.Errorf("pick chose %q, want the first row", chosen.Href)
	}
	if key != "" {
		t.Errorf("key = %q, want no key for a plain Enter", key)
	}
}

func TestPickReportsTheDeleteKey(t *testing.T) {
	dir := t.TempDir()
	stubCommand(t, dir, "fzf", chooseFirstAndReportKey)
	t.Setenv("PATH", stubPath(dir))
	entries := []Entry{{Title: "Only", Folder: "Phone", Host: "example.org", Href: "https://example.org/1"}}
	chosen, key, err := pick(entries, true, "1 bookmark")
	if err != nil {
		t.Fatalf("pick: %v", err)
	}
	if key != deleteKey {
		t.Errorf("key = %q, want %q", key, deleteKey)
	}
	if chosen.Title != "Only" {
		t.Errorf("pick chose %q, want the row below the key line", chosen.Title)
	}
}

func TestPickReportsCancellation(t *testing.T) {
	dir := t.TempDir()
	stubCommand(t, dir, "fzf", cancelFZF)
	t.Setenv("PATH", stubPath(dir))
	entries := []Entry{{Title: "Only", Folder: "Phone", Host: "example.org", Href: "https://example.org/1"}}
	if _, _, err := pick(entries, false, "1 bookmark"); !errors.Is(err, errCancelled) {
		t.Errorf("pick returned %v, want errCancelled", err)
	}
}

func TestPickNeedsFZFInstalled(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	entries := []Entry{{Title: "Only", Href: "https://example.org/1"}}
	if _, _, err := pick(entries, false, "1 bookmark"); err == nil {
		t.Error("pick ran without fzf on PATH")
	}
}

func TestPickWaitsForALongLook(t *testing.T) {
	dir := t.TempDir()
	stubCommand(t, dir, "fzf", slowChooseFirst)
	t.Setenv("PATH", stubPath(dir))
	entries := []Entry{{Title: "Only", Folder: "Phone", Host: "example.org", Href: "https://example.org/1"}}
	chosen, _, err := pick(entries, false, "1 bookmark")
	if err != nil {
		t.Fatalf("pick killed a reader who took their time: %v", err)
	}
	if chosen.Href != "https://example.org/1" {
		t.Errorf("pick chose %q, want the first row", chosen.Href)
	}
}
