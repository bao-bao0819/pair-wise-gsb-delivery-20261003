package toc_test

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yuin/goldmark/v2/ast"
	"github.com/yuin/goldmark/v2/parser"
	"github.com/yuin/goldmark/v2/renderer/html"
	"github.com/yuin/goldmark/v2/toc"
)

func parseSources(t *testing.T, names ...string) []toc.Source {
	t.Helper()
	p := parser.New(parser.WithAttribute())
	sources := make([]toc.Source, 0, len(names))
	for _, name := range names {
		b, err := os.ReadFile(filepath.Join("testdata", name))
		if err != nil {
			t.Fatal(err)
		}
		sources = append(sources, toc.Source{
			Name:   name,
			Node:   p.Parse(b),
			Source: b,
		})
	}
	return sources
}

func flatten(items []*toc.Heading) []*toc.Heading {
	var out []*toc.Heading
	var walk func([]*toc.Heading)
	walk = func(list []*toc.Heading) {
		for _, item := range list {
			out = append(out, item)
			walk(item.Children)
		}
	}
	walk(items)
	return out
}

func TestBuildAssignments(t *testing.T) {
	sources := parseSources(t, "intro.md", "guide.md", "appendix.md")
	result := toc.Build(sources)

	got := make([]string, 0, len(result.Headings))
	for _, h := range result.Headings {
		got = append(got, h.Source.Name+"|"+h.Text+"|"+h.ID+"|"+
			boolMark(h.Explicit))
	}
	want := []string{
		"intro.md|Intro|intro|false",
		"intro.md|Overview|overview|false",
		"intro.md|Code & Friends|code--friends|false",
		"intro.md|Shared|shared|true",
		"guide.md|Guide|guide|false",
		"guide.md|Setup|setup|true",
		"guide.md|Setup|setup-1|false",
		"guide.md|Shared|shared-1|false",
		"guide.md|Deep Section|deep-section|false",
		"guide.md|A & B * Star|a--b--star|false",
		"guide.md|Setext Title|setext-title|false",
		"appendix.md|Appendix|appendix|false",
		"appendix.md||heading|false",
		"appendix.md||heading-1|false",
		"appendix.md|Top|top-2|false",
		"appendix.md|References|top|true",
		"appendix.md|Citations|top-1|true",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("unexpected headings:\n%s", strings.Join(got, "\n"))
	}

	if len(result.Conflicts) != 1 {
		t.Fatalf("expected 1 conflict, got %+v", result.Conflicts)
	}
	c := result.Conflicts[0]
	if c.ID != "top" || c.First.Source.Name != "appendix.md" ||
		c.First.Text != "References" || c.Duplicate.Text != "Citations" ||
		c.Duplicate.ID != "top-1" || c.Duplicate.Source.Name != "appendix.md" {
		t.Fatalf("unexpected conflict: %+v", c)
	}
}

func TestExplicitBeforeAutomatic(t *testing.T) {
	// The automatic heading in the first fragment is followed by an explicit
	// heading with the same base text in the second fragment. Explicit ids win.
	sources := []toc.Source{
		{Name: "a.md", Node: parse(t, "# Alpha\n"), Source: []byte("# Alpha\n")},
		{Name: "b.md", Node: parse(t, "# Alpha {#alpha}\n"), Source: []byte("# Alpha {#alpha}\n")},
	}
	result := toc.Build(sources)
	ids := []string{result.Headings[0].ID, result.Headings[1].ID}
	if ids[0] != "alpha-1" || ids[1] != "alpha" {
		t.Fatalf("expected automatic heading to yield alpha-1 and explicit alpha, got %v", ids)
	}
	if !result.Headings[1].Explicit {
		t.Fatal("explicit heading should be marked")
	}
}

func TestTreeSkipsAndVirtualLevels(t *testing.T) {
	sources := []toc.Source{
		{Name: "a.md", Node: parse(t, "# H1\n#### H4\n### H3\n"), Source: []byte("# H1\n#### H4\n### H3\n")},
	}
	result := toc.Build(sources)

	items := flatten(result.Tree)
	type node struct {
		text    string
		level   int
		virtual bool
		depth   int
		anchor  string
	}
	got := make([]node, 0, len(items))
	for _, item := range items {
		got = append(got, node{item.Text, item.Level, item.Virtual, item.Depth, item.Anchor})
	}
	want := []node{
		{"H1", 1, false, 1, "#h1"},
		{"", 2, true, 2, ""},
		{"", 3, true, 3, ""},
		{"H4", 4, false, 4, "#h4"},
		{"H3", 3, false, 3, "#h3"},
	}
	if len(got) != len(want) {
		t.Fatalf("expected %d tree nodes, got %d: %+v", len(want), len(got), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("node %d mismatch: got %+v want %+v", i, got[i], want[i])
		}
	}

	// Virtual levels must not exist in the flat heading list or AST.
	for _, h := range result.Headings {
		if h.Virtual || h.Node == nil {
			t.Fatal("flat list must contain real headings only")
		}
	}
	astCount := 0
	_ = ast.Walk(sources[0].Node, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		if entering && n.Kind() == ast.KindHeading {
			astCount++
		}
		return ast.WalkContinue, nil
	})
	if astCount != 3 {
		t.Fatalf("AST must remain unchanged, got %d headings", astCount)
	}
}

func TestInclusionLevels(t *testing.T) {
	source := []byte("# H1\n## H2\n### H3\n#### H4\n")
	sources := []toc.Source{{Name: "a.md", Node: parse(t, string(source)), Source: source}}

	result := toc.Build(sources, toc.WithMinLevel(2), toc.WithMaxLevel(3))
	items := flatten(result.Tree)
	var texts []string
	for _, item := range items {
		texts = append(texts, item.Text)
	}
	if strings.Join(texts, ",") != "H2,H3" {
		t.Fatalf("unexpected included headings: %v", texts)
	}

	// Excluded headings still receive anchors and may collide with included ones.
	ids := map[string]string{}
	for _, h := range result.Headings {
		ids[h.Text] = h.ID
	}
	if ids["H1"] != "h1" || ids["H4"] != "h4" {
		t.Fatalf("excluded headings must still get ids: %+v", ids)
	}

	// H2 begins at the first tree depth.
	if result.Tree[0].Level != 2 || result.Tree[0].Depth != 1 {
		t.Fatal("first included heading must start at depth 1")
	}
}

func TestWriteBackAndReadOnly(t *testing.T) {
	source := []byte("# A\n# A {#a}\n")

	readOnly := []toc.Source{{Name: "a.md", Node: parse(t, string(source)), Source: source}}
	renderedBefore := render(t, readOnly[0])
	result := toc.Build(readOnly)
	renderedAfter := render(t, readOnly[0])
	if renderedBefore != renderedAfter {
		t.Fatalf("read-only Build modified the AST:\nbefore:\n%s\nafter:\n%s",
			renderedBefore, renderedAfter)
	}
	// With no explicit ids in the source, a read-only Build must not add any
	// id attributes to the document.
	plain := []byte("# A\n# B\n")
	plainSrc := []toc.Source{{Name: "a.md", Node: parse(t, string(plain)), Source: plain}}
	toc.Build(plainSrc)
	if rendered := render(t, plainSrc[0]); strings.Contains(rendered, "id=") {
		t.Fatalf("read-only Build must not assign id attributes:\n%s", rendered)
	}
	_ = result

	writeBack := []toc.Source{{Name: "a.md", Node: parse(t, string(source)), Source: source}}
	toc.Build(writeBack, toc.WithWriteBack())
	renderedWrite := render(t, writeBack[0])
	want := `<h1 id="a-1">A</h1>
<h1 id="a">A</h1>
`
	if renderedWrite != want {
		t.Fatalf("write-back output mismatch:\n%s", testDiff(want, renderedWrite))
	}
}

func TestRenderedAnchorsMatchLinks(t *testing.T) {
	sources := parseSources(t, "intro.md", "guide.md", "appendix.md")
	result := toc.Build(sources, toc.WithWriteBack())

	// Every assigned anchor must appear as an id in exactly one document.
	r := html.New()
	docAnchors := map[string]bool{}
	for _, src := range sources {
		var b bytes.Buffer
		if err := r.Render(&b, src.Source, src.Node); err != nil {
			t.Fatal(err)
		}
		for _, h := range result.Headings {
			if h.Source.Name != src.Name {
				continue
			}
			if !strings.Contains(b.String(), `id="`+h.ID+`"`) {
				t.Fatalf("rendered %s missing id %q:\n%s", src.Name, h.ID, b.String())
			}
			docAnchors[h.ID] = true
		}
	}

	var nav bytes.Buffer
	if err := toc.Render(&nav, result.Tree); err != nil {
		t.Fatal(err)
	}
	for _, h := range result.Headings {
		if !strings.Contains(nav.String(), `href="#`+h.ID+`"`) {
			t.Fatalf("TOC missing link to %q:\n%s", h.ID, nav.String())
		}
	}
	if len(docAnchors) != len(result.Headings) {
		t.Fatalf("anchor count mismatch: %d vs %d", len(docAnchors), len(result.Headings))
	}
}

func TestDeterministic(t *testing.T) {
	first := toc.Build(parseSources(t, "intro.md", "guide.md", "appendix.md"),
		toc.WithWriteBack())
	second := toc.Build(parseSources(t, "intro.md", "guide.md", "appendix.md"),
		toc.WithWriteBack())

	var a, b bytes.Buffer
	if err := toc.Render(&a, first.Tree); err != nil {
		t.Fatal(err)
	}
	if err := toc.Render(&b, second.Tree); err != nil {
		t.Fatal(err)
	}
	if a.String() != b.String() {
		t.Fatalf("non-deterministic output:\n%s\n%s", a.String(), b.String())
	}
	for i := range first.Headings {
		if first.Headings[i].ID != second.Headings[i].ID {
			t.Fatalf("id mismatch at %d: %s vs %s", i,
				first.Headings[i].ID, second.Headings[i].ID)
		}
	}
}

func TestRender(t *testing.T) {
	source := []byte("# H1\n#### H4\n")
	sources := []toc.Source{{Name: "a.md", Node: parse(t, string(source)), Source: source}}
	result := toc.Build(sources)

	var b bytes.Buffer
	if err := toc.Render(&b, result.Tree); err != nil {
		t.Fatal(err)
	}
	want := `<nav class="toc"><ul>` +
		`<li><a href="#h1">H1</a><ul>` +
		`<li class="toc-virtual"><span class="toc-placeholder"></span><ul>` +
		`<li class="toc-virtual"><span class="toc-placeholder"></span><ul>` +
		`<li><a href="#h4">H4</a></li>` +
		`</ul></li></ul></li></ul></li></ul></nav>`
	if b.String() != want {
		t.Fatalf("unexpected TOC HTML:\nwant: %s\ngot:  %s", want, b.String())
	}
}

func parse(t *testing.T, source string) ast.Node {
	return parser.New(parser.WithAttribute()).Parse([]byte(source))
}

func render(t *testing.T, src toc.Source) string {
	t.Helper()
	var b bytes.Buffer
	if err := html.New().Render(&b, src.Source, src.Node); err != nil {
		t.Fatal(err)
	}
	return b.String()
}

func boolMark(v bool) string {
	if v {
		return "true"
	}
	return "false"
}

func testDiff(want, got string) string {
	return "want:\n" + want + "\ngot:\n" + got
}
