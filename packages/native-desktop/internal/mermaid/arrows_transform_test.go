package mermaid

import (
	"image"
	"testing"
)

func TestArrowheadsRespectBothEndsAndNestedCoordinates(t *testing.T) {
	svg := `<svg viewBox="0 0 100 100"><g transform="translate(30 20)"><line x1="0" y1="10" x2="30" y2="10" stroke="red" marker-start="url(#arrow)" marker-end="url(#arrow)"/></g></svg>`
	img := image.NewRGBA(image.Rect(0, 0, 100, 100))
	drawArrows(img, svg, 1)
	for _, p := range []image.Point{{35, 30}, {55, 30}} {
		if img.RGBAAt(p.X, p.Y).A == 0 {
			t.Errorf("arrow missing at transformed endpoint region %v", p)
		}
	}
}
func TestArrowheadTransformDoesNotLeakToFollowingSibling(t *testing.T) {
	svg := `<svg viewBox="0 0 140 120"><g transform="translate(30,20)"><line x1="0" y1="10" x2="30" y2="10" stroke="red" marker-end="url(#arrow)"/></g><line x1="70" y1="80" x2="90" y2="80" stroke="blue" marker-end="url(#arrow)"/></svg>`
	img := image.NewRGBA(image.Rect(0, 0, 140, 120))
	drawArrows(img, svg, 1)
	pixel := img.RGBAAt(85, 80)
	if pixel.A == 0 || pixel.B == 0 {
		t.Fatalf("sibling arrow was shifted out of place: %v", pixel)
	}
}
