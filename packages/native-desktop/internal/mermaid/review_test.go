package mermaid

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	"image/color"
	"strings"
	"testing"

	"golang.org/x/image/font/gofont/gobold"
	"golang.org/x/image/font/gofont/goregular"
	"golang.org/x/image/font/opentype"
)

func TestBoldFontChangesOnlyBoldLabels(t *testing.T) {
	regular, err := opentype.Parse(goregular.TTF)
	if err != nil {
		t.Fatal(err)
	}
	bold, err := opentype.Parse(gobold.TTF)
	if err != nil {
		t.Fatal(err)
	}
	plain := rasterFont{font: regular}
	weighted := rasterFont{font: regular, bold: bold}
	draw := func(f rasterFont, weight string) []byte {
		img := image.NewRGBA(image.Rect(0, 0, 180, 50))
		f.drawText(img, `<svg><text x="5" y="30" font-weight="`+weight+`">Important</text></svg>`, 1, 20)
		return img.Pix
	}
	if bytes.Equal(draw(plain, "bold"), draw(weighted, "bold")) {
		t.Fatal("bold font did not change bold text")
	}
	if !bytes.Equal(draw(plain, "normal"), draw(weighted, "normal")) {
		t.Fatal("bold font changed regular text")
	}
}

func TestRenderCachesBoldFontsSeparately(t *testing.T) {
	r := NewRenderer(1, 1<<20)
	q := Request{Source: "sequenceDiagram\nAlice->>Bob: Hi\nloop Again\nAlice->>Bob: Hi\nend", Font: goregular.TTF}
	plain, err := r.Render(context.Background(), q)
	if err != nil {
		t.Fatal(err)
	}
	q.BoldFont = gobold.TTF
	bold, err := r.Render(context.Background(), q)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(plain, bold) {
		t.Fatal("different bold fonts returned the same rendered image")
	}
}
func TestTextColors(t *testing.T) {
	for _, tc := range []struct {
		input string
		want  color.NRGBA
	}{
		{"white", color.NRGBA{255, 255, 255, 255}}, {"rebeccapurple", color.NRGBA{102, 51, 153, 255}},
		{"none", color.NRGBA{}}, {"transparent", color.NRGBA{}}, {"#f00", color.NRGBA{255, 0, 0, 255}},
	} {
		t.Run(tc.input, func(t *testing.T) {
			got := color.NRGBAModel.Convert(colorOf(tc.input)).(color.NRGBA)
			if got != tc.want {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
		})
	}
}

func TestRejectsUnboundedInputBeforeRendering(t *testing.T) {
	r := NewRenderer(1, 0)
	for _, source := range []string{
		"graph TD\nA[" + strings.Repeat("x", 20000) + "]",
		"graph TD\n" + strings.Repeat("A-->B;", 600),
		"packet-beta\n0-1000000000: payload",
		"graph TD\n A[don't] --> B\n" + strings.Repeat("A-->B;", 600),
	} {
		_, err := r.Render(context.Background(), Request{Source: source, Font: goregular.TTF})
		if !errors.Is(err, ErrTooComplex) {
			t.Errorf("expected complexity error: %v", err)
		}
	}
}

func TestRejectsOversizedSVGBeforeParsing(t *testing.T) {
	for _, svg := range []string{
		`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 100000 100000"><broken`,
		fmt.Sprintf(`<svg viewBox="0 0 10 10">%s</svg>`, strings.Repeat(" ", 2<<20)),
	} {
		_, err := rasterize(context.Background(), []byte(svg), 1, rasterFont{})
		if !errors.Is(err, ErrTooComplex) {
			t.Errorf("expected size error before SVG parse: %v", err)
		}
	}
}

func TestRejectsOversizedRotatedLabel(t *testing.T) {
	face, err := opentype.Parse(goregular.TTF)
	if err != nil {
		t.Fatal(err)
	}
	img := image.NewRGBA(image.Rect(0, 0, 100, 100))
	svg := `<svg><text x="1" y="20" transform="rotate(90)" font-size="100">` + strings.Repeat("W", 2000) + `</text></svg>`
	if err := (rasterFont{font: face}).drawText(img, svg, 8, 14); !errors.Is(err, ErrTooComplex) {
		t.Fatalf("expected oversized rotated label error, got %v", err)
	}
}
