package block

import (
	"fmt"
	"math"
	"strings"

	"github.com/zkrebbekx/go-mermaid/internal/svgutil"
	"github.com/zkrebbekx/go-mermaid/internal/theme"
)

// RenderOptions controls block diagram appearance.
type RenderOptions struct {
	Theme    string
	FontFace string
	FontSize float64
	Padding  float64
	Title    string
}

const (
	cellW = 110.0
	rowH  = 48.0
	gap   = 8.0
)

// Render parses and renders block-beta source to SVG.
func Render(src string, o RenderOptions) ([]byte, error) {
	d, err := Parse(src)
	if err != nil {
		return nil, err
	}
	return svg(d, o), nil
}

func svg(d *Diagram, o RenderOptions) []byte {
	pal := theme.For(o.Theme)
	pad := o.Padding
	titleH := svgutil.TitleHeight(o.Title, o.FontSize)

	gridW := float64(d.Columns) * cellW
	w := gridW + pad*2
	top := pad + titleH
	h := top + float64(len(d.Rows))*rowH + pad

	var b strings.Builder
	fmt.Fprintf(&b, `<svg xmlns="http://www.w3.org/2000/svg" width="%s" height="%s" viewBox="0 0 %s %s" font-family="%s" font-size="%s">`,
		svgutil.Num(w), svgutil.Num(h), svgutil.Num(w), svgutil.Num(h), svgutil.Esc(o.FontFace), svgutil.Num(o.FontSize))
	b.WriteByte('\n')
	if len(d.Links) > 0 {
		fmt.Fprintf(&b, `  <defs><marker id="block-arrow" viewBox="0 0 10 10" refX="9" refY="5" markerWidth="7" markerHeight="7" orient="auto-start-reverse"><path d="M0,0 L10,5 L0,10 z" fill="%s"/></marker></defs>`, pal.Edge)
		b.WriteByte('\n')
	}
	fmt.Fprintf(&b, `  <rect width="100%%" height="100%%" fill="%s"/>`, pal.Background)
	b.WriteByte('\n')
	if o.Title != "" {
		fmt.Fprintf(&b, `  <text x="%s" y="%s" fill="%s" text-anchor="middle" font-weight="bold">%s</text>`,
			svgutil.Num(w/2), svgutil.Num(pad+o.FontSize), pal.Text, svgutil.Esc(o.Title))
		b.WriteByte('\n')
	}
	type bounds struct{ x, y, w, h float64 }
	positions := map[string]bounds{}
	for ri, row := range d.Rows {
		col := 0
		for _, blk := range row {
			if !blk.Space {
				positions[blk.ID] = bounds{pad + float64(col)*cellW, top + float64(ri)*rowH, float64(blk.Span)*cellW - gap, rowH - gap}
			}
			col += blk.Span
		}
	}
	for _, link := range d.Links {
		a, okA := positions[link.From]
		z, okZ := positions[link.To]
		if !okA || !okZ {
			continue
		}
		ax, ay := a.x+a.w/2, a.y+a.h/2
		zx, zy := z.x+z.w/2, z.y+z.h/2
		dx, dy := zx-ax, zy-ay
		if dx == 0 && dy == 0 {
			continue
		}
		factor := func(w, h float64) float64 {
			tx, ty := math.Inf(1), math.Inf(1)
			if dx != 0 {
				tx = w / 2 / math.Abs(dx)
			}
			if dy != 0 {
				ty = h / 2 / math.Abs(dy)
			}
			return math.Min(tx, ty)
		}
		ta, tz := factor(a.w, a.h), factor(z.w, z.h)
		x1, y1, x2, y2 := ax+dx*ta, ay+dy*ta, zx-dx*tz, zy-dy*tz
		attrs := ""
		if strings.HasPrefix(link.Connector, "<") {
			attrs += ` marker-start="url(#block-arrow)"`
		}
		if strings.HasSuffix(link.Connector, ">") {
			attrs += ` marker-end="url(#block-arrow)"`
		}
		if strings.HasPrefix(link.Connector, "-.") {
			attrs += ` stroke-dasharray="4,3"`
		}
		if link.Connector != "~~~" {
			fmt.Fprintf(&b, `  <path d="M%s,%s L%s,%s" fill="none" stroke="%s"%s/>`+"\n", svgutil.Num(x1), svgutil.Num(y1), svgutil.Num(x2), svgutil.Num(y2), pal.Edge, attrs)
		}
		if link.Label != "" {
			fmt.Fprintf(&b, `  <text x="%s" y="%s" text-anchor="middle" fill="%s">%s</text>`+"\n", svgutil.Num((x1+x2)/2), svgutil.Num((y1+y2)/2-4), pal.Text, svgutil.Esc(link.Label))
		}
	}

	for ri, row := range d.Rows {
		y := top + float64(ri)*rowH
		col := 0
		for _, blk := range row {
			span := blk.Span
			if col+span > d.Columns {
				span = d.Columns - col
				if span < 1 {
					span = 1
				}
			}
			if blk.Space {
				// A gap in the grid: advance the column, draw nothing.
				col += span
				continue
			}
			x := pad + float64(col)*cellW
			bw := float64(span)*cellW - gap
			fmt.Fprintf(&b, `  <rect x="%s" y="%s" width="%s" height="%s" rx="4" fill="%s" stroke="%s"/>`,
				svgutil.Num(x), svgutil.Num(y), svgutil.Num(bw), svgutil.Num(rowH-gap), pal.NodeFill, pal.NodeStroke)
			b.WriteByte('\n')
			fmt.Fprintf(&b, `  <text x="%s" y="%s" fill="%s" text-anchor="middle">%s</text>`,
				svgutil.Num(x+bw/2), svgutil.Num(y+(rowH-gap)/2+o.FontSize*0.35), pal.Text, svgutil.Esc(blk.Label))
			b.WriteByte('\n')
			col += blk.Span
		}
	}

	b.WriteString("</svg>\n")
	return []byte(b.String())
}
