package gantt

import (
	"strings"
	"testing"
)

func TestAxisFormatUsesD3DirectivesIndependentOfInputFormat(t *testing.T) {
	out, err := Render("gantt\ndateFormat DD-MM-YYYY\naxisFormat %a %d/%m/%Y %%\nTask : 06-11-2026, 1d", RenderOptions{FontSize: 14})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), "Fri 06/11/2026 %") || !strings.Contains(string(out), "Sat 07/11/2026 %") {
		t.Fatalf("unexpected axis: %s", out)
	}
}

func TestExcludedDatesDoNotExtendExplicitEnd(t *testing.T) {
	d, err := Parse("gantt\nexcludes weekends 2026-11-09\nFixed : 2026-11-06, 2026-11-10")
	if err != nil {
		t.Fatal(err)
	}
	if got := d.Tasks[0].End().Format("2006-01-02"); got != "2026-11-10" {
		t.Fatalf("explicit end moved to %s", got)
	}
}

func TestCompactDateFormatParsesExclusions(t *testing.T) {
	d, err := Parse("gantt\ndateFormat YYYYMMDD\nexcludes 20261109\nTask : 20261106, 3d")
	if err != nil {
		t.Fatal(err)
	}
	if got := d.Tasks[0].End().Format("2006-01-02"); got != "2026-11-10" {
		t.Fatalf("end = %s", got)
	}
}
