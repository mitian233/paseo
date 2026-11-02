package layout

import (
	"github.com/zkrebbekx/go-mermaid/internal/domain"
	"math"
)

// shapePort moves an existing box port onto the outline along its edge segment.
func shapePort(n *domain.Node, port, toward domain.Point) domain.Point {
	dx, dy := toward.X-port.X, toward.Y-port.Y
	if dx == 0 && dy == 0 {
		return port
	}
	// Intersect the whole line through the port with the boundary, choosing the
	// intersection furthest toward the adjacent edge point.
	best := math.Inf(-1)
	result := port
	outline := domain.ShapeOutline(n.Shape, n.Size.W, n.Size.H)
	paths := domain.ShapeOpenOutlines(n.Shape)
	if n.Image != nil && n.Size.W > 0 && n.Size.H > 0 {
		w, h := n.Image.Dimensions()
		x := (n.Size.W - w) / (2 * n.Size.W)
		y := 0.0
		labelH := n.Size.H - h
		if n.Image.LabelPosition == "t" {
			y = labelH / n.Size.H
		}
		right, bottom := x+w/n.Size.W, y+h/n.Size.H
		paths = append(paths, []domain.Point{{x, y}, {right, y}, {right, bottom}, {x, bottom}, {x, y}})
		if n.Label != "" && labelH > 0 {
			top := 0.0
			if n.Image.LabelPosition != "t" {
				top = h / n.Size.H
			}
			bottom := top + labelH/n.Size.H
			paths = append(paths, []domain.Point{{0, top}, {1, top}, {1, bottom}, {0, bottom}, {0, top}})
		}
	}
	if len(outline) > 0 {
		paths = append(paths, append(outline, outline[0]))
	}
	nearest := math.Inf(1)
	closest := port
	for _, path := range paths {
		for i := 0; i+1 < len(path); i++ {
			a, b := path[i], path[i+1]
			ax, ay := n.Pos.X+a.X*n.Size.W, n.Pos.Y+a.Y*n.Size.H
			bx, by := n.Pos.X+b.X*n.Size.W, n.Pos.Y+b.Y*n.Size.H
			sx, sy := bx-ax, by-ay
			length := sx*sx + sy*sy
			if length > 0 {
				u := ((port.X-ax)*sx + (port.Y-ay)*sy) / length
				u = math.Max(0, math.Min(1, u))
				x, y := ax+u*sx, ay+u*sy
				distance := (x-port.X)*(x-port.X) + (y-port.Y)*(y-port.Y)
				if distance < nearest {
					nearest = distance
					closest = domain.Point{X: x, Y: y}
				}
			}
			den := dx*sy - dy*sx
			if math.Abs(den) < 1e-9 {
				continue
			}
			t := ((ax-port.X)*sy - (ay-port.Y)*sx) / den
			u := ((ax-port.X)*dy - (ay-port.Y)*dx) / den
			if u >= -1e-9 && u <= 1+1e-9 && t > best {
				best = t
				result = domain.Point{X: port.X + t*dx, Y: port.Y + t*dy}
			}
		}
	}
	if len(paths) > 0 {
		if math.IsInf(best, -1) {
			return closest
		}
		return result
	}
	switch n.Shape {
	case domain.ShapeCircle, "f-circ", "sm-circ", "dbl-circ", "fr-circ", "cross-circ":
		cx, cy := n.Pos.X+n.Size.W/2, n.Pos.Y+n.Size.H/2
		rx, ry := n.Size.W/2, n.Size.H/2
		if rx == 0 || ry == 0 {
			return port
		}
		px, py := port.X-cx, port.Y-cy
		a := dx*dx/(rx*rx) + dy*dy/(ry*ry)
		b := 2 * (px*dx/(rx*rx) + py*dy/(ry*ry))
		c := px*px/(rx*rx) + py*py/(ry*ry) - 1
		disc := b*b - 4*a*c
		if disc >= 0 {
			t := (-b + math.Sqrt(disc)) / (2 * a)
			return domain.Point{X: port.X + t*dx, Y: port.Y + t*dy}
		}
	}
	return port
}

func clipShapePorts(g *domain.Graph) {
	for _, e := range g.Edges {
		if len(e.Points) < 2 {
			continue
		}
		if n := g.NodeByID(e.From); n != nil {
			e.Points = clipEnd(e.Points, n, true)
		}
		if n := g.NodeByID(e.To); n != nil {
			e.Points = clipEnd(e.Points, n, false)
		}
	}
}

func clipEnd(points []domain.Point, n *domain.Node, start bool) []domain.Point {
	i, j := 0, 1
	if !start {
		i = len(points) - 1
		j = i - 1
	}
	old, next := points[i], points[j]
	p := shapePort(n, old, next)
	points[i] = p
	var elbow domain.Point
	need := false
	if old.X == next.X && p.X != next.X {
		elbow = domain.Point{X: p.X, Y: next.Y}
		need = true
	}
	if old.Y == next.Y && p.Y != next.Y {
		elbow = domain.Point{X: next.X, Y: p.Y}
		need = true
	}
	if !need {
		return points
	}
	if start {
		return append([]domain.Point{p, elbow}, points[1:]...)
	}
	return append(points[:len(points)-1], elbow, p)
}
