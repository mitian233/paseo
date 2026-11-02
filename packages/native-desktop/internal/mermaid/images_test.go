package mermaid

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"image"
	"image/color"
	"image/gif"
	"image/jpeg"
	"image/png"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/image/math/f64"
)

func imageData(t *testing.T) string {
	t.Helper()
	im := image.NewRGBA(image.Rect(0, 0, 2, 1))
	im.SetRGBA(0, 0, color.RGBA{255, 0, 0, 255})
	im.SetRGBA(1, 0, color.RGBA{0, 0, 255, 255})
	var b bytes.Buffer
	if err := png.Encode(&b, im); err != nil {
		t.Fatal(err)
	}
	return "data:image/png;base64," + base64.StdEncoding.EncodeToString(b.Bytes())
}
func svgData(s string) string {
	return "data:image/svg+xml;base64," + base64.StdEncoding.EncodeToString([]byte(s))
}
func imageElement(src, aspect string) svgElement {
	return svgElement{Attrs: map[string]string{"href": src, "x": "1", "y": "1", "width": "8", "height": "8", "preserveAspectRatio": aspect}, Matrix: svgIdentity}
}
func TestImageDrawAspectAndTransform(t *testing.T) {
	src := imageData(t)
	for _, tc := range []struct {
		name, aspect     string
		red, blue, empty image.Point
	}{
		{"contain", "xMidYMid meet", image.Pt(4, 5), image.Pt(8, 5), image.Pt(4, 2)},
		{"cover", "xMidYMid slice", image.Pt(4, 5), image.Pt(8, 5), image.Pt(2, 5)},
		{"stretch", "none", image.Pt(4, 2), image.Pt(8, 2), image.Pt(2, 2)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			l := newSVGImageLoader(context.Background(), rasterFont{})
			w, h, err := l.Resolve(src)
			if err != nil || w != 2 || h != 1 {
				t.Fatalf("resolve %v %v %v", w, h, err)
			}
			e := imageElement(src, tc.aspect)
			e.Matrix = f64.Aff3{1, 0, 2, 0, 1, 0}
			dst := image.NewRGBA(image.Rect(0, 0, 16, 12))
			if err := l.Draw(dst, []svgElement{e}, 1); err != nil {
				t.Fatal(err)
			}
			r := dst.RGBAAt(tc.red.X, tc.red.Y)
			b := dst.RGBAAt(tc.blue.X, tc.blue.Y)
			z := dst.RGBAAt(tc.empty.X, tc.empty.Y)
			if r.R <= r.B || r.A == 0 || b.B <= b.R || b.A == 0 || z.A != 0 {
				t.Fatalf("pixels red=%v blue=%v outside=%v", r, b, z)
			}
		})
	}
}
func TestImageFetchReuse(t *testing.T) {
	data := strings.TrimPrefix(imageData(t), "data:image/png;base64,")
	payload, err := base64.StdEncoding.DecodeString(data)
	if err != nil {
		t.Fatal(err)
	}
	var count atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { count.Add(1); _, _ = w.Write(payload) }))
	defer server.Close()
	l := newSVGImageLoader(context.Background(), rasterFont{})
	for range 2 {
		if _, _, err := l.Resolve(server.URL); err != nil {
			t.Fatal(err)
		}
	}
	if err := l.Draw(image.NewRGBA(image.Rect(0, 0, 12, 12)), []svgElement{imageElement(server.URL, "none")}, 1); err != nil {
		t.Fatal(err)
	}
	if count.Load() != 1 {
		t.Fatalf("fetched %d times", count.Load())
	}
}
func TestImageRejectInvalidAndLarge(t *testing.T) {
	l := newSVGImageLoader(context.Background(), rasterFont{})
	for _, s := range []string{"file:///tmp/a.png", "data:image/png;base64,!!!!", "data:image/png;base64," + base64.StdEncoding.EncodeToString([]byte("not a png")), svgData(`<svg viewBox="0 0 999999 999999"/>`), "data:image/png;base64," + strings.Repeat("A", 12*1024*1024)} {
		if _, _, err := l.Resolve(s); err == nil {
			t.Errorf("accepted invalid image %.60q", s)
		}
	}
}
func TestImageFetchCancellation(t *testing.T) {
	started := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { close(started); <-r.Context().Done() }))
	defer server.Close()
	ctx, cancel := context.WithCancel(context.Background())
	l := newSVGImageLoader(ctx, rasterFont{})
	done := make(chan error, 1)
	go func() { _, _, e := l.Resolve(server.URL); done <- e }()
	<-started
	cancel()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("canceled fetch succeeded")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("fetch did not cancel")
	}
}
func TestImageSVGNestedAndCycle(t *testing.T) {
	pngSource := imageData(t)
	nested := svgData(fmt.Sprintf(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 8 8"><image href="%s" width="8" height="8" preserveAspectRatio="none"/></svg>`, pngSource))
	l := newSVGImageLoader(context.Background(), rasterFont{})
	dst := image.NewRGBA(image.Rect(0, 0, 12, 12))
	if err := l.Draw(dst, []svgElement{imageElement(nested, "none")}, 1); err != nil {
		t.Fatal(err)
	}
	if c := dst.RGBAAt(2, 4); c.R <= c.B || c.A == 0 {
		t.Fatalf("nested red pixel %v", c)
	}
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 8 8"><image href="%s" width="8" height="8"/></svg>`, server.URL)
	}))
	defer server.Close()
	if _, _, err := newSVGImageLoader(context.Background(), rasterFont{}).Resolve(server.URL); err == nil {
		t.Fatal("cyclic SVG accepted")
	}
}

func TestImageFormatsAndRotation(t *testing.T) {
	im := image.NewRGBA(image.Rect(0, 0, 2, 1))
	im.SetRGBA(0, 0, color.RGBA{255, 0, 0, 255})
	im.SetRGBA(1, 0, color.RGBA{0, 0, 255, 255})
	for _, encode := range []func(*bytes.Buffer) error{
		func(b *bytes.Buffer) error { return jpeg.Encode(b, im, &jpeg.Options{Quality: 100}) },
		func(b *bytes.Buffer) error { return gif.Encode(b, im, nil) },
	} {
		var b bytes.Buffer
		if err := encode(&b); err != nil {
			t.Fatal(err)
		}
		source := "data:application/octet-stream;base64," + base64.StdEncoding.EncodeToString(b.Bytes())
		l := newSVGImageLoader(context.Background(), rasterFont{})
		w, h, err := l.Resolve(source)
		if err != nil || w != 2 || h != 1 {
			t.Fatalf("format dimensions %v %v: %v", w, h, err)
		}
	}
	l := newSVGImageLoader(context.Background(), rasterFont{})
	e := imageElement(imageData(t), "none")
	e.Attrs["x"] = "0"
	e.Attrs["y"] = "0"
	e.Attrs["width"] = "8"
	e.Attrs["height"] = "4"
	e.Matrix = f64.Aff3{0, -1, 8, 1, 0, 0}
	dst := image.NewRGBA(image.Rect(0, 0, 10, 10))
	if err := l.Draw(dst, []svgElement{e}, 1); err != nil {
		t.Fatal(err)
	}
	top, bottom := dst.RGBAAt(6, 1), dst.RGBAAt(6, 6)
	if top.R <= top.B || bottom.B <= bottom.R {
		t.Fatalf("rotated colors %v %v", top, bottom)
	}
}

func TestImageSVGIntrinsicViewport(t *testing.T) {
	source := svgData(`<svg xmlns="http://www.w3.org/2000/svg" width="12" height="6" viewBox="0 0 6 6"><rect width="6" height="6" fill="red"/></svg>`)
	l := newSVGImageLoader(context.Background(), rasterFont{})
	w, h, err := l.Resolve(source)
	if err != nil || w != 12 || h != 6 {
		t.Fatalf("SVG dimensions %v %v: %v", w, h, err)
	}
	dst := image.NewRGBA(image.Rect(0, 0, 12, 6))
	e := svgElement{Attrs: map[string]string{"href": source, "width": "12", "height": "6"}, Matrix: svgIdentity}
	if err := l.Draw(dst, []svgElement{e}, 1); err != nil {
		t.Fatal(err)
	}
	if dst.RGBAAt(0, 3).A != 0 || dst.RGBAAt(6, 3).R < 200 || dst.RGBAAt(11, 3).A != 0 {
		t.Fatalf("SVG viewport pixels %v %v %v", dst.RGBAAt(0, 3), dst.RGBAAt(6, 3), dst.RGBAAt(11, 3))
	}
}

func TestImageRejectsOversizedRasterBeforeDecode(t *testing.T) {
	im := image.NewRGBA(image.Rect(0, 0, 4097, 1))
	var b bytes.Buffer
	if err := png.Encode(&b, im); err != nil {
		t.Fatal(err)
	}
	source := "data:image/png;base64," + base64.StdEncoding.EncodeToString(b.Bytes())
	if _, _, err := newSVGImageLoader(context.Background(), rasterFont{}).Resolve(source); err == nil {
		t.Fatal("accepted oversized PNG")
	}
}
