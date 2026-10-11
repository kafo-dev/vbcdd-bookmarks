package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestTitleFromHTML(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		body string
		want string
	}{
		{"plain", "<html><head><title>Example</title></head></html>", "Example"},
		{"entities", "<title>Cats &amp; Dogs</title>", "Cats & Dogs"},
		{"whitespace", "<title>\n  Two\n  lines\n</title>", "Two lines"},
		{"attributes", `<title id="x">Attributed</title>`, "Attributed"},
		{"empty", "<title></title>", ""},
		{"missing", "<html><body>no title here</body></html>", ""},
		{"after a script", "<script>var t = '</title>'</script><title>Late</title>", "Late"},
		{"uppercase", "<TITLE>Shouted</TITLE>", "Shouted"},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if got := titleFromHTML(strings.NewReader(test.body)); got != test.want {
				t.Errorf("titleFromHTML = %q, want %q", got, test.want)
			}
		})
	}
}

func TestFetchTitleOverHTTP(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("User-Agent") == "" {
			t.Error("the request carried no User-Agent")
		}
		w.Header().Set("Content-Type", "text/html; charset=iso-8859-1")
		if _, err := w.Write([]byte("<html><head><title>Caf\xe9 Central</title></head></html>")); err != nil {
			t.Errorf("write response: %v", err)
		}
	}))
	t.Cleanup(server.Close)

	if got := fetchTitle(context.Background(), server.URL); got != "Café Central" {
		t.Errorf("fetchTitle = %q, want %q", got, "Café Central")
	}
}

func TestFetchTitleGivesUpOnAnErrorStatus(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	t.Cleanup(server.Close)

	if got := fetchTitle(context.Background(), server.URL); got != "" {
		t.Errorf("fetchTitle = %q for a 404, want an empty title", got)
	}
}

func TestFetchTitleGivesUpOnADeadHost(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {}))
	url := server.URL
	server.Close()

	if got := fetchTitle(context.Background(), url); got != "" {
		t.Errorf("fetchTitle = %q against a closed port, want an empty title", got)
	}
}
