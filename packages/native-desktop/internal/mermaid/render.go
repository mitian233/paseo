// Package mermaid renders Mermaid diagrams locally into PNG images.
package mermaid

import (
	"bytes"
	"container/list"
	"context"
	"crypto/sha256"
	"encoding/xml"
	"errors"
	"fmt"
	"html"
	"image"
	"image/png"
	"math"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"unicode"

	"github.com/srwiley/oksvg"
	"github.com/srwiley/rasterx"
	upstream "github.com/zkrebbekx/go-mermaid"
	"golang.org/x/image/font/opentype"
	"golang.org/x/image/font/sfnt"
)

// ErrUnsupported reports syntax the selected renderer cannot represent.
var ErrUnsupported = upstream.ErrUnsupported

// ErrMissingGlyph indicates the supplied font cannot draw a diagram label.
var ErrMissingGlyph = errors.New("mermaid: font is missing a glyph")

// ErrTooComplex indicates that an input exceeds the renderer's resource limits.
var ErrTooComplex = errors.New("mermaid: diagram exceeds resource limits")

// Request contains the complete source and raster settings. Font and optional
// BoldFont are OpenType or TrueType fonts, or collections using their first face.
// Callers must not mutate the font bytes until Render returns.
type Request struct {
	Source   string
	Theme    string
	Font     []byte
	BoldFont []byte
	Scale    float64
}

type cacheKey struct {
	source, theme string
	font          [32]byte
	boldFont      [32]byte
	scale         float64
}
type entry struct {
	key cacheKey
	png []byte
}

// Renderer limits concurrent work and caches successful PNGs by byte count.
// Returned bytes are owned by the caller. A zero byte limit disables caching.
type Renderer struct {
	slots       chan struct{}
	limit, size int
	mu          sync.Mutex
	lru         list.List
	entries     map[cacheKey]*list.Element
}

func NewRenderer(workers, maxBytes int) *Renderer {
	if workers < 1 {
		workers = 1
	}
	if maxBytes < 0 {
		maxBytes = 0
	}
	return &Renderer{slots: make(chan struct{}, workers), limit: maxBytes, entries: make(map[cacheKey]*list.Element)}
}

// Render waits for an available worker. Cancellation stops waiting and is
// checked between parsing, rasterization and encoding; upstream computation
// already in progress cannot be interrupted.
func (r *Renderer) Render(ctx context.Context, req Request) (result []byte, err error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := validateSource(req.Source); err != nil {
		return nil, err
	}
	if req.Scale == 0 {
		req.Scale = 1
	}
	if req.Scale < 0 || req.Scale > 8 || math.IsNaN(req.Scale) || math.IsInf(req.Scale, 0) {
		return nil, errors.New("mermaid: invalid scale")
	}
	if len(req.Font) == 0 {
		return nil, errors.New("mermaid: font is required")
	}
	if req.Theme == "" {
		req.Theme = "default"
	}
	switch req.Theme {
	case "default", "dark", "neutral", "forest", "base":
	default:
		return nil, fmt.Errorf("mermaid: unknown theme %q", req.Theme)
	}
	key := cacheKey{source: req.Source, theme: req.Theme, font: sha256.Sum256(req.Font), boldFont: sha256.Sum256(req.BoldFont), scale: req.Scale}
	r.mu.Lock()
	if e := r.entries[key]; e != nil {
		r.lru.MoveToFront(e)
		data := bytes.Clone(e.Value.(entry).png)
		r.mu.Unlock()
		return data, nil
	}
	r.mu.Unlock()
	select {
	case r.slots <- struct{}{}:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	defer func() { <-r.slots }()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	defer func() {
		if v := recover(); v != nil {
			result = nil
			err = fmt.Errorf("mermaid: raster panic: %v", v)
		}
	}()
	collection, err := opentype.ParseCollection(req.Font)
	if err != nil {
		return nil, fmt.Errorf("mermaid: invalid font: %w", err)
	}
	face, err := collection.Font(0)
	if err != nil {
		return nil, fmt.Errorf("mermaid: invalid font face: %w", err)
	}
	bold := face
	if len(req.BoldFont) != 0 {
		collection, err := opentype.ParseCollection(req.BoldFont)
		if err != nil {
			return nil, fmt.Errorf("mermaid: invalid bold font: %w", err)
		}
		bold, err = collection.Font(0)
		if err != nil {
			return nil, fmt.Errorf("mermaid: invalid bold font face: %w", err)
		}
	}
	if err := validateRasterBudget(req.Source); err != nil {
		return nil, err
	}
	font := rasterFont{font: face, bold: bold}
	images := newSVGImageLoader(ctx, font)
	svg, err := upstream.Render(req.Source, upstream.WithTheme(upstream.Theme(req.Theme)), upstream.WithFont("sans-serif", 14), upstream.WithImageResolver(images.Resolve))
	if err != nil {
		return nil, fmt.Errorf("mermaid: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if len(svg) > maxSVGBytes {
		return nil, ErrTooComplex
	}
	if err := checkGlyphs(face, bold, string(svg)); err != nil {
		return nil, err
	}
	data, err := rasterizeWithImages(ctx, svg, req.Scale, font, images)
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	r.mu.Lock()
	cost := len(data) + len(key.source) + len(key.theme) + 128
	if cost <= r.limit {
		if old := r.entries[key]; old != nil {
			r.lru.MoveToFront(old)
		} else {
			for r.size+cost > r.limit {
				last := r.lru.Back()
				v := last.Value.(entry)
				delete(r.entries, v.key)
				r.size -= len(v.png) + len(v.key.source) + len(v.key.theme) + 128
				r.lru.Remove(last)
			}
			e := r.lru.PushFront(entry{key, bytes.Clone(data)})
			r.entries[key] = e
			r.size += cost
		}
	}
	r.mu.Unlock()
	return data, nil
}

type rasterFont struct{ font, bold *opentype.Font }

// Verify the actual SVG labels, including generated labels, before drawing.
func checkGlyphs(f, bold *opentype.Font, svg string) error {
	var buffer sfnt.Buffer
	for _, m := range textRe.FindAllStringSubmatch(svg, -1) {
		text := m[2]
		face := f
		if parseAttrs(m[1])["font-weight"] == "bold" {
			face = bold
		}
		if strings.Contains(text, "<tspan") {
			var parts []string
			for _, span := range tspanRe.FindAllStringSubmatch(text, -1) {
				parts = append(parts, span[2])
			}
			text = strings.Join(parts, "")
		}
		for _, ch := range html.UnescapeString(text) {
			if unicode.IsSpace(ch) || unicode.Is(unicode.Cf, ch) {
				continue
			}
			idx, err := face.GlyphIndex(&buffer, ch)
			if err != nil {
				return fmt.Errorf("mermaid: reading glyph %U: %w", ch, err)
			}
			if idx == 0 {
				return fmt.Errorf("%w: %U", ErrMissingGlyph, ch)
			}
		}
	}
	return nil
}

var rootSizeRe = regexp.MustCompile(`<svg\b[^>]*\bfont-size="([0-9.]+)"`)

// Inspect only the root element before oksvg builds its in-memory scene.
func validateSVG(svg []byte, scale float64) error {
	if len(svg) > maxSVGBytes {
		return ErrTooComplex
	}
	decoder := xml.NewDecoder(bytes.NewReader(svg))
	for {
		token, err := decoder.Token()
		if err != nil {
			return fmt.Errorf("mermaid: parse SVG: %w", err)
		}
		root, ok := token.(xml.StartElement)
		if !ok {
			continue
		}
		if root.Name.Local != "svg" {
			return errors.New("mermaid: invalid SVG root")
		}
		for _, attr := range root.Attr {
			if attr.Name.Local != "viewBox" {
				continue
			}
			values := strings.FieldsFunc(attr.Value, func(r rune) bool { return r == ',' || unicode.IsSpace(r) })
			if len(values) != 4 {
				return errors.New("mermaid: invalid SVG viewBox")
			}
			w, e1 := strconv.ParseFloat(values[2], 64)
			h, e2 := strconv.ParseFloat(values[3], 64)
			w *= scale
			h *= scale
			if e1 != nil || e2 != nil || math.IsNaN(w) || math.IsNaN(h) || w < 1 || h < 1 || w > 4096 || h > 4096 || w*h > 4*1024*1024 {
				return ErrTooComplex
			}
			return nil
		}
		return errors.New("mermaid: missing SVG viewBox")
	}
}

func rasterize(ctx context.Context, svg []byte, scale float64, f rasterFont) ([]byte, error) {
	return rasterizeWithImages(ctx, svg, scale, f, newSVGImageLoader(ctx, f))
}

func rasterizeWithImages(ctx context.Context, svg []byte, scale float64, f rasterFont, images *svgImageLoader) ([]byte, error) {
	if err := validateSVG(svg, scale); err != nil {
		return nil, err
	}
	scene, err := scanSVGScene(string(svg))
	if err != nil {
		return nil, err
	}
	icon, err := oksvg.ReadIconStream(bytes.NewReader(svg))
	if err != nil {
		return nil, fmt.Errorf("mermaid: parse SVG: %w", err)
	}
	wf, hf := icon.ViewBox.W*scale, icon.ViewBox.H*scale
	// Bound both dimensions and total pixel allocation before converting to int.
	if wf < 1 || hf < 1 || wf > 4096 || hf > 4096 || wf*hf > 4*1024*1024 || math.IsNaN(wf) || math.IsNaN(hf) {
		return nil, ErrTooComplex
	}
	w, h := int(wf), int(hf)
	icon.SetTarget(0, 0, float64(w), float64(h))
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	scanner := rasterx.NewScannerGV(w, h, img, img.Bounds())
	icon.Draw(rasterx.NewDasher(w, h, scanner), 1)
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	drawArrowElements(img, scene.Arrows, scale)
	if err := images.Draw(img, scene.Images, scale); err != nil {
		return nil, err
	}
	size := 14.0
	if m := rootSizeRe.FindSubmatch(svg); m != nil {
		if v, e := strconv.ParseFloat(string(m[1]), 64); e == nil && v > 0 {
			size = v
		}
	}
	if err := f.drawTextElements(img, scene.Texts, scale, size); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return nil, fmt.Errorf("mermaid: encode PNG: %w", err)
	}
	return buf.Bytes(), nil
}
