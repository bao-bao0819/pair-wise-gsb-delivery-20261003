package toc_test

import (
	"fmt"

	"github.com/yuin/goldmark/v2/parser"
	"github.com/yuin/goldmark/v2/toc"
)

// This example combines two documents that both contain an "Install" heading.
// Explicit ids are reserved before automatic ids are allocated, and the
// combined anchors are printed as fragment links.
func ExampleBuilder() {
	sources := map[string]string{
		"docs/install.md":  "# Install\n",
		"docs/advanced.md": "# Install {#install}\n\n## Setup\n",
	}
	order := []string{"docs/install.md", "docs/advanced.md"}

	p := parser.New(parser.WithAttribute())
	var fragments []toc.Fragment
	for _, name := range order {
		source := []byte(sources[name])
		fragments = append(fragments, toc.Fragment{
			Name:   name,
			Source: source,
			Node:   p.Parse(source),
		})
	}

	table, err := toc.NewBuilder().Build(fragments...)
	fmt.Println("error:", err)
	for _, item := range table.Headings() {
		fmt.Printf("%s: [text](%s) %s\n", item.Source, "#"+item.Anchor, item.Text)
	}
	// Output:
	// error: <nil>
	// docs/install.md: [text](#install-1) Install
	// docs/advanced.md: [text](#install) Install
	// docs/advanced.md: [text](#setup) Setup
}
