package architecture

import (
	"strings"
	"testing"
)

func TestAlignedSiblingsAndNestedBounds(t *testing.T) {
	d, err := Parse("architecture-beta\ngroup outer(cloud)[Outer]\ngroup inner(server)[Inner] in outer\nservice a(database)[Alpha] in inner\nservice b(database)[Beta] in inner\nservice c(database)[Gamma] in inner\nalign row a b c")
	if err != nil {
		t.Fatal(err)
	}
	if err = layout(d, RenderOptions{FontSize: 16}); err != nil {
		t.Fatal(err)
	}
	a, b, c := d.nodes["a"], d.nodes["b"], d.nodes["c"]
	if a.y != b.y || b.y != c.y || a.x+a.w > b.x || b.x+b.w > c.x {
		t.Fatal("aligned siblings overlap or are out of order")
	}
	inner, outer := d.nodes["inner"], d.nodes["outer"]
	if !contains(inner, a) || !contains(inner, c) || !contains(outer, inner) {
		t.Fatal("nested bounds do not contain descendants")
	}
}
func TestGroupEdgesJunctionsAndLabels(t *testing.T) {
	src := "architecture-beta\ngroup g(cloud)[Cloud Group]\nservice a(server)[Server Label] in g\njunction j\na{group}:R <--> L:j"
	svg, err := Render(src, RenderOptions{FontFace: "sans-serif", FontSize: 16, Padding: 10})
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range []string{"Cloud Group", "Server Label", "marker-start", "marker-end", "<path"} {
		if !strings.Contains(string(svg), s) {
			t.Errorf("missing %s", s)
		}
	}
}
func TestInvalidReferencesCyclesAndAlignment(t *testing.T) {
	for _, src := range []string{
		"architecture-beta\nservice a in missing",
		"architecture-beta\ngroup a in b\ngroup b in a",
		"architecture-beta\nservice a\na:R -- L:missing",
		"architecture-beta\nservice a\nservice b\na:L -- R:b\nalign row a b",
		"architecture-beta\nservice a\nservice b\nalign row a b\nalign row b a",
		"architecture-beta\nservice a(unknown)[A]",
	} {
		if _, err := Render(src, RenderOptions{FontSize: 16}); err == nil {
			t.Errorf("accepted %q", src)
		}
	}
}

func TestLayoutConfiguration(t *testing.T) {
	source := "architecture-beta\nservice a(server)[A]\nservice b(server)[B]\nservice c(server)[C]\nservice d(server)[D]"
	render := func(config map[string]any) string {
		b, err := Render(source, RenderOptions{FontSize: 16, Config: config})
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	a := render(map[string]any{"randomize": true, "seed": 17})
	if a != render(map[string]any{"randomize": true, "seed": 17}) {
		t.Fatal("seeded layout is not reproducible")
	}
	if a == render(map[string]any{"randomize": true, "seed": 23}) {
		t.Fatal("seed did not change free node layout")
	}
	chain := "architecture-beta\nservice a(server)[A]\nservice b(server)[B]\na:R -- L:b"
	b, err := Render(chain, RenderOptions{FontSize: 16, Config: map[string]any{"edgeElasticity": 0.1}})
	if err != nil {
		t.Fatal(err)
	}
	c, err := Render(chain, RenderOptions{FontSize: 16, Config: map[string]any{"edgeElasticity": 0.9}})
	if err != nil {
		t.Fatal(err)
	}
	if string(b) == string(c) {
		t.Fatal("edge elasticity did not affect layout")
	}
	if _, err = Render(chain, RenderOptions{FontSize: 16, Config: map[string]any{"numIter": 1}}); err == nil {
		t.Fatal("iteration limit was ignored")
	}
}

func TestBuiltinIconsAndEscapedLabels(t *testing.T) {
	for _, icon := range []string{"cloud", "database", "disk", "internet", "server"} {
		svg, err := Render("architecture-beta\nservice item("+icon+")[A & B]", RenderOptions{FontSize: 16})
		if err != nil {
			t.Fatal(err)
		}
		s := string(svg)
		if !strings.Contains(s, "A &amp; B") || !strings.Contains(s, `stroke-linejoin="round"`) {
			t.Errorf("icon %s or label missing", icon)
		}
	}
}
