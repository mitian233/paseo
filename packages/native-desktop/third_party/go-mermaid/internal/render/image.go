package render

import (
	"fmt"
	"github.com/zkrebbekx/go-mermaid/internal/domain"
	"github.com/zkrebbekx/go-mermaid/internal/svgutil"
	"strings"
)

func writeImageNode(b *strings.Builder, n *domain.Node, stroke, text string, opts Options) {
	w, h := n.Image.Dimensions()
	x := n.Pos.X + (n.Size.W-w)/2
	y := n.Pos.Y
	lines := svgutil.SplitLines(n.Label)
	labelHeight := 0.0
	if n.Label != "" {
		labelHeight = float64(len(lines))*(opts.FontSize+2) + 4
	}
	if n.Image.LabelPosition == "t" {
		y += labelHeight
	}
	preserve := "none"
	if n.Image.Constraint {
		preserve = "xMidYMid meet"
	}
	fmt.Fprintf(b, `    <image href="%s" x="%s" y="%s" width="%s" height="%s" preserveAspectRatio="%s"/>`, esc(n.Image.URL), num(x), num(y), num(w), num(h), preserve)
	b.WriteByte('\n')
	fmt.Fprintf(b, `    <rect x="%s" y="%s" width="%s" height="%s" fill="none" stroke="%s"/>`, num(x), num(y), num(w), num(h), stroke)
	b.WriteByte('\n')
	if n.Label != "" {
		labelY := n.Pos.Y + opts.FontSize
		if n.Image.LabelPosition != "t" {
			labelY = y + h + 4 + opts.FontSize
		}
		b.WriteString("    ")
		labelY += float64(len(lines)-1) * (opts.FontSize + 2) / 2
		svgutil.MultilineText(b, lines, n.Pos.X+n.Size.W/2, labelY, opts.FontSize+2, text, "")
		b.WriteByte('\n')
	}
}
