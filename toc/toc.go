// Package toc builds a table of contents and assigns final anchors for a
// collection of already parsed Markdown documents.
//
// A documentation service often concatenates several Markdown documents into a
// single page. When each document is parsed independently, headings that share
// text end up with colliding generated ids, and an id generated automatically
// for an earlier document can silently occupy an id declared explicitly by a
// later document. This package assigns anchors globally across all documents
// after parsing:
//
//   - Explicit ids (for example "# Title {#my-id}") are collected first, so an
//     automatic id can never take over an explicit id.
//   - Automatic ids are allocated afterwards, with numeric suffixes
//     ("foo", "foo-1", "foo-2", ...) applied in document order.
//   - Repeated explicit ids are reported as a ConflictError together with the
//     fragments that declare them.
//
// Fragments should be parsed with attribute support enabled and automatic
// heading ids disabled, for example:
//
//	p := parser.New(parser.WithAttribute())
//
// With [parser.WithAutoHeadingID], every heading already carries an id
// attribute after parsing, and explicit and automatic ids can no longer be
// distinguished.
package toc

import (
	"strconv"
	"strings"

	"github.com/yuin/goldmark/v2/ast"
	"github.com/yuin/goldmark/v2/parser"
	"github.com/yuin/goldmark/v2/text"
	"github.com/yuin/goldmark/v2/util"
)

// A Fragment is a single parsed document combined into a page.
type Fragment struct {
	// Name identifies the fragment in conflict reports. When empty, the
	// 1-based fragment index is used ("#1", "#2", ...).
	Name string

	// Source is the source byte slice the Node was parsed from. It is required
	// when the AST values still reference source positions. It may be nil when
	// the AST was built programmatically with owned values.
	Source []byte

	// Node is the parsed document (typically the value returned by
	// [parser.Parser.Parse]).
	Node ast.Node
}

// An Item is a single heading entry in a TOC.
type Item struct {
	// Heading is the source heading node.
	Heading *ast.Heading

	// Level is the original heading level (1-6). It is never modified, even
	// when the heading is nested under a virtual level due to skipped levels.
	Level int

	// VirtualLevel is the depth of this item in the TOC tree, starting at 1.
	// When heading levels are skipped (for example h1 followed by h3),
	// VirtualLevel can be smaller than Level. It is a property of the generated
	// TOC and is never written back to the source heading.
	VirtualLevel int

	// Text is the visible heading text, assembled from the heading's inline
	// nodes. Backslash escapes and character references are resolved, code
	// spans contribute their literal content, and line breaks become spaces.
	Text string

	// Anchor is the final id assigned to this heading. It matches the id
	// rendered for the heading and can be used as a fragment link target.
	Anchor string

	// Source names the fragment this heading belongs to. It equals
	// Fragment.Name (or its generated fallback).
	Source string

	// Children are the nested TOC entries, in document order.
	Children []*Item
}

// A TOC is the result of Builder.Build: a heading tree and the anchors
// assigned to every included heading.
type TOC struct {
	// Items are the top-level entries of the heading tree, in document order.
	Items []*Item

	headings []*Item
	anchors  map[*ast.Heading]string
}

// Headings returns every included heading in document order, ignoring the tree
// structure.
func (t *TOC) Headings() []*Item {
	return t.headings
}

// Anchor returns the final anchor assigned to the given heading and true if it
// is part of the TOC.
func (t *TOC) Anchor(heading *ast.Heading) (string, bool) {
	id, ok := t.anchors[heading]
	return id, ok
}

// An IDConflict describes an explicit id declared by more than one heading.
type IDConflict struct {
	// ID is the duplicated explicit id.
	ID string

	// First is the heading that first declares the id.
	First *Item

	// Duplicate is a later heading that declares the same id.
	Duplicate *Item

	// Fragment is the name of the fragment that contains Duplicate.
	Fragment string
}

// ConflictError is returned by Builder.Build when one or more explicit ids are
// declared multiple times. The returned TOC is still fully populated and
// usable; the error only reports id attributes that must be disambiguated in
// the source documents.
type ConflictError []IDConflict

// Error implements the error interface.
func (e ConflictError) Error() string {
	var b strings.Builder
	for i, c := range e {
		if i > 0 {
			b.WriteString("; ")
		}
		b.WriteString("duplicate heading id ")
		b.WriteString(strconv.Quote(c.ID))
		b.WriteString(" in fragment ")
		b.WriteString(c.Fragment)
	}
	return b.String()
}

// Option configures a Builder.
type Option func(*Builder)

// WithLevels limits the headings included in the TOC to levels in [min, max].
// Levels outside the valid 1-6 range are clamped, and a min greater than max
// is swapped. By default all levels 1-6 are included.
func WithLevels(min, max int) Option {
	return func(b *Builder) {
		if min < 1 {
			min = 1
		}
		if max > 6 {
			max = 6
		}
		if max < 1 {
			max = 1
		}
		if min > max {
			min, max = max, min
		}
		b.minLevel = min
		b.maxLevel = max
	}
}

// WithIDGenerator sets the generator used for automatic ids. By default the
// same generator as [parser.NewIDs] is used, so slugs match the ids the parser
// would generate for a single document.
func WithIDGenerator(generator parser.IDGenerator) Option {
	return func(b *Builder) {
		if generator != nil {
			b.generator = generator
		}
	}
}

// WithWriteBack makes Builder.Build write generated anchors back onto the
// heading nodes as id attributes. Without this option, Build is read-only and
// never modifies the AST. Explicit ids are never overwritten.
func WithWriteBack() Option {
	return func(b *Builder) {
		b.writeBack = true
	}
}

// A Builder builds a TOC for combined documents. A Builder is safe to reuse:
// every call to Build starts with a fresh set of allocated ids, so repeated
// calls with the same input produce identical results.
type Builder struct {
	minLevel  int
	maxLevel  int
	generator parser.IDGenerator
	writeBack bool
}

// NewBuilder returns a new Builder with the given options.
func NewBuilder(opts ...Option) *Builder {
	b := &Builder{
		minLevel: 1,
		maxLevel: 6,
	}
	for _, opt := range opts {
		opt(b)
	}
	return b
}

// Build collects the headings of all fragments in order, assigns anchors
// globally, and returns the resulting TOC. If explicit ids are declared more
// than once, the returned error is a ConflictError; the TOC is still returned
// and populated.
func (b *Builder) Build(fragments ...Fragment) (*TOC, error) {
	toc := &TOC{
		anchors: map[*ast.Heading]string{},
	}

	type collected struct {
		item     *Item
		explicit string
	}
	var entries []collected

	for fi, fragment := range fragments {
		if fragment.Node == nil {
			continue
		}
		sourceName := fragment.Name
		if sourceName == "" {
			sourceName = fragmentName(fi)
		}
		if err := ast.Walk(fragment.Node, func(node ast.Node, entering bool) (ast.WalkStatus, error) {
			if !entering {
				return ast.WalkContinue, nil
			}
			heading, ok := node.(*ast.Heading)
			if !ok {
				return ast.WalkContinue, nil
			}
			if heading.Level < b.minLevel || heading.Level > b.maxLevel {
				return ast.WalkSkipChildren, nil
			}
			item := &Item{
				Heading: heading,
				Level:   heading.Level,
				Text:    headingText(heading, fragment.Source),
				Source:  sourceName,
			}
			entries = append(entries, collected{
				item:     item,
				explicit: explicitID(heading, fragment.Source),
			})
			toc.headings = append(toc.headings, item)
			toc.anchors[heading] = ""
			return ast.WalkSkipChildren, nil
		}); err != nil {
			return nil, err
		}
	}

	ids := b.newIDs()

	// Phase 1: reserve explicit ids first and record every repeated explicit
	// id. The first occurrence keeps the id; later occurrences are reported.
	reserved := map[string]*Item{}
	var conflicts ConflictError
	for _, entry := range entries {
		if entry.explicit == "" {
			continue
		}
		item := entry.item
		item.Anchor = entry.explicit
		if first, ok := reserved[entry.explicit]; ok {
			conflicts = append(conflicts, IDConflict{
				ID:        entry.explicit,
				First:     first,
				Duplicate: item,
				Fragment:  item.Source,
			})
			continue
		}
		reserved[entry.explicit] = item
		ids.Put(util.StringToReadOnlyBytes(entry.explicit))
	}

	// Phase 2: allocate automatic ids only after every explicit id is reserved,
	// so an automatic id can never occupy a later explicit id.
	for _, entry := range entries {
		if entry.explicit != "" {
			continue
		}
		item := entry.item
		anchor := ids.Generate(util.StringToReadOnlyBytes(item.Text), ast.KindHeading)
		item.Anchor = util.BytesToReadOnlyString(anchor)
		if b.writeBack {
			item.Heading.SetAttribute(
				"id",
				text.NewMultiLineValueFromString(item.Anchor, text.IdentityDecoder),
			)
		}
	}

	for _, entry := range entries {
		toc.anchors[entry.item.Heading] = entry.item.Anchor
	}

	buildTree(toc.headings, &toc.Items)

	if len(conflicts) > 0 {
		return toc, conflicts
	}
	return toc, nil
}

func (b *Builder) newIDs() *parser.IDs {
	if b.generator != nil {
		return parser.NewIDs(parser.WithIDGenerator(b.generator))
	}
	return parser.NewIDs()
}

func fragmentName(index int) string {
	return "#" + strconv.Itoa(index+1)
}

func explicitID(heading *ast.Heading, source []byte) string {
	value, ok := heading.Attribute("id")
	if !ok || value.IsEmpty() {
		return ""
	}
	return value.Value(source)
}

// buildTree nests document-order headings into a tree. Skipped levels produce
// a shallower virtual hierarchy; no heading node is modified.
func buildTree(headings []*Item, roots *[]*Item) {
	stack := make([]*Item, 0, 6)
	for _, item := range headings {
		for len(stack) > 0 && stack[len(stack)-1].Level >= item.Level {
			stack = stack[:len(stack)-1]
		}
		if len(stack) == 0 {
			*roots = append(*roots, item)
		} else {
			parent := stack[len(stack)-1]
			parent.Children = append(parent.Children, item)
		}
		stack = append(stack, item)
		item.VirtualLevel = len(stack)
	}
}

// headingText assembles the visible text of a heading from its inline nodes.
func headingText(heading *ast.Heading, source []byte) string {
	var b strings.Builder
	for child := heading.FirstChild(); child != nil; child = child.NextSibling() {
		writeInlineText(&b, source, child)
	}
	return strings.TrimSpace(b.String())
}

func writeInlineText(b *strings.Builder, source []byte, node ast.Node) {
	switch node := node.(type) {
	case *ast.Text:
		b.WriteString(node.Value.Value(source))
		// Soft and hard line breaks become whitespace between visible lines.
		if node.SoftLineBreak() || node.HardLineBreak() {
			b.WriteByte(' ')
		}
	case *ast.CodeSpan:
		// Code spans carry their already normalized, literal content.
		b.WriteString(node.Value.Value(source))
	case *ast.AutoLink:
		// An autolink has no children; its visible part is the label.
		b.WriteString(node.Label.Value(source))
	case *ast.RawHTML:
		// Inline HTML tags produce no visible text.
	default:
		for child := node.FirstChild(); child != nil; child = child.NextSibling() {
			writeInlineText(b, source, child)
		}
	}
}
