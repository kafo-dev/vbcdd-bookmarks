package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

func writeFile(path, body string) error {
	return os.WriteFile(path, []byte(body), 0o600)
}

func TestClientFetchesWithCredentials(t *testing.T) {
	t.Parallel()
	dav := newFakeDAV()
	dav.set("Documents/Bookmarks.xbel", sampleDoc)
	_, server := newTestStore(t, dav)
	client := NewClient(server.URL+"/", "example", "secret")

	response, err := client.Get(context.Background(), "Documents/Bookmarks.xbel")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if response.Status != http.StatusOK {
		t.Fatalf("status = %d, want 200", response.Status)
	}
	if response.text() != sampleDoc {
		t.Error("the body does not match the stored file")
	}
	if response.LastModified == nil {
		t.Error("the response lost its Last-Modified header")
	}
}

func TestClientReportsAnUnauthenticatedRequest(t *testing.T) {
	t.Parallel()
	dav := newFakeDAV()
	dav.set("Documents/Bookmarks.xbel", sampleDoc)
	_, server := newTestStore(t, dav)
	client := NewClient(server.URL+"/", "example", "wrong")

	response, err := client.Get(context.Background(), "Documents/Bookmarks.xbel")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if response.Status != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", response.Status)
	}
}

func TestClientKeeps404AsAnOutcome(t *testing.T) {
	t.Parallel()
	dav := newFakeDAV()
	_, server := newTestStore(t, dav)
	client := NewClient(server.URL+"/", "example", "secret")

	response, err := client.Get(context.Background(), "Documents/absent.xbel")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if response.Status != http.StatusNotFound {
		t.Errorf("status = %d, want 404", response.Status)
	}
}

func TestClientReadsThePublishedSize(t *testing.T) {
	t.Parallel()
	dav := newFakeDAV()
	dav.set("Documents/Bookmarks.xbel", sampleDoc)
	_, server := newTestStore(t, dav)
	client := NewClient(server.URL+"/", "example", "secret")

	size, known, err := client.Size(context.Background(), "Documents/Bookmarks.xbel")
	if err != nil {
		t.Fatalf("Size: %v", err)
	}
	if !known {
		t.Fatal("Size reported no length for a file that exists")
	}
	if size != int64(len(sampleDoc)) {
		t.Errorf("size = %d, want %d", size, len(sampleDoc))
	}
}

func TestClientReportsAWriteTheServerRefused(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	t.Cleanup(server.Close)
	client := NewClient(server.URL+"/", "example", "secret")

	err := client.Put(context.Background(), "Documents/Bookmarks.xbel.temp", []byte("x"), xbelContentType)
	if err == nil {
		t.Fatal("Put accepted a 500 response")
	}
	if !strings.Contains(err.Error(), "500") {
		t.Errorf("error %q does not name the status", err)
	}
}

func TestClientDeleteToleratesAMissingResource(t *testing.T) {
	t.Parallel()
	dav := newFakeDAV()
	_, server := newTestStore(t, dav)
	client := NewClient(server.URL+"/", "example", "secret")

	if err := client.Delete(context.Background(), "Documents/absent.lock"); err != nil {
		t.Errorf("Delete of an absent lock: %v", err)
	}
}
