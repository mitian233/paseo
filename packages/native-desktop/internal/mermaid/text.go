// Adapted from go-mermaid raster/text.go; see UPSTREAM-LICENSE.
package mermaid

import (
	"encoding/xml"
	"image"
	"image/color"
	"io"
	"regexp"
	"strconv"
	"strings"

	"golang.org/x/image/colornames"
	xdraw "golang.org/x/image/draw"
	"golang.org/x/image/font"
	"golang.org/x/image/font/opentype"
	"golang.org/x/image/math/f64"
	"golang.org/x/image/math/fixed"
)

// These helpers are also used by the existing arrow and glyph scanners.
var (
	textRe  = regexp.MustCompile(`(?s)<text\b([^>]*)>(.*?)</text>`)
	tspanRe = regexp.MustCompile(`(?s)<tspan\b([^>]*)>(.*?)</tspan>`)
	attrRe  = regexp.MustCompile(`([\w-]+)="([^"]*)"`)
	groupRe = regexp.MustCompile(`<g transform="translate\(([0-9.-]+),([0-9.-]+)\)">`)
)

func parseAttrs(s string) map[string]string {
	m := map[string]string{}
	for _, a := range attrRe.FindAllStringSubmatch(s, -1) {
		m[a[1]] = a[2]
	}
	return m
}

// drawText is retained for callers with an SVG rather than a parsed scene.
func (rf rasterFont) drawText(img *image.RGBA, svg string, scale, rootSize float64) error {
	scene, err := scanSVGScene(svg)
	if err != nil {
		return err
	}
	return rf.drawTextElements(img, scene.Texts, scale, rootSize)
}

type textStyle struct {
	fill, size, weight, anchor, baseline string
}

func textStyleAttrs(style textStyle, attrs map[string]string) textStyle {
	if v, ok := attrs["fill"]; ok && v != "" {
		style.fill = v
	}
	if v, ok := attrs["font-size"]; ok && v != "" {
		style.size = v
	}
	if v, ok := attrs["font-weight"]; ok && v != "" {
		style.weight = v
	}
	if v, ok := attrs["text-anchor"]; ok && v != "" {
		style.anchor = v
	}
	if v, ok := attrs["dominant-baseline"]; ok && v != "" {
		style.baseline = v
	}
	return style
}

// Each element already has the complete transform accumulated by the scene.
func (rf rasterFont) drawTextElements(img *image.RGBA, elements []svgElement, scale, rootSize float64) error {
	for _, element := range elements {
		attrs := element.Attrs
		style := textStyleAttrs(textStyle{}, attrs)
		x := numAttr(attrs["x"], "0") + numAttr(attrs["dx"], "0")
		y := numAttr(attrs["y"], "0") + numAttr(attrs["dy"], "0")
		styles := []textStyle{style}
		decoder := xml.NewDecoder(strings.NewReader("<root>" + element.Inner + "</root>"))
		for {
			token, err := decoder.Token()
			if err == io.EOF {
				break
			}
			if err != nil {
				return err
			}
			switch t := token.(type) {
			case xml.StartElement:
				if t.Name.Local != "tspan" {
					continue
				}
				span := make(map[string]string, len(t.Attr))
				for _, a := range t.Attr {
					span[a.Name.Local] = a.Value
				}
				styles = append(styles, textStyleAttrs(styles[len(styles)-1], span))
				if v, ok := span["x"]; ok {
					x = numAttr(v, "0")
				}
				if v, ok := span["y"]; ok {
					y = numAttr(v, "0")
				}
				x += numAttr(span["dx"], "0")
				y += numAttr(span["dy"], "0")
			case xml.EndElement:
				if t.Name.Local == "tspan" && len(styles) > 1 {
					styles = styles[:len(styles)-1]
				}
			case xml.CharData:
				if strings.TrimSpace(string(t)) == "" {
					continue
				}
				s := styles[len(styles)-1]
				advance, err := rf.drawTextRun(img, string(t), x, y, sizeOf(s.size, rootSize), scale, colorOf(s.fill), s.weight == "bold", s.anchor, s.baseline, element.Matrix)
				if err != nil {
					return err
				}
				x += advance
			}
		}
	}
	return nil
}

// buildFace returns a font face sized to px and the advance width of s, shrunk
// to the width the layout reserved (svgutil.TextWidth) so glyphs stay in place.
func (rf rasterFont) buildFace(s string, size, scale float64, bold bool) (font.Face, float64) {
	fnt := rf.font
	if bold && rf.bold != nil {
		fnt = rf.bold
	}
	if fnt == nil {
		return nil, 0
	}
	px := size * scale
	face, err := opentype.NewFace(fnt, &opentype.FaceOptions{Size: px, DPI: 72})
	if err != nil {
		return nil, 0
	}
	adv := float64(font.MeasureString(face, s)) / 64
	if reserved := TextWidth(s, size) * scale; reserved > 0 && adv > reserved {
		_ = face.Close()
		px *= reserved / adv
		face, err = opentype.NewFace(fnt, &opentype.FaceOptions{Size: px, DPI: 72})
		if err != nil {
			return nil, 0
		}
		adv = float64(font.MeasureString(face, s)) / 64
	}
	return face, adv
}

// drawTextRun rasterizes in local coordinates before applying the scene matrix.
// A translated or uniformly scaled label can go straight to the destination.
func (rf rasterFont) drawTextRun(img *image.RGBA, s string, x, y, size, scale float64, fill color.Color, bold bool, anchor, baseline string, matrix f64.Aff3) (float64, error) {
	if scale <= 0 {
		return 0, nil
	}
	direct := matrix[1] == 0 && matrix[3] == 0 && matrix[0] == matrix[4] && matrix[0] > 0
	faceScale := scale
	if direct {
		faceScale *= matrix[0]
	}
	face, adv := rf.buildFace(s, size, faceScale, bold)
	if face == nil {
		return 0, nil
	}
	defer func() { _ = face.Close() }()
	localAdvance := adv / faceScale
	startX := x * faceScale
	switch anchor {
	case "middle":
		startX -= adv / 2
	case "end":
		startX -= adv
	}
	baseY := y * faceScale
	metrics := face.Metrics()
	switch baseline {
	case "middle", "central":
		baseY += float64(metrics.Ascent-metrics.Descent) / 128
	case "hanging", "text-before-edge":
		baseY += float64(metrics.Ascent) / 64
	case "text-after-edge":
		baseY -= float64(metrics.Descent) / 64
	}
	d := &font.Drawer{Src: image.NewUniform(fill), Face: face}
	if direct {
		d.Dst = img
		d.Dot = fixed.Point26_6{X: fixed.Int26_6((startX + matrix[2]*scale) * 64), Y: fixed.Int26_6((baseY + matrix[5]*scale) * 64)}
		d.DrawString(s)
		return localAdvance, nil
	}

	bound, _ := font.BoundString(face, s)
	left, top := bound.Min.X.Floor()-1, bound.Min.Y.Floor()-1
	right, bottom := bound.Max.X.Ceil()+1, bound.Max.Y.Ceil()+1
	w, h := right-left, bottom-top
	if w <= 0 || h <= 0 {
		return localAdvance, nil
	}
	if w > 4*1024*1024 || h > 4*1024*1024 || int64(w)*int64(h) > 4*1024*1024 {
		return 0, ErrTooComplex
	}
	tmp := image.NewRGBA(image.Rect(0, 0, w, h))
	d.Dst = tmp
	d.Dot = fixed.Point26_6{X: fixed.I(-left), Y: fixed.I(-top)}
	d.DrawString(s)
	ox, oy := startX+float64(left), baseY+float64(top)
	transform := f64.Aff3{
		matrix[0], matrix[1], matrix[0]*ox + matrix[1]*oy + matrix[2]*scale,
		matrix[3], matrix[4], matrix[3]*ox + matrix[4]*oy + matrix[5]*scale,
	}
	xdraw.BiLinear.Transform(img, transform, tmp, tmp.Bounds(), xdraw.Over, nil)
	return localAdvance, nil
}

func numAttr(v, fallback string) float64 {
	if v == "" {
		v = fallback
	}
	f, _ := strconv.ParseFloat(v, 64)
	return f
}

func sizeOf(v string, root float64) float64 {
	if v == "" {
		if root > 0 {
			return root
		}
		return 14
	}
	f, _ := strconv.ParseFloat(v, 64)
	if f <= 0 {
		return 14
	}
	return f
}

// colorOf parses SVG text colors, defaulting unknown colors to black.
func colorOf(s string) color.Color {
	s = strings.ToLower(strings.TrimSpace(s))
	if s == "none" || s == "transparent" {
		return color.NRGBA{}
	}
	if s == "rebeccapurple" {
		return color.NRGBA{R: 102, G: 51, B: 153, A: 255}
	}
	if c, ok := colornames.Map[s]; ok {
		return c
	}
	if !strings.HasPrefix(s, "#") {
		return color.Black
	}
	hex := s[1:]
	if len(hex) == 3 {
		hex = string([]byte{hex[0], hex[0], hex[1], hex[1], hex[2], hex[2]})
	}
	if len(hex) != 6 {
		return color.Black
	}
	v, err := strconv.ParseUint(hex, 16, 32)
	if err != nil {
		return color.Black
	}
	return color.RGBA{R: byte(v >> 16), G: byte(v >> 8), B: byte(v), A: 255}
}
