// Package toc builds a combined table of contents for multiple Markdown
// documents that have already been parsed independently.
//
// When several documents are concatenated into a single page, the heading ids
// generated for each document in isolation collide. Build collects every
// heading across the given [Source]s, resolves anchor ids globally and returns
// both a flat heading list and a hierarchical heading tree. Explicit ids
// (declared via heading attributes) take precedence over automatic ones: all
// explicit ids are reserved before any automatic id is allocated, so an
// explicit id can never be consumed by an earlier automatic heading. Repeated
// explicit ids are reported as [Conflict]s together with the fragments that
// own them.
package toc

import (
	"fmt"
	"html"
	"io"

	"github.com/yuin/goldmark/v2/ast"
	"github.com/yuin/goldmark/v2/parser"
	"github.com/yuin/goldmark/v2/text"
)

// Source is a single parsed document that participates in the combined table
// of contents.
type Source struct {
	// Name identifies this fragment in conflict reports, e.g. a file name.
	Name string

	// Node is the parsed document root returned by the parser.
	Node ast.Node

	// Source is the original Markdown source the document was parsed from.
	// It is required to resolve text values that reference the source.
	Source []byte
}

// A Heading represents a single heading entry in the table of contents.
type Heading struct {
	// Level is the heading level, between 1 and 6.
	Level int

	// Text is the visible heading text assembled from inline nodes.
	Text string

	// ID is the final anchor id assigned to this heading.
	ID string

	// Anchor is the fragment reference that points at this heading, i.e. "#" + ID.
	Anchor string

	// Node is the original AST heading node. It is nil for virtual levels
	// inserted to bridge skipped heading levels.
	Node *ast.Heading

	// Source is the fragment this heading belongs to. It is nil for virtual
	// levels.
	Source *Source

	// Explicit reports whether ID was declared explicitly through a heading
	// attribute. A duplicated explicit id that was forced to use a unique
	// suffix still reports Explicit.
	Explicit bool

	// Virtual reports a synthetic level that exists only in the heading tree.
	// Virtual levels bridge skipped heading levels and are never written back
	// to the AST. Virtual headings have no Node, Source, ID or Anchor.
	Virtual bool

	// Depth is the 1-based depth of this heading within the tree.
	Depth int

	// Children are the headings nested immediately under this heading.
	Children []*Heading
}

// A Conflict describes an explicit id declared by more than one heading.
type Conflict struct {
	// ID is the duplicated explicit id.
	ID string

	// First is the heading that first declared ID and keeps it.
	First *Heading

	// Duplicate is the later heading that also declared ID; it was assigned a
	// unique suffixed anchor.
	Duplicate *Heading
}

// Result is the output of [Build].
type Result struct {
	// Tree holds the top level headings, including virtual levels.
	Tree []*Heading

	// Headings holds every real heading in document order, including headings
	// outside the configured inclusion levels. It never contains virtual
	// levels.
	Headings []*Heading

	// Conflicts holds repeated explicit id conflicts in encounter order.
	Conflicts []Conflict
}

// Option configures [Build].
type Option interface {
	setOption(*config)
}

type config struct {
	minLevel  int
	maxLevel  int
	writeBack bool
	generator parser.IDGenerator
}

type minLevelOption int

func (l minLevelOption) setOption(c *config) { c.minLevel = int(l) }

// WithMinLevel excludes headings below the given level (1-based) from the
// heading tree. Such headings still participate in anchor allocation.
func WithMinLevel(level int) Option { return minLevelOption(level) }

type maxLevelOption int

func (l maxLevelOption) setOption(c *config) { c.maxLevel = int(l) }

// WithMaxLevel excludes headings above the given level (1-based) from the
// heading tree. Such headings still participate in anchor allocation.
func WithMaxLevel(level int) Option { return maxLevelOption(level) }

type writeBackOption struct{}

func (writeBackOption) setOption(c *config) { c.writeBack = true }

// WithWriteBack enables writing the assigned anchor ids back onto the AST
// heading nodes. Without this option Build is read-only and never modifies the
// AST.
func WithWriteBack() Option { return writeBackOption{} }

type idGeneratorOption struct{ generator parser.IDGenerator }

func (o idGeneratorOption) setOption(c *config) { c.generator = o.generator }

// WithIDGenerator sets a custom generator used to derive the base id of
// automatic headings. It must implement [parser.IDGenerator].
func WithIDGenerator(generator parser.IDGenerator) Option {
	return idGeneratorOption{generator: generator}
}

type entry struct {
	heading *Heading
	node    *ast.Heading
	rawID   string
}

// Build collects the headings of all sources, assigns globally unique anchor
// ids and builds the heading tree. It is deterministic: the same inputs always
// produce the same ids, tree and conflict order.
func Build(sources []Source, opts ...Option) *Result {
	cfg := config{minLevel: 1, maxLevel: 6}
	for _, opt := range opts {
		opt.setOption(&cfg)
	}
	if cfg.minLevel < 1 {
		cfg.minLevel = 1
	}
	if cfg.maxLevel > 6 {
		cfg.maxLevel = 6
	}
	if cfg.maxLevel < cfg.minLevel {
		cfg.maxLevel = cfg.minLevel
	}

	entries := collectEntries(sources)

	used := map[string]bool{}
	var idsOpts []parser.IDsOption
	if cfg.generator != nil {
		idsOpts = append(idsOpts, parser.WithIDGenerator(cfg.generator))
	}
	ids := parser.NewIDs(idsOpts...)

	reserve := func(id string) string {
		if !used[id] {
			used[id] = true
			ids.Put([]byte(id))
			return id
		}
		for i := 1; ; i++ {
			candidate := fmt.Sprintf("%s-%d", id, i)
			if !used[candidate] {
				used[candidate] = true
				ids.Put([]byte(candidate))
				return candidate
			}
		}
	}

	// Phase 1: collect and reserve all explicit ids first, so later explicit
	// ids are never occupied by earlier automatic headings.
	owners := map[string]*entry{}
	var conflicts []Conflict
	for i := range entries {
		e := &entries[i]
		if e.rawID == "" {
			continue
		}
		e.heading.Explicit = true
		if owner, ok := owners[e.rawID]; ok {
			assigned := reserve(e.rawID)
			e.heading.ID = assigned
			e.heading.Anchor = "#" + assigned
			conflicts = append(conflicts, Conflict{
				ID:        e.rawID,
				First:     owner.heading,
				Duplicate: e.heading,
			})
			continue
		}
		e.heading.ID = reserve(e.rawID)
		e.heading.Anchor = "#" + e.heading.ID
		owners[e.rawID] = e
	}

	// Phase 2: allocate automatic ids.
	for i := range entries {
		e := &entries[i]
		if e.rawID != "" {
			continue
		}
		id := string(ids.Generate([]byte(e.heading.Text), ast.KindHeading))
		used[id] = true
		e.heading.ID = id
		e.heading.Anchor = "#" + id
	}

	if cfg.writeBack {
		for i := range entries {
			e := &entries[i]
			if _, ok := owners[e.rawID]; ok && owners[e.rawID] == e {
				continue
			}
			e.node.SetAttribute("id",
				text.NewMultiLineValueFromString(e.heading.ID, text.IdentityDecoder))
		}
	}

	result := &Result{Conflicts: conflicts}
	for _, e := range entries {
		result.Headings = append(result.Headings, e.heading)
	}
	result.Tree = buildTree(entries, cfg.minLevel, cfg.maxLevel)
	assignDepth(result.Tree, 1)
	return result
}

func collectEntries(sources []Source) []entry {
	var entries []entry
	for i := range sources {
		src := &sources[i]
		_ = ast.Walk(src.Node, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
			if !entering {
				return ast.WalkContinue, nil
			}
			h, ok := n.(*ast.Heading)
			if !ok {
				return ast.WalkContinue, nil
			}
			heading := &Heading{
				Level:  h.Level,
				Text:   HeadingText(h, src.Source),
				Node:   h,
				Source: src,
			}
			rawID := ""
			if value, ok := h.Attribute("id"); ok {
				rawID = string(value.Bytes(src.Source))
			}
			entries = append(entries, entry{
				heading: heading,
				node:    h,
				rawID:   rawID,
			})
			return ast.WalkSkipChildren, nil
		})
	}
	return entries
}

type treeLevel struct {
	item  *Heading
	level int
}

func buildTree(entries []entry, minLevel, maxLevel int) []*Heading {
	var roots []*Heading
	var stack []treeLevel
	prevLevel := minLevel - 1

	appendItem := func(parent *Heading, item *Heading) {
		if parent == nil {
			roots = append(roots, item)
		} else {
			parent.Children = append(parent.Children, item)
		}
	}

	for _, e := range entries {
		if e.heading.Level < minLevel || e.heading.Level > maxLevel {
			continue
		}
		level := e.heading.Level

		for level > prevLevel+1 {
			virtualLevel := prevLevel + 1
			parent := topItem(stack)
			virtual := &Heading{
				Level:   virtualLevel,
				Virtual: true,
			}
			appendItem(parent, virtual)
			stack = append(stack, treeLevel{item: virtual, level: virtualLevel})
			prevLevel = virtualLevel
		}

		for len(stack) > 0 && stack[len(stack)-1].level >= level {
			stack = stack[:len(stack)-1]
		}
		appendItem(topItem(stack), e.heading)
		stack = append(stack, treeLevel{item: e.heading, level: level})
		prevLevel = level
	}

	return roots
}

func topItem(stack []treeLevel) *Heading {
	if len(stack) == 0 {
		return nil
	}
	return stack[len(stack)-1].item
}

func assignDepth(items []*Heading, depth int) {
	for _, item := range items {
		item.Depth = depth
		assignDepth(item.Children, depth+1)
	}
}

// Render renders the heading tree as an HTML nested list and writes it to w.
// Virtual levels render as placeholder list items without links. The output is
// deterministic for a given tree.
func Render(w io.Writer, tree []*Heading) error {
	if _, err := io.WriteString(w, `<nav class="toc"><ul>`); err != nil {
		return err
	}
	if err := renderItems(w, tree); err != nil {
		return err
	}
	_, err := io.WriteString(w, `</ul></nav>`)
	return err
}

func renderItems(w io.Writer, items []*Heading) error {
	for _, item := range items {
		if _, err := io.WriteString(w, `<li`); err != nil {
			return err
		}
		if item.Virtual {
			if _, err := io.WriteString(w, ` class="toc-virtual"><span class="toc-placeholder"></span><ul>`); err != nil {
				return err
			}
			if err := renderItems(w, item.Children); err != nil {
				return err
			}
			if _, err := io.WriteString(w, `</ul></li>`); err != nil {
				return err
			}
			continue
		}
		if _, err := io.WriteString(w, `><a href="`); err != nil {
			return err
		}
		if err := writeEscaped(w, item.Anchor); err != nil {
			return err
		}
		if _, err := io.WriteString(w, `">`); err != nil {
			return err
		}
		if err := writeEscaped(w, item.Text); err != nil {
			return err
		}
		if _, err := io.WriteString(w, `</a>`); err != nil {
			return err
		}
		if len(item.Children) > 0 {
			if _, err := io.WriteString(w, `<ul>`); err != nil {
				return err
			}
			if err := renderItems(w, item.Children); err != nil {
				return err
			}
			if _, err := io.WriteString(w, `</ul>`); err != nil {
				return err
			}
		}
		if _, err := io.WriteString(w, `</li>`); err != nil {
			return err
		}
	}
	return nil
}

func writeEscaped(w io.Writer, s string) error {
	_, err := io.WriteString(w, html.EscapeString(s))
	return err
}
