package render

import (
	"encoding/xml"
	"github.com/zkrebbekx/go-mermaid/internal/domain"
	"github.com/zkrebbekx/go-mermaid/internal/theme"
	"strings"
	"testing"
)

func TestModernShapesRenderDistinctOutlinesAndLabels(t *testing.T) {
	cases := []struct {
		shape   domain.Shape
		element string
	}{
		{"hourglass", "polygon"}, {"doc", "path"}, {"docs", "path"}, {"bow-rect", "path"},
		{"sl-rect", "polygon"}, {"brace", "path"}, {"f-circ", "circle"}, {"fork", "rect"},
	}
	for _, c := range cases {
		t.Run(string(c.shape), func(t *testing.T) {
			var b strings.Builder
			writeNode(&b, &domain.Node{ID: "a", Label: "中文", Shape: c.shape, Size: domain.Size{W: 120, H: 60}}, theme.For(""), Options{FontSize: 14})
			dec := xml.NewDecoder(strings.NewReader("<g>" + b.String() + "</g>"))
			found, label := false, false
			for {
				tok, err := dec.Token()
				if err != nil {
					break
				}
				switch v := tok.(type) {
				case xml.StartElement:
					if v.Name.Local == c.element {
						found = true
					}
				case xml.CharData:
					if strings.Contains(string(v), "中文") {
						label = true
					}
				}
			}
			if !found || !label {
				t.Fatalf("outline %s=%v label=%v: %s", c.element, found, label, b.String())
			}
		})
	}
}

func TestHourglassVerticesCrossAtCenter(t *testing.T) {
	var b strings.Builder
	writeNode(&b, &domain.Node{ID: "a", Label: "Collate", Shape: "hourglass", Size: domain.Size{W: 100, H: 80}}, theme.For(""), Options{FontSize: 14})
	d := xml.NewDecoder(strings.NewReader("<g>" + b.String() + "</g>"))
	for {
		tok, err := d.Token()
		if err != nil {
			break
		}
		if e, ok := tok.(xml.StartElement); ok && e.Name.Local == "polygon" {
			for _, a := range e.Attr {
				if a.Name.Local == "points" {
					got := strings.Fields(a.Value)
					want := []string{"0,0", "100,0", "0,80", "100,80"}
					if len(got) != len(want) {
						t.Fatalf("vertices: %v", got)
					}
					for i := range want {
						if got[i] != want[i] {
							t.Fatalf("vertices: %v", got)
						}
					}
					return
				}
			}
		}
	}
	t.Fatal("hourglass polygon missing")
}
