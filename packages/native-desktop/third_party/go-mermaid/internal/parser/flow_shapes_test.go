package parser

import (
	"github.com/zkrebbekx/go-mermaid/internal/lexer"
	"testing"
)

func TestMetadataShapesAndLabels(t *testing.T) {
	cases := []struct{ alias, shape string }{
		{"collate", "hourglass"}, {"document", "doc"}, {"stacked-document", "docs"},
		{"stored-data", "bow-rect"}, {"manual-input", "sl-rect"}, {"manual", "trapezoid_alt"},
		{"junction", "f-circ"}, {"join", "fork"}, {"comment", "brace"}, {"text", "text"},
		{"lean-right", "parallelogram"}, {"question", "diamond"},
	}
	for _, c := range cases {
		t.Run(c.alias, func(t *testing.T) {
			src, _, _, _ := Preprocess("flowchart LR\nA@{ shape: " + c.alias + ", label: \"中文, x] y\" } --> B")
			toks, err := lexer.Lex(src)
			if err != nil {
				t.Fatal(err)
			}
			g, err := Parse(toks)
			if err != nil {
				t.Fatal(err)
			}
			if string(g.Nodes[0].Shape) != c.shape || g.Nodes[0].Label != "中文, x] y" {
				t.Fatalf("got shape %q label %q", g.Nodes[0].Shape, g.Nodes[0].Label)
			}
		})
	}
}

func TestUnknownMetadataShapeIsRejected(t *testing.T) {
	src, _, _, _ := Preprocess("flowchart LR\nA@{shape: imaginary}")
	toks, err := lexer.Lex(src)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = Parse(toks); err == nil {
		t.Fatal("unknown shape accepted")
	}
}

func TestImageMetadataPreservesDimensionsAndLabelPosition(t *testing.T) {
	src, _, _, _ := Preprocess(`flowchart LR
A@{img: "https://example.org/a.png", label: "图片", w: 120, h: 80, pos: t, constraint: on}`)
	toks, err := lexer.Lex(src)
	if err != nil {
		t.Fatal(err)
	}
	g, err := Parse(toks)
	if err != nil {
		t.Fatal(err)
	}
	n := g.Nodes[0]
	if n.Image == nil || n.Image.URL != "https://example.org/a.png" || n.Image.Width != 120 || n.Image.Height != 80 || !n.Image.Constraint || n.Image.LabelPosition != "t" || n.Label != "图片" {
		t.Fatalf("image node: %+v image=%+v", n, n.Image)
	}
}
