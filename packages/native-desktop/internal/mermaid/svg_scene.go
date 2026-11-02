package mermaid

import (
	"encoding/xml"
	"fmt"
	"io"
	"math"
	"regexp"
	"strconv"
	"strings"
	"unicode"

	"golang.org/x/image/math/f64"
)

type svgElement struct {
	Attrs  map[string]string
	Matrix f64.Aff3
	Inner  string
}
type svgScene struct{ Texts, Images, Arrows []svgElement }
type svgTextStyle struct{ fill, size, weight, anchor, baseline string }
type svgFrame struct {
	matrix        f64.Aff3
	style         svgTextStyle
	ignored       bool
	text, content int
}

var svgIdentity = f64.Aff3{1, 0, 0, 0, 1, 0}
var svgNumberPattern = regexp.MustCompile(`[+-]?(?:[0-9]+\.?[0-9]*|\.[0-9]+)(?:[eE][+-]?[0-9]+)?`)

func svgPoint(m f64.Aff3, x, y float64) (float64, float64) {
	return m[0]*x + m[1]*y + m[2], m[3]*x + m[4]*y + m[5]
}
func svgMultiply(a, b f64.Aff3) f64.Aff3 {
	return f64.Aff3{a[0]*b[0] + a[1]*b[3], a[0]*b[1] + a[1]*b[4], a[0]*b[2] + a[1]*b[5] + a[2], a[3]*b[0] + a[4]*b[3], a[3]*b[1] + a[4]*b[4], a[3]*b[2] + a[4]*b[5] + a[5]}
}
func svgNumbers(source string) ([]float64, error) {
	var values []float64
	last := 0
	for _, loc := range svgNumberPattern.FindAllStringIndex(source, -1) {
		if strings.TrimFunc(source[last:loc[0]], func(r rune) bool { return unicode.IsSpace(r) || r == ',' }) != "" {
			return nil, fmt.Errorf("invalid SVG transform number")
		}
		value, err := strconv.ParseFloat(source[loc[0]:loc[1]], 64)
		if err != nil || math.IsNaN(value) || math.IsInf(value, 0) {
			return nil, fmt.Errorf("invalid SVG transform number")
		}
		values = append(values, value)
		last = loc[1]
	}
	if strings.TrimFunc(source[last:], func(r rune) bool { return unicode.IsSpace(r) || r == ',' }) != "" {
		return nil, fmt.Errorf("invalid SVG transform number")
	}
	return values, nil
}
func parseSVGTransform(source string) (f64.Aff3, error) {
	matrix := svgIdentity
	for source = strings.TrimSpace(source); source != ""; source = strings.TrimLeft(source, " ,\t\r\n") {
		open := strings.IndexByte(source, '(')
		close := strings.IndexByte(source, ')')
		if open < 1 || close < open {
			return matrix, fmt.Errorf("invalid SVG transform %q", source)
		}
		name := strings.TrimSpace(source[:open])
		v, err := svgNumbers(source[open+1 : close])
		if err != nil {
			return matrix, err
		}
		next := svgIdentity
		switch name {
		case "matrix":
			if len(v) != 6 {
				return matrix, fmt.Errorf("invalid SVG matrix arity")
			}
			next = f64.Aff3{v[0], v[2], v[4], v[1], v[3], v[5]}
		case "translate":
			if len(v) < 1 || len(v) > 2 {
				return matrix, fmt.Errorf("invalid SVG translate arity")
			}
			next[2] = v[0]
			if len(v) == 2 {
				next[5] = v[1]
			}
		case "scale":
			if len(v) < 1 || len(v) > 2 {
				return matrix, fmt.Errorf("invalid SVG scale arity")
			}
			next[0], next[4] = v[0], v[0]
			if len(v) == 2 {
				next[4] = v[1]
			}
		case "rotate":
			if len(v) != 1 && len(v) != 3 {
				return matrix, fmt.Errorf("invalid SVG rotate arity")
			}
			rad := v[0] * math.Pi / 180
			c, s := math.Cos(rad), math.Sin(rad)
			next = f64.Aff3{c, -s, 0, s, c, 0}
			if len(v) == 3 {
				next[2] = v[1] - c*v[1] + s*v[2]
				next[5] = v[2] - s*v[1] - c*v[2]
			}
		case "skewX", "skewY":
			if len(v) != 1 {
				return matrix, fmt.Errorf("invalid SVG skew arity")
			}
			tan := math.Tan(v[0] * math.Pi / 180)
			if name == "skewX" {
				next[1] = tan
			} else {
				next[3] = tan
			}
		default:
			return matrix, fmt.Errorf("unsupported SVG transform %q", name)
		}
		matrix = svgMultiply(matrix, next)
		for _, n := range matrix {
			if math.IsNaN(n) || math.IsInf(n, 0) {
				return matrix, fmt.Errorf("nonfinite SVG transform")
			}
		}
		source = source[close+1:]
	}
	return matrix, nil
}

// scanSVGScene keeps text and image coordinates in their actual ancestor scope.
// Vector paths remain with oksvg; both native overlays consume this same scene.
func scanSVGScene(source string) (svgScene, error) {
	var scene svgScene
	decoder := xml.NewDecoder(strings.NewReader(source))
	stack := []svgFrame{{matrix: svgIdentity, text: -1}}
	for {
		before := int(decoder.InputOffset())
		token, err := decoder.Token()
		if err == io.EOF {
			return scene, nil
		}
		if err != nil {
			return scene, fmt.Errorf("parse SVG overlays: %w", err)
		}
		switch element := token.(type) {
		case xml.StartElement:
			parent := stack[len(stack)-1]
			frame := svgFrame{matrix: parent.matrix, style: parent.style, ignored: parent.ignored, text: -1}
			switch element.Name.Local {
			case "defs", "clipPath", "symbol", "mask":
				frame.ignored = true
			}
			for _, a := range element.Attr {
				switch a.Name.Local {
				case "fill":
					frame.style.fill = a.Value
				case "font-size":
					frame.style.size = a.Value
				case "font-weight":
					frame.style.weight = a.Value
				case "text-anchor":
					frame.style.anchor = a.Value
				case "dominant-baseline":
					frame.style.baseline = a.Value
				case "viewBox":
					if len(stack) == 1 {
						v, e := svgNumbers(a.Value)
						if e != nil || len(v) != 4 {
							return scene, fmt.Errorf("invalid SVG overlay viewBox")
						}
						frame.matrix = svgMultiply(frame.matrix, f64.Aff3{1, 0, -v[0], 0, 1, -v[1]})
					}
				}
			}
			for _, a := range element.Attr {
				if a.Name.Local == "transform" {
					m, e := parseSVGTransform(a.Value)
					if e != nil {
						return scene, e
					}
					frame.matrix = svgMultiply(frame.matrix, m)
				}
			}
			for _, value := range frame.matrix {
				if math.IsNaN(value) || math.IsInf(value, 0) {
					return scene, fmt.Errorf("nonfinite accumulated SVG transform")
				}
			}
			marker := false
			if element.Name.Local == "path" || element.Name.Local == "line" || element.Name.Local == "polyline" {
				for _, a := range element.Attr {
					if (a.Name.Local == "marker-start" || a.Name.Local == "marker-end") && a.Value != "" && a.Value != "none" {
						marker = true
					}
				}
			}
			if !frame.ignored && (element.Name.Local == "text" || element.Name.Local == "image" || marker) {
				attrs := map[string]string{"fill": frame.style.fill, "font-size": frame.style.size, "font-weight": frame.style.weight, "text-anchor": frame.style.anchor, "dominant-baseline": frame.style.baseline}
				for _, a := range element.Attr {
					attrs[a.Name.Local] = a.Value
				}
				item := svgElement{Attrs: attrs, Matrix: frame.matrix}
				if element.Name.Local == "text" {
					frame.text = len(scene.Texts)
					frame.content = int(decoder.InputOffset())
					scene.Texts = append(scene.Texts, item)
				} else if element.Name.Local == "image" {
					scene.Images = append(scene.Images, item)
				} else {
					attrs["element"] = element.Name.Local
					scene.Arrows = append(scene.Arrows, item)
				}
			}
			stack = append(stack, frame)
		case xml.EndElement:
			if len(stack) <= 1 {
				return scene, fmt.Errorf("invalid SVG overlay nesting")
			}
			frame := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			if frame.text >= 0 {
				scene.Texts[frame.text].Inner = source[frame.content:before]
			}
		}
	}
}
