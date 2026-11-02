package mermaid

import (
	"strconv"
	"strings"
	"unicode"
)

// These caps bound the upstream parser and its potentially quadratic graph
// layout before either receives untrusted chat content.
const maxSourceBytes = 16 << 10
const maxSyntaxTokens = 768
const maxSVGBytes = 1 << 20

func validateSource(source string) error {
	if len(source) > maxSourceBytes {
		return ErrTooComplex
	}
	tokens := 0
	inWord := false
	for _, ch := range source {
		if unicode.IsLetter(ch) || unicode.IsDigit(ch) || ch == '_' {
			if !inWord {
				tokens++
			}
			inWord = true
		} else {
			inWord = false
			if !unicode.IsSpace(ch) {
				tokens++
			}
		}
		if tokens > maxSyntaxTokens {
			return ErrTooComplex
		}
	}
	return nil
}

func validateRasterBudget(source string) error {
	header, frontmatter := false, false
	for _, raw := range strings.Split(source, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "%%") {
			continue
		}
		if !header {
			if line == "---" {
				frontmatter = !frontmatter
				continue
			}
			if frontmatter || strings.HasPrefix(line, "accTitle:") || strings.HasPrefix(line, "accDescr:") {
				continue
			}
			kind := strings.ToLower(strings.Fields(line)[0])
			if kind != "packet" && kind != "packet-beta" {
				return nil
			}
			header = true
			continue
		}
		rng, _, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		for _, part := range strings.Split(rng, "-") {
			if n, err := strconv.ParseUint(strings.TrimSpace(part), 10, 64); err == nil && n > 2047 {
				return ErrTooComplex
			}
		}
	}
	return nil
}
