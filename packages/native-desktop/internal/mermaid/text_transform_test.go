package mermaid

import (
	"image"
	"testing"

	"golang.org/x/image/font/gofont/goregular"
	"golang.org/x/image/font/opentype"
)

func textTestImage(t *testing.T, source string) *image.RGBA {
	t.Helper()
	face, err := opentype.Parse(goregular.TTF)
	if err != nil {
		t.Fatal(err)
	}
	scene, err := scanSVGScene(source)
	if err != nil {
		t.Fatal(err)
	}
	img := image.NewRGBA(image.Rect(0, 0, 240, 180))
	if err := (rasterFont{font: face}).drawTextElements(img, scene.Texts, 1, 20); err != nil {
		t.Fatal(err)
	}
	return img
}

func textInkBounds(img *image.RGBA, region image.Rectangle) image.Rectangle {
	bounds := image.Rectangle{}
	for y := region.Min.Y; y < region.Max.Y; y++ {
		for x := region.Min.X; x < region.Max.X; x++ {
			if img.RGBAAt(x, y).A == 0 {
				continue
			}
			p := image.Rect(x, y, x+1, y+1)
			if bounds.Empty() {
				bounds = p
			} else {
				bounds = bounds.Union(p)
			}
		}
	}
	return bounds
}

func TestTextNestedTransformAndSiblingIsolation(t *testing.T) {
	img := textTestImage(t, `<svg><g transform="translate(40 20)"><g transform="scale(2)"><text x="10" y="20">A</text></g></g><text x="150" y="40">B</text></svg>`)
	a := textInkBounds(img, image.Rect(40, 20, 120, 70))
	b := textInkBounds(img, image.Rect(145, 15, 185, 45))
	if a.Empty() || a.Min.X < 58 || a.Min.X > 65 || a.Max.Y > 61 {
		t.Fatalf("nested text bounds: %v", a)
	}
	if b.Empty() || b.Min.X < 148 || b.Min.X > 155 || b.Max.Y > 41 {
		t.Fatalf("sibling text bounds: %v", b)
	}
	if got := textInkBounds(img, image.Rect(185, 0, 240, 100)); !got.Empty() {
		t.Fatalf("sibling inherited a group transform: %v", got)
	}
}

func TestTextMultispanAndInheritedStyle(t *testing.T) {
	img := textTestImage(t, `<svg><g fill="red" transform="translate(20 10)"><text x="20" y="30"><tspan x="20">One</tspan><tspan x="20" dy="25">Two</tspan></text></g></svg>`)
	for _, r := range []image.Rectangle{image.Rect(38, 20, 90, 42), image.Rect(38, 45, 90, 67)} {
		var found bool
		for y := r.Min.Y; y < r.Max.Y; y++ {
			for x := r.Min.X; x < r.Max.X; x++ {
				c := img.RGBAAt(x, y)
				if c.A == 0 {
					continue
				}
				found = true
				if c.R == 0 || c.G != 0 || c.B != 0 {
					t.Fatalf("span did not inherit red: %v", c)
				}
			}
		}
		if !found {
			t.Fatalf("missing colored span in %v", r)
		}
	}
}

func TestTextRotationAndSkew(t *testing.T) {
	rotated := textTestImage(t, `<svg><text x="20" y="30" transform="translate(100 0) rotate(90)">Wide</text></svg>`)
	b := textInkBounds(rotated, image.Rect(50, 15, 110, 100))
	if b.Empty() || b.Dy() <= b.Dx() || b.Min.X < 69 || b.Max.X > 100 || b.Min.Y < 18 {
		t.Fatalf("rotated bounds: %v", b)
	}
	skewed := textTestImage(t, `<svg><g transform="matrix(1 0 0.5 1 20 0)"><text x="20" y="40">A</text></g></svg>`)
	b = textInkBounds(skewed, image.Rect(50, 10, 90, 45))
	if b.Empty() || b.Min.X < 50 || b.Max.X > 80 {
		t.Fatalf("skewed text bounds: %v", b)
	}
}

func TestTextNonUniformScaleAndInheritedBaseline(t *testing.T) {
	img := textTestImage(t, `<svg><g transform="translate(30 10) scale(2 1)" dominant-baseline="middle" text-anchor="middle"><text x="30" y="40">A</text></g></svg>`)
	b := textInkBounds(img, image.Rect(65, 30, 115, 70))
	if b.Empty() || b.Min.X < 70 || b.Max.X > 110 || b.Min.Y < 40 || b.Max.Y > 60 {
		t.Fatalf("scaled centered text bounds: %v", b)
	}
}
