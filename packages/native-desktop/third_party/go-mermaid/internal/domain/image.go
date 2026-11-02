package domain

import (
	"fmt"
	"math"
)

// ResolveImages resolves intrinsic dimensions before layout. A resolver may be
// omitted only when each image has explicit dimensions and no aspect constraint.
func ResolveImages(graph *Graph, resolve func(string) (float64, float64, error)) error {
	for _, n := range graph.Nodes {
		image := n.Image
		if image == nil {
			continue
		}
		if resolve == nil {
			if image.Width <= 0 || image.Height <= 0 || image.Constraint {
				return fmt.Errorf("image %q requires intrinsic dimensions", n.ID)
			}
			continue
		}
		w, h, err := resolve(image.URL)
		if err != nil {
			return fmt.Errorf("image %q: %w", n.ID, err)
		}
		if w <= 0 || h <= 0 || math.IsNaN(w) || math.IsNaN(h) || math.IsInf(w, 0) || math.IsInf(h, 0) {
			return fmt.Errorf("image %q has invalid intrinsic dimensions", n.ID)
		}
		image.NaturalWidth, image.NaturalHeight = w, h
	}
	return nil
}

// Dimensions returns the requested image dimensions after resolving its
// intrinsic dimensions. Constraint preserves the intrinsic aspect ratio.
func (image *Image) Dimensions() (float64, float64) {
	w, h := image.Width, image.Height
	if image.Constraint && image.NaturalWidth > 0 && image.NaturalHeight > 0 {
		if h > 0 {
			return h * image.NaturalWidth / image.NaturalHeight, h
		}
		if w > 0 {
			return w, w * image.NaturalHeight / image.NaturalWidth
		}
	}
	if w == 0 {
		w = image.NaturalWidth
	}
	if h == 0 {
		h = image.NaturalHeight
	}
	return w, h
}
