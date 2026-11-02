package mermaid

import "testing"

func TestInvalidFrontmatterConfigurationIsRejected(t *testing.T) {
	for _, source := range []string{
		"---\nconfig:\n  treemap: [1, 2\n---\npie\n\"A\": 1",
		"---\nconfig: 42\n---\npie\n\"A\": 1",
		"---\nconfig: {treemap: {padding: 8}}\npie\n\"A\": 1",
	} {
		if _, err := parseFrontmatterConfig(source); err == nil {
			t.Errorf("malformed frontmatter silently accepted: %q", source)
		}
	}
}
