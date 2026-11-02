package gantt

import (
	"strings"
	"testing"
)

func TestExclusionsExtendDurationsAndDependenciesSkipExcludedStarts(t *testing.T) {
	src := "gantt\ndateFormat DD/MM/YYYY\naxisFormat %d/%m\nexcludes weekends, 09/11/2026\nexcludes tuesday\nsection Work\nFirst :a, 06/11/2026, 2d\nNext :b, after a, 1d\nFixed : 06/11/2026, 12/11/2026\nMilestone :milestone, after b, 0d"
	d, err := Parse(src)
	if err != nil {
		t.Fatal(err)
	}
	want := [][2]string{{"2026-11-06", "2026-11-12"}, {"2026-11-12", "2026-11-13"}, {"2026-11-06", "2026-11-12"}, {"2026-11-13", "2026-11-13"}}
	for i, task := range d.Tasks {
		if task.Start.Format("2006-01-02") != want[i][0] || task.End().Format("2006-01-02") != want[i][1] {
			t.Errorf("%s: %v to %v, want %v", task.Name, task.Start, task.End(), want[i])
		}
	}
	out := string(svg(d, RenderOptions{FontSize: 14}))
	if !strings.Contains(out, "06/11") || !strings.Contains(out, "13/11") {
		t.Errorf("axis format not rendered: %s", out)
	}
	if strings.Count(out, `class="excluded-day"`) != 4 {
		t.Errorf("expected weekend and two named exclusion bands: %s", out)
	}
}

func TestDependencyMovesPastWeekend(t *testing.T) {
	d, err := Parse("gantt\nexcludes weekends\nFirst :a, 2026-11-06, 1d\nNext :after a, 1d")
	if err != nil {
		t.Fatal(err)
	}
	if got := d.Tasks[1].Start.Format("2006-01-02"); got != "2026-11-09" {
		t.Fatalf("dependent starts %s", got)
	}
}

func TestInvalidWeekdaysExclusionReturnsError(t *testing.T) {
	_, err := Parse("gantt\nexcludes weekdays\nTask :2026-11-06, 2d")
	if err == nil {
		t.Fatal("invalid weekdays exclusion was silently ignored")
	}
}
