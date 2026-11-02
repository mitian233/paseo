package layout

import (
	"github.com/zkrebbekx/go-mermaid/internal/domain"
	"math"
	"testing"
)

func TestShapeBoundaryIntersections(t *testing.T) {
	n := &domain.Node{Shape: "tri", Pos: domain.Point{X: 0, Y: 0}, Size: domain.Size{W: 100, H: 100}}
	p := shapePort(n, domain.Point{X: 100, Y: 50}, domain.Point{X: 200, Y: 50})
	if math.Abs(p.X-75) > 0.01 || math.Abs(p.Y-50) > 0.01 {
		t.Fatalf("triangle side port = %+v, want (75,50)", p)
	}
	n.Shape = "hourglass"
	p = shapePort(n, domain.Point{X: 100, Y: 50}, domain.Point{X: 200, Y: 50})
	if math.Abs(p.X-50) > 0.01 {
		t.Fatalf("hourglass side port = %+v, want x=50", p)
	}
}

func TestCurvedAndOpenShapePorts(t *testing.T) {
	n := &domain.Node{Shape: "bow-rect", Size: domain.Size{W: 100, H: 100}}
	p := shapePort(n, domain.Point{X: 100, Y: 50}, domain.Point{X: 200, Y: 50})
	if math.Abs(p.X-90) > .1 {
		t.Fatalf("stored data side port = %+v", p)
	}
	n.Shape = "brace"
	p = shapePort(n, domain.Point{X: 100, Y: 50}, domain.Point{X: 200, Y: 50})
	if math.Abs(p.X) > .1 {
		t.Fatalf("comment port misses brace: %+v", p)
	}
}

func TestImagePortUsesImageAndLabelBounds(t *testing.T) {
	n := &domain.Node{Shape: "image", Label: "Wide label", Size: domain.Size{W: 200, H: 120}, Image: &domain.Image{Width: 100, Height: 100, LabelPosition: "t"}}
	p := shapePort(n, domain.Point{X: 200, Y: 70}, domain.Point{X: 300, Y: 70})
	if math.Abs(p.X-150) > .01 {
		t.Fatalf("image edge landed outside image: %+v", p)
	}
	p = shapePort(n, domain.Point{X: 200, Y: 10}, domain.Point{X: 300, Y: 10})
	if math.Abs(p.X-200) > .01 {
		t.Fatalf("label edge missed label bounds: %+v", p)
	}
}
