package main

import (
	"strconv"
	"strings"
	"testing"
)

// sampleDoc is what Floccus writes: the declarations, the counter comment,
// a blank line, then one element per line at two spaces per level.
const sampleDoc = `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE xbel PUBLIC "+//IDN python.org//DTD XML Bookmark Exchange Language 1.0//EN//XML" "http://pyxml.sourceforge.net/topics/dtds/xbel.dtd">
<xbel version="1.0">
<!--- highestId :12: for Floccus bookmark sync browser extension -->

<folder id="1">
  <title>Bookmarks Bar</title>
  <bookmark href="https://example.org/?a=1&amp;b=2" id="7">
    <title>Example &amp; Co</title>
  </bookmark>
  <folder id="3">
    <title>Nested</title>
  </folder>
</folder>
<folder id="2">
  <title>Other Bookmarks</title>
</folder>
</xbel>`

func TestParseRenderRoundTrip(t *testing.T) {
	t.Parallel()
	doc, err := ParseDoc(sampleDoc)
	if err != nil {
		t.Fatalf("ParseDoc: %v", err)
	}
	if got := doc.Render(); got != sampleDoc {
		t.Errorf("render does not reproduce the Floccus bytes:\n got %q\nwant %q", got, sampleDoc)
	}
	if doc.HighestID != 12 {
		t.Errorf("HighestID = %d, want 12", doc.HighestID)
	}
}

func TestParseDecodesEntities(t *testing.T) {
	t.Parallel()
	doc, err := ParseDoc(sampleDoc)
	if err != nil {
		t.Fatalf("ParseDoc: %v", err)
	}
	bookmark := doc.FindBookmark("https://example.org/?a=1&b=2", "")
	if bookmark == nil {
		t.Fatal("the escaped href did not decode to the query string")
	}
	if bookmark.Title != "Example & Co" {
		t.Errorf("Title = %q, want %q", bookmark.Title, "Example & Co")
	}
	if bookmark.ParentID != 1 {
		t.Errorf("ParentID = %d, want 1", bookmark.ParentID)
	}
}

func TestRenderEscapesEveryReservedCharacter(t *testing.T) {
	t.Parallel()
	doc, err := ParseDoc(sampleDoc)
	if err != nil {
		t.Fatalf("ParseDoc: %v", err)
	}
	folder, err := doc.NewFolder(phoneFolderTitle, nil)
	if err != nil {
		t.Fatalf("NewFolder: %v", err)
	}
	title := `a < b & c > d " e ' f`
	href := `https://example.net/?q="x"&y=<z>`
	if _, err = doc.NewBookmark(folder, href, title); err != nil {
		t.Fatalf("NewBookmark: %v", err)
	}
	rendered := doc.Render()
	for _, entity := range []string{"&lt;", "&amp;", "&gt;", "&quot;", "&apos;"} {
		if !strings.Contains(rendered, entity) {
			t.Errorf("rendered file is missing %s", entity)
		}
	}
	again, err := ParseDoc(rendered)
	if err != nil {
		t.Fatalf("ParseDoc of the rendered file: %v", err)
	}
	bookmark := again.FindBookmark(href, title)
	if bookmark == nil {
		t.Fatal("the escaped bookmark did not survive the round trip")
	}
}

func TestCounterFloorRisesToTheHighestID(t *testing.T) {
	t.Parallel()
	// The counter line claims 2, and the tree holds id 7: Floccus would
	// reuse ids from 3 up, so the floor rises to the highest id present.
	low := strings.Replace(sampleDoc, "highestId :12:", "highestId :2:", 1)
	doc, err := ParseDoc(low)
	if err != nil {
		t.Fatalf("ParseDoc: %v", err)
	}
	if doc.HighestID != 7 {
		t.Errorf("HighestID = %d, want the id 7 in the tree", doc.HighestID)
	}
	if id := doc.nextID(); id != 8 {
		t.Errorf("nextID = %d, want 8", id)
	}
}

func TestMissingCounterLineStartsFromTheTree(t *testing.T) {
	t.Parallel()
	without := strings.Split(sampleDoc, "\n")
	var kept []string
	for _, line := range without {
		if !strings.Contains(line, counterPrefix) {
			kept = append(kept, line)
		}
	}
	doc, err := ParseDoc(strings.Join(kept, "\n"))
	if err != nil {
		t.Fatalf("ParseDoc: %v", err)
	}
	if doc.HighestID != 7 {
		t.Errorf("HighestID = %d, want the highest id in the tree", doc.HighestID)
	}
	if !strings.Contains(doc.Render(), counterPrefix+"7"+counterSuffix) {
		t.Error("render did not restore the counter line")
	}
}

func TestChildFolderIgnoresNestedFolders(t *testing.T) {
	t.Parallel()
	doc, err := ParseDoc(sampleDoc)
	if err != nil {
		t.Fatalf("ParseDoc: %v", err)
	}
	if folder := doc.ChildFolder("Nested"); folder != nil {
		t.Errorf("ChildFolder found the nested folder %d at the root", folder.ID)
	}
	if folder := doc.ChildFolder("Bookmarks Bar"); folder == nil {
		t.Error("ChildFolder missed a direct child of <xbel>")
	}
}

func TestNewBookmarkAppendsToTheEnd(t *testing.T) {
	t.Parallel()
	doc, err := ParseDoc(sampleDoc)
	if err != nil {
		t.Fatalf("ParseDoc: %v", err)
	}
	folder := doc.ChildFolder("Bookmarks Bar")
	bookmark, err := doc.NewBookmark(folder, "https://example.net/last", "Last")
	if err != nil {
		t.Fatalf("NewBookmark: %v", err)
	}
	if bookmark.ID != 13 {
		t.Errorf("id = %d, want 13", bookmark.ID)
	}
	if folder.Children[len(folder.Children)-1] != Node(bookmark) {
		t.Error("the bookmark did not land at the end of the child list")
	}
	if doc.HighestID != 13 {
		t.Errorf("HighestID = %d, want 13", doc.HighestID)
	}
}

func TestRemoveDropsNestedNodes(t *testing.T) {
	t.Parallel()
	doc, err := ParseDoc(sampleDoc)
	if err != nil {
		t.Fatalf("ParseDoc: %v", err)
	}
	bar := doc.ChildFolder("Bookmarks Bar")
	if len(bar.Children) != 2 {
		t.Fatalf("the sample holds %d children in Bookmarks Bar, want 2", len(bar.Children))
	}
	if err = doc.Remove(bar.Children[1]); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if len(bar.Children) != 1 {
		t.Errorf("the folder holds %d children after the removal, want 1", len(bar.Children))
	}
	if strings.Contains(doc.Render(), "<title>Nested</title>") {
		t.Error("the rendered file still holds the removed folder")
	}
	foreign := &Bookmark{ID: 99, Href: "https://example.com/", Title: "x"}
	if err = doc.Remove(foreign); err == nil {
		t.Error("removing a node from another document succeeded")
	}
}

func TestNewFolderNeedsARealParent(t *testing.T) {
	t.Parallel()
	doc, err := ParseDoc(sampleDoc)
	if err != nil {
		t.Fatalf("ParseDoc: %v", err)
	}
	if _, err = doc.NewFolder("  ", nil); err == nil {
		t.Error("a blank folder title was accepted")
	}
	if _, err = doc.NewFolder("Away", &Folder{ID: 77}); err == nil {
		t.Error("a folder outside the document was accepted as a parent")
	}
	phone, err := doc.NewFolder(phoneFolderTitle, nil)
	if err != nil {
		t.Fatalf("NewFolder: %v", err)
	}
	if phone.ID != 13 {
		t.Errorf("id = %d, want 13", phone.ID)
	}
	if doc.Children[len(doc.Children)-1] != Node(phone) {
		t.Error("the folder did not land at the end of the root child list")
	}
}

func TestParseRejectsWhatFloccusRejects(t *testing.T) {
	t.Parallel()
	cases := map[string]string{
		"no declaration":   "<xbel version=\"1.0\">\n</xbel>",
		"wrong root":       xmlDeclaration + "\n<bookmarks></bookmarks>",
		"malformed xml":    xmlDeclaration + "\n<xbel version=\"1.0\">",
		"unknown element":  xmlDeclaration + "\n<xbel version=\"1.0\">\n<separator/>\n</xbel>",
		"bookmark without": xmlDeclaration + "\n<xbel version=\"1.0\">\n<bookmark id=\"1\">\n</bookmark>\n</xbel>",
		"id is not a number": xmlDeclaration +
			"\n<xbel version=\"1.0\">\n<folder id=\"x\">\n</folder>\n</xbel>",
		"zero id": xmlDeclaration +
			"\n<xbel version=\"1.0\">\n<folder id=\"0\">\n</folder>\n</xbel>",
		"stray text": xmlDeclaration +
			"\n<xbel version=\"1.0\">\ntext\n</xbel>",
	}
	for name, text := range cases {
		if _, err := ParseDoc(text); err == nil {
			t.Errorf("%s: ParseDoc accepted the file", name)
		}
	}
}

func TestParseRejectsADeepTree(t *testing.T) {
	t.Parallel()
	var out strings.Builder
	out.WriteString(xmlDeclaration + "\n" + rootOpen + "\n")
	depth := maxDepth + 2
	for i := 1; i <= depth; i++ {
		out.WriteString(strings.Repeat("  ", i-1) + "<folder id=\"" + strconv.Itoa(i) + "\">\n")
	}
	for i := depth; i >= 1; i-- {
		out.WriteString(strings.Repeat("  ", i-1) + "</folder>\n")
	}
	out.WriteString(rootClose)
	if _, err := ParseDoc(out.String()); err == nil {
		t.Error("ParseDoc accepted a tree deeper than the bound")
	}
}

func TestEmptyDocumentMatchesTheFloccusShape(t *testing.T) {
	t.Parallel()
	want := xmlDeclaration + "\n" + doctype + "\n" + rootOpen + "\n" +
		counterPrefix + "0" + counterSuffix + "\n\n" + rootClose
	if got := emptyDocument(); got != want {
		t.Errorf("emptyDocument:\n got %q\nwant %q", got, want)
	}
}
