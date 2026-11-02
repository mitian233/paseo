// Package c4 parses and renders Mermaid C4 diagrams (C4Context, C4Container,
// …) to SVG, reusing the shared layered layout engine.
//
// Syntax (subset):
//
//	C4Context
//	    title System Context
//	    Person(custA, "Customer", "A bank customer")
//	    System(sysA, "Internet Banking", "Lets customers ...")
//	    Rel(custA, sysA, "Uses")
package c4

import (
	"strconv"
	"strings"

	"github.com/zkrebbekx/go-mermaid/internal/syntax"
)

// Element is a C4 element (Person, System, Container, Component, …).
type Element struct {
	ID       string
	Kind     string
	Label    string
	Descr    string
	Tech     string
	Boundary string
	Style    map[string]string
}

// Rel is a relationship between two elements.
type Rel struct {
	From  string
	To    string
	Label string

	// Tech is the optional technology written as the fourth argument, such
	// as the protocol in Rel(a, b, "reads", "HTTPS").
	Tech  string
	Style map[string]string
}

// Boundary groups elements and may itself belong to another boundary.
type Boundary struct{ ID, Label, Kind, Parent string }

// LayoutConfig retains Mermaid's requested row widths.
type LayoutConfig struct{ ShapeInRow, BoundaryInRow int }

// Diagram is a parsed C4 diagram.
type Diagram struct {
	Title      string
	Elements   []*Element
	Rels       []*Rel
	Boundaries []*Boundary
	Layout     LayoutConfig
}

func (d *Diagram) element(id string) *Element {
	for _, e := range d.Elements {
		if e.ID == id {
			return e
		}
	}
	return nil
}

// Parse builds a Diagram from C4 source.
func Parse(src string) (*Diagram, error) {
	d := &Diagram{}
	headerSeen := false
	var stack []string
	elementStyles := map[string]map[string]string{}
	relStyles := map[string]map[string]string{}
	for i, raw := range strings.Split(src, "\n") {
		lineNo := i + 1
		line := strings.TrimSpace(stripComment(raw))
		if line == "" {
			continue
		}
		if !headerSeen {
			if !strings.HasPrefix(strings.ToLower(line), "c4") {
				return nil, syntax.Errorf(lineNo, 1, "expected a C4 header")
			}
			headerSeen = true
			continue
		}
		switch {
		case strings.HasPrefix(line, "title "):
			d.Title = strings.TrimSpace(line[len("title "):])
		case strings.HasPrefix(line, "UpdateElementStyle("):
			args := parseArgs(line)
			if len(args) > 0 {
				elementStyles[args[0]] = namedArgs(args[1:])
			}
		case strings.HasPrefix(line, "UpdateRelStyle("):
			args := parseArgs(line)
			if len(args) > 1 {
				relStyles[args[0]+"\x00"+args[1]] = namedArgs(args[2:])
			}
		case strings.HasPrefix(line, "UpdateLayoutConfig("):
			args := namedArgs(parseArgs(line))
			d.Layout.ShapeInRow, _ = strconv.Atoi(args["c4ShapeInRow"])
			d.Layout.BoundaryInRow, _ = strconv.Atoi(args["c4BoundaryInRow"])
		case strings.HasPrefix(line, "Rel") && strings.Contains(line, "("):
			args := parseArgs(line)
			if len(args) >= 2 {
				r := &Rel{From: args[0], To: args[1]}
				if len(args) >= 3 {
					r.Label = args[2]
				}
				if len(args) >= 4 {
					r.Tech = args[3]
				}
				d.Rels = append(d.Rels, r)
			}
		case strings.HasPrefix(line, "Boundary("), strings.HasPrefix(line, "Enterprise_Boundary("), strings.HasPrefix(line, "System_Boundary("):
			args := parseArgs(line)
			if len(args) >= 2 {
				boundary := &Boundary{ID: args[0], Label: args[1], Kind: line[:strings.IndexByte(line, '(')]}
				if len(stack) > 0 {
					boundary.Parent = stack[len(stack)-1]
				}
				d.Boundaries = append(d.Boundaries, boundary)
				stack = append(stack, boundary.ID)
			}
		case strings.HasPrefix(line, "}"):
			if len(stack) > 0 {
				stack = stack[:len(stack)-1]
			}
		case line == "{":
		case strings.Contains(line, "("):
			kind := line[:strings.IndexByte(line, '(')]
			args := parseArgs(line)
			if len(args) >= 2 {
				e := &Element{ID: args[0], Kind: strings.TrimSpace(kind), Label: args[1]}
				if len(stack) > 0 {
					e.Boundary = stack[len(stack)-1]
				}
				if strings.HasPrefix(e.Kind, "Container") || strings.HasPrefix(e.Kind, "Component") {
					if len(args) >= 3 {
						e.Tech = args[2]
					}
					if len(args) >= 4 {
						e.Descr = args[3]
					}
				} else if len(args) >= 3 {
					e.Descr = args[2]
				}
				d.Elements = append(d.Elements, e)
			}
		}
	}
	if !headerSeen {
		return nil, syntax.Errorf(1, 1, "expected a C4 header")
	}
	for _, e := range d.Elements {
		e.Style = elementStyles[e.ID]
	}
	for _, r := range d.Rels {
		r.Style = relStyles[r.From+"\x00"+r.To]
	}
	return d, nil
}

// parseArgs returns the comma-separated arguments inside the first (...) on the
// line, trimming quotes from each.
func parseArgs(line string) []string {
	open := strings.IndexByte(line, '(')
	closeIdx := strings.LastIndexByte(line, ')')
	if open < 0 || closeIdx <= open {
		return nil
	}
	inner := line[open+1 : closeIdx]

	var args []string
	var cur strings.Builder
	inQuote := false
	for _, r := range inner {
		switch {
		case r == '"':
			inQuote = !inQuote
		case r == ',' && !inQuote:
			args = append(args, strings.TrimSpace(cur.String()))
			cur.Reset()
		default:
			cur.WriteRune(r)
		}
	}
	args = append(args, strings.TrimSpace(cur.String()))
	return args
}

func namedArgs(args []string) map[string]string {
	result := map[string]string{}
	for _, arg := range args {
		if i := strings.IndexByte(arg, '='); i >= 0 {
			result[strings.TrimPrefix(strings.TrimSpace(arg[:i]), "$")] = strings.TrimSpace(arg[i+1:])
		}
	}
	return result
}

func stripComment(s string) string {
	if i := strings.Index(s, "%%"); i >= 0 {
		return s[:i]
	}
	return s
}
