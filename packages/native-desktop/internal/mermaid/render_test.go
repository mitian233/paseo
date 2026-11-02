package mermaid

import (
	"bytes"
	"context"
	"errors"
	"image/png"
	"os"
	"testing"

	"golang.org/x/image/font/gofont/goregular"
)

func TestRenderScaleThemeAndSyntax(t *testing.T) {
	r := NewRenderer(2, 1<<20)
	q := Request{Source: "graph TD\n A[Start] --> B[Finish]", Font: goregular.TTF, Scale: 1}
	p, err := r.Render(context.Background(), q)
	if err != nil {
		t.Fatal(err)
	}
	light := append([]byte(nil), p...)
	a, err := png.Decode(bytes.NewReader(p))
	if err != nil {
		t.Fatal(err)
	}
	background := a.At(0, 0)
	drawn := 0
	for y := range a.Bounds().Dy() {
		for x := range a.Bounds().Dx() {
			if a.At(x, y) != background {
				drawn++
			}
		}
	}
	if drawn < 100 {
		t.Fatalf("diagram contains only %d non-background pixels", drawn)
	}
	q.Scale = 2
	p, err = r.Render(context.Background(), q)
	if err != nil {
		t.Fatal(err)
	}
	b, err := png.Decode(bytes.NewReader(p))
	if err != nil {
		t.Fatal(err)
	}
	if b.Bounds().Dx() < a.Bounds().Dx()*2-1 || b.Bounds().Dy() < a.Bounds().Dy()*2-1 {
		t.Fatalf("scale did not enlarge image: %v -> %v", a.Bounds(), b.Bounds())
	}
	q.Scale = 1
	q.Theme = "dark"
	dark, err := r.Render(context.Background(), q)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(dark, light) {
		t.Fatal("theme did not change image")
	}
	q.Source = "not-a-diagram"
	if _, err := r.Render(context.Background(), q); err == nil {
		t.Fatal("unsupported source must fail")
	}
}

func TestMissingGlyphAndCancellation(t *testing.T) {
	r := NewRenderer(1, 4096)
	q := Request{Source: "graph TD\n A[中文]", Font: goregular.TTF, Scale: 1}
	if _, err := r.Render(context.Background(), q); !errors.Is(err, ErrMissingGlyph) {
		t.Fatalf("missing Chinese glyph: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := r.Render(ctx, q); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled render: %v", err)
	}
}

func TestCJKLabelsRenderDistinctGlyphs(t *testing.T) {
	path := os.Getenv("PASEO_CJK_FONT")
	if path == "" {
		t.Skip("set PASEO_CJK_FONT to an OpenType CJK font for glyph verification")
	}
	font, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	r := NewRenderer(1, 1<<20)
	q := Request{Source: "graph TD\n A[中文]", Font: font, Scale: 2}
	a, err := r.Render(context.Background(), q)
	if err != nil {
		t.Fatal(err)
	}
	q.Source = "graph TD\n A[汉字]"
	b, err := r.Render(context.Background(), q)
	if err != nil {
		t.Fatal(err)
	}
	ia, err := png.Decode(bytes.NewReader(a))
	if err != nil {
		t.Fatal(err)
	}
	ib, err := png.Decode(bytes.NewReader(b))
	if err != nil {
		t.Fatal(err)
	}
	if ia.Bounds() != ib.Bounds() {
		t.Fatalf("equal-width labels changed layout: %v %v", ia.Bounds(), ib.Bounds())
	}
	different := 0
	for y := range ia.Bounds().Dy() {
		for x := range ia.Bounds().Dx() {
			if ia.At(x, y) != ib.At(x, y) {
				different++
			}
		}
	}
	if different < 20 {
		t.Fatalf("distinct Chinese glyphs changed only %d pixels", different)
	}
}
