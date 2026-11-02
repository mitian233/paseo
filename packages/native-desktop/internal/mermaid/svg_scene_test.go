package mermaid

import "testing"

func TestSVGScenePreservesNestedTransformsAndSiblingScope(t *testing.T) {
	scene, err := scanSVGScene(`<svg viewBox="0 0 200 100"><text x="1" y="2">Title</text><g transform="translate(10 20)"><g transform="scale(2)"><text x="3" y="4">Nested</text><image x="2" y="3" width="4" height="5" href="data:image/png;base64,aA=="/></g></g><text x="1" y="2">After</text></svg>`)
	if err != nil {
		t.Fatal(err)
	}
	if len(scene.Texts) != 3 || len(scene.Images) != 1 {
		t.Fatalf("scene: %+v", scene)
	}
	x, y := svgPoint(scene.Texts[1].Matrix, 3, 4)
	if x != 16 || y != 28 {
		t.Fatalf("nested text position: %v,%v", x, y)
	}
	x, y = svgPoint(scene.Texts[2].Matrix, 1, 2)
	if x != 1 || y != 2 {
		t.Fatalf("closed group leaked into sibling: %v,%v", x, y)
	}
	x, y = svgPoint(scene.Images[0].Matrix, 2, 3)
	if x != 14 || y != 26 {
		t.Fatalf("image transform: %v,%v", x, y)
	}
	if scene.Texts[1].Inner != "Nested" {
		t.Fatalf("text content: %q", scene.Texts[1].Inner)
	}
}
func TestSVGTransformRejectsInvalidNumbersAndArity(t *testing.T) {
	for _, input := range []string{"translate(NaN,1)", "scale(1,2,3)", "matrix(1 0 0 1 3)", "rotate(10,2)", "unknown(1)"} {
		if _, err := parseSVGTransform(input); err == nil {
			t.Errorf("invalid transform accepted: %q", input)
		}
	}
}
