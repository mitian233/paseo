package render

import (
	"encoding/xml"
	"github.com/zkrebbekx/go-mermaid/internal/domain"
	"github.com/zkrebbekx/go-mermaid/internal/theme"
	"io"
	"strconv"
	"strings"
	"testing"
)

func TestImageNodeEmitsPositionedImageAndTopLabel(t *testing.T) {
	var b strings.Builder
	n := &domain.Node{ID: "img", Label: "示例", Shape: "image", Pos: domain.Point{X: 20, Y: 30}, Size: domain.Size{W: 120, H: 100}, Image: &domain.Image{URL: "https://example.org/x.png?a=1&b=2", Width: 120, Height: 80, LabelPosition: "t"}}
	writeNode(&b, n, theme.For(""), Options{FontSize: 14})
	d := xml.NewDecoder(strings.NewReader("<g>" + b.String() + "</g>"))
	imageY, labelY := -1.0, -1.0
	href := ""
	for {
		tok, err := d.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		if e, ok := tok.(xml.StartElement); ok {
			for _, a := range e.Attr {
				if e.Name.Local == "image" && a.Name.Local == "href" {
					href = a.Value
				}
				if a.Name.Local == "y" {
					v, _ := strconv.ParseFloat(a.Value, 64)
					if e.Name.Local == "image" {
						imageY = v
					}
					if e.Name.Local == "tspan" {
						labelY = v
					}
				}
			}
		}
	}
	if href != n.Image.URL || imageY <= labelY || labelY < 30 {
		t.Fatalf("href %q imageY %v labelY %v svg %s", href, imageY, labelY, b.String())
	}
}
