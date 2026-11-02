package sequence

import (
	"strings"
	"testing"
)

func TestParticipantBoxesContainHeadersAndKeepFrameRows(t *testing.T) {
	src := "sequenceDiagram\nparticipant Outside\nbox rgb(240, 240, 240) Backend services with a long label\nparticipant API as Gateway\nactor DB as Storage\nend\nloop retry\nAPI->>DB: Query\nend\nOutside->>API: Call"
	d, err := Parse(src)
	if err != nil {
		t.Fatal(err)
	}
	if len(d.Boxes) != 1 {
		t.Fatalf("boxes: %#v", d.Boxes)
	}
	box := d.Boxes[0]
	if box.Label != "Backend services with a long label" || box.Color != "rgb(240, 240, 240)" || len(box.Participants) != 2 || box.Participants[0] != "API" || box.Participants[1] != "DB" {
		t.Fatalf("box: %#v", box)
	}
	if len(d.Frames) != 1 || d.Frames[0].StartRow != 0 || d.Frames[0].EndRow != 0 || d.Messages[1].Row != 1 {
		t.Fatalf("frame or messages lost: %#v %#v", d.Frames, d.Messages)
	}
	lay := Compute(d, Options{FontSize: 14})
	if box.X > d.Participants[1].X-d.Participants[1].Width/2 || box.X+box.Width < d.Participants[2].X+d.Participants[2].Width/2 || box.X < -lay.OffsetX || box.X+box.Width > lay.Width-lay.OffsetX {
		t.Fatalf("box outside layout or participants: %#v layout %#v", box, lay)
	}
	out := string(svg(lay, RenderOptions{FontSize: 14}))
	for _, s := range []string{"Backend services with a long label", "rgb(240, 240, 240)", "Gateway", "Storage", "Query", "retry"} {
		if !strings.Contains(out, s) {
			t.Errorf("missing %q", s)
		}
	}
}

func TestParticipantBoxWithoutColor(t *testing.T) {
	d, err := Parse("sequenceDiagram\nbox Team\nparticipant A\nend\nA->>A: work")
	if err != nil {
		t.Fatal(err)
	}
	if len(d.Boxes) != 1 || d.Boxes[0].Label != "Team" || d.Boxes[0].Color != "" {
		t.Fatalf("boxes: %#v", d.Boxes)
	}
}
