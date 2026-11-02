package domain

import (
	"errors"
	"testing"
)

func TestResolveImagesRequiresIntrinsicDimensionsWhenNeeded(t *testing.T) {
	g := &Graph{Nodes: []*Node{{ID: "a", Image: &Image{URL: "https://example.org/a", Height: 60, Constraint: true}}}}
	if err := ResolveImages(g, nil); err == nil {
		t.Fatal("missing intrinsic dimensions accepted")
	}
	if err := ResolveImages(g, func(string) (float64, float64, error) { return 400, 200, nil }); err != nil {
		t.Fatal(err)
	}
	if g.Nodes[0].Image.NaturalWidth != 400 || g.Nodes[0].Image.NaturalHeight != 200 {
		t.Fatal(g.Nodes[0].Image)
	}
	if err := ResolveImages(g, func(string) (float64, float64, error) { return 0, 0, errors.New("bad image") }); err == nil {
		t.Fatal("resolver error ignored")
	}
}

func TestImageDimensionsUseNaturalAspectRatio(t *testing.T) {
	image := Image{Height: 60, Constraint: true, NaturalWidth: 400, NaturalHeight: 200}
	if w, h := image.Dimensions(); w != 120 || h != 60 {
		t.Fatalf("constrained = %v x %v", w, h)
	}
	image.Constraint = false
	if w, h := image.Dimensions(); w != 400 || h != 60 {
		t.Fatalf("unconstrained = %v x %v", w, h)
	}
}
