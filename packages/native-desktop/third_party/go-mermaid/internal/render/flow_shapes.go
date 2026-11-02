package render

import (
	"fmt"
	"github.com/zkrebbekx/go-mermaid/internal/domain"
	"strings"
)

// writeModernShape uses normalized coordinates for shapes with distinctive
// outlines. Labels are drawn by writeNode after the outline.
func writeModernShape(b *strings.Builder, shape domain.Shape, x, y, w, h float64, fill, stroke string) bool {
	switch shape {
	case "hourglass", "tri", "flip-tri", "sl-rect", "notch-rect", "notch-pent", "bolt", "bang", "bucket", "odd":
		pts := domain.ShapeOutline(shape, w, h)
		var p strings.Builder
		for _, v := range pts {
			fmt.Fprintf(&p, "%s,%s ", num(x+v.X*w), num(y+v.Y*h))
		}
		fmt.Fprintf(b, `    <polygon points="%s" fill="%s" stroke="%s"/>`, strings.TrimSpace(p.String()), fill, stroke)
		return true
	case "f-circ":
		fmt.Fprintf(b, `    <circle cx="%s" cy="%s" r="%s" fill="%s" stroke="%s"/>`, num(x+w/2), num(y+h/2), num(w/2), stroke, stroke)
		return true
	case "fork":
		fmt.Fprintf(b, `    <rect x="%s" y="%s" width="%s" height="%s" fill="%s" stroke="%s"/>`, num(x), num(y+h*.42), num(w), num(h*.16), stroke, stroke)
		return true
	case "text":
		return true
	}
	// The viewBox coordinate system permits curves and decorative lines to scale
	// with the node's label-driven dimensions.
	path, extra, solid := "", "", false
	switch shape {
	case "doc":
		path = "M0 0 H100 V85 Q75 100 50 90 T0 90 Z"
	case "lin-doc":
		path = "M0 0 H100 V85 Q75 100 50 90 T0 90 Z"
		extra = "M15 25 H85 M15 40 H85 M15 55 H75"
	case "tag-doc":
		path = "M0 0 H100 V85 Q75 100 50 90 T0 90 Z"
		extra = "M8 0 V90"
	case "docs":
		path = "M0 12 H88 V85 Q66 98 44 89 T0 89 Z"
		extra = "M8 12 V6 H94 V80 M14 6 V0 H100 V74"
	case "bow-rect":
		path = "M0 0 H100 Q80 50 100 100 H0 Q20 50 0 0 Z"
	case "flag":
		path = "M0 10 Q25 -5 50 10 T100 10 V90 Q75 75 50 90 T0 90 Z"
	case "delay":
		path = "M0 0 H50 A50 50 0 0 1 50 100 H0 Z"
	case "brace":
		path = "M15 0 C0 0 10 35 0 50 C10 65 0 100 15 100"
		fill = "none"
	case "brace-r":
		path = "M85 0 C100 0 90 35 100 50 C90 65 100 100 85 100"
		fill = "none"
	case "braces":
		path = "M15 0 C0 0 10 35 0 50 C10 65 0 100 15 100 M85 0 C100 0 90 35 100 50 C90 65 100 100 85 100"
		fill = "none"
	case "sm-circ":
		path = "M50 0 A50 50 0 1 1 49.99 0 Z"
	case "dbl-circ", "fr-circ":
		path = "M50 0 A50 50 0 1 1 49.99 0 Z"
		extra = "M50 8 A42 42 0 1 1 49.99 8 Z"
	case "cross-circ":
		path = "M50 0 A50 50 0 1 1 49.99 0 Z"
		extra = "M15 15 L85 85 M85 15 L15 85"
	case "div-rect":
		path = "M0 0 H100 V100 H0 Z"
		extra = "M0 25 H100"
	case "lin-rect":
		path = "M0 0 H100 V100 H0 Z"
		extra = "M10 0 V100 M90 0 V100"
	case "tag-rect":
		path = "M0 0 H100 V100 H0 Z"
		extra = "M10 0 V100"
	case "win-pane":
		path = "M0 0 H100 V100 H0 Z"
		extra = "M0 25 H100 M20 0 V100"
	case "st-rect":
		path = "M0 12 H88 V100 H0 Z"
		extra = "M8 12 V6 H94 V94 M14 6 V0 H100 V88"
	case "browser":
		path = "M0 0 H100 V100 H0 Z"
		extra = "M0 22 H100 M6 11 H10 M16 11 H20 M26 11 H30"
	case "console":
		path = "M0 0 H100 V100 H0 Z"
		extra = "M10 25 L25 40 L10 55 M32 58 H60"
	case "folder":
		path = "M0 0 H35 L45 15 H100 V100 H0 Z"
	case "datastore":
		path = "M0 0 H100 V100 H0"
		fill = "none"
	case "h-cyl":
		path = "M12 0 H88 A12 50 0 0 1 88 100 H12 A12 50 0 0 1 12 0 Z"
		extra = "M12 0 A12 50 0 0 1 12 100"
	case "lin-cyl":
		path = "M0 12 A50 12 0 0 1 100 12 V88 A50 12 0 0 1 0 88 Z"
		extra = "M0 12 A50 12 0 0 0 100 12 M0 20 A50 12 0 0 0 100 20"
	case "curv-trap":
		path = "M0 50 L20 0 H85 Q115 50 85 100 H20 Z"
	case "cloud":
		path = "M15 75 C-5 75 -5 45 15 42 C10 20 30 10 42 25 C52 0 78 10 80 28 C105 25 110 55 92 62 C100 82 75 90 62 82 C45 100 27 88 25 80 Z"
	case "person":
		path = "M50 0 A18 18 0 1 1 49.99 0 M20 100 V70 Q20 40 50 40 Q80 40 80 70 V100 Z"
	default:
		return false
	}
	if solid {
		fill = stroke
	}
	fmt.Fprintf(b, `    <g transform="translate(%s,%s) scale(%s,%s)"><path d="%s" fill="%s" stroke="%s" vector-effect="non-scaling-stroke"/>`, num(x), num(y), num(w/100), num(h/100), path, fill, stroke)
	if extra != "" {
		fmt.Fprintf(b, `<path d="%s" fill="none" stroke="%s" vector-effect="non-scaling-stroke"/>`, extra, stroke)
	}
	b.WriteString("</g>")
	return true
}
