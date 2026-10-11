package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"regexp"
	"slices"
	"strings"
)

const (
	fieldSeparator = "\t"
	fzfHeight      = "50%"
	fzfCancelled   = 130
	deleteKey      = "ctrl-d"

	// Title, folder, host and href travel in one row.
	entryFieldCount = 4
)

// Entry is one row of the picker: what fzf shows and what the action needs.
type Entry struct {
	Title  string
	Folder string
	Host   string
	Href   string
}

func sanitizeField(text string) string {
	return strings.NewReplacer("\t", " ", "\r", " ", "\n", " ").Replace(text)
}

// line renders one row. The delimiter separates the fields and fzf matches
// on the first three, so a tab inside a title would shift every field.
func (e Entry) line() string {
	return strings.Join([]string{
		sanitizeField(e.Title),
		sanitizeField(e.Folder),
		sanitizeField(e.Host),
		e.Href,
	}, fieldSeparator)
}

func parseEntry(line string) (Entry, error) {
	fields := strings.Split(line, fieldSeparator)
	if len(fields) != entryFieldCount {
		return Entry{}, fmt.Errorf("picker: %q does not hold four fields", line)
	}
	return Entry{Title: fields[0], Folder: fields[1], Host: fields[2], Href: fields[3]}, nil
}

// collectEntries walks the tree in display order and carries the title of
// the folder that holds each bookmark.
func collectEntries(doc *Doc) []Entry {
	type frame struct {
		node   Node
		folder string
	}
	stack := make([]frame, 0, len(doc.Children))
	for _, v := range slices.Backward(doc.Children) {
		stack = append(stack, frame{node: v})
	}
	entries := []Entry{}
	for len(stack) > 0 {
		current := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		switch typed := current.node.(type) {
		case *Bookmark:
			entries = append(entries, Entry{
				Title:  typed.Title,
				Folder: current.folder,
				Host:   hostOf(typed.Href),
				Href:   typed.Href,
			})
		case *Folder:
			for _, v := range slices.Backward(typed.Children) {
				stack = append(stack, frame{node: v, folder: typed.Title})
			}
		default:
			panic(fmt.Sprintf("picker: cannot walk %T", current.node))
		}
	}
	return entries
}

func hostOf(href string) string {
	parsed, err := url.Parse(href)
	if err != nil {
		return ""
	}
	return parsed.Host
}

// pick runs fzf over the entries and returns the choice with the key that
// made it. No context reaches fzf: a caller that bounded the picker with a
// deadline would kill it under the reader's hands. An interrupted fzf
// reports `errCancelled`.
func pick(entries []Entry, allowDelete bool, header string) (Entry, string, error) {
	rows := make([]string, 0, len(entries))
	for _, entry := range entries {
		rows = append(rows, entry.line())
	}
	fzf, err := exec.LookPath("fzf")
	if err != nil {
		return Entry{}, "", errors.New("picker: fzf is not installed")
	}
	args := []string{
		"--delimiter=" + fieldSeparator,
		"--with-nth=1,3",
		"--nth=1,2,3",
		"--height=" + fzfHeight,
		"--prompt=bookmark> ",
		"--header=" + sanitizeField(header),
		"--bind=ctrl-p:change-query(" + phoneFolderTitle + ")",
	}
	if allowDelete {
		args = append(args, "--expect="+deleteKey)
	}
	// The context closes nothing: the picker carries no deadline, because
	// a reader takes as long as the list needs.
	command := exec.CommandContext(context.Background(), fzf, args...)
	command.Stdin = strings.NewReader(strings.Join(rows, "\n") + "\n")
	var output bytes.Buffer
	command.Stdout = &output
	command.Stderr = os.Stderr
	if err = command.Run(); err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) && exit.ExitCode() == fzfCancelled {
			return Entry{}, "", errCancelled
		}
		return Entry{}, "", fmt.Errorf("picker: fzf: %w", err)
	}
	selected := strings.Split(strings.TrimRight(output.String(), "\n"), "\n")
	key := ""
	if allowDelete {
		key = strings.TrimSpace(selected[0])
		selected = selected[1:]
	}
	if len(selected) == 0 || strings.TrimSpace(selected[0]) == "" {
		return Entry{}, "", errCancelled
	}
	entry, err := parseEntry(selected[0])
	if err != nil {
		return Entry{}, "", err
	}
	return entry, key, nil
}

var urlPattern = regexp.MustCompile(`https?://[^\s]+`)

// firstURL finds the link in what the share sheet sent. An app may hand over
// a title and a URL, or a URL with the punctuation of the sentence it sat in.
func firstURL(text string) (string, bool) {
	match := strings.TrimRight(urlPattern.FindString(text), `.,;:!?)"'»”`)
	if match == "" {
		return "", false
	}
	parsed, err := url.Parse(match)
	if err != nil || parsed.Host == "" {
		return "", false
	}
	return match, true
}
