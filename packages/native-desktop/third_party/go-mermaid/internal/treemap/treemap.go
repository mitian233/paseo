// Package treemap renders Mermaid treemap diagrams as nested SVG rectangles.
package treemap

import (
	"fmt"
	"math"
	"math/big"
	"regexp"
	"strconv"
	"strings"

	"github.com/zkrebbekx/go-mermaid/internal/svgutil"
	"github.com/zkrebbekx/go-mermaid/internal/theme"
)

type RenderOptions struct {
	Theme    string
	FontFace string
	FontSize float64
	Padding  float64
	Title    string
	Config   map[string]any
}

type node struct {
	name, class string
	value       float64
	leaf        bool
	children    []*node
}
type style struct{ fill, color, stroke, width string }
type diagram struct {
	roots  []*node
	styles map[string]style
}

var nodePattern = regexp.MustCompile(`^"((?:[^"\\]|\\.)*)"\s*(?::\s*([^\s:;]+))?\s*(?::::([\w-]+))?\s*;?\s*$`)
var classPattern = regexp.MustCompile(`^classDef\s+([\w-]+(?:\s*,\s*[\w-]+)*)\s+(.+?)\s*;?\s*$`)

func parse(src string) (*diagram, error) {
	d := &diagram{styles: map[string]style{}}
	type level struct {
		indent int
		n      *node
	}
	var stack []level
	header := false
	base := -1
	for i, line := range strings.Split(strings.ReplaceAll(src, "\r\n", "\n"), "\n") {
		text := strings.TrimSpace(line)
		if text == "" || strings.HasPrefix(text, "%%") {
			continue
		}
		if !header {
			if text != "treemap-beta" {
				return nil, fmt.Errorf("line %d: expected treemap-beta", i+1)
			}
			header = true
			continue
		}
		if strings.HasPrefix(text, "classDef ") || strings.HasPrefix(text, "classDef\t") {
			m := classPattern.FindStringSubmatch(text)
			if m == nil {
				return nil, fmt.Errorf("line %d: invalid class definition", i+1)
			}
			s := style{}
			for _, part := range strings.Split(strings.TrimSuffix(m[2], ";"), ",") {
				kv := strings.SplitN(strings.TrimSpace(part), ":", 2)
				if len(kv) != 2 {
					return nil, fmt.Errorf("line %d: invalid style", i+1)
				}
				v := strings.TrimSpace(kv[1])
				switch strings.TrimSpace(kv[0]) {
				case "fill":
					s.fill = v
				case "color":
					s.color = v
				case "stroke":
					s.stroke = v
				case "stroke-width":
					s.width = strings.TrimSuffix(v, "px")
				}
			}
			for _, name := range strings.Split(m[1], ",") {
				d.styles[strings.TrimSpace(name)] = s
			}
			continue
		}
		m := nodePattern.FindStringSubmatch(text)
		if m == nil {
			return nil, fmt.Errorf("line %d: invalid treemap node", i+1)
		}
		indent := 0
		for _, r := range line {
			if r == ' ' {
				indent++
			} else if r == '\t' {
				indent += 4
			} else {
				break
			}
		}
		if base < 0 {
			base = indent
		}
		if indent < base {
			return nil, fmt.Errorf("line %d: invalid indentation", i+1)
		}
		if len(stack) > 0 && indent < stack[len(stack)-1].indent {
			found := false
			for _, v := range stack {
				if v.indent == indent {
					found = true
					break
				}
			}
			if !found {
				return nil, fmt.Errorf("line %d: inconsistent indentation", i+1)
			}
		}
		for len(stack) > 0 && indent <= stack[len(stack)-1].indent {
			stack = stack[:len(stack)-1]
		}
		if len(stack) == 0 && indent != base {
			return nil, fmt.Errorf("line %d: indentation has no parent", i+1)
		}
		if len(stack) > 0 && stack[len(stack)-1].n.leaf {
			return nil, fmt.Errorf("line %d: leaf cannot have children", i+1)
		}
		name := strings.ReplaceAll(strings.ReplaceAll(m[1], `\"`, `"`), `\\`, `\`)
		if name == "" {
			return nil, fmt.Errorf("line %d: empty name", i+1)
		}
		n := &node{name: name, class: m[3], leaf: m[2] != ""}
		if n.leaf {
			v, err := strconv.ParseFloat(m[2], 64)
			if err != nil || math.IsNaN(v) || math.IsInf(v, 0) || v <= 0 {
				return nil, fmt.Errorf("line %d: value must be positive and finite", i+1)
			}
			n.value = v
		}
		if len(stack) == 0 {
			d.roots = append(d.roots, n)
		} else {
			p := stack[len(stack)-1].n
			p.children = append(p.children, n)
		}
		stack = append(stack, level{indent, n})
	}
	if !header || len(d.roots) == 0 {
		return nil, fmt.Errorf("empty treemap")
	}
	var sum func(*node) (float64, error)
	sum = func(n *node) (float64, error) {
		if n.leaf {
			return n.value, nil
		}
		if len(n.children) == 0 {
			return 0, fmt.Errorf("section %q has no children", n.name)
		}
		for _, c := range n.children {
			v, e := sum(c)
			if e != nil {
				return 0, e
			}
			n.value += v
		}
		if math.IsInf(n.value, 0) {
			return 0, fmt.Errorf("section %q value overflow", n.name)
		}
		return n.value, nil
	}
	total := 0.0
	for _, n := range d.roots {
		v, e := sum(n)
		if e != nil {
			return nil, e
		}
		total += v
	}
	if math.IsInf(total, 0) {
		return nil, fmt.Errorf("treemap value overflow")
	}
	return d, nil
}

type config struct {
	padding, diagramPadding, nodeWidth, nodeHeight, borderWidth, valueFontSize, labelFontSize float64
	showValues                                                                                bool
	valueFormat                                                                               string
}

func number(m map[string]any, key string, def float64) float64 {
	var n float64
	switch v := m[key].(type) {
	case float64:
		n = v
	case float32:
		n = float64(v)
	case int:
		n = float64(v)
	case int64:
		n = float64(v)
	case uint:
		n = float64(v)
	default:
		return def
	}
	if math.IsNaN(n) || math.IsInf(n, 0) || n < 0 {
		return def
	}
	return n
}
func settings(o RenderOptions) config {
	c := config{padding: 10, diagramPadding: 8, nodeWidth: 100, nodeHeight: 40, borderWidth: 1, valueFontSize: 12, labelFontSize: 14, showValues: true, valueFormat: ","}
	c.padding = number(o.Config, "padding", c.padding)
	c.diagramPadding = number(o.Config, "diagramPadding", c.diagramPadding)
	c.nodeWidth = number(o.Config, "nodeWidth", c.nodeWidth)
	c.nodeHeight = number(o.Config, "nodeHeight", c.nodeHeight)
	c.borderWidth = number(o.Config, "borderWidth", c.borderWidth)
	c.valueFontSize = number(o.Config, "valueFontSize", c.valueFontSize)
	c.labelFontSize = number(o.Config, "labelFontSize", c.labelFontSize)
	if v, ok := o.Config["showValues"].(bool); ok {
		c.showValues = v
	}
	if v, ok := o.Config["valueFormat"].(string); ok {
		c.valueFormat = v
	}
	return c
}

// formatValue implements the documented D3 fixed-point, currency, grouping and percent formats.
func formatValue(v float64, format string) string {
	currency := strings.Contains(format, "$")
	percent := strings.Contains(format, "%")
	grouped := strings.Contains(format, ",")
	precision := -1
	if dot := strings.IndexByte(format, '.'); dot >= 0 {
		end := dot + 1
		for end < len(format) && format[end] >= '0' && format[end] <= '9' {
			end++
		}
		if end > dot+1 {
			if n, e := strconv.Atoi(format[dot+1 : end]); e == nil && n <= 20 {
				precision = n
			}
		}
	}
	if format == "$0,0" {
		precision = 0
	}
	if percent {
		v *= 100
		if precision < 0 {
			precision = 0
		}
	}
	var s string
	if precision < 0 {
		s = strconv.FormatFloat(v, 'f', -1, 64)
	} else {
		s = fixedDecimal(v, precision)
	}
	if grouped {
		parts := strings.SplitN(s, ".", 2)
		whole := parts[0]
		var b strings.Builder
		for i, r := range whole {
			if i > 0 && (len(whole)-i)%3 == 0 {
				b.WriteByte(',')
			}
			b.WriteRune(r)
		}
		s = b.String()
		if len(parts) == 2 {
			s += "." + parts[1]
		}
	}
	if currency {
		s = "$" + s
	}
	if percent {
		s += "%"
	}
	return s
}

// JavaScript's toFixed rounds exact halfway values away from zero; Go's
// FormatFloat uses ties-to-even. Preserve the input float's exact value.
func fixedDecimal(value float64, precision int) string {
	negative := value < 0
	rational := new(big.Rat).SetFloat64(math.Abs(value))
	var factor, numerator, whole, remainder big.Int
	factor.Exp(big.NewInt(10), big.NewInt(int64(precision)), nil)
	numerator.Mul(rational.Num(), &factor)
	whole.QuoRem(&numerator, rational.Denom(), &remainder)
	remainder.Lsh(&remainder, 1)
	if remainder.Cmp(rational.Denom()) >= 0 {
		whole.Add(&whole, big.NewInt(1))
	}
	digits := whole.String()
	if precision > 0 {
		if len(digits) <= precision {
			digits = strings.Repeat("0", precision+1-len(digits)) + digits
		}
		split := len(digits) - precision
		digits = digits[:split] + "." + digits[split:]
	}
	if negative {
		digits = "-" + digits
	}
	return digits
}

type box struct{ x, y, w, h float64 }

func Render(source string, o RenderOptions) ([]byte, error) {
	d, err := parse(source)
	if err != nil {
		return nil, err
	}
	c := settings(o)
	p := theme.For(o.Theme)
	if o.FontFace == "" {
		o.FontFace = "sans-serif"
	}
	if o.FontSize <= 0 {
		o.FontSize = 16
	}
	leaves, maxDepth := 0, 0
	var count func(*node, int)
	count = func(n *node, depth int) {
		if n.leaf {
			leaves++
			if depth > maxDepth {
				maxDepth = depth
			}
		}
		for _, ch := range n.children {
			count(ch, depth+1)
		}
	}
	for _, n := range d.roots {
		count(n, 0)
	}
	scale := math.Sqrt(float64(leaves))
	w := c.nodeWidth * scale
	h := c.nodeHeight * scale
	header := c.labelFontSize + 4
	if c.showValues {
		header += c.valueFontSize + 2
	}
	if maxDepth > 0 {
		h = math.Max(h, float64(maxDepth)*(header+2*c.padding)+c.nodeHeight)
	}
	titleH := svgutil.TitleHeight(o.Title, o.FontSize)
	var b strings.Builder
	fmt.Fprintf(&b, `<svg xmlns="http://www.w3.org/2000/svg" width="%s" height="%s" viewBox="0 0 %s %s" font-family="%s">`, svgutil.Num(w+2*c.diagramPadding), svgutil.Num(h+2*c.diagramPadding+titleH), svgutil.Num(w+2*c.diagramPadding), svgutil.Num(h+2*c.diagramPadding+titleH), svgutil.Esc(o.FontFace))
	fmt.Fprintf(&b, `<rect width="100%%" height="100%%" fill="%s"/>`, svgutil.Esc(p.Background))
	if o.Title != "" {
		fmt.Fprintf(&b, `<text x="%s" y="%s" text-anchor="middle" font-size="%s" fill="%s">%s</text>`, svgutil.Num(c.diagramPadding+w/2), svgutil.Num(o.FontSize), svgutil.Num(o.FontSize), svgutil.Esc(p.Text), svgutil.Esc(o.Title))
	}
	id := 0
	var renderNodes func([]*node, box, int)
	renderNodes = func(nodes []*node, area box, depth int) {
		total := 0.0
		for _, n := range nodes {
			total += n.value
		}
		offset := 0.0
		gap := 0.0
		length := area.h
		if area.w >= area.h {
			length = area.w
		}
		if len(nodes) > 1 {
			gap = math.Min(c.padding, length/(2*float64(len(nodes)-1)))
			length -= gap * float64(len(nodes)-1)
		}
		for i, n := range nodes {
			fraction := n.value / total
			r := area
			if area.w >= area.h {
				r.x += offset
				if i == len(nodes)-1 {
					r.w = area.w - offset
				} else {
					r.w = length * fraction
				}
				offset += r.w + gap
			} else {
				r.y += offset
				if i == len(nodes)-1 {
					r.h = area.h - offset
				} else {
					r.h = length * fraction
				}
				offset += r.h + gap
			}
			st := d.styles[n.class]
			if st.fill == "" {
				st.fill = p.NodeFill
			}
			if st.color == "" {
				st.color = p.Text
			}
			if st.stroke == "" {
				st.stroke = p.NodeStroke
			}
			if st.width == "" {
				st.width = svgutil.Num(c.borderWidth)
			}
			id++
			clip := id
			fmt.Fprintf(&b, `<rect data-name="%s" x="%s" y="%s" width="%s" height="%s" fill="%s" stroke="%s" stroke-width="%s"/>`, svgutil.Esc(n.name), svgutil.Num(r.x), svgutil.Num(r.y), svgutil.Num(r.w), svgutil.Num(r.h), svgutil.Esc(st.fill), svgutil.Esc(st.stroke), svgutil.Esc(st.width))
			fmt.Fprintf(&b, `<clipPath id="treemap-clip-%d"><rect x="%s" y="%s" width="%s" height="%s"/></clipPath>`, clip, svgutil.Num(r.x), svgutil.Num(r.y), svgutil.Num(r.w), svgutil.Num(r.h))
			labelY := r.y + c.labelFontSize + 2
			fmt.Fprintf(&b, `<g clip-path="url(#treemap-clip-%d)" fill="%s"><text x="%s" y="%s" font-size="%s">%s</text>`, clip, svgutil.Esc(st.color), svgutil.Num(r.x+2), svgutil.Num(labelY), svgutil.Num(c.labelFontSize), svgutil.Esc(n.name))
			if c.showValues {
				fmt.Fprintf(&b, `<text x="%s" y="%s" font-size="%s">%s</text>`, svgutil.Num(r.x+2), svgutil.Num(labelY+c.valueFontSize+2), svgutil.Num(c.valueFontSize), svgutil.Esc(formatValue(n.value, c.valueFormat)))
			}
			b.WriteString(`</g>`)
			if !n.leaf {
				pad := math.Min(c.padding, math.Min(r.w, r.h)/4)
				header := c.labelFontSize + 4
				if c.showValues {
					header += c.valueFontSize + 2
				}
				header = math.Min(header, math.Max(0, (r.h-2*pad)/2))
				inner := box{r.x + pad, r.y + pad + header, math.Max(0, r.w-2*pad), math.Max(0, r.h-2*pad-header)}
				renderNodes(n.children, inner, depth+1)
			}
		}
	}
	renderNodes(d.roots, box{c.diagramPadding, c.diagramPadding + titleH, w, h}, 0)
	b.WriteString(`</svg>`)
	return []byte(b.String()), nil
}
