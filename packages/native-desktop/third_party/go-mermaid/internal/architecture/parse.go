package architecture

import (
	"fmt"
	"regexp"
	"strings"
)

type node struct {
	id, kind, icon, label, parent string
	x, y, w, h                    float64
}
type edge struct {
	a, b, ap, bp        string
	ag, bg, left, right bool
}
type alignment struct {
	axis string
	ids  []string
}
type Diagram struct {
	nodes  map[string]*node
	order  []*node
	edges  []edge
	aligns []alignment
}

var declaration = regexp.MustCompile(`^(group|service|junction)\s+([A-Za-z0-9_-]+)(?:\(([^()]*)\))?(?:\[([^\]]*)\])?(?:\s+in\s+([A-Za-z0-9_-]+))?\s*$`)
var edgePattern = regexp.MustCompile(`^([A-Za-z0-9_-]+)(\{group\})?:([LRTB])\s*(<)?--(>)?\s*([LRTB]):([A-Za-z0-9_-]+)(\{group\})?\s*$`)

// Parse parses the built-in architecture diagram syntax.
func Parse(src string) (*Diagram, error) {
	d := &Diagram{nodes: map[string]*node{}}
	header := false
	for lineNo, line := range strings.Split(src, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "%%") {
			continue
		}
		if !header {
			if line != "architecture-beta" {
				return nil, fmt.Errorf("line %d: expected architecture-beta", lineNo+1)
			}
			header = true
			continue
		}
		if m := declaration.FindStringSubmatch(line); m != nil {
			if _, ok := d.nodes[m[2]]; ok {
				return nil, fmt.Errorf("line %d: duplicate identifier %s", lineNo+1, m[2])
			}
			if m[5] != "" {
				p := d.nodes[m[5]]
				if p == nil || p.kind != "group" {
					return nil, fmt.Errorf("line %d: unknown parent group %q", lineNo+1, m[5])
				}
			}
			if m[1] == "junction" && (m[3] != "" || m[4] != "") {
				return nil, fmt.Errorf("line %d: junction cannot have icon or label", lineNo+1)
			}
			if m[3] != "" && m[3] != "cloud" && m[3] != "database" && m[3] != "disk" && m[3] != "internet" && m[3] != "server" {
				return nil, fmt.Errorf("line %d: unregistered icon %q", lineNo+1, m[3])
			}
			n := &node{kind: m[1], id: m[2], icon: m[3], label: m[4], parent: m[5]}
			d.nodes[n.id] = n
			d.order = append(d.order, n)
			continue
		}
		if m := edgePattern.FindStringSubmatch(line); m != nil {
			for _, id := range []string{m[1], m[7]} {
				n := d.nodes[id]
				if n == nil || n.kind == "group" {
					return nil, fmt.Errorf("line %d: unknown endpoint %q", lineNo+1, id)
				}
			}
			d.edges = append(d.edges, edge{a: m[1], ag: m[2] != "", ap: m[3], left: m[4] != "", right: m[5] != "", bp: m[6], b: m[7], bg: m[8] != ""})
			continue
		}
		fields := strings.Fields(line)
		if len(fields) > 0 && fields[0] == "align" {
			if len(fields) < 4 || (fields[1] != "row" && fields[1] != "column") {
				return nil, fmt.Errorf("line %d: invalid alignment", lineNo+1)
			}
			for _, id := range fields[2:] {
				n := d.nodes[id]
				if n == nil || n.kind == "group" {
					return nil, fmt.Errorf("line %d: unknown alignment member %q", lineNo+1, id)
				}
			}
			d.aligns = append(d.aligns, alignment{fields[1], fields[2:]})
			continue
		}
		return nil, fmt.Errorf("line %d: invalid architecture statement", lineNo+1)
	}
	if !header {
		return nil, fmt.Errorf("missing architecture-beta")
	}
	for _, n := range d.order {
		if n.parent != "" {
			p := d.nodes[n.parent]
			if p == nil || p.kind != "group" {
				return nil, fmt.Errorf("unknown parent group %q", n.parent)
			}
		}
		seen := map[string]bool{}
		for p := n; p != nil && p.parent != ""; p = d.nodes[p.parent] {
			if seen[p.id] {
				return nil, fmt.Errorf("cyclic group membership")
			}
			seen[p.id] = true
		}
	}
	for _, e := range d.edges {
		for _, end := range []struct {
			id    string
			group bool
		}{{e.a, e.ag}, {e.b, e.bg}} {
			n := d.nodes[end.id]
			if n == nil || n.kind == "group" {
				return nil, fmt.Errorf("unknown service or junction %q", end.id)
			}
			if end.group && (n.kind != "service" || n.parent == "") {
				return nil, fmt.Errorf("group endpoint requires a service in a group")
			}
		}
		if (e.ag || e.bg) && d.nodes[e.a].parent != "" && d.nodes[e.a].parent == d.nodes[e.b].parent {
			return nil, fmt.Errorf("group edge must cross a group boundary")
		}
	}
	for _, a := range d.aligns {
		seen := map[string]bool{}
		for _, id := range a.ids {
			n := d.nodes[id]
			if n == nil || n.kind == "group" {
				return nil, fmt.Errorf("unknown alignment member %q", id)
			}
			if seen[id] {
				return nil, fmt.Errorf("duplicate alignment member %q", id)
			}
			seen[id] = true
		}
	}
	return d, nil
}
