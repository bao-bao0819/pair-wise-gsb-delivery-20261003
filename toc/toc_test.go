package toc_test

import (
	"bytes"
	"errors"
	"regexp"
	"strings"
	"testing"

	"github.com/yuin/goldmark/v2/ast"
	"github.com/yuin/goldmark/v2/parser"
	"github.com/yuin/goldmark/v2/renderer/html"
	"github.com/yuin/goldmark/v2/toc"
)

type namedSource struct {
	name string
	src  string
}

func frag(name, src string) namedSource {
	return namedSource{name: name, src: src}
}

func parseFragments(t *testing.T, namedSources ...namedSource) []toc.Fragment {
	t.Helper()
	p := parser.New(parser.WithAttribute())
	fragments := make([]toc.Fragment, 0, len(namedSources))
	for _, ns := range namedSources {
		src := []byte(ns.src)
		fragments = append(fragments, toc.Fragment{
			Name:   ns.name,
			Source: src,
			Node:   p.Parse(src),
		})
	}
	return fragments
}

type anchorExpectation struct {
	level        int
	virtualLevel int
	text         string
	anchor       string
	source       string
}

func assertHeadings(t *testing.T, got *toc.TOC, want []anchorExpectation) {
	t.Helper()
	headings := got.Headings()
	if len(headings) != len(want) {
		t.Fatalf("heading count = %d, want %d", len(headings), len(want))
	}
	for i, h := range headings {
		w := want[i]
		if h.Level != w.level || h.VirtualLevel != w.virtualLevel ||
			h.Text != w.text || h.Anchor != w.anchor || h.Source != w.source {
			t.Errorf("heading %d = (L%d V%d %q -> %q in %s), want (L%d V%d %q -> %q in %s)",
				i, h.Level, h.VirtualLevel, h.Text, h.Anchor, h.Source,
				w.level, w.virtualLevel, w.text, w.anchor, w.source)
		}
	}
}

func renderHTML(t *testing.T, node ast.Node, source []byte) string {
	t.Helper()
	var buf bytes.Buffer
	if err := html.New().Render(&buf, source, node); err != nil {
		t.Fatalf("render error: %v", err)
	}
	return buf.String()
}

// Multiple fragments containing headings with the same text receive distinct
// numeric anchors in document order.
func TestDuplicateTitlesAcrossFragments(t *testing.T) {
	fragments := parseFragments(t,
		frag("a", "# Overview\n\n## Details\n"),
		frag("b", "# Overview\n"),
	)
	got, err := toc.NewBuilder().Build(fragments...)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertHeadings(t, got, []anchorExpectation{
		{1, 1, "Overview", "overview", "a"},
		{2, 2, "Details", "details", "a"},
		{1, 1, "Overview", "overview-1", "b"},
	})
}

// Explicit ids are reserved before automatic ids are allocated: an automatic
// heading preceding a later explicit id must not occupy that id.
func TestAutomaticBeforeExplicit(t *testing.T) {
	fragments := parseFragments(t,
		frag("intro.md", "# Intro\n\n# Other\n"),
		frag("fixed.md", "# Wrapped Intro {#intro}\n"),
	)
	got, err := toc.NewBuilder().Build(fragments...)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertHeadings(t, got, []anchorExpectation{
		{1, 1, "Intro", "intro-1", "intro.md"},
		{1, 1, "Other", "other", "intro.md"},
		{1, 1, "Wrapped Intro", "intro", "fixed.md"},
	})
}

// Repeated explicit ids are reported as conflicts naming the fragment that
// contains each duplicate.
func TestDuplicateExplicitIDConflict(t *testing.T) {
	fragments := parseFragments(t,
		frag("first.md", "# A {#shared}\n"),
		frag("second.md", "# B {#shared}\n"),
		frag("third.md", "# C {#shared}\n"),
	)
	got, err := toc.NewBuilder().Build(fragments...)
	if err == nil {
		t.Fatal("expected a conflict error, got nil")
	}
	var conflicts toc.ConflictError
	if !errors.As(err, &conflicts) {
		t.Fatalf("error type = %T, want toc.ConflictError", err)
	}
	if len(conflicts) != 2 {
		t.Fatalf("conflict count = %d, want 2", len(conflicts))
	}
	wantSources := []string{"second.md", "third.md"}
	wantDup := []string{"B", "C"}
	for i, c := range conflicts {
		if c.ID != "shared" {
			t.Errorf("conflict %d id = %q, want %q", i, c.ID, "shared")
		}
		if c.First == nil || c.First.Text != "A" || c.First.Source != "first.md" {
			t.Errorf("conflict %d first = %+v, want heading A in first.md", i, c.First)
		}
		if c.Duplicate == nil || c.Duplicate.Text != wantDup[i] {
			t.Errorf("conflict %d duplicate = %+v, want %q", i, c.Duplicate, wantDup[i])
		}
		if c.Fragment != wantSources[i] {
			t.Errorf("conflict %d fragment = %q, want %q", i, c.Fragment, wantSources[i])
		}
	}
	if len(got.Headings()) != 3 {
		t.Fatalf("headings in conflict result = %d, want 3", len(got.Headings()))
	}
	for _, h := range got.Headings() {
		if h.Anchor != "shared" {
			t.Errorf("anchor = %q, want shared", h.Anchor)
		}
	}
}

// Empty headings fall back to the generator base name with numeric suffixes.
func TestEmptyHeadings(t *testing.T) {
	fragments := parseFragments(t,
		frag("a", "#\n\n## \n"),
		frag("b", "### ###\n"),
	)
	got, err := toc.NewBuilder().Build(fragments...)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertHeadings(t, got, []anchorExpectation{
		{1, 1, "", "heading", "a"},
		{2, 2, "", "heading-1", "a"},
		{3, 3, "", "heading-2", "b"},
	})
}

// Both ATX and Setext headings are collected, and skipped levels produce a
// virtual hierarchy that is not reflected in Heading.Level.
func TestATXAndSetextAndSkippedLevels(t *testing.T) {
	src := "Title\n=====\n\n### Deep\n\n## Mid\n\n#### Deeper\n"
	fragments := parseFragments(t, frag("doc", src))
	got, err := toc.NewBuilder().Build(fragments...)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertHeadings(t, got, []anchorExpectation{
		{1, 1, "Title", "title", "doc"},
		{3, 2, "Deep", "deep", "doc"},
		{2, 2, "Mid", "mid", "doc"},
		{4, 3, "Deeper", "deeper", "doc"},
	})

	roots := got.Items
	if len(roots) != 1 || roots[0].Text != "Title" {
		t.Fatalf("roots = %+v, want single Title root", roots)
	}
	if len(roots[0].Children) != 2 {
		t.Fatalf("Title children = %d, want 2 (Deep, Mid)", len(roots[0].Children))
	}
	mid := roots[0].Children[1]
	if mid.Text != "Mid" || len(mid.Children) != 1 || mid.Children[0].Text != "Deeper" {
		t.Fatalf("Mid subtree = %+v, want single Deeper child", mid)
	}
	if mid.Heading.Level != 2 {
		t.Errorf("Heading.Level mutated to %d, want 2", mid.Heading.Level)
	}
}

// WithLevels restricts included headings and rebuilds the virtual hierarchy
// from the filtered sequence.
func TestWithLevels(t *testing.T) {
	src := "# H1\n\n## H2\n\n### H3\n\n#### H4\n"
	fragments := parseFragments(t, frag("doc", src))
	got, err := toc.NewBuilder(toc.WithLevels(2, 3)).Build(fragments...)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertHeadings(t, got, []anchorExpectation{
		{2, 1, "H2", "h2", "doc"},
		{3, 2, "H3", "h3", "doc"},
	})
}

// Heading text is derived from inline nodes: code spans keep their literal
// text, escapes and entities resolve to visible characters, links and emphasis
// contribute their inner text, and multiline Setext headings fold the break.
func TestHeadingTextFromInlineNodes(t *testing.T) {
	src := strings.Join([]string{
		"## Code `a & b` end",
		"",
		"# Star *x* &amp; [link](/u) & a\\*b",
		"",
		"Line one",
		"line two",
		"========",
		"",
	}, "\n")
	fragments := parseFragments(t, frag("doc", src))
	got, err := toc.NewBuilder().Build(fragments...)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	textByAnchor := map[string]string{}
	for _, h := range got.Headings() {
		textByAnchor[h.Anchor] = h.Text
	}
	want := map[string]string{
		"code-a--b-end":     "Code a & b end",
		"star-x--link--ab":  "Star x & link & a*b",
		"line-one-line-two": "Line one line two",
	}
	for anchor, text := range want {
		if gotText, ok := textByAnchor[anchor]; !ok {
			t.Errorf("anchor %q missing (have %v)", anchor, textByAnchor)
		} else if gotText != text {
			t.Errorf("anchor %q text = %q, want %q", anchor, gotText, text)
		}
	}
}

// Read-only mode never sets id attributes, while WithWriteBack makes rendered
// anchors match the TOC anchors one-to-one.
func TestWriteBackAndReadOnly(t *testing.T) {
	sources := []string{
		"# Install\n\n## `go get` &amp; run\n",
		"# Install\n\n### Skip\n\n# Pin {#install}\n",
	}

	roFragments := parseFragments(t,
		frag("doc1", sources[0]),
		frag("doc2", sources[1]),
	)
	roTOC, err := toc.NewBuilder().Build(roFragments...)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	headingIDRe := regexp.MustCompile(`<h\d(?:\s[^>]*)?>`)
	// No new automatic ids are written; the pre-existing explicit id remains.
	for i, f := range roFragments {
		for _, tag := range headingIDRe.FindAllString(renderHTML(t, f.Node, f.Source), -1) {
			if strings.Contains(tag, `id="`) && tag != `<h1 id="install">` {
				t.Errorf("read-only fragment %d gained generated id: %s", i, tag)
			}
		}
	}
	if n := len(roTOC.Headings()); n != 5 {
		t.Fatalf("read-only heading count = %d, want 5", n)
	}

	wbFragments := parseFragments(t,
		frag("doc1", sources[0]),
		frag("doc2", sources[1]),
	)
	wbTOC, err := toc.NewBuilder(toc.WithWriteBack()).Build(wbFragments...)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	renderedIDs := map[string]bool{}
	idRe := regexp.MustCompile(`<h\d id="([^"]+)"`)
	for _, f := range wbFragments {
		for _, match := range idRe.FindAllStringSubmatch(renderHTML(t, f.Node, f.Source), -1) {
			renderedIDs[match[1]] = true
		}
	}
	if len(renderedIDs) != len(wbTOC.Headings()) {
		t.Fatalf("rendered id count = %d, want %d", len(renderedIDs), len(wbTOC.Headings()))
	}
	for _, h := range wbTOC.Headings() {
		if !renderedIDs[h.Anchor] {
			t.Errorf("TOC anchor %q has no matching rendered heading id", h.Anchor)
		}
		if id, ok := wbTOC.Anchor(h.Heading); !ok || id != h.Anchor {
			t.Errorf("TOC.Anchor = %q,%v want %q,true", id, ok, h.Anchor)
		}
	}
	anchors := map[string]bool{}
	for _, h := range wbTOC.Headings() {
		anchors[h.Anchor] = true
	}
	for _, want := range []string{"install", "install-1", "install-2", "go-get--run", "skip"} {
		if !anchors[want] {
			t.Errorf("expected anchor %q in %v", want, anchors)
		}
	}
}

// Building the same input repeatedly, including reusing the same Builder,
// yields identical anchors.
func TestDeterministicAndReusable(t *testing.T) {
	fragments := parseFragments(t,
		frag("a", "# Same\n"),
		frag("b", "# Same\n\n# Same {#same}\n"),
	)
	builder := toc.NewBuilder()
	var previous []string
	for run := 0; run < 3; run++ {
		got, err := builder.Build(fragments...)
		if err != nil {
			t.Fatalf("run %d unexpected error: %v", run, err)
		}
		var anchors []string
		for _, h := range got.Headings() {
			anchors = append(anchors, h.Anchor)
		}
		if previous != nil && strings.Join(anchors, ",") != strings.Join(previous, ",") {
			t.Fatalf("run %d anchors = %v, want %v", run, anchors, previous)
		}
		previous = anchors
	}
}

// A custom IDGenerator is honored for automatic ids while explicit ids are
// still reserved first.
func TestWithIDGenerator(t *testing.T) {
	fragments := parseFragments(t,
		frag("a", "# Hello\n"),
		frag("b", "# Hello {#custom}\n"),
	)
	got, err := toc.NewBuilder(toc.WithIDGenerator(constantGenerator{})).Build(fragments...)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertHeadings(t, got, []anchorExpectation{
		{1, 1, "Hello", "auto", "a"},
		{1, 1, "Hello", "custom", "b"},
	})
}

type constantGenerator struct{}

func (constantGenerator) Generate([]byte, ast.NodeKind) []byte {
	return []byte("auto")
}

// Unnamed fragments fall back to generated names, and the conflict error
// message identifies both the id and the fragment.
func TestUnnamedFragmentsAndErrorMessage(t *testing.T) {
	fragments := parseFragments(t,
		frag("", "# A {#dup}\n"),
		frag("", "# B {#dup}\n"),
	)
	_, err := toc.NewBuilder().Build(fragments...)
	if err == nil {
		t.Fatal("expected a conflict error")
	}
	want := `duplicate heading id "dup" in fragment #2`
	if err.Error() != want {
		t.Fatalf("error message = %q, want %q", err.Error(), want)
	}
}

// Out-of-range and inverted level ranges are normalized, and autolinks and
// inline HTML do not break heading text extraction.
func TestLevelClampingAndInlineHTMLAndAutolink(t *testing.T) {
	src := "# One <https://example.com> <span>x</span>\n\n###### Six\n"
	fragments := parseFragments(t, frag("doc", src))
	got, err := toc.NewBuilder(toc.WithLevels(0, 99)).Build(fragments...)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got.Headings()) != 2 {
		t.Fatalf("heading count = %d, want 2", len(got.Headings()))
	}
	if text := got.Headings()[0].Text; text != "One https://example.com x" {
		t.Errorf("heading text = %q, want %q", text, "One https://example.com x")
	}

	// Inverted range is swapped, so 6-3 becomes 3-6.
	inverted, err := toc.NewBuilder(toc.WithLevels(6, 3)).Build(fragments...)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(inverted.Headings()) != 1 || inverted.Headings()[0].Text != "Six" {
		t.Fatalf("inverted range result = %+v, want only Six", inverted.Headings())
	}
}

// nil fragment nodes are skipped without panicking.
func TestNilFragmentNode(t *testing.T) {
	got, err := toc.NewBuilder().Build(
		toc.Fragment{Name: "empty", Node: nil},
		toc.Fragment{Name: "real", Source: []byte("# Hi\n"), Node: parser.New().Parse([]byte("# Hi\n"))},
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got.Headings()) != 1 || got.Headings()[0].Anchor != "hi" {
		t.Fatalf("headings = %+v, want single hi", got.Headings())
	}
}
