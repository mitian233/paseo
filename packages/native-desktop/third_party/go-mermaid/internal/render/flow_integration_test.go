package render

import (
	"github.com/zkrebbekx/go-mermaid/internal/layout"
	"github.com/zkrebbekx/go-mermaid/internal/lexer"
	"github.com/zkrebbekx/go-mermaid/internal/parser"
	"math"
	"strings"
	"testing"
)

func TestFlowMetadataTriangleLayoutAndRender(t *testing.T) {
	src, _, _, _ := parser.Preprocess("flowchart LR\nA@{shape: tri, label: \"中文\"} --> B")
	tok, err := lexer.Lex(src)
	if err != nil {
		t.Fatal(err)
	}
	g, err := parser.Parse(tok)
	if err != nil {
		t.Fatal(err)
	}
	result, err := layout.Compute(g, layout.Options{FontSize: 14, NodeSep: 40, RankSep: 80})
	if err != nil {
		t.Fatal(err)
	}
	n := g.Nodes[0]
	port := g.Edges[0].Points[0]
	// An isosceles triangle's right boundary at mid-height is 75% across.
	if math.Abs(port.X-(n.Pos.X+.75*n.Size.W)) > 0.01 || math.Abs(port.Y-(n.Pos.Y+.5*n.Size.H)) > 0.01 {
		t.Fatalf("edge endpoint %+v misses triangle %+v", port, n)
	}
	svg, err := SVG(result, Options{FontSize: 14})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(svg), "中文") || !strings.Contains(string(svg), "<polygon") {
		t.Fatalf("triangle and label missing from %s", svg)
	}
}
