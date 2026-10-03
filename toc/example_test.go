package toc_test

import (
	"bytes"
	"fmt"

	"github.com/yuin/goldmark/v2/toc"
)

func ExampleBuild() {
	first := []byte("# Title\n")
	second := []byte("# Title {#title}\n")
	sources := []toc.Source{
		{Name: "first.md", Node: parse(nil, string(first)), Source: first},
		{Name: "second.md", Node: parse(nil, string(second)), Source: second},
	}

	result := toc.Build(sources)
	for _, h := range result.Headings {
		fmt.Printf("%s: %s -> %s\n", h.Source.Name, h.Text, h.Anchor)
	}

	var nav bytes.Buffer
	_ = toc.Render(&nav, result.Tree)
	fmt.Println(nav.String())

	// Output:
	// first.md: Title -> #title-1
	// second.md: Title -> #title
	// <nav class="toc"><ul><li><a href="#title-1">Title</a></li><li><a href="#title">Title</a></li></ul></nav>
}

func ExampleBuild_empty() {
	result := toc.Build(nil)
	var nav bytes.Buffer
	_ = toc.Render(&nav, result.Tree)
	fmt.Printf("headings=%d conflicts=%d nav=%q\n",
		len(result.Headings), len(result.Conflicts), nav.String())

	// Output:
	// headings=0 conflicts=0 nav="<nav class=\"toc\"><ul></ul></nav>"
}
