package main

import (
	"bytes"
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const (
	// One bookmark file fits in a millisecond; anything larger is a server
	// answering with the wrong resource.
	maxBodyBytes = 16 << 20

	lockContentType = "text/html"
	xbelContentType = "application/xml"

	// The body Floccus writes into the lock file.
	lockBody = `<html><body>I am a lock file</body></html>`
)

// Response keeps the status intact, so a caller can treat 404 as an outcome
// rather than as a failure.
type Response struct {
	Status       int
	Body         []byte
	LastModified *time.Time
}

func (r *Response) text() string { return string(r.Body) }

// Client talks to one WebDAV directory with one set of credentials.
type Client struct {
	base   string
	user   string
	secret string
	http   *http.Client
}

// NewClient panics unless the base URL is the trailing-slash directory the
// configuration promises; a caller that skipped `normalize` is broken.
func NewClient(base, user, password string) *Client {
	if !strings.HasSuffix(base, "/") {
		panic("webdav: base URL must end with a slash: " + base)
	}
	if user == "" {
		panic("webdav: empty user")
	}
	return &Client{base: base, user: user, secret: password, http: &http.Client{}}
}

func (c *Client) do(
	ctx context.Context,
	method, path string,
	body []byte,
	contentType string,
	headers map[string]string,
) (*Response, error) {
	if path == "" {
		panic("webdav: empty path")
	}
	target := c.base + path
	var payload io.Reader
	if body != nil {
		payload = bytes.NewReader(body)
	}
	request, err := http.NewRequestWithContext(ctx, method, target, payload)
	if err != nil {
		return nil, fmt.Errorf("webdav: %w", err)
	}
	request.SetBasicAuth(c.user, c.secret)
	if contentType != "" {
		request.Header.Set("Content-Type", contentType)
	}
	for name, value := range headers {
		request.Header.Set(name, value)
	}
	answer, err := c.http.Do(request)
	if err != nil {
		return nil, fmt.Errorf("webdav: %s %s: %w", method, target, err)
	}
	// Read one byte past the ceiling, so a longer body is an error rather
	// than a silent truncation.
	raw, err := io.ReadAll(io.LimitReader(answer.Body, maxBodyBytes+1))
	closeErr := answer.Body.Close()
	if err != nil {
		return nil, fmt.Errorf("webdav: %s %s: %w", method, target, err)
	}
	if closeErr != nil {
		return nil, fmt.Errorf("webdav: %s %s: %w", method, target, closeErr)
	}
	if len(raw) > maxBodyBytes {
		return nil, fmt.Errorf("webdav: %s %s: the body exceeds %d bytes", method, target, maxBodyBytes)
	}
	response := &Response{Status: answer.StatusCode, Body: raw}
	stamp, err := http.ParseTime(answer.Header.Get("Last-Modified"))
	if err == nil {
		response.LastModified = &stamp
	}
	return response, nil
}

// Get fetches a resource. A missing one answers 404 with an empty body.
func (c *Client) Get(ctx context.Context, path string) (*Response, error) {
	return c.do(ctx, http.MethodGet, path, nil, "", nil)
}

// Put writes a resource, reporting a status the server should not have sent.
func (c *Client) Put(ctx context.Context, path string, body []byte, contentType string) error {
	response, err := c.do(ctx, http.MethodPut, path, body, contentType, nil)
	return accepted("PUT", path, response, err)
}

// Delete removes a resource. An absent resource counts as removed.
func (c *Client) Delete(ctx context.Context, path string) error {
	response, err := c.do(ctx, http.MethodDelete, path, nil, "", nil)
	if err == nil && response.Status == http.StatusNotFound {
		return nil
	}
	return accepted("DELETE", path, response, err)
}

// Move renames a resource in place, replacing whatever sat at the target.
func (c *Client) Move(ctx context.Context, from, to string) error {
	if from == to {
		panic("webdav: a move onto itself replaces nothing")
	}
	headers := map[string]string{"Destination": c.base + to, "Overwrite": "T"}
	response, err := c.do(ctx, "MOVE", from, nil, "", headers)
	return accepted("MOVE", from, response, err)
}

// Size reports the length the server publishes, and whether it published one.
func (c *Client) Size(ctx context.Context, path string) (int64, bool, error) {
	response, err := c.do(ctx, "PROPFIND", path, nil, xbelContentType, map[string]string{"Depth": "0"})
	if err != nil {
		return 0, false, err
	}
	if response.Status != http.StatusMultiStatus {
		return 0, false, nil
	}
	var multistatus struct {
		Responses []struct {
			Propstats []struct {
				Prop struct {
					Length int64 `xml:"DAV: getcontentlength"`
				} `xml:"DAV: prop"`
			} `xml:"DAV: propstat"`
		} `xml:"DAV: response"`
	}
	if err = xml.Unmarshal(response.Body, &multistatus); err != nil {
		// A body that publishes no length leaves the check out, which is
		// what Floccus does with a server that reports none.
		return 0, false, nil //nolint:nilerr // no length is an outcome, not a failure
	}
	for _, item := range multistatus.Responses {
		for _, stat := range item.Propstats {
			if stat.Prop.Length < 0 {
				continue
			}
			return stat.Prop.Length, true, nil
		}
	}
	return 0, false, nil
}

// accepted turns a response the write path cannot use into an error.
func accepted(method, path string, response *Response, err error) error {
	if err != nil {
		return err
	}
	switch response.Status {
	case http.StatusOK, http.StatusCreated, http.StatusNoContent:
		return nil
	default:
		return fmt.Errorf("webdav: %s %s: %d %s",
			method, path, response.Status, http.StatusText(response.Status))
	}
}
