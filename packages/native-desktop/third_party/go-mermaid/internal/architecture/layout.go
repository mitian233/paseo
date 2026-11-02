package architecture

import (
	"fmt"
	"math"
	"math/rand/v2"
	"strconv"
	"time"

	"github.com/zkrebbekx/go-mermaid/internal/svgutil"
)

type constraint struct {
	from, to int
	distance float64
}
type axisSolver struct {
	parent      []int
	constraints []constraint
	positions   []float64
	limit       int
}

func newAxis(n, limit int) *axisSolver {
	s := &axisSolver{parent: make([]int, n), positions: make([]float64, n), limit: limit}
	for i := range s.parent {
		s.parent[i] = i
	}
	return s
}
func (s *axisSolver) root(i int) int {
	for s.parent[i] != i {
		i = s.parent[i]
	}
	return i
}
func (s *axisSolver) union(a, b int) {
	a = s.root(a)
	b = s.root(b)
	if a != b {
		s.parent[b] = a
	}
}
func (s *axisSolver) add(a, b int, dist float64) {
	s.constraints = append(s.constraints, constraint{a, b, dist})
}
func (s *axisSolver) solve() bool {
	for i := range s.positions {
		s.positions[i] = 0
	}
	n := len(s.parent)
	if n == 0 {
		return true
	}
	if s.limit < n {
		n = s.limit
	}
	for range n {
		changed := false
		for _, c := range s.constraints {
			a, b := s.root(c.from), s.root(c.to)
			if s.positions[b]+1e-8 < s.positions[a]+c.distance {
				s.positions[b] = s.positions[a] + c.distance
				changed = true
			}
		}
		if !changed {
			return true
		}
	}
	return false
}
func numeric(c map[string]any, key string, def float64) float64 {
	v, ok := c[key]
	if !ok {
		return def
	}
	switch n := v.(type) {
	case float64:
		return n
	case int:
		return float64(n)
	case string:
		if f, e := strconv.ParseFloat(n, 64); e == nil {
			return f
		}
	}
	return def
}
func number(c map[string]any, key string, def float64) float64 {
	n := numeric(c, key, def)
	if n > 0 {
		return n
	}
	return def
}
func contains(a, b *node) bool {
	return a.x <= b.x && a.y <= b.y && a.x+a.w >= b.x+b.w && a.y+a.h >= b.y+b.h
}
func layout(d *Diagram, o RenderOptions) error {
	font := o.FontSize
	if font <= 0 {
		font = 16
	}
	icon := number(o.Config, "iconSize", 40)
	elasticity := math.Max(0, math.Min(1, numeric(o.Config, "edgeElasticity", 0.45)))
	gap := number(o.Config, "nodeSeparation", 75) + icon*number(o.Config, "idealEdgeLengthMultiplier", 1.5)*(1-elasticity)
	face := svgutil.FaceFor(o.FontFace)
	leaves := []*node{}
	index := map[string]int{}
	children := map[string][]*node{}
	for _, n := range d.order {
		children[n.parent] = append(children[n.parent], n)
	}
	seed := int64(numeric(o.Config, "seed", 1))
	randomize, _ := o.Config["randomize"].(bool)
	if seed == 0 {
		seed = time.Now().UnixNano()
		randomize = true
	}
	if randomize || seed != 1 {
		rng := rand.New(rand.NewPCG(uint64(seed), uint64(seed)^0x9e3779b97f4a7c15))
		shuffle := func(siblings []*node) {
			rng.Shuffle(len(siblings), func(i, j int) { siblings[i], siblings[j] = siblings[j], siblings[i] })
		}
		shuffle(children[""])
		for _, node := range d.order {
			if node.kind == "group" {
				shuffle(children[node.id])
			}
		}
	}
	var visit func(string)
	visit = func(parent string) {
		for _, n := range children[parent] {
			if n.kind == "group" && len(children[n.id]) > 0 {
				visit(n.id)
			} else {
				index[n.id] = len(leaves)
				leaves = append(leaves, n)
			}
		}
	}
	visit("")
	for _, n := range leaves {
		if n.kind == "junction" {
			n.w = 8
			n.h = 8
			continue
		}
		n.w = math.Max(icon, face.Width(n.label, font)+12)
		n.h = icon
		if n.label != "" {
			n.h += font * 1.5
		}
		if n.kind == "group" {
			n.w = math.Max(n.w, 80)
			n.h = math.Max(n.h, 60)
		}
	}
	limit := int(number(o.Config, "numIter", 2500))
	x, y := newAxis(len(leaves), limit), newAxis(len(leaves), limit)
	for _, a := range d.aligns {
		for _, id := range a.ids[1:] {
			if a.axis == "row" {
				y.union(index[a.ids[0]], index[id])
			} else {
				x.union(index[a.ids[0]], index[id])
			}
		}
	}
	for _, a := range d.aligns {
		for i := 1; i < len(a.ids); i++ {
			u, v := index[a.ids[i-1]], index[a.ids[i]]
			if a.axis == "row" {
				x.add(u, v, (leaves[u].w+leaves[v].w)/2+gap)
			} else {
				y.add(u, v, (leaves[u].h+leaves[v].h)/2+gap)
			}
		}
	}
	for _, e := range d.edges {
		a, b := index[e.a], index[e.b]
		switch {
		case e.ap == "R" && e.bp == "L":
			x.add(a, b, (leaves[a].w+leaves[b].w)/2+gap)
		case e.ap == "L" && e.bp == "R":
			x.add(b, a, (leaves[a].w+leaves[b].w)/2+gap)
		case e.ap == "B" && e.bp == "T":
			y.add(a, b, (leaves[a].h+leaves[b].h)/2+gap)
		case e.ap == "T" && e.bp == "B":
			y.add(b, a, (leaves[a].h+leaves[b].h)/2+gap)
		}
	}
	if !x.solve() || !y.solve() {
		return fmt.Errorf("conflicting architecture alignment or edge directions, or numIter limit reached")
	}
	// Keep each sibling group in its own region, including nested groups.
	var descendants func(*node) []int
	descendants = func(n *node) []int {
		if idx, ok := index[n.id]; ok {
			return []int{idx}
		}
		var result []int
		for _, child := range children[n.id] {
			result = append(result, descendants(child)...)
		}
		return result
	}
	var separate func(string) error
	separate = func(parent string) error {
		siblings := children[parent]
		for i, a := range siblings {
			for _, b := range siblings[i+1:] {
				if a.kind != "group" && b.kind != "group" {
					continue
				}
				left, right := descendants(a), descendants(b)
				placed := false
				for _, candidate := range []struct {
					s          *axisSolver
					reverse    bool
					horizontal bool
				}{{x, false, true}, {y, false, false}, {x, true, true}, {y, true, false}} {
					start := len(candidate.s.constraints)
					for _, u := range left {
						for _, v := range right {
							distance := gap + 120 + face.Width(a.label, font) + face.Width(b.label, font)
							if candidate.horizontal {
								distance += (leaves[u].w + leaves[v].w) / 2
							} else {
								distance += (leaves[u].h + leaves[v].h) / 2
							}
							if candidate.reverse {
								candidate.s.add(v, u, distance)
							} else {
								candidate.s.add(u, v, distance)
							}
						}
					}
					if candidate.s.solve() {
						placed = true
						break
					}
					candidate.s.constraints = candidate.s.constraints[:start]
					candidate.s.solve()
				}
				if !placed {
					return fmt.Errorf("conflicting group placement")
				}
			}
		}
		for _, child := range siblings {
			if child.kind == "group" {
				if err := separate(child.id); err != nil {
					return err
				}
			}
		}
		return nil
	}
	if err := separate(""); err != nil {
		return err
	}
	// Add deterministic separation constraints only where boxes intersect. Preserve
	// existing constraints, trying each direction and axis before reporting conflict.
	for i := range leaves {
		for j := i + 1; j < len(leaves); j++ {
			a, b := leaves[i], leaves[j]
			dx := math.Abs(x.positions[x.root(i)] - x.positions[x.root(j)])
			dy := math.Abs(y.positions[y.root(i)] - y.positions[y.root(j)])
			if dx >= (a.w+b.w)/2+gap || dy >= (a.h+b.h)/2+gap {
				continue
			}
			placed := false
			for _, c := range []struct {
				s    *axisSolver
				u, v int
				dist float64
			}{{x, i, j, (a.w+b.w)/2 + gap}, {y, i, j, (a.h+b.h)/2 + gap}, {x, j, i, (a.w+b.w)/2 + gap}, {y, j, i, (a.h+b.h)/2 + gap}} {
				c.s.add(c.u, c.v, c.dist)
				if c.s.solve() {
					placed = true
					break
				}
				c.s.constraints = c.s.constraints[:len(c.s.constraints)-1]
				c.s.solve()
			}
			if !placed {
				return fmt.Errorf("conflicting architecture placement")
			}
		}
	}
	for i, n := range leaves {
		n.x = x.positions[x.root(i)] - n.w/2
		n.y = y.positions[y.root(i)] - n.h/2
	}
	var bounds func(*node)
	bounds = func(g *node) {
		if g.kind != "group" || len(children[g.id]) == 0 {
			return
		}
		minX, minY, maxX, maxY := math.Inf(1), math.Inf(1), math.Inf(-1), math.Inf(-1)
		for _, n := range children[g.id] {
			bounds(n)
			minX = math.Min(minX, n.x)
			minY = math.Min(minY, n.y)
			maxX = math.Max(maxX, n.x+n.w)
			maxY = math.Max(maxY, n.y+n.h)
		}
		header := font * 1.8
		g.x = minX - 20
		g.y = minY - header - 20
		g.w = math.Max(maxX-minX+40, face.Width(g.label, font)+icon+40)
		g.h = maxY - minY + header + 40
	}
	for _, n := range children[""] {
		bounds(n)
	}
	return nil
}
