// Adapted from go-mermaid raster/arrows.go; see UPSTREAM-LICENSE.
package mermaid

import (
	"image"
	"image/color"
	"math"
	"strconv"
)

// oksvg omits marker primitives. The maintained core emits the same triangular
// marker geometry for its directed edges; preserve its coordinate scope here.
func drawArrows(img *image.RGBA, svg string, scale float64) {
	if scene, err := scanSVGScene(svg); err == nil {
		drawArrowElements(img, scene.Arrows, scale)
	}
}

func drawArrowElements(img *image.RGBA, elements []svgElement, scale float64) {
	for _, element := range elements {
		a := element.Attrs
		var sx, sy, nx, ny, ex, ey, px, py float64
		if a["element"] == "line" {
			sx, sy = numAttr(a["x1"], "0"), numAttr(a["y1"], "0")
			ex, ey = numAttr(a["x2"], "0"), numAttr(a["y2"], "0")
			nx, ny, px, py = ex, ey, sx, sy
		} else {
			path := a["d"]
			if a["element"] == "polyline" {
				path = a["points"]
			}
			numbers := svgNumberPattern.FindAllString(path, -1)
			if len(numbers) < 4 {
				continue
			}
			value := func(index int) float64 { v, _ := strconv.ParseFloat(numbers[index], 64); return v }
			sx, sy, nx, ny = value(0), value(1), value(2), value(3)
			ex, ey, px, py = value(len(numbers)-2), value(len(numbers)-1), value(len(numbers)-4), value(len(numbers)-3)
		}
		stroke := numAttr(a["stroke-width"], "1")
		if stroke <= 0 {
			continue
		}
		paint := func(tx, ty, bx, by float64) {
			dx, dy := tx-bx, ty-by
			length := math.Hypot(dx, dy)
			if length == 0 {
				return
			}
			dx /= length
			dy /= length
			tx += dx * 0.7 * stroke
			ty += dy * 0.7 * stroke
			bx, by = tx-dx*7*stroke, ty-dy*7*stroke
			half := 3.5 * stroke
			x0, y0 := svgPoint(element.Matrix, tx, ty)
			x1, y1 := svgPoint(element.Matrix, bx-dy*half, by+dx*half)
			x2, y2 := svgPoint(element.Matrix, bx+dy*half, by-dx*half)
			fillTriangle(img, x0*scale, y0*scale, x1*scale, y1*scale, x2*scale, y2*scale, colorOf(a["stroke"]))
		}
		if a["marker-start"] != "" && a["marker-start"] != "none" {
			paint(sx, sy, nx, ny)
		}
		if a["marker-end"] != "" && a["marker-end"] != "none" {
			paint(ex, ey, px, py)
		}
	}
}

// fillTriangle rasterizes a solid triangle with a simple scanline fill.
func fillTriangle(img *image.RGBA, x0, y0, x1, y1, x2, y2 float64, col color.Color) {
	for _, v := range [...]float64{x0, y0, x1, y1, x2, y2} {
		if math.IsNaN(v) || math.IsInf(v, 0) {
			return
		}
	}
	b := img.Bounds()
	left := math.Max(float64(b.Min.X), math.Floor(min3(x0, x1, x2)))
	right := math.Min(float64(b.Max.X-1), math.Ceil(max3(x0, x1, x2)))
	top := math.Max(float64(b.Min.Y), math.Floor(min3(y0, y1, y2)))
	bottom := math.Min(float64(b.Max.Y-1), math.Ceil(max3(y0, y1, y2)))
	if left > right || top > bottom {
		return
	}
	minX, maxX, minY, maxY := int(left), int(right), int(top), int(bottom)
	area := edge(x0, y0, x1, y1, x2, y2)
	if area == 0 {
		return
	}
	r, g, bl, a := col.RGBA()
	if a == 0 {
		return
	}
	pixel := color.RGBA{uint8(r >> 8), uint8(g >> 8), uint8(bl >> 8), uint8(a >> 8)}
	for y := minY; y <= maxY; y++ {
		for x := minX; x <= maxX; x++ {
			fx, fy := float64(x)+0.5, float64(y)+0.5
			w0 := edge(x1, y1, x2, y2, fx, fy)
			w1 := edge(x2, y2, x0, y0, fx, fy)
			w2 := edge(x0, y0, x1, y1, fx, fy)
			if (w0 >= 0 && w1 >= 0 && w2 >= 0) || (w0 <= 0 && w1 <= 0 && w2 <= 0) {
				img.SetRGBA(x, y, pixel)
			}
		}
	}
}

func edge(ax, ay, bx, by, cx, cy float64) float64 {
	return (bx-ax)*(cy-ay) - (by-ay)*(cx-ax)
}

func min3(a, b, c float64) float64 { return math.Min(a, math.Min(b, c)) }
func max3(a, b, c float64) float64 { return math.Max(a, math.Max(b, c)) }
