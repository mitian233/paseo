// Package block parses and renders Mermaid block-beta diagrams to SVG as a
// column grid of labeled blocks.
//
// Syntax (subset):
//
//	block-beta
//	    columns 3
//	    a b c
//	    d["wide"]:2 e
package block

import (
	"regexp"
	"strconv"
	"strings"

	"github.com/zkrebbekx/go-mermaid/internal/syntax"
)

// Block is one cell in the grid.
type Block struct {
	ID    string
	Label string
	Span  int

	// Space marks a gap in the grid rather than a drawn block. Mermaid
	// writes it as `space` or `space:2`.
	Space bool
}

// Link connects two named blocks without creating extra grid cells.
type Link struct {
	From, To, Label, Connector string
}

// Diagram is a parsed block diagram.
type Diagram struct {
	Columns int
	Rows    [][]*Block
	Links   []*Link
}

// Parse builds a Diagram from block-beta source.
func Parse(src string) (*Diagram, error) {
	d := &Diagram{Columns: 0}
	headerSeen := false
	declared := map[string]bool{}
	for _, raw := range strings.Split(src, "\n") {
		line := strings.TrimSpace(stripComment(raw))
		if line == "" || strings.HasPrefix(line, "columns ") || firstWord(line) == "block-beta" || firstWord(line) == "block" || connectorPattern.MatchString(line) {
			continue
		}
		for _, b := range parseRow(line) {
			if !b.Space {
				declared[b.ID] = true
			}
		}
	}
	seen := map[string]bool{}
	for i, raw := range strings.Split(src, "\n") {
		lineNo := i + 1
		line := strings.TrimSpace(stripComment(raw))
		if line == "" {
			continue
		}
		if !headerSeen {
			if w := strings.ToLower(firstWord(line)); w != "block-beta" && w != "block" {
				return nil, syntax.Errorf(lineNo, 1, "expected 'block-beta' header")
			}
			headerSeen = true
			continue
		}
		if strings.HasPrefix(line, "columns ") {
			if n, err := strconv.Atoi(strings.TrimSpace(line[len("columns "):])); err == nil {
				d.Columns = n
			}
			continue
		}
		row, links := parseLine(line, seen, declared)
		d.Links = append(d.Links, links...)
		if len(row) > 0 {
			d.Rows = append(d.Rows, row)
		}
	}
	if !headerSeen {
		return nil, syntax.Errorf(1, 1, "expected 'block-beta' header")
	}
	if d.Columns <= 0 {
		d.Columns = widestRow(d.Rows)
	}
	if d.Columns <= 0 {
		d.Columns = 1
	}
	return d, nil
}

var connectorPattern = regexp.MustCompile(`(<-->|<--|--\s*"[^"]*"\s*-->|--\|[^|]*\|-->|-->|---|==>|-\.->|~~~)`)

// parseLine keeps link references anchored to the original grid cells.
func parseLine(line string, seen, declared map[string]bool) ([]*Block, []*Link) {
	indices := connectorPattern.FindAllStringIndex(line, -1)
	if len(indices) == 0 {
		row := parseRow(line)
		for _, b := range row {
			if !b.Space {
				seen[b.ID] = true
			}
		}
		return row, nil
	}
	var row []*Block
	var links []*Link
	start := 0
	var previous *Block
	var pending string
	for i := 0; i <= len(indices); i++ {
		end := len(line)
		if i < len(indices) {
			end = indices[i][0]
		}
		blocks := parseRow(strings.TrimSpace(line[start:end]))
		for j, b := range blocks {
			if pending != "" && j == 0 && previous != nil && !previous.Space && !b.Space {
				label := ""
				if q := strings.IndexAny(pending, `"|`); q >= 0 {
					if end := strings.IndexByte(pending[q+1:], pending[q]); end >= 0 {
						label = pending[q+1 : q+1+end]
					}
				}
				links = append(links, &Link{From: previous.ID, To: b.ID, Label: label, Connector: pending})
			}
			if b.Space || (!seen[b.ID] && !declared[b.ID]) {
				row = append(row, b)
				if !b.Space {
					seen[b.ID] = true
				}
			}
			previous = b
		}
		if i < len(indices) {
			pending = line[indices[i][0]:indices[i][1]]
			start = indices[i][1]
		}
	}
	return row, links
}

// parseRow tokenizes a row, keeping bracketed labels intact.
func parseRow(line string) []*Block {
	var tokens []string
	var cur strings.Builder
	depth := 0
	for _, r := range line {
		switch {
		case r == '[':
			depth++
			cur.WriteRune(r)
		case r == ']':
			if depth > 0 {
				depth--
			}
			cur.WriteRune(r)
		case (r == ' ' || r == '\t') && depth == 0:
			if cur.Len() > 0 {
				tokens = append(tokens, cur.String())
				cur.Reset()
			}
		default:
			cur.WriteRune(r)
		}
	}
	if cur.Len() > 0 {
		tokens = append(tokens, cur.String())
	}

	var blocks []*Block
	for _, t := range tokens {
		if isConnector(t) {
			// An arrow between blocks is a link, not a cell. Without this it
			// was tokenized like any other word and drawn as a labelled box.
			continue
		}
		blocks = append(blocks, parseBlock(t))
	}
	return blocks
}

// isConnector reports whether a token is an arrow drawn between blocks.
func isConnector(t string) bool {
	switch t {
	case "-->", "---", "<--", "<-->", "==>", "-.->", "~~~":
		return true
	}
	return false
}

// parseBlock parses id["label"]:span into a Block.
func parseBlock(t string) *Block {
	b := &Block{Span: 1}
	// A `space` or `space:2` token leaves a gap in the grid.
	if t == "space" || strings.HasPrefix(t, "space:") {
		b.Space = true
		if i := strings.IndexByte(t, ':'); i >= 0 {
			if n, err := strconv.Atoi(t[i+1:]); err == nil {
				b.Span = n
			}
		}
		return b
	}
	// trailing :span
	if i := strings.LastIndexByte(t, ':'); i >= 0 {
		if n, err := strconv.Atoi(t[i+1:]); err == nil {
			b.Span = n
			t = t[:i]
		}
	}
	if o := strings.IndexByte(t, '['); o >= 0 && strings.HasSuffix(t, "]") {
		b.ID = strings.TrimSpace(t[:o])
		b.Label = strings.Trim(strings.TrimSuffix(t[o+1:], "]"), `"`)
	} else {
		b.ID = t
		b.Label = t
	}
	if b.Span < 1 {
		b.Span = 1
	}
	return b
}

func widestRow(rows [][]*Block) int {
	widest := 0
	for _, r := range rows {
		span := 0
		for _, b := range r {
			span += b.Span
		}
		if span > widest {
			widest = span
		}
	}
	return widest
}

func firstWord(s string) string {
	if i := strings.IndexAny(s, " \t"); i >= 0 {
		return s[:i]
	}
	return s
}

func stripComment(s string) string {
	if i := strings.Index(s, "%%"); i >= 0 {
		return s[:i]
	}
	return s
}
