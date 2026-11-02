package mermaid

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/xml"
	"errors"
	"fmt"
	"image"
	"image/color"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	xdraw "golang.org/x/image/draw"
	"golang.org/x/image/math/f64"
	_ "golang.org/x/image/webp"
)

const (
	maxImageBytes       = 8 << 20
	maxImageTotalBytes  = 32 << 20
	maxImagePixels      = 4 << 20
	maxImageTotalPixels = 16 << 20
	maxImageDimension   = 4096
	maxImageDepth       = 8
)

type loadedSVGImage struct {
	image         image.Image
	width, height float64
}
type svgImageLoader struct {
	ctx           context.Context
	font          rasterFont
	client        *http.Client
	cache         map[string]loadedSVGImage
	active        map[string]bool
	bytes, pixels int64
	depth         int
}

func newSVGImageLoader(ctx context.Context, font rasterFont) *svgImageLoader {
	return &svgImageLoader{ctx: ctx, font: font, client: &http.Client{Timeout: 15 * time.Second}, cache: make(map[string]loadedSVGImage), active: make(map[string]bool)}
}
func (l *svgImageLoader) Resolve(source string) (float64, float64, error) {
	img, err := l.load(source)
	if err != nil {
		return 0, 0, err
	}
	return img.width, img.height, nil
}
func (l *svgImageLoader) load(source string) (loadedSVGImage, error) {
	var empty loadedSVGImage
	if err := l.ctx.Err(); err != nil {
		return empty, err
	}
	if item, ok := l.cache[source]; ok {
		return item, nil
	}
	if l.active[source] || l.depth >= maxImageDepth {
		return empty, errors.New("mermaid: recursive SVG image")
	}
	l.active[source] = true
	l.depth++
	defer func() { delete(l.active, source); l.depth-- }()
	data, err := l.fetch(source)
	if err != nil {
		return empty, err
	}
	cfg, format, decodeErr := image.DecodeConfig(bytes.NewReader(data))
	var width, height float64
	var svgSource []byte
	if decodeErr == nil {
		if format != "png" && format != "jpeg" && format != "gif" && format != "webp" {
			return empty, errors.New("mermaid: unsupported image format")
		}
		width, height = float64(cfg.Width), float64(cfg.Height)
		if err = l.reservePixels(width, height); err != nil {
			return empty, err
		}
	} else {
		var normalized []byte
		normalized, width, height, err = l.inspectImageSVG(data)
		if err != nil {
			return empty, fmt.Errorf("mermaid: invalid image: %w", err)
		}
		svgSource = normalized
		if err = l.reservePixels(width, height); err != nil {
			return empty, err
		}
		data, err = rasterizeWithImages(l.ctx, normalized, 1, l.font, l)
		if err != nil {
			return empty, fmt.Errorf("mermaid: rasterize SVG image: %w", err)
		}
		cfg, _, err = image.DecodeConfig(bytes.NewReader(data))
		if err != nil {
			return empty, err
		}
		if err = validImageDimensions(float64(cfg.Width), float64(cfg.Height)); err != nil {
			return empty, err
		}
	}
	if err = l.ctx.Err(); err != nil {
		return empty, err
	}
	decoded, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return empty, fmt.Errorf("mermaid: decode image: %w", err)
	}
	if decoded.Bounds().Dx() != cfg.Width || decoded.Bounds().Dy() != cfg.Height {
		return empty, errors.New("mermaid: image dimensions changed during decode")
	}
	if svgSource != nil && math.Abs(width/height-float64(cfg.Width)/float64(cfg.Height)) > 1e-9 {
		aspect := ""
		dec := xml.NewDecoder(bytes.NewReader(svgSource))
		for {
			tok, e := dec.Token()
			if e != nil {
				return empty, e
			}
			if root, ok := tok.(xml.StartElement); ok {
				for _, a := range root.Attr {
					if a.Name.Local == "preserveAspectRatio" {
						aspect = a.Value
					}
				}
				break
			}
		}
		viewport := image.NewRGBA(image.Rect(0, 0, int(math.Ceil(width)), int(math.Ceil(height))))
		key := fmt.Sprintf("\x00svg-viewport-%d", l.depth)
		l.cache[key] = loadedSVGImage{decoded, float64(cfg.Width), float64(cfg.Height)}
		attrs := map[string]string{"href": key, "width": strconv.FormatFloat(width, 'g', -1, 64), "height": strconv.FormatFloat(height, 'g', -1, 64), "preserveAspectRatio": aspect}
		err = l.Draw(viewport, []svgElement{{Attrs: attrs, Matrix: svgIdentity}}, 1)
		delete(l.cache, key)
		if err != nil {
			return empty, err
		}
		decoded = viewport
	}
	item := loadedSVGImage{decoded, width, height}
	l.cache[source] = item
	return item, nil
}
func validImageDimensions(w, h float64) error {
	if math.IsNaN(w) || math.IsNaN(h) || math.IsInf(w, 0) || math.IsInf(h, 0) || w <= 0 || h <= 0 || w > maxImageDimension || h > maxImageDimension || math.Ceil(w)*math.Ceil(h) > maxImagePixels {
		return ErrTooComplex
	}
	return nil
}
func (l *svgImageLoader) reservePixels(w, h float64) error {
	if err := validImageDimensions(w, h); err != nil {
		return err
	}
	n := int64(math.Ceil(w)) * int64(math.Ceil(h))
	if n > maxImageTotalPixels-l.pixels {
		return ErrTooComplex
	}
	l.pixels += n
	return nil
}
func (l *svgImageLoader) fetch(source string) ([]byte, error) {
	if len(source) > maxImageBytes*2 {
		return nil, ErrTooComplex
	}
	var data []byte
	var err error
	if strings.HasPrefix(strings.ToLower(source), "data:") {
		pos := strings.IndexByte(source, ',')
		if pos < 0 {
			return nil, errors.New("mermaid: invalid data URL")
		}
		meta, body := source[5:pos], source[pos+1:]
		if strings.HasSuffix(strings.ToLower(meta), ";base64") {
			body, err = url.PathUnescape(body)
			if err == nil {
				if base64.StdEncoding.DecodedLen(len(body)) > maxImageBytes+2 {
					return nil, ErrTooComplex
				}
				data, err = base64.StdEncoding.DecodeString(body)
			}
		} else {
			var s string
			s, err = url.PathUnescape(body)
			data = []byte(s)
		}
	} else {
		u, e := url.Parse(source)
		if e != nil || u == nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			return nil, errors.New("mermaid: unsupported image URL")
		}
		req, e := http.NewRequestWithContext(l.ctx, http.MethodGet, source, nil)
		if e != nil {
			return nil, e
		}
		resp, e := l.client.Do(req)
		if e != nil {
			return nil, e
		}
		defer resp.Body.Close()
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			return nil, fmt.Errorf("mermaid: image HTTP status %d", resp.StatusCode)
		}
		if resp.ContentLength > maxImageBytes {
			return nil, ErrTooComplex
		}
		data, err = io.ReadAll(io.LimitReader(resp.Body, maxImageBytes+1))
	}
	if err != nil {
		return nil, fmt.Errorf("mermaid: reading image: %w", err)
	}
	if len(data) > maxImageBytes || int64(len(data)) > maxImageTotalBytes-l.bytes {
		return nil, ErrTooComplex
	}
	l.bytes += int64(len(data))
	return data, nil
}

// inspectImageSVG validates the complete XML before the rasterizer sees it.
func (l *svgImageLoader) inspectImageSVG(data []byte) ([]byte, float64, float64, error) {
	dec := xml.NewDecoder(bytes.NewReader(data))
	depth, count := 0, 0
	var width, height, vw, vh float64
	var rootEnd int64
	hasViewBox := false
	seen := false
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, 0, 0, err
		}
		count++
		if count > 100000 {
			return nil, 0, 0, ErrTooComplex
		}
		switch t := tok.(type) {
		case xml.StartElement:
			depth++
			if depth > 128 {
				return nil, 0, 0, ErrTooComplex
			}
			if seen && depth == 1 {
				return nil, 0, 0, errors.New("multiple SVG roots")
			}
			if !seen {
				seen = true
				rootEnd = dec.InputOffset()
				if t.Name.Local != "svg" {
					return nil, 0, 0, errors.New("invalid SVG root")
				}
				for _, a := range t.Attr {
					switch a.Name.Local {
					case "width":
						width, err = imageLength(a.Value)
					case "height":
						height, err = imageLength(a.Value)
					case "viewBox":
						var v []float64
						v, err = svgNumbers(a.Value)
						if err == nil && len(v) == 4 {
							vw, vh = v[2], v[3]
							hasViewBox = true
						} else {
							err = errors.New("invalid SVG viewBox")
						}
					}
					if err != nil {
						return nil, 0, 0, err
					}
				}
			}
		case xml.EndElement:
			depth--
		case xml.Directive:
			return nil, 0, 0, errors.New("SVG directives are unsupported")
		}
	}
	if !seen || depth != 0 {
		return nil, 0, 0, errors.New("invalid SVG document")
	}
	if hasViewBox {
		if err := l.reservePixels(vw, vh); err != nil {
			return nil, 0, 0, err
		}
		if width == 0 && height == 0 {
			width, height = vw, vh
		} else if width == 0 {
			width = height * vw / vh
		} else if height == 0 {
			height = width * vh / vw
		}
	} else if width > 0 && height > 0 {
		// Supply a viewBox for the shared rasterizer without changing the content.
		end := int(rootEnd)
		start := bytes.LastIndexByte(data[:end], '>')
		if start < 0 {
			return nil, 0, 0, errors.New("invalid SVG root")
		}
		if start > 0 && data[start-1] == '/' {
			start--
		}
		insert := fmt.Sprintf(` viewBox="0 0 %g %g"`, width, height)
		normalized := make([]byte, 0, len(data)+len(insert))
		normalized = append(normalized, data[:start]...)
		normalized = append(normalized, insert...)
		normalized = append(normalized, data[start:]...)
		data = normalized
	} else {
		return nil, 0, 0, errors.New("SVG image needs dimensions or viewBox")
	}
	if err := validImageDimensions(width, height); err != nil {
		return nil, 0, 0, err
	}
	return data, width, height, nil
}
func imageLength(s string) (float64, error) {
	s = strings.TrimSpace(s)
	if strings.HasSuffix(s, "px") {
		s = strings.TrimSpace(strings.TrimSuffix(s, "px"))
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil || math.IsNaN(v) || math.IsInf(v, 0) || v <= 0 {
		return 0, errors.New("invalid SVG image length")
	}
	return v, nil
}
func imageAttr(a map[string]string, key string, def float64) (float64, error) {
	if a[key] == "" {
		return def, nil
	}
	v, err := strconv.ParseFloat(strings.TrimSuffix(strings.TrimSpace(a[key]), "px"), 64)
	if err != nil || math.IsNaN(v) || math.IsInf(v, 0) {
		return 0, fmt.Errorf("invalid image %s", key)
	}
	return v, nil
}

type imageViewportMask struct {
	bounds     image.Rectangle
	inverse    f64.Aff3
	x, y, w, h float64
}

func (m imageViewportMask) ColorModel() color.Model { return color.AlphaModel }
func (m imageViewportMask) Bounds() image.Rectangle { return m.bounds }
func (m imageViewportMask) At(x, y int) color.Color {
	u, v := svgPoint(m.inverse, float64(x)+0.5, float64(y)+0.5)
	if u >= m.x && u < m.x+m.w && v >= m.y && v < m.y+m.h {
		return color.Alpha{255}
	}
	return color.Alpha{0}
}
func (l *svgImageLoader) Draw(dst *image.RGBA, elements []svgElement, scale float64) error {
	if scale <= 0 || math.IsNaN(scale) || math.IsInf(scale, 0) {
		return errors.New("mermaid: invalid image scale")
	}
	for _, e := range elements {
		if err := l.ctx.Err(); err != nil {
			return err
		}
		source := e.Attrs["href"]
		if source == "" {
			return errors.New("mermaid: image missing href")
		}
		item, err := l.load(source)
		if err != nil {
			return err
		}
		x, err := imageAttr(e.Attrs, "x", 0)
		if err != nil {
			return err
		}
		y, err := imageAttr(e.Attrs, "y", 0)
		if err != nil {
			return err
		}
		w, err := imageAttr(e.Attrs, "width", 0)
		if err != nil {
			return err
		}
		h, err := imageAttr(e.Attrs, "height", 0)
		if err != nil {
			return err
		}
		if w < 0 || h < 0 {
			return errors.New("mermaid: negative image dimensions")
		}
		if w == 0 || h == 0 {
			continue
		}
		fields := strings.Fields(e.Attrs["preserveAspectRatio"])
		if len(fields) > 0 && fields[0] == "defer" {
			fields = fields[1:]
		}
		align, mode := "xMidYMid", "meet"
		if len(fields) > 0 {
			align = fields[0]
		}
		if len(fields) > 1 {
			mode = fields[1]
		}
		if len(fields) > 2 {
			return errors.New("mermaid: invalid preserveAspectRatio")
		}
		sx, sy := w/float64(item.image.Bounds().Dx()), h/float64(item.image.Bounds().Dy())
		ox, oy := x, y
		if align != "none" {
			if mode != "meet" && mode != "slice" {
				return errors.New("mermaid: invalid preserveAspectRatio mode")
			}
			factor := math.Min(sx, sy)
			if mode == "slice" {
				factor = math.Max(sx, sy)
			}
			sx, sy = factor, factor
			switch align {
			case "xMinYMin", "xMinYMid", "xMinYMax":
			case "xMidYMin", "xMidYMid", "xMidYMax":
				ox += (w - float64(item.image.Bounds().Dx())*sx) / 2
			case "xMaxYMin", "xMaxYMid", "xMaxYMax":
				ox += w - float64(item.image.Bounds().Dx())*sx
			default:
				return errors.New("mermaid: invalid preserveAspectRatio alignment")
			}
			if strings.HasSuffix(align, "YMid") {
				oy += (h - float64(item.image.Bounds().Dy())*sy) / 2
			} else if strings.HasSuffix(align, "YMax") {
				oy += h - float64(item.image.Bounds().Dy())*sy
			}
		}
		outer := svgMultiply(f64.Aff3{scale, 0, 0, 0, scale, 0}, e.Matrix)
		det := outer[0]*outer[4] - outer[1]*outer[3]
		if det == 0 || math.IsNaN(det) || math.IsInf(det, 0) {
			continue
		}
		inv := f64.Aff3{outer[4] / det, -outer[1] / det, (outer[1]*outer[5] - outer[4]*outer[2]) / det, -outer[3] / det, outer[0] / det, (outer[3]*outer[2] - outer[0]*outer[5]) / det}
		b := item.image.Bounds()
		transform := svgMultiply(outer, f64.Aff3{sx, 0, ox - float64(b.Min.X)*sx, 0, sy, oy - float64(b.Min.Y)*sy})
		for _, v := range transform {
			if math.IsNaN(v) || math.IsInf(v, 0) {
				return errors.New("mermaid: invalid image transform")
			}
		}
		for _, v := range inv {
			if math.IsNaN(v) || math.IsInf(v, 0) {
				return errors.New("mermaid: invalid image transform")
			}
		}
		mask := imageViewportMask{dst.Bounds(), inv, x, y, w, h}
		xdraw.BiLinear.Transform(dst, transform, item.image, b, xdraw.Over, &xdraw.Options{DstMask: mask})
	}
	return nil
}
