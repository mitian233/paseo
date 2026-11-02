package architecture

import (
	"fmt"
	"math"
	"sort"
	"strings"

	"github.com/zkrebbekx/go-mermaid/internal/svgutil"
	"github.com/zkrebbekx/go-mermaid/internal/theme"
)

type RenderOptions struct {
	Theme    string
	FontFace string
	FontSize float64
	Padding  float64
	Title    string
	Config   map[string]any
}

// Render renders an architecture-beta diagram as native SVG.
func Render(source string, o RenderOptions) ([]byte, error) {
	d, err := Parse(source)
	if err != nil {
		return nil, err
	}
	if o.FontSize <= 0 {
		o.FontSize = 16
	}
	if o.FontFace == "" {
		o.FontFace = "sans-serif"
	}
	if err = layout(d, o); err != nil {
		return nil, err
	}
	return render(d, o), nil
}

type point struct{ x, y float64 }

func port(n *node, side string, icon float64) point {
	x, y, w, h := n.x, n.y, n.w, n.h
	if n.kind == "service" {
		x += (w - icon) / 2
		w = icon
		h = icon
	}
	switch side {
	case "L":
		return point{x, y + h/2}
	case "R":
		return point{x + w, y + h/2}
	case "T":
		return point{x + w/2, y}
	default:
		return point{x + w/2, y + h}
	}
}
func groupPort(g, n *node, side string, icon float64) point {
	p := port(n, side, icon)
	switch side {
	case "L":
		p.x = g.x
	case "R":
		p.x = g.x + g.w
	case "T":
		p.y = g.y
	case "B":
		p.y = g.y + g.h
	}
	return p
}
func extend(p point, side string) point {
	switch side {
	case "L":
		p.x -= 18
	case "R":
		p.x += 18
	case "T":
		p.y -= 18
	case "B":
		p.y += 18
	}
	return p
}
func n(v float64) string { return svgutil.Num(v) }
func render(d *Diagram, o RenderOptions) []byte {
	pal := theme.For(o.Theme)
	minX, minY, maxX, maxY := 0.0, 0.0, 0.0, 0.0
	for i, v := range d.order {
		if i == 0 {
			minX = v.x
			minY = v.y
			maxX = v.x + v.w
			maxY = v.y + v.h
		}
		minX = math.Min(minX, v.x)
		minY = math.Min(minY, v.y)
		maxX = math.Max(maxX, v.x+v.w)
		maxY = math.Max(maxY, v.y+v.h)
	}
	pad := math.Max(0, o.Padding) + 24
	titleH := svgutil.TitleHeight(o.Title, o.FontSize)
	w := math.Max(1, maxX-minX+pad*2)
	h := math.Max(1, maxY-minY+pad*2+titleH)
	if tw := svgutil.FaceFor(o.FontFace).Width(o.Title, o.FontSize) + pad*2; tw > w {
		w = tw
	}
	var b strings.Builder
	fmt.Fprintf(&b, `<svg xmlns="http://www.w3.org/2000/svg" width="%s" height="%s" viewBox="0 0 %s %s" font-family="%s" font-size="%s">`, n(w), n(h), n(w), n(h), svgutil.Esc(o.FontFace), n(o.FontSize))
	fmt.Fprintf(&b, `<defs><marker id="architecture-arrow" viewBox="0 0 10 10" refX="9" refY="5" markerWidth="7" markerHeight="7" orient="auto-start-reverse"><path d="M 0 0 L 10 5 L 0 10 z" fill="%s"/></marker></defs><rect width="100%%" height="100%%" fill="%s"/>`, pal.Edge, pal.Background)
	if o.Title != "" {
		fmt.Fprintf(&b, `<text x="%s" y="%s" text-anchor="middle" fill="%s">%s</text>`, n(w/2), n(pad+o.FontSize), pal.Text, svgutil.Esc(o.Title))
	}
	fmt.Fprintf(&b, `<g transform="translate(%s %s)">`, n(pad-minX), n(pad+titleH-minY))
	groups := make([]*node, 0)
	for _, v := range d.order {
		if v.kind == "group" {
			groups = append(groups, v)
		}
	}
	depth := func(v *node) int {
		count := 0
		for v.parent != "" {
			count++
			v = d.nodes[v.parent]
		}
		return count
	}
	sort.SliceStable(groups, func(i, j int) bool { return depth(groups[i]) < depth(groups[j]) })
	for _, v := range groups {
		fmt.Fprintf(&b, `<g class="architecture-group" data-id="%s"><rect x="%s" y="%s" width="%s" height="%s" rx="6" fill="%s" stroke="%s"/>`, svgutil.Esc(v.id), n(v.x), n(v.y), n(v.w), n(v.h), pal.Background, pal.NodeStroke)
		if v.icon != "" {
			drawIcon(&b, v.icon, v.x+10, v.y+7, o.FontSize*1.2, pal.NodeStroke)
		}
		tx := v.x + 10
		if v.icon != "" {
			tx += o.FontSize * 1.5
		}
		if v.label != "" {
			fmt.Fprintf(&b, `<text x="%s" y="%s" dominant-baseline="middle" fill="%s">%s</text>`, n(tx), n(v.y+o.FontSize*0.95+7), pal.Text, svgutil.Esc(v.label))
		}
		b.WriteString(`</g>`)
	}
	icon := number(o.Config, "iconSize", 40)
	for _, e := range d.edges {
		a, bn := d.nodes[e.a], d.nodes[e.b]
		p, q := port(a, e.ap, icon), port(bn, e.bp, icon)
		if e.ag {
			p = groupPort(d.nodes[a.parent], a, e.ap, icon)
		}
		if e.bg {
			q = groupPort(d.nodes[bn.parent], bn, e.bp, icon)
		}
		p2, q2 := extend(p, e.ap), extend(q, e.bp)
		var mid string
		if e.ap == "L" || e.ap == "R" {
			mx := (p2.x + q2.x) / 2
			mid = fmt.Sprintf("L %s %s L %s %s ", n(mx), n(p2.y), n(mx), n(q2.y))
		} else {
			my := (p2.y + q2.y) / 2
			mid = fmt.Sprintf("L %s %s L %s %s ", n(p2.x), n(my), n(q2.x), n(my))
		}
		attrs := ""
		if e.left {
			attrs += ` marker-start="url(#architecture-arrow)"`
		}
		if e.right {
			attrs += ` marker-end="url(#architecture-arrow)"`
		}
		fmt.Fprintf(&b, `<path class="architecture-edge" d="M %s %s L %s %s %sL %s %s L %s %s" fill="none" stroke="%s"%s/>`, n(p.x), n(p.y), n(p2.x), n(p2.y), mid, n(q2.x), n(q2.y), n(q.x), n(q.y), pal.Edge, attrs)
	}
	for _, v := range d.order {
		if v.kind == "group" {
			continue
		}
		fmt.Fprintf(&b, `<g class="architecture-%s" data-id="%s">`, v.kind, svgutil.Esc(v.id))
		if v.kind == "junction" {
			fmt.Fprintf(&b, `<circle cx="%s" cy="%s" r="4" fill="%s"/>`, n(v.x+v.w/2), n(v.y+v.h/2), pal.Edge)
		} else {
			if v.icon != "" {
				drawIcon(&b, v.icon, v.x+(v.w-icon)/2, v.y, icon, pal.NodeStroke)
			}
			if v.label != "" {
				fmt.Fprintf(&b, `<text x="%s" y="%s" text-anchor="middle" fill="%s">%s</text>`, n(v.x+v.w/2), n(v.y+icon+o.FontSize*1.2), pal.Text, svgutil.Esc(v.label))
			}
		}
		b.WriteString(`</g>`)
	}
	b.WriteString(`</g></svg>`)
	return []byte(b.String())
}

// All built-in symbols are vector drawings in a shared 24 by 24 coordinate system.
func drawIcon(b *strings.Builder, icon string, x, y, size float64, color string) {
	fmt.Fprintf(b, `<g transform="translate(%s %s) scale(%s)" fill="none" stroke="%s" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round">`, n(x), n(y), n(size/24), color)
	switch icon {
	case "cloud":
		b.WriteString(`<path d="M 6 19 H 18 A 4 4 0 0 0 18.4 11 A 6.5 6.5 0 0 0 5.8 10 A 4.5 4.5 0 0 0 6 19 Z"/>`)
	case "database":
		b.WriteString(`<ellipse cx="12" cy="5" rx="8" ry="3"/><path d="M 4 5 V 19 C 4 23 20 23 20 19 V 5 M 4 12 C 4 16 20 16 20 12"/>`)
	case "disk":
		b.WriteString(`<path d="M 5 3 H 17 L 21 7 V 21 H 3 V 3 Z M 7 3 V 10 H 17 V 3 M 7 21 V 14 H 17 V 21 M 14 5 V 8"/>`)
	case "internet":
		b.WriteString(`<circle cx="12" cy="12" r="9"/><ellipse cx="12" cy="12" rx="4" ry="9"/><path d="M 3 12 H 21 M 5 7 H 19 M 5 17 H 19"/>`)
	case "server":
		b.WriteString(`<rect x="3" y="3" width="18" height="7" rx="1"/><rect x="3" y="14" width="18" height="7" rx="1"/><path d="M 7 6.5 H 7.1 M 7 17.5 H 7.1 M 11 6.5 H 18 M 11 17.5 H 18"/>`)
	}
	b.WriteString(`</g>`)
}
