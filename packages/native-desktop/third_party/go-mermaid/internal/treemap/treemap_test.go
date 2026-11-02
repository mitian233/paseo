package treemap

import (
	"encoding/xml"
	"math"
	"strconv"
	"strings"
	"testing"
)

type element struct {
	XMLName  xml.Name
	Attr     []xml.Attr `xml:",any,attr"`
	Text     string     `xml:",chardata"`
	Children []element  `xml:",any"`
}

func attrs(e element) map[string]string {
	m := map[string]string{}
	for _, a := range e.Attr {
		m[a.Name.Local] = a.Value
	}
	return m
}
func rectangles(e element, out map[string]map[string]string) {
	if e.XMLName.Local == "rect" {
		a := attrs(e)
		if a["data-name"] != "" {
			out[a["data-name"]] = a
		}
	}
	for _, c := range e.Children {
		rectangles(c, out)
	}
}
func rect(t *testing.T, m map[string]map[string]string, name string) (float64, float64, float64, float64) {
	t.Helper()
	a, ok := m[name]
	if !ok {
		t.Fatalf("missing rectangle %q", name)
	}
	f := func(k string) float64 {
		n, e := strconv.ParseFloat(a[k], 64)
		if e != nil {
			t.Fatalf("%s: %v", k, e)
		}
		return n
	}
	return f("x"), f("y"), f("width"), f("height")
}
func TestProportionsAndNestedContainment(t *testing.T) {
	src := "treemap-beta\n\"Group\"\n  \"Inner\"\n    \"A\": 10\n    \"B\": 30\n  \"C\": 40\n"
	b, err := Render(src, RenderOptions{Config: map[string]any{"padding": 0, "diagramPadding": 0}})
	if err != nil {
		t.Fatal(err)
	}
	var root element
	if err = xml.Unmarshal(b, &root); err != nil {
		t.Fatal(err)
	}
	m := map[string]map[string]string{}
	rectangles(root, m)
	ix, iy, iw, ih := rect(t, m, "Inner")
	ax, ay, aw, ah := rect(t, m, "A")
	bx, by, bw, bh := rect(t, m, "B")
	if math.Abs((bw*bh)/(aw*ah)-3) > 0.01 {
		t.Fatalf("area ratio got %g", bw*bh/(aw*ah))
	}
	for _, r := range [][4]float64{{ax, ay, aw, ah}, {bx, by, bw, bh}} {
		if r[0] < ix-0.01 || r[1] < iy-0.01 || r[0]+r[2] > ix+iw+0.01 || r[1]+r[3] > iy+ih+0.01 {
			t.Fatalf("child outside parent: %v", r)
		}
	}
}
func TestStylesAndFormatting(t *testing.T) {
	src := "treemap-beta\n\"预算\":::hot\n\t\"A & B\": 1234.5:::hot\nclassDef hot fill:red,color:blue,stroke:#123456,stroke-width:2px;\n"
	b, err := Render(src, RenderOptions{Config: map[string]any{"valueFormat": "$,.2f"}})
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	for _, want := range []string{"fill=\"red\"", "fill=\"blue\"", "stroke=\"#123456\"", "stroke-width=\"2\"", "$1,234.50", "A &amp; B", "预算"} {
		if !strings.Contains(s, want) {
			t.Errorf("missing %q in %s", want, s)
		}
	}
	for _, tc := range []struct{ format, want string }{{",", "1,234.5"}, {"$", "$1234.5"}, {"$0,0", "$1,235"}, {".1f", "1234.5"}, {".1%", "123450.0%"}, {"$.1%", "$123450.0%"}} {
		b, e := Render("treemap-beta\n\"A\": 1234.5", RenderOptions{Config: map[string]any{"valueFormat": tc.format}})
		if e != nil || !strings.Contains(string(b), tc.want) {
			t.Errorf("format %q: %s (%v)", tc.format, b, e)
		}
	}
}
func TestRejectInvalidTrees(t *testing.T) {
	for _, src := range []string{"treemap-beta\n\"A\": -1", "treemap-beta\n\"A\": 0", "treemap-beta\n\"A\": NaN", "treemap-beta\n\"A\": Inf", "treemap-beta\n\"A\": 1\n  \"B\": 2", "treemap-beta\n\"A\"\n\"B\": 1", "treemap-beta\n  \"A\": 1\n \"B\": 2", "treemap-beta\n\"A\": nope"} {
		if _, err := Render(src, RenderOptions{}); err == nil {
			t.Errorf("accepted %q", src)
		}
	}
}
func TestConfigAndVisibility(t *testing.T) {
	b, e := Render("treemap-beta\n\"A\": 42", RenderOptions{Theme: "forest", Config: map[string]any{"showValues": false, "nodeWidth": 200, "nodeHeight": 100, "diagramPadding": 20, "borderWidth": 3, "labelFontSize": 19}})
	if e != nil {
		t.Fatal(e)
	}
	s := string(b)
	for _, v := range []string{"width=\"240\"", "height=\"140\"", "stroke-width=\"3\"", "font-size=\"19\"", "#cde498"} {
		if !strings.Contains(s, v) {
			t.Errorf("missing %q", v)
		}
	}
	if strings.Contains(s, ">42<") {
		t.Error("value shown when disabled")
	}
}

func TestPaddingAndValueFont(t *testing.T) {
	src := "treemap-beta\n\"A\": 1\n\"B\": 1"
	options := RenderOptions{Config: map[string]any{"padding": 12, "diagramPadding": 7, "valueFontSize": 21, "nodeWidth": 100, "nodeHeight": 100}}
	b, err := Render(src, options)
	if err != nil {
		t.Fatal(err)
	}
	again, err := Render(src, options)
	if err != nil || string(b) != string(again) {
		t.Fatal("rendering is not deterministic")
	}
	var root element
	if err = xml.Unmarshal(b, &root); err != nil {
		t.Fatal(err)
	}
	m := map[string]map[string]string{}
	rectangles(root, m)
	ax, _, aw, _ := rect(t, m, "A")
	bx, _, _, _ := rect(t, m, "B")
	if math.Abs((bx-ax-aw)-12) > 0.02 {
		t.Fatalf("padding gap is %g", bx-ax-aw)
	}
	if !strings.Contains(string(b), `font-size="21"`) {
		t.Fatal("value font size ignored")
	}
}

func TestRejectInconsistentDedent(t *testing.T) {
	_, err := Render("treemap-beta\n\"A\"\n    \"B\"\n        \"C\": 1\n  \"D\": 1", RenderOptions{})
	if err == nil {
		t.Fatal("accepted dedent to an unknown level")
	}
}
