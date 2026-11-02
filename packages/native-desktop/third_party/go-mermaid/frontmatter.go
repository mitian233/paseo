package mermaid

import (
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
)

type frontmatterDocument struct {
	Title  string         `yaml:"title"`
	Config map[string]any `yaml:"config"`
	Body   string         `yaml:"-"`
}

func parseFrontmatterConfig(source string) (frontmatterDocument, error) {
	document := frontmatterDocument{Body: source}
	s := strings.TrimLeft(source, "\ufeff \t\r\n")
	firstEnd := strings.IndexByte(s, '\n')
	firstLine := s
	if firstEnd >= 0 {
		firstLine = s[:firstEnd]
	}
	if strings.TrimSpace(firstLine) != "---" {
		return document, nil
	}
	if firstEnd < 0 {
		return document, fmt.Errorf("unterminated YAML frontmatter")
	}
	for start := firstEnd + 1; start <= len(s); {
		end := len(s)
		if next := strings.IndexByte(s[start:], '\n'); next >= 0 {
			end = start + next
		}
		if strings.TrimSpace(s[start:end]) == "---" {
			if err := yaml.Unmarshal([]byte(s[firstEnd+1:start]), &document); err != nil {
				return frontmatterDocument{}, fmt.Errorf("invalid YAML frontmatter: %w", err)
			}
			bodyStart := end
			if bodyStart < len(s) {
				bodyStart++
			}
			document.Body = s[bodyStart:]
			return document, nil
		}
		if end == len(s) {
			break
		}
		start = end + 1
	}
	return frontmatterDocument{}, fmt.Errorf("unterminated YAML frontmatter")
}
