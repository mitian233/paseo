package c4

import (
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/zkrebbekx/go-mermaid/internal/domain"
	"github.com/zkrebbekx/go-mermaid/internal/layout"
	"github.com/zkrebbekx/go-mermaid/internal/svgutil"
	"github.com/zkrebbekx/go-mermaid/internal/theme"
)

// RenderOptions controls C4 appearance.
type RenderOptions struct {
	Theme    string
	FontFace string
	FontSize float64
	Padding  float64
	Title    string
}

// kindFill maps an element kind to a fill color.
func kindFill(kind string) string {
	switch {
	case strings.HasPrefix(kind, "Person"):
		if strings.Contains(kind, "_Ext") {
			return "#686868"
		}
		return "#08427b"
	case strings.Contains(kind, "_Ext"):
		return "#999999"
	default:
		return "#1168bd"
	}
}

// Render parses and renders C4 source to SVG.
func Render(src string, o RenderOptions) ([]byte, error) {
	d, err := Parse(src)
	if err != nil {
		return nil, err
	}

	if o.Title == "" {
		o.Title = d.Title
	}
	g := &domain.Graph{Direction: domain.TopBottom}
	for _, e := range d.Elements {
		n := &domain.Node{ID: e.ID, Label: e.Label, Shape: domain.ShapeRect}
		n.Size = elementSize(e, svgutil.FaceFor(o.FontFace), o.FontSize)
		g.Nodes = append(g.Nodes, n)
	}
	for _, boundary := range d.Boundaries {
		sg := &domain.Subgraph{ID: boundary.ID, Title: boundary.Label}
		for _, e := range d.Elements {
			if insideBoundary(d, e.Boundary, boundary.ID) {
				sg.NodeIDs = append(sg.NodeIDs, e.ID)
			}
		}
		g.Subgraphs = append(g.Subgraphs, sg)
	}
	for _, r := range d.Rels {
		g.Edges = append(g.Edges, &domain.Edge{From: r.From, To: r.To, Label: r.Label})
	}
	res, err := layout.Compute(g, layout.Options{NodeSep: 60, RankSep: 100, FontSize: o.FontSize, FontFace: o.FontFace})
	if err != nil {
		return nil, err
	}
	if d.Layout.ShapeInRow > 0 || d.Layout.BoundaryInRow > 0 {
		applyRows(d, g, res, o.FontSize)
	}
	return svg(d, g, res, o), nil
}

func svg(d *Diagram, g *domain.Graph, res *layout.Result, o RenderOptions) []byte {
	pal := theme.For(o.Theme)
	pad := o.Padding
	titleH := svgutil.TitleHeight(o.Title, o.FontSize)
	boxes := boundaryBoxes(d, g, o.FontSize)
	minX, minY, maxX, maxY := 0.0, 0.0, res.Width, res.Height
	for _, box := range boxes {
		minX = math.Min(minX, box.x)
		minY = math.Min(minY, box.y)
		maxX = math.Max(maxX, box.x+box.w)
		maxY = math.Max(maxY, box.y+box.h)
	}
	w := maxX - minX + pad*2
	h := maxY - minY + titleH + pad*2

	var b strings.Builder
	fmt.Fprintf(&b, `<svg xmlns="http://www.w3.org/2000/svg" width="%s" height="%s" viewBox="0 0 %s %s" font-family="%s" font-size="%s">`,
		svgutil.Num(w), svgutil.Num(h), svgutil.Num(w), svgutil.Num(h), svgutil.Esc(o.FontFace), svgutil.Num(o.FontSize))
	b.WriteByte('\n')
	fmt.Fprintf(&b, `  <defs><marker id="c4-arrow" viewBox="0 0 10 10" refX="9" refY="5" markerWidth="7" markerHeight="7" orient="auto-start-reverse"><path d="M0,0 L10,5 L0,10 z" fill="%s"/></marker></defs>`, pal.Edge)
	b.WriteByte('\n')
	fmt.Fprintf(&b, `  <rect width="100%%" height="100%%" fill="%s"/>`, pal.Background)
	b.WriteByte('\n')
	if o.Title != "" {
		fmt.Fprintf(&b, `  <text x="%s" y="%s" fill="%s" text-anchor="middle" font-weight="bold">%s</text>`,
			svgutil.Num(w/2), svgutil.Num(pad+o.FontSize), pal.Text, svgutil.Esc(o.Title))
		b.WriteByte('\n')
	}
	fmt.Fprintf(&b, `  <g transform="translate(%s,%s)">`, svgutil.Num(pad-minX), svgutil.Num(pad+titleH-minY))
	b.WriteByte('\n')
	for _, boundary := range d.Boundaries {
		box, ok := boxes[boundary.ID]
		if !ok {
			continue
		}
		fmt.Fprintf(&b, `    <rect x="%s" y="%s" width="%s" height="%s" fill="none" stroke="%s" stroke-dasharray="6,4"/>`+"\n", svgutil.Num(box.x), svgutil.Num(box.y), svgutil.Num(box.w), svgutil.Num(box.h), pal.NodeStroke)
		fmt.Fprintf(&b, `    <text x="%s" y="%s" fill="%s" font-weight="bold">%s</text>`+"\n", svgutil.Num(box.x+10), svgutil.Num(box.y+o.FontSize+5), pal.Text, svgutil.Esc(boundary.Label))
	}

	for i, r := range d.Rels {
		writeRel(&b, r, g.Edges[i], pal, o)
	}
	for _, e := range d.Elements {
		writeElement(&b, e, g.NodeByID(e.ID), o)
	}

	b.WriteString("  </g>\n</svg>\n")
	return []byte(b.String())
}

func writeElement(b *strings.Builder, e *Element, n *domain.Node, o RenderOptions) {
	if n == nil {
		return
	}
	x, y, w, h := n.Pos.X, n.Pos.Y, n.Size.W, n.Size.H
	fill := styleValue(e.Style, "bgColor", kindFill(e.Kind))
	stroke := styleValue(e.Style, "borderColor", fill)
	font := styleValue(e.Style, "fontColor", "#ffffff")
	rx := 4.0
	if strings.HasPrefix(e.Kind, "Person") {
		rx = 16
	}
	fmt.Fprintf(b, `    <rect x="%s" y="%s" width="%s" height="%s" rx="%s" fill="%s" stroke="%s"/>`,
		svgutil.Num(x), svgutil.Num(y), svgutil.Num(w), svgutil.Num(h), svgutil.Num(rx), svgutil.Esc(fill), svgutil.Esc(stroke))
	b.WriteByte('\n')
	fmt.Fprintf(b, `    <text x="%s" y="%s" fill="%s" text-anchor="middle" font-weight="bold">%s</text>`,
		svgutil.Num(x+w/2), svgutil.Num(y+o.FontSize+4), svgutil.Esc(font), svgutil.Esc(e.Label))
	b.WriteByte('\n')
	tag := kindShort(e.Kind)
	if e.Tech != "" {
		tag += ": " + e.Tech
	}
	fmt.Fprintf(b, `    <text x="%s" y="%s" fill="%s" text-anchor="middle" font-size="%s">[%s]</text>`,
		svgutil.Num(x+w/2), svgutil.Num(y+o.FontSize*2), svgutil.Esc(font), svgutil.Num(o.FontSize*0.8), svgutil.Esc(tag))
	b.WriteByte('\n')
	if e.Descr != "" {
		for j, ln := range svgutil.SplitLines(wrapText(e.Descr, 26)) {
			fmt.Fprintf(b, `    <text x="%s" y="%s" fill="%s" text-anchor="middle" font-size="%s">%s</text>`,
				svgutil.Num(x+w/2), svgutil.Num(y+o.FontSize*3+float64(j)*(o.FontSize)), svgutil.Esc(font), svgutil.Num(o.FontSize*0.8), svgutil.Esc(ln))
			b.WriteByte('\n')
		}
	}
}

func writeRel(b *strings.Builder, r *Rel, e *domain.Edge, pal theme.Palette, o RenderOptions) {
	if len(e.Points) < 2 {
		return
	}
	lineColor := styleValue(r.Style, "lineColor", pal.Edge)
	textColor := styleValue(r.Style, "textColor", pal.Text)
	ox, _ := strconv.ParseFloat(r.Style["offsetX"], 64)
	oy, _ := strconv.ParseFloat(r.Style["offsetY"], 64)
	var d strings.Builder
	for i, p := range e.Points {
		cmd := "L"
		if i == 0 {
			cmd = "M"
		}
		fmt.Fprintf(&d, "%s%s,%s ", cmd, svgutil.Num(p.X), svgutil.Num(p.Y))
	}
	fmt.Fprintf(b, `    <path d="%s" fill="none" stroke="%s" stroke-dasharray="4,3" marker-end="url(#c4-arrow)"/>`,
		strings.TrimSpace(d.String()), svgutil.Esc(lineColor))
	b.WriteByte('\n')
	if r.Tech != "" {
		mid := e.LabelPos
		fmt.Fprintf(b, `    <text x="%s" y="%s" fill="%s" text-anchor="middle" font-size="%s">%s</text>`,
			svgutil.Num(mid.X+ox), svgutil.Num(mid.Y+oy+o.FontSize), svgutil.Esc(textColor),
			svgutil.Num(o.FontSize-2), svgutil.Esc("["+r.Tech+"]"))
		b.WriteByte('\n')
	}
	if r.Label != "" {
		mid := e.LabelPos
		fmt.Fprintf(b, `    <text x="%s" y="%s" fill="%s" text-anchor="middle" dy="-2">%s</text>`,
			svgutil.Num(mid.X+ox), svgutil.Num(mid.Y+oy), svgutil.Esc(textColor), svgutil.Esc(r.Label))
		b.WriteByte('\n')
	}
}

func styleValue(style map[string]string, key, fallback string) string {
	if value := style[key]; value != "" {
		return value
	}
	return fallback
}

func insideBoundary(d *Diagram, child, ancestor string) bool {
	for child != "" {
		if child == ancestor {
			return true
		}
		parent := ""
		for _, b := range d.Boundaries {
			if b.ID == child {
				parent = b.Parent
				break
			}
		}
		child = parent
	}
	return false
}

type boundaryBox struct{ x, y, w, h float64 }

func boundaryBoxes(d *Diagram, g *domain.Graph, fontSize float64) map[string]boundaryBox {
	boxes := map[string]boundaryBox{}
	// Children are processed first so their complete boxes fit inside parents.
	for i := len(d.Boundaries) - 1; i >= 0; i-- {
		boundary := d.Boundaries[i]
		minX, minY, maxX, maxY := math.Inf(1), math.Inf(1), math.Inf(-1), math.Inf(-1)
		include := func(x, y, w, h float64) {
			minX = math.Min(minX, x)
			minY = math.Min(minY, y)
			maxX = math.Max(maxX, x+w)
			maxY = math.Max(maxY, y+h)
		}
		for _, e := range d.Elements {
			if e.Boundary == boundary.ID {
				if n := g.NodeByID(e.ID); n != nil {
					include(n.Pos.X, n.Pos.Y, n.Size.W, n.Size.H)
				}
			}
		}
		for _, child := range d.Boundaries {
			if child.Parent == boundary.ID {
				if box, ok := boxes[child.ID]; ok {
					include(box.x, box.y, box.w, box.h)
				}
			}
		}
		if math.IsInf(minX, 1) {
			boxes[boundary.ID] = boundaryBox{float64(i) * 150, 0, 120, fontSize + 43}
			continue
		}
		boxes[boundary.ID] = boundaryBox{minX - 18, minY - fontSize - 25, maxX - minX + 36, maxY - minY + fontSize + 43}
	}
	return boxes
}

// applyRows packs each boundary's direct elements and child boundaries in rows,
// then reroutes relationships against the resulting positions.
func applyRows(d *Diagram, g *domain.Graph, res *layout.Result, fontSize float64) {
	type item struct {
		w, h float64
		move func(float64, float64)
	}
	var pack func(string) item
	pack = func(parent string) item {
		var elements, children []item
		for _, e := range d.Elements {
			if e.Boundary != parent {
				continue
			}
			if n := g.NodeByID(e.ID); n != nil {
				node := n
				elements = append(elements, item{node.Size.W, node.Size.H, func(x, y float64) { node.Pos.X = x; node.Pos.Y = y }})
			}
		}
		for _, b := range d.Boundaries {
			if b.Parent == parent {
				children = append(children, pack(b.ID))
			}
		}
		type positioned struct {
			item item
			x, y float64
		}
		var positionedItems []positioned
		y, width := 0.0, 0.0
		place := func(items []item, columns int) {
			if columns <= 0 {
				columns = 1
			}
			for i := 0; i < len(items); i += columns {
				end := i + columns
				if end > len(items) {
					end = len(items)
				}
				x, height := 0.0, 0.0
				for _, it := range items[i:end] {
					positionedItems = append(positionedItems, positioned{it, x, y})
					x += it.w + 60
					height = math.Max(height, it.h)
				}
				width = math.Max(width, x-60)
				y += height + 60
			}
		}
		place(elements, d.Layout.ShapeInRow)
		place(children, d.Layout.BoundaryInRow)
		if len(positionedItems) == 0 {
			return item{120, fontSize + 40, func(float64, float64) {}}
		}
		height := y - 60
		insetX, insetY := 0.0, 0.0
		if parent != "" {
			insetX = 18
			insetY = fontSize + 25
			width += 36
			height += fontSize + 43
		}
		return item{width, height, func(x, y float64) {
			for _, p := range positionedItems {
				p.item.move(x+insetX+p.x, y+insetY+p.y)
			}
		}}
	}
	root := pack("")
	root.move(0, 0)
	res.Width, res.Height = root.w, root.h
	for _, edge := range g.Edges {
		a, b := g.NodeByID(edge.From), g.NodeByID(edge.To)
		if a == nil || b == nil || a == b {
			continue
		}
		ac, bc := a.Center(), b.Center()
		dx, dy := bc.X-ac.X, bc.Y-ac.Y
		if dx == 0 && dy == 0 {
			continue
		}
		factor := func(n *domain.Node) float64 {
			x, y := math.Inf(1), math.Inf(1)
			if dx != 0 {
				x = n.Size.W / 2 / math.Abs(dx)
			}
			if dy != 0 {
				y = n.Size.H / 2 / math.Abs(dy)
			}
			return math.Min(x, y)
		}
		at, bt := factor(a), factor(b)
		edge.Points = []domain.Point{{X: ac.X + dx*at, Y: ac.Y + dy*at}, {X: bc.X - dx*bt, Y: bc.Y - dy*bt}}
		edge.LabelPos = domain.PolylineMidpoint(edge.Points)
	}
}

func elementSize(e *Element, face svgutil.Face, fontSize float64) domain.Size {
	maxW := face.Width(e.Label, fontSize)
	if l := len([]rune(e.Descr)); l < 28 {
		if wd := face.Width(e.Descr, fontSize); wd > maxW {
			maxW = wd
		}
	}
	w := maxW + 24
	if w < 120 {
		w = 120
	}
	if w > 200 {
		w = 200
	}
	h := fontSize*3 + 8
	if e.Descr != "" {
		lines := len(svgutil.SplitLines(wrapText(e.Descr, 26)))
		h += float64(lines) * fontSize
	}
	return domain.Size{W: w, H: h}
}

// kindShort returns a short label for the element kind tag.
func kindShort(kind string) string {
	switch {
	case strings.HasPrefix(kind, "Person"):
		return "Person"
	case strings.HasPrefix(kind, "System"):
		return "System"
	case strings.HasPrefix(kind, "Container"):
		return "Container"
	case strings.HasPrefix(kind, "Component"):
		return "Component"
	}
	return kind
}

// wrapText inserts <br> roughly every width runes at word boundaries.
func wrapText(s string, width int) string {
	words := strings.Fields(s)
	var lines []string
	var cur strings.Builder
	for _, w := range words {
		if cur.Len() > 0 && cur.Len()+1+len(w) > width {
			lines = append(lines, cur.String())
			cur.Reset()
		}
		if cur.Len() > 0 {
			cur.WriteByte(' ')
		}
		cur.WriteString(w)
	}
	if cur.Len() > 0 {
		lines = append(lines, cur.String())
	}
	return strings.Join(lines, "<br>")
}
