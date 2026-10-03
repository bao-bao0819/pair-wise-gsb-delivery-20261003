package toc

import (
	"strings"

	"github.com/yuin/goldmark/v2/ast"
)

// HeadingText returns the visible text of a heading, assembled from its inline
// nodes. It matches what a reader sees after Markdown rendering:
//
//   - backslash escapes and character references in plain text are decoded,
//   - code spans contribute their literal (but normalized) content,
//   - links, emphasis and similar containers contribute their text children,
//   - inline raw HTML tags are omitted,
//   - hard and soft line breaks become single spaces.
func HeadingText(h *ast.Heading, source []byte) string {
	var b strings.Builder
	collectText(&b, h, source)
	return strings.TrimSpace(strings.Join(strings.Fields(b.String()), " "))
}

func collectText(b *strings.Builder, n ast.Node, source []byte) {
	for child := n.FirstChild(); child != nil; child = child.NextSibling() {
		switch c := child.(type) {
		case *ast.Text:
			b.WriteString(c.Value.Value(source))
			if c.HardLineBreak() || c.SoftLineBreak() {
				b.WriteByte(' ')
			}
		case *ast.CodeSpan:
			b.WriteString(c.Value.Value(source))
		case *ast.RawHTML:
			// Raw HTML tags are not visible text.
		default:
			collectText(b, child, source)
		}
	}
}
