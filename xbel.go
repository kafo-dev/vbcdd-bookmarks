package main

import (
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"iter"
	"slices"
	"strconv"
	"strings"
)

// The shape Floccus writes with fast-xml-parser: one element per line, two
// spaces per level, a counter comment between the root element and the first
// item. Rendering the same bytes keeps a file written here readable by every
// Floccus profile pointed at it.
const (
	xmlDeclaration = `<?xml version="1.0" encoding="UTF-8"?>`
	doctype        = `<!DOCTYPE xbel PUBLIC "+//IDN python.org//DTD XML Bookmark` +
		` Exchange Language 1.0//EN//XML" "http://pyxml.sourceforge.net/topics/dtds/xbel.dtd">`
	rootOpen      = `<xbel version="1.0">`
	rootClose     = `</xbel>`
	counterPrefix = "<!--- highestId :"
	counterSuffix = ": for Floccus bookmark sync browser extension -->"
	levelIndent   = "  "

	// A folder chain deeper than this marks a file that is not a bookmark
	// tree; the bound keeps every walk in this file finite.
	maxDepth = 32

	// The three element names a Floccus file uses inside <xbel>.
	bookmarkElement = "bookmark"
	folderElement   = "folder"
	titleElement    = "title"
	hrefAttribute   = "href"
)

// escaping matches fast-xml-parser: the five characters XML reserves.
var escaper = strings.NewReplacer(
	"&", "&amp;",
	"<", "&lt;",
	">", "&gt;",
	`"`, "&quot;",
	"'", "&apos;",
)

// Node is one element below <xbel>.
type Node interface {
	nodeID() int
}

// Bookmark is one <bookmark href id> element.
type Bookmark struct {
	ID       int
	Href     string
	Title    string
	ParentID int
}

func (b *Bookmark) nodeID() int { return b.ID }

// Folder is one <folder id> element, holding its children in display order.
type Folder struct {
	ID       int
	Title    string
	ParentID int
	Children []Node
}

func (f *Folder) nodeID() int { return f.ID }

// Doc is the whole file: the children of <xbel> and the id counter.
type Doc struct {
	Children  []Node
	HighestID int
}

func escape(text string) string {
	out := escaper.Replace(text)
	if strings.ContainsAny(out, `<>"'`) {
		panic("xbel: escape left a reserved character in " + out)
	}
	return out
}

// ParseDoc reads the file Floccus would read, rejecting anything Floccus
// would reject plus the elements it would silently drop.
func ParseDoc(text string) (*Doc, error) {
	if !strings.Contains(text, xmlDeclaration) {
		return nil, errors.New("xbel: the file lost its XML declaration")
	}
	decoder := xml.NewDecoder(strings.NewReader(text))
	doc := &Doc{}
	for {
		token, err := decoder.Token()
		if errors.Is(err, io.EOF) {
			return finishDoc(doc, text)
		}
		if err != nil {
			return nil, fmt.Errorf("xbel: %w", err)
		}
		start, ok := token.(xml.StartElement)
		if !ok {
			continue
		}
		if start.Name.Local != "xbel" {
			return nil, fmt.Errorf("xbel: unexpected <%s> at the top level", start.Name.Local)
		}
		children, err := readItems(decoder, start, 0, 0)
		if err != nil {
			return nil, err
		}
		doc.Children = children
	}
}

// finishDoc fills in the counter. Floccus reads the comment line and nothing
// else, so a file whose line fell below an id in the tree would hand out
// duplicate ids; the higher of the two wins.
func finishDoc(doc *Doc, text string) (*Doc, error) {
	counter, _ := parseCounter(text)
	doc.HighestID = counter
	if peak := maxID(doc.Children); peak > doc.HighestID {
		doc.HighestID = peak
	}
	return doc, nil
}

func parseCounter(text string) (int, bool) {
	for line := range strings.Lines(text) {
		if !strings.Contains(line, counterPrefix) {
			continue
		}
		first := strings.Index(line, ":")
		last := strings.LastIndex(line, ":")
		if first < 0 || last <= first {
			return 0, false
		}
		value, err := strconv.Atoi(strings.TrimSpace(line[first+1 : last]))
		if err != nil {
			return 0, false
		}
		return value, true
	}
	return 0, false
}

// readItems consumes tokens until the end element of the enclosing element.
func readItems(decoder *xml.Decoder, start xml.StartElement, parentID, depth int) ([]Node, error) {
	if depth > maxDepth {
		return nil, fmt.Errorf("xbel: the tree nests deeper than %d folders", maxDepth)
	}
	var (
		children []Node
		child    Node
	)
	for {
		token, err := decoder.Token()
		if err != nil {
			return nil, fmt.Errorf("xbel: <%s>: %w", start.Name.Local, err)
		}
		switch typed := token.(type) {
		case xml.EndElement:
			if typed.Name != start.Name {
				return nil, fmt.Errorf("xbel: <%s> closed by </%s>", start.Name.Local, typed.Name.Local)
			}
			return children, nil
		case xml.StartElement:
			if child, err = readItem(decoder, typed, parentID, depth); err != nil {
				return nil, err
			}
			children = append(children, child)
		case xml.CharData:
			if strings.TrimSpace(string(typed)) != "" {
				return nil, fmt.Errorf("xbel: stray text in <%s>", start.Name.Local)
			}
		}
	}
}

// readItem consumes one <bookmark> or <folder>, its start element already
// read. A bookmark holds a title and nothing else.
func readItem(decoder *xml.Decoder, start xml.StartElement, parentID, depth int) (Node, error) {
	name := start.Name.Local
	if name != bookmarkElement && name != folderElement {
		return nil, fmt.Errorf("xbel: unexpected <%s>", name)
	}
	// Every level of nesting passes here, which is where the bound has to
	// bite: a file deeper than this is not a bookmark tree.
	if depth > maxDepth {
		return nil, fmt.Errorf("xbel: the tree nests deeper than %d folders", maxDepth)
	}
	id, err := attributeID(start)
	if err != nil {
		return nil, err
	}
	item := itemReader{
		decoder:    decoder,
		start:      start,
		name:       name,
		id:         id,
		parentID:   parentID,
		depth:      depth,
		isBookmark: name == bookmarkElement,
	}
	return item.read()
}

// itemReader carries one element through the token stream.
type itemReader struct {
	decoder    *xml.Decoder
	start      xml.StartElement
	name       string
	id         int
	parentID   int
	depth      int
	isBookmark bool
	title      string
	children   []Node
}

// read consumes the content of the element and returns the node it holds.
func (r *itemReader) read() (Node, error) {
	for {
		token, err := r.decoder.Token()
		if err != nil {
			return nil, fmt.Errorf("xbel: <%s> %d: %w", r.name, r.id, err)
		}
		switch typed := token.(type) {
		case xml.EndElement:
			if typed.Name != r.start.Name {
				return nil, fmt.Errorf("xbel: <%s> closed by </%s>", r.name, typed.Name.Local)
			}
			return r.node()
		case xml.StartElement:
			if err = r.element(typed); err != nil {
				return nil, err
			}
		}
	}
}

// element consumes one child of the element being read: a bookmark holds a
// title, a folder holds items.
func (r *itemReader) element(start xml.StartElement) error {
	switch start.Name.Local {
	case titleElement:
		text, err := readText(r.decoder, start)
		if err != nil {
			return err
		}
		r.title = text
		return nil
	case bookmarkElement, folderElement:
		if r.isBookmark {
			return fmt.Errorf("xbel: bookmark %d holds a <%s>", r.id, start.Name.Local)
		}
		child, err := readItem(r.decoder, start, r.id, r.depth+1)
		if err != nil {
			return err
		}
		r.children = append(r.children, child)
		return nil
	default:
		return fmt.Errorf("xbel: unexpected <%s> in <%s>", start.Name.Local, r.name)
	}
}

// node returns what the element held.
func (r *itemReader) node() (Node, error) {
	if r.isBookmark {
		href, ok := attribute(r.start, hrefAttribute)
		if !ok {
			return nil, fmt.Errorf("xbel: bookmark %d has no href", r.id)
		}
		return &Bookmark{ID: r.id, Href: href, Title: r.title, ParentID: r.parentID}, nil
	}
	return &Folder{ID: r.id, Title: r.title, ParentID: r.parentID, Children: r.children}, nil
}

func readText(decoder *xml.Decoder, start xml.StartElement) (string, error) {
	var text strings.Builder
	for {
		token, err := decoder.Token()
		if err != nil {
			return "", fmt.Errorf("xbel: <title>: %w", err)
		}
		switch typed := token.(type) {
		case xml.EndElement:
			if typed.Name != start.Name {
				return "", fmt.Errorf("xbel: <title> closed by </%s>", typed.Name.Local)
			}
			return text.String(), nil
		case xml.CharData:
			text.Write(typed)
		case xml.StartElement:
			return "", fmt.Errorf("xbel: unexpected <%s> in <title>", typed.Name.Local)
		}
	}
}

func attribute(start xml.StartElement, name string) (string, bool) {
	for _, attr := range start.Attr {
		if attr.Name.Local == name {
			return attr.Value, true
		}
	}
	return "", false
}

func attributeID(start xml.StartElement) (int, error) {
	raw, ok := attribute(start, "id")
	if !ok {
		return 0, fmt.Errorf("xbel: <%s> without an id", start.Name.Local)
	}
	id, err := strconv.Atoi(raw)
	if err != nil {
		return 0, fmt.Errorf("xbel: <%s> id %q is not an integer", start.Name.Local, raw)
	}
	if id <= 0 {
		return 0, fmt.Errorf("xbel: <%s> id %d must be positive", start.Name.Local, id)
	}
	return id, nil
}

func maxID(nodes []Node) int {
	peak := 0
	for node := range walk(nodes) {
		if id := node.nodeID(); id > peak {
			peak = id
		}
	}
	return peak
}

// walk yields every node, a folder before the children it holds.
func walk(nodes []Node) iter.Seq[Node] {
	return func(yield func(Node) bool) {
		stack := make([]Node, 0, len(nodes))
		for _, node := range slices.Backward(nodes) {
			stack = append(stack, node)
		}
		for len(stack) > 0 {
			node := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			if !yield(node) {
				return
			}
			folder, ok := node.(*Folder)
			if !ok {
				continue
			}
			for _, v := range slices.Backward(folder.Children) {
				stack = append(stack, v)
			}
		}
	}
}

// Render writes the file in the shape Floccus writes it.
func (d *Doc) Render() string {
	var out strings.Builder
	out.WriteString(xmlDeclaration)
	out.WriteByte('\n')
	out.WriteString(doctype)
	out.WriteByte('\n')
	out.WriteString(rootOpen)
	out.WriteByte('\n')
	fmt.Fprintf(&out, "%s%d%s", counterPrefix, d.HighestID, counterSuffix)
	out.WriteByte('\n')
	for _, child := range d.Children {
		out.WriteByte('\n')
		writeItem(&out, child, 0)
	}
	out.WriteByte('\n')
	out.WriteString(rootClose)
	rendered := out.String()
	if !strings.HasSuffix(rendered, rootClose) {
		panic("xbel: render lost the root close tag")
	}
	if strings.Count(rendered, counterPrefix) != 1 {
		panic("xbel: render wrote the counter line more than once")
	}
	return rendered
}

func writeItem(out *strings.Builder, node Node, depth int) {
	if depth > maxDepth {
		panic(fmt.Sprintf("xbel: rendering at depth %d", depth))
	}
	pad := strings.Repeat(levelIndent, depth)
	switch typed := node.(type) {
	case *Bookmark:
		fmt.Fprintf(out, "%s<bookmark href=\"%s\" id=\"%d\">\n", pad, escape(typed.Href), typed.ID)
		fmt.Fprintf(out, "%s%s<title>%s</title>\n", pad, levelIndent, escape(typed.Title))
		fmt.Fprintf(out, "%s</bookmark>", pad)
	case *Folder:
		fmt.Fprintf(out, "%s<folder id=\"%d\">\n", pad, typed.ID)
		fmt.Fprintf(out, "%s%s<title>%s</title>", pad, levelIndent, escape(typed.Title))
		for _, child := range typed.Children {
			out.WriteByte('\n')
			writeItem(out, child, depth+1)
		}
		fmt.Fprintf(out, "\n%s</folder>", pad)
	default:
		panic(fmt.Sprintf("xbel: cannot render %T", node))
	}
}

// ChildFolder returns a folder directly under <xbel>, never a nested one:
// the writer owns one folder at the root, and a folder below it belongs to
// the browser alone.
func (d *Doc) ChildFolder(title string) *Folder {
	for _, child := range d.Children {
		folder, ok := child.(*Folder)
		if ok && folder.Title == title {
			return folder
		}
	}
	return nil
}

// FindBookmark returns the first bookmark with this href, and this title
// when a title is given.
func (d *Doc) FindBookmark(href, title string) *Bookmark {
	for node := range walk(d.Children) {
		bookmark, ok := node.(*Bookmark)
		if !ok || bookmark.Href != href {
			continue
		}
		if title == "" || bookmark.Title == title {
			return bookmark
		}
	}
	return nil
}

// NewFolder appends a folder to the root, or to parent when one is given.
func (d *Doc) NewFolder(title string, parent *Folder) (*Folder, error) {
	if strings.TrimSpace(title) == "" {
		return nil, errors.New("xbel: an empty title is not a folder name")
	}
	if parent != nil && !d.contains(parent) {
		return nil, fmt.Errorf("xbel: folder %d is not in the document", parent.ID)
	}
	folder := &Folder{ID: d.nextID(), Title: title}
	if parent == nil {
		d.Children = append(d.Children, folder)
		return folder, nil
	}
	folder.ParentID = parent.ID
	parent.Children = append(parent.Children, folder)
	return folder, nil
}

// NewBookmark appends a bookmark to the end of a folder's child list, which
// is the position the browser displays last.
func (d *Doc) NewBookmark(parent *Folder, href, title string) (*Bookmark, error) {
	if strings.TrimSpace(href) == "" {
		return nil, errors.New("xbel: an empty href is not addressable")
	}
	if !d.contains(parent) {
		return nil, fmt.Errorf("xbel: folder %d is not in the document", parent.ID)
	}
	bookmark := &Bookmark{ID: d.nextID(), Href: href, Title: title, ParentID: parent.ID}
	parent.Children = append(parent.Children, bookmark)
	return bookmark, nil
}

// Remove drops a node from the child list that holds it.
func (d *Doc) Remove(target Node) error {
	if removeFrom(&d.Children, target) {
		return nil
	}
	for node := range walk(d.Children) {
		folder, ok := node.(*Folder)
		if !ok {
			continue
		}
		if removeFrom(&folder.Children, target) {
			return nil
		}
	}
	return fmt.Errorf("xbel: node %d is not in the document", target.nodeID())
}

func removeFrom(children *[]Node, target Node) bool {
	for i, child := range *children {
		if child == target {
			*children = append((*children)[:i], (*children)[i+1:]...)
			return true
		}
	}
	return false
}

func (d *Doc) contains(target Node) bool {
	if target == nil {
		return false
	}
	for node := range walk(d.Children) {
		if node == target {
			return true
		}
	}
	return false
}

// nextID hands out the id Floccus hands out next: one above the counter.
func (d *Doc) nextID() int {
	if peak := maxID(d.Children); peak > d.HighestID {
		panic(fmt.Sprintf("xbel: counter %d fell below id %d", d.HighestID, peak))
	}
	d.HighestID++
	return d.HighestID
}
