package gantt

import (
	"encoding/xml"
	"fmt"
	"io"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestSubdayDurationsAndLatestDependency(t *testing.T) {
	src := "gantt\ndateFormat YYYY-MM-DD HH:mm:ss\naxisFormat %H:%M:%S\nEarly :a, 2026-11-06 08:00:00, 90m\nLate :b, 2026-11-06 08:30:00, 2h\nTogether :c, after a b, 30s\nNext :after c, 15m"
	d, err := Parse(src)
	if err != nil {
		t.Fatal(err)
	}
	want := [][2]string{{"08:00:00", "09:30:00"}, {"08:30:00", "10:30:00"}, {"10:30:00", "10:30:30"}, {"10:30:30", "10:45:30"}}
	for i, task := range d.Tasks {
		if task.Start.Format("15:04:05") != want[i][0] || task.End().Format("15:04:05") != want[i][1] {
			t.Errorf("%s: %s-%s want %v", task.Name, task.Start, task.End(), want[i])
		}
	}
	out := string(svg(d, RenderOptions{FontSize: 14}))
	for _, text := range []string{"08:00:00", "10:45:30"} {
		if !strings.Contains(out, text) {
			t.Errorf("missing axis label %q", text)
		}
	}
	decoder := xml.NewDecoder(strings.NewReader(out))
	var bars []map[string]string
	for {
		token, err := decoder.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		if element, ok := token.(xml.StartElement); ok && element.Name.Local == "rect" {
			attrs := map[string]string{}
			for _, attr := range element.Attr {
				attrs[attr.Name.Local] = attr.Value
			}
			if attrs["rx"] == "3" {
				bars = append(bars, attrs)
			}
		}
	}
	if len(bars) != 4 {
		t.Fatalf("expected four task bars, got %d", len(bars))
	}
	x0, _ := strconv.ParseFloat(bars[0]["x"], 64)
	x1, _ := strconv.ParseFloat(bars[1]["x"], 64)
	w0, _ := strconv.ParseFloat(bars[0]["width"], 64)
	w1, _ := strconv.ParseFloat(bars[1]["width"], 64)
	if x1 <= x0 || w1 <= w0 {
		t.Errorf("subday bars did not reflect times and durations: %#v", bars)
	}
}

func TestWeekTicksStartOnConfiguredWeekday(t *testing.T) {
	src := "gantt\ndateFormat YYYY-MM-DD\naxisFormat %Y-%m-%d\ntickInterval 1week\nweekday monday\nTask :2026-11-01, 16d"
	out, err := Render(src, RenderOptions{FontSize: 14})
	if err != nil {
		t.Fatal(err)
	}
	for _, label := range []string{"2026-11-02", "2026-11-09", "2026-11-16"} {
		if !strings.Contains(string(out), label) {
			t.Errorf("missing tick %s", label)
		}
	}
	if strings.Contains(string(out), "2026-11-08") {
		t.Error("week starts on Sunday")
	}
}

func TestTodayMarkerStyleAndOff(t *testing.T) {
	now := time.Now().UTC()
	start := now.AddDate(0, 0, -1).Format("2006-01-02")
	base := fmt.Sprintf("gantt\ndateFormat YYYY-MM-DD\n%s\nTask :%s, 3d", "%s", start)
	out, err := Render(fmt.Sprintf(base, "todayMarker stroke:#0f0,stroke-width:5px,opacity:0.5"), RenderOptions{FontSize: 14})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), `class="today-marker"`) || !strings.Contains(string(out), "stroke:#0f0") {
		t.Fatalf("missing styled marker: %s", out)
	}
	out, err = Render(fmt.Sprintf(base, "todayMarker off"), RenderOptions{FontSize: 14})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(out), `class="today-marker"`) {
		t.Fatal("disabled marker rendered")
	}
}

func TestInvalidGanttDirectivesAreErrors(t *testing.T) {
	for _, directive := range []string{"tickInterval 0week", "tickInterval 2fortnight", "weekday someday", "todayMarker nonsense"} {
		t.Run(directive, func(t *testing.T) {
			_, err := Parse("gantt\n" + directive + "\nTask :2026-11-06, 1d")
			if err == nil {
				t.Fatalf("accepted %s", directive)
			}
		})
	}
}

func TestForwardMultipleDependenciesAndExplicitEndTime(t *testing.T) {
	src := "gantt\ndateFormat YYYY-MM-DD HH:mm\nCombined :after early late, 30m\nEarly :early, 2026-11-06 08:00, 2026-11-06 09:15\nLate :late, 2026-11-06 08:00, 2h"
	d, err := Parse(src)
	if err != nil {
		t.Fatal(err)
	}
	if got := d.Tasks[0].Start.Format("15:04"); got != "10:00" {
		t.Fatalf("combined start = %s", got)
	}
	if got := d.Tasks[1].End().Format("15:04"); got != "09:15" {
		t.Fatalf("explicit end = %s", got)
	}
}

func TestFridayWeekendAndWeekDuration(t *testing.T) {
	d, err := Parse("gantt\nweekend friday\nexcludes weekends\nTask :2026-11-05, 1w")
	if err != nil {
		t.Fatal(err)
	}
	if got := d.Tasks[0].End().Format("2006-01-02"); got != "2026-11-16" {
		t.Fatalf("end = %s", got)
	}
}

func TestMilestoneUsesDurationMidpoint(t *testing.T) {
	src := "gantt\ndateFormat YYYY-MM-DD HH:mm\nStart :2026-11-06 08:00, 4h\nPoint :milestone, 2026-11-06 09:00, 2h"
	out, err := Render(src, RenderOptions{FontSize: 14})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), `<polygon`) || !strings.Contains(string(out), `450,`) {
		t.Fatalf("milestone not centered at 10:00: %s", out)
	}
}

func TestTimeOnlyInputUsesCurrentDate(t *testing.T) {
	d, err := Parse("gantt\ndateFormat HH:mm:ss\nTask :08:00:00, 30m")
	if err != nil {
		t.Fatal(err)
	}
	if d.Tasks[0].Start.Format("2006-01-02") != time.Now().UTC().Format("2006-01-02") {
		t.Fatalf("wrong date: %s", d.Tasks[0].Start)
	}
}

func TestUnixTimestampInput(t *testing.T) {
	d, err := Parse("gantt\ndateFormat X\nTask : 0, 90s")
	if err != nil {
		t.Fatal(err)
	}
	if d.Tasks[0].Start.Unix() != 0 || d.Tasks[0].End().Unix() != 90 {
		t.Fatalf("unexpected timestamp range %s to %s", d.Tasks[0].Start, d.Tasks[0].End())
	}
}

func TestDayOfYearAndOrdinalDateFormats(t *testing.T) {
	for _, tc := range []struct{ format, value, want string }{
		{"YYYY-DDD", "2026-310", "2026-11-06"},
		{"YYYY-MM-Do", "2026-11-6th", "2026-11-06"},
	} {
		d, err := Parse("gantt\ndateFormat " + tc.format + "\nTask :" + tc.value + ", 1d")
		if err != nil {
			t.Fatal(err)
		}
		if got := d.Tasks[0].Start.Format("2006-01-02"); got != tc.want {
			t.Errorf("%s: got %s want %s", tc.format, got, tc.want)
		}
	}
}

func TestOrdinalFormattedExcludedDate(t *testing.T) {
	d, err := Parse("gantt\ndateFormat YYYY-MM-Do\nexcludes 2026-11-7th\nTask :2026-11-6th, 1d")
	if err != nil {
		t.Fatal(err)
	}
	if got := d.Tasks[0].End().Format("2006-01-02"); got != "2026-11-08" {
		t.Fatalf("end = %s", got)
	}
}

func TestQuarterDateFormat(t *testing.T) {
	d, err := Parse("gantt\ndateFormat YYYY-Q\nTask :2026-4, 1d")
	if err != nil {
		t.Fatal(err)
	}
	if got := d.Tasks[0].Start.Format("2006-01-02"); got != "2026-10-01" {
		t.Fatalf("quarter start = %s", got)
	}
}
