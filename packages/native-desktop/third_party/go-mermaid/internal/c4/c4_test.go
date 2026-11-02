package c4

import (
	"github.com/zkrebbekx/go-mermaid/internal/domain"
	"github.com/zkrebbekx/go-mermaid/internal/layout"
	"strings"
	"testing"
)

func TestNestedBoundariesPreserveElementsAndDescriptions(t *testing.T) {
	src := `C4Container
Enterprise_Boundary(org, "Company") {
 Person(user, "User", "Customer")
 System_Boundary(sys, "Platform") {
  Container(api, "API", "Go", "Handles requests")
 }
}
Rel(user, api, "Uses")`
	d, err := Parse(src)
	if err != nil {
		t.Fatal(err)
	}
	if len(d.Boundaries) != 2 || d.Boundaries[1].Parent != "org" || len(d.Elements) != 2 || d.Elements[1].Boundary != "sys" || d.Elements[1].Descr != "Handles requests" || d.Elements[1].Tech != "Go" {
		t.Fatalf("lost nested data: %#v", d)
	}
	out, err := Render(src, RenderOptions{FontSize: 14, Padding: 20})
	if err != nil {
		t.Fatal(err)
	}
	for _, text := range []string{"Company", "Platform", "User", "API", "Handles requests", "Go", "Uses"} {
		if !strings.Contains(string(out), text) {
			t.Errorf("missing %q", text)
		}
	}
}

func TestStyleAndLayoutDirectivesAffectRendering(t *testing.T) {
	src := `C4Container
Container(a, "A", "Go", "First")
Container(b, "B", "Go", "Second")
Rel(a, b, "Calls")
UpdateElementStyle(a, $fontColor="red", $bgColor="#123456", $borderColor="blue")
UpdateRelStyle(a, b, $textColor="green", $lineColor="purple", $offsetX="12", $offsetY="5")
UpdateLayoutConfig($c4ShapeInRow="2", $c4BoundaryInRow="1")`
	d, err := Parse(src)
	if err != nil {
		t.Fatal(err)
	}
	if d.Layout.ShapeInRow != 2 || d.Layout.BoundaryInRow != 1 {
		t.Fatalf("layout lost: %#v", d.Layout)
	}
	out, err := Render(src, RenderOptions{FontSize: 14, Padding: 20})
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range []string{"#123456", "red", "blue", "green", "purple"} {
		if !strings.Contains(string(out), s) {
			t.Errorf("missing style %q", s)
		}
	}
}

func TestLayoutRowsPlaceNodesAndRerouteRelationships(t *testing.T) {
	d, err := Parse("C4Container\nContainer(a, \"A\")\nContainer(b, \"B\")\nContainer(c, \"C\")\nRel(a,b,\"Calls\")\nUpdateLayoutConfig($c4ShapeInRow=2)")
	if err != nil {
		t.Fatal(err)
	}
	g := &domain.Graph{}
	for _, e := range d.Elements {
		g.Nodes = append(g.Nodes, &domain.Node{ID: e.ID, Size: domain.Size{W: 100, H: 50}})
	}
	g.Edges = []*domain.Edge{{From: "a", To: "b"}}
	r := &layout.Result{Graph: g}
	applyRows(d, g, r, 14)
	a, b, c := g.NodeByID("a"), g.NodeByID("b"), g.NodeByID("c")
	if a.Pos.Y != b.Pos.Y || b.Pos.X <= a.Pos.X || c.Pos.Y <= a.Pos.Y {
		t.Fatalf("wrong row placement: %v %v %v", a.Pos, b.Pos, c.Pos)
	}
	if len(g.Edges[0].Points) != 2 || g.Edges[0].Points[0].X != a.Pos.X+a.Size.W || g.Edges[0].Points[1].X != b.Pos.X {
		t.Fatalf("edge not anchored: %#v", g.Edges[0].Points)
	}
}
