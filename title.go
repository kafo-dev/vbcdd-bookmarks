package main

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"strings"
	"time"

	"golang.org/x/net/html"
	"golang.org/x/net/html/charset"
)

const (
	titleTimeout = 8 * time.Second

	// A page cut off at these ceilings still yields its title, and a page
	// that answers with a stream cannot hold the share hook open.
	titleReadLimit = 1 << 20
	titleCharLimit = 4096

	userAgent = "vbcdd-bookmarks/" + version

	// titleTag is the element that holds the page title.
	titleTag = "title"
)

// fetchTitle returns the page title, or "" when the page cannot be read. An
// empty title is an acceptable outcome: a share that fails because one site
// answered slowly is worse than a bookmark named after its URL.
func fetchTitle(ctx context.Context, target string) string {
	ctx, cancel := context.WithTimeout(ctx, titleTimeout)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return ""
	}
	request.Header.Set("User-Agent", userAgent)
	answer, err := http.DefaultClient.Do(request)
	if err != nil {
		return ""
	}
	raw, err := io.ReadAll(io.LimitReader(answer.Body, titleReadLimit))
	closeErr := answer.Body.Close()
	if answer.StatusCode != http.StatusOK {
		return ""
	}
	if err != nil {
		return ""
	}
	if closeErr != nil {
		// A truncated response can surface only at the close.
		return ""
	}
	// The declared charset wins; a page that declares nonsense is read as
	// UTF-8 and the replacement character shows where the guess was wrong.
	source := io.Reader(bytes.NewReader(raw))
	decoded, err := charset.NewReader(bytes.NewReader(raw), answer.Header.Get("Content-Type"))
	if err == nil {
		source = decoded
	}
	return titleFromHTML(source)
}

// titleFromHTML reads the first <title> element and nothing else.
func titleFromHTML(source io.Reader) string {
	tokenizer := html.NewTokenizer(source)
	for {
		switch tokenizer.Next() {
		case html.ErrorToken:
			return ""
		case html.StartTagToken, html.SelfClosingTagToken:
			name, _ := tokenizer.TagName()
			if string(name) == titleTag {
				return readTitle(tokenizer)
			}
		case html.TextToken, html.EndTagToken, html.CommentToken, html.DoctypeToken:
			// Only a start tag opens the title element.
		}
	}
}

func readTitle(tokenizer *html.Tokenizer) string {
	var text strings.Builder
	for text.Len() < titleCharLimit {
		switch tokenizer.Next() {
		case html.ErrorToken:
			return cleanTitle(text.String())
		case html.TextToken:
			text.Write(tokenizer.Text())
		case html.EndTagToken:
			name, _ := tokenizer.TagName()
			if string(name) == titleTag {
				return cleanTitle(text.String())
			}
		case html.StartTagToken, html.SelfClosingTagToken, html.CommentToken, html.DoctypeToken:
			// A nested element is not text the reader sees.
		}
	}
	return cleanTitle(text.String())
}

// cleanTitle folds the whitespace a title element may carry across lines.
func cleanTitle(text string) string {
	return strings.Join(strings.Fields(text), " ")
}
