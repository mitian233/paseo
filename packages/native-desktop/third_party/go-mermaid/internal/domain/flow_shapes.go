package domain

import "math"

// ShapeOutline returns a node's polygonal boundary in local unit coordinates.
// Nil indicates an elliptic or rectangular outline handled separately.
func ShapeOutline(shape Shape, w, h float64) []Point {
	k := .2
	if w > 0 {
		k = h / (2 * w)
	}
	switch shape {
	case ShapeRound, ShapeStadium:
		radius := 6.0
		if shape == ShapeStadium {
			radius = h / 2
		}
		if radius > w/2 {
			radius = w / 2
		}
		if radius > h/2 {
			radius = h / 2
		}
		if w <= 0 || h <= 0 {
			return nil
		}
		rx, ry := radius/w, radius/h
		p := []Point{}
		centers := []Point{{1 - rx, ry}, {1 - rx, 1 - ry}, {rx, 1 - ry}, {rx, ry}}
		for corner, c := range centers {
			for i := 0; i <= 8; i++ {
				a := -math.Pi/2 + float64(corner)*math.Pi/2 + float64(i)*math.Pi/16
				p = append(p, Point{c.X + rx*math.Cos(a), c.Y + ry*math.Sin(a)})
			}
		}
		return p
	case "person":
		p := []Point{}
		for i := 0; i <= 16; i++ {
			a := -math.Pi/2 + math.Pi*float64(i)/16
			p = append(p, Point{.5 + .18*math.Cos(a), .18 + .18*math.Sin(a)})
		}
		p = append(p, Point{.5, .4})
		p = quad(p, Point{.8, .4}, Point{.8, .7})
		p = append(p, Point{.8, 1}, Point{.2, 1}, Point{.2, .7})
		p = quad(p, Point{.2, .4}, Point{.5, .4})
		p = append(p, Point{.5, .36})
		for i := 1; i <= 16; i++ {
			a := math.Pi/2 + math.Pi*float64(i)/16
			p = append(p, Point{.5 + .18*math.Cos(a), .18 + .18*math.Sin(a)})
		}
		return p
	case ShapeCylinder:
		p := []Point{}
		for i := 0; i <= 32; i++ {
			a := math.Pi + math.Pi*float64(i)/32
			p = append(p, Point{.5 + .5*math.Cos(a), .12 + .12*math.Sin(a)})
		}
		p = append(p, Point{1, .88})
		for i := 0; i <= 32; i++ {
			a := math.Pi * float64(i) / 32
			p = append(p, Point{.5 + .5*math.Cos(a), .88 + .12*math.Sin(a)})
		}
		return p
	case "hourglass":
		return []Point{{0, 0}, {1, 0}, {0, 1}, {1, 1}}
	case "fork":
		return []Point{{0, .42}, {1, .42}, {1, .58}, {0, .58}}
	case "doc", "lin-doc", "tag-doc":
		p := []Point{{0, 0}, {1, 0}, {1, .85}}
		p = quad(p, Point{.75, 1}, Point{.5, .9})
		p = quad(p, Point{.25, .8}, Point{0, .9})
		return p
	case "bow-rect":
		p := []Point{{0, 0}, {1, 0}}
		p = quad(p, Point{.8, .5}, Point{1, 1})
		p = append(p, Point{0, 1})
		return quad(p, Point{.2, .5}, Point{0, 0})
	case "delay":
		p := []Point{{0, 0}, {.5, 0}}
		for i := 1; i <= 32; i++ {
			a := -math.Pi/2 + math.Pi*float64(i)/32
			p = append(p, Point{.5 + .5*math.Cos(a), .5 + .5*math.Sin(a)})
		}
		return append(p, Point{0, 1})
	case "docs":
		p := []Point{{0, .12}, {.88, .12}, {.88, .85}}
		p = quad(p, Point{.66, .98}, Point{.44, .89})
		return quad(p, Point{.22, .8}, Point{0, .89})
	case "flag":
		p := []Point{{0, .1}}
		p = quad(p, Point{.25, -.05}, Point{.5, .1})
		p = quad(p, Point{.75, .25}, Point{1, .1})
		p = append(p, Point{1, .9})
		p = quad(p, Point{.75, .75}, Point{.5, .9})
		return quad(p, Point{.25, 1.05}, Point{0, .9})
	case "folder":
		return []Point{{0, 0}, {.35, 0}, {.45, .15}, {1, .15}, {1, 1}, {0, 1}}
	case "st-rect":
		return []Point{{0, .12}, {.88, .12}, {.88, 1}, {0, 1}}
	case "curv-trap":
		p := []Point{{0, .5}, {.2, 0}, {.85, 0}}
		p = quad(p, Point{1.15, .5}, Point{.85, 1})
		return append(p, Point{.2, 1})
	case "h-cyl":
		p := []Point{{.12, 0}, {.88, 0}}
		for i := 1; i <= 32; i++ {
			a := -math.Pi/2 + math.Pi*float64(i)/32
			p = append(p, Point{.88 + .12*math.Cos(a), .5 + .5*math.Sin(a)})
		}
		p = append(p, Point{.12, 1})
		for i := 1; i <= 32; i++ {
			a := math.Pi/2 + math.Pi*float64(i)/32
			p = append(p, Point{.12 + .12*math.Cos(a), .5 + .5*math.Sin(a)})
		}
		return p
	case "lin-cyl":
		p := []Point{}
		for i := 0; i <= 32; i++ {
			a := math.Pi + math.Pi*float64(i)/32
			p = append(p, Point{.5 + .5*math.Cos(a), .12 + .12*math.Sin(a)})
		}
		p = append(p, Point{1, .88})
		for i := 1; i <= 32; i++ {
			a := math.Pi * float64(i) / 32
			p = append(p, Point{.5 + .5*math.Cos(a), .88 + .12*math.Sin(a)})
		}
		return p
	case "cloud":
		p := []Point{{.15, .75}}
		p = cubic(p, Point{-.05, .75}, Point{-.05, .45}, Point{.15, .42})
		p = cubic(p, Point{.1, .2}, Point{.3, .1}, Point{.42, .25})
		p = cubic(p, Point{.52, 0}, Point{.78, .1}, Point{.8, .28})
		p = cubic(p, Point{1.05, .25}, Point{1.1, .55}, Point{.92, .62})
		p = cubic(p, Point{1, .82}, Point{.75, .9}, Point{.62, .82})
		p = cubic(p, Point{.45, 1}, Point{.27, .88}, Point{.25, .8})
		return p
	case "tri":
		return []Point{{.5, 0}, {1, 1}, {0, 1}}
	case "flip-tri":
		return []Point{{0, 0}, {1, 0}, {.5, 1}}
	case "sl-rect":
		return []Point{{0, .2}, {1, 0}, {1, 1}, {0, 1}}
	case "notch-rect":
		return []Point{{.15, 0}, {1, 0}, {1, 1}, {0, 1}, {0, .2}}
	case "notch-pent":
		return []Point{{.15, 0}, {.85, 0}, {1, .25}, {1, 1}, {0, 1}, {0, .25}}
	case "bolt":
		return []Point{{.55, 0}, {.1, .55}, {.42, .55}, {.3, 1}, {.9, .38}, {.57, .38}}
	case "bang":
		return []Point{{.5, 0}, {.6, .2}, {.8, .08}, {.8, .3}, {1, .3}, {.88, .5}, {1, .7}, {.8, .7}, {.8, .92}, {.6, .8}, {.5, 1}, {.4, .8}, {.2, .92}, {.2, .7}, {0, .7}, {.12, .5}, {0, .3}, {.2, .3}, {.2, .08}, {.4, .2}}
	case "bucket":
		return []Point{{0, 0}, {1, 0}, {.85, 1}, {.15, 1}}
	case "odd":
		return []Point{{0, 0}, {.8, 0}, {1, .5}, {.8, 1}, {0, 1}, {.2, .5}}
	case ShapeHexagon:
		return []Point{{k, 0}, {1 - k, 0}, {1, .5}, {1 - k, 1}, {k, 1}, {0, .5}}
	case ShapeParallelogram:
		return []Point{{k, 0}, {1, 0}, {1 - k, 1}, {0, 1}}
	case ShapeParallelogramAlt:
		return []Point{{0, 0}, {1 - k, 0}, {1, 1}, {k, 1}}
	case ShapeTrapezoid:
		return []Point{{k, 0}, {1 - k, 0}, {1, 1}, {0, 1}}
	case ShapeTrapezoidAlt:
		return []Point{{0, 0}, {1, 0}, {1 - k, 1}, {k, 1}}
	case ShapeDiamond:
		return []Point{{.5, 0}, {1, .5}, {.5, 1}, {0, .5}}
	}
	return nil
}

func quad(p []Point, control, end Point) []Point {
	start := p[len(p)-1]
	for i := 1; i <= 32; i++ {
		t := float64(i) / 32
		u := 1 - t
		p = append(p, Point{u*u*start.X + 2*u*t*control.X + t*t*end.X, u*u*start.Y + 2*u*t*control.Y + t*t*end.Y})
	}
	return p
}

func cubic(p []Point, a, b, end Point) []Point {
	start := p[len(p)-1]
	for i := 1; i <= 32; i++ {
		t := float64(i) / 32
		u := 1 - t
		p = append(p, Point{u*u*u*start.X + 3*u*u*t*a.X + 3*u*t*t*b.X + t*t*t*end.X, u*u*u*start.Y + 3*u*u*t*a.Y + 3*u*t*t*b.Y + t*t*t*end.Y})
	}
	return p
}
