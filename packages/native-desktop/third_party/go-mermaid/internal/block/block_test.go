package block

import (
	"strings"
	"testing"
)

func TestLinksReuseGridNodesAndRenderLabels(t *testing.T) {
	src := "block-beta\ncolumns 3\na[\"Alpha\"] space b[\"Beta\"]\na --\"connects\"--> b\nb <--> a"
	d, err := Parse(src)
	if err != nil {
		t.Fatal(err)
	}
	if len(d.Rows) != 1 || len(d.Rows[0]) != 3 || len(d.Links) != 2 {
		t.Fatalf("rows/links: %#v %#v", d.Rows, d.Links)
	}
	if d.Links[0].From != "a" || d.Links[0].To != "b" || d.Links[0].Label != "connects" {
		t.Fatalf("link: %#v", d.Links[0])
	}
	out, err := Render(src, RenderOptions{FontSize: 14, Padding: 20})
	if err != nil {
		t.Fatal(err)
	}
	svg := string(out)
	for _, s := range []string{"Alpha", "Beta", "connects", "marker-end", "marker-start"} {
		if !strings.Contains(svg, s) {
			t.Errorf("missing %q in SVG", s)
		}
	}
	if strings.Count(svg, ">Alpha</text>") != 1 || strings.Count(svg, ">Beta</text>") != 1 {
		t.Error("link references duplicated nodes")
	}
}

func TestInlineLinkCreatesNodes(t *testing.T) {
	d, err := Parse("block-beta\na --> b")
	if err != nil {
		t.Fatal(err)
	}
	if len(d.Rows) != 1 || len(d.Rows[0]) != 2 || len(d.Links) != 1 {
		t.Fatalf("rows/links: %#v %#v", d.Rows, d.Links)
	}
}
