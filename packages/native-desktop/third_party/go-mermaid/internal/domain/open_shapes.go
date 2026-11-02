package domain

// ShapeOpenOutlines returns the actual strokes of unfilled shapes in unit
// coordinates. Separate paths remain separate; none is implicitly closed.
func ShapeOpenOutlines(shape Shape) [][]Point {
	left := func() []Point {
		p := []Point{{.15, 0}}
		p = cubic(p, Point{0, 0}, Point{.1, .35}, Point{0, .5})
		return cubic(p, Point{.1, .65}, Point{0, 1}, Point{.15, 1})
	}
	right := func() []Point {
		p := left()
		for i := range p {
			p[i].X = 1 - p[i].X
		}
		return p
	}
	switch shape {
	case "brace":
		return [][]Point{left()}
	case "brace-r":
		return [][]Point{right()}
	case "braces":
		return [][]Point{left(), right()}
	case "docs":
		return [][]Point{{{.08, .12}, {.08, .06}, {.94, .06}, {.94, .8}}, {{.14, .06}, {.14, 0}, {1, 0}, {1, .74}}}
	case "st-rect":
		return [][]Point{{{.08, .12}, {.08, .06}, {.94, .06}, {.94, .94}}, {{.14, .06}, {.14, 0}, {1, 0}, {1, .88}}}
	case "datastore":
		return [][]Point{{{0, 0}, {1, 0}, {1, 1}, {0, 1}}}
	}
	return nil
}
