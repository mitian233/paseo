// Package gantt parses and renders Mermaid gantt charts to SVG.
//
// Syntax (subset):
//
//	gantt
//	    title Project
//	    dateFormat YYYY-MM-DD
//	    section Phase 1
//	      Design : d1, 2024-01-01, 10d
//	      Build  : after d1, 2w
package gantt

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/zkrebbekx/go-mermaid/internal/syntax"
)

// Task is a bar with a resolved start and end time.
type Task struct {
	ID       string
	Name     string
	Section  string
	AfterID  string
	UntilID  string
	StartIn  string
	EndIn    string
	Duration time.Duration
	Months   int
	end      time.Time
	Line     int // source line, for error reporting

	// Status is a tag such as done, active or crit. Milestone marks a
	// zero-length point rather than a bar.
	Status    string
	Milestone bool

	Start    time.Time
	resolved bool
}

// End returns the resolved end time.
func (t *Task) End() time.Time { return t.end }

// Diagram is a parsed gantt chart.
type Diagram struct {
	Title         string
	Layout        string // Go time layout derived from dateFormat
	Sections      []string
	AxisFormat    string
	Excludes      []string
	excludedDates map[string]bool
	TickCount     int
	TickUnit      string
	Weekday       time.Weekday
	Weekend       time.Weekday
	TodayMarker   string
	Tasks         []*Task
}

// Parse builds a Diagram from gantt source and resolves task start dates.
func Parse(src string) (*Diagram, error) {
	d := &Diagram{Layout: "2006-01-02", Weekend: time.Saturday}
	section := ""
	excludeLines := map[string]int{}

	headerSeen := false
	for i, raw := range strings.Split(src, "\n") {
		lineNo := i + 1
		line := strings.TrimSpace(raw)
		if !strings.HasPrefix(line, "axisFormat ") {
			line = strings.TrimSpace(stripComment(line))
		}
		if line == "" {
			continue
		}
		if !headerSeen {
			if firstWord(line) != "gantt" {
				return nil, syntax.Errorf(lineNo, 1, "expected 'gantt' header")
			}
			headerSeen = true
			continue
		}
		switch {
		case strings.HasPrefix(line, "title "):
			d.Title = strings.TrimSpace(line[len("title "):])
		case strings.HasPrefix(line, "dateFormat "):
			d.Layout = toGoLayout(strings.TrimSpace(line[len("dateFormat "):]))
		case strings.HasPrefix(line, "excludes "):
			tokens := strings.FieldsFunc(strings.TrimSpace(line[len("excludes "):]), func(r rune) bool { return r == ',' || r == ' ' || r == '\t' })
			for _, token := range tokens {
				if strings.EqualFold(token, "weekdays") {
					return nil, syntax.Errorf(lineNo, 1, "excludes does not support weekdays; list the individual days")
				}
			}
			d.Excludes = append(d.Excludes, tokens...)
			for _, token := range tokens {
				excludeLines[token] = lineNo
			}
		case strings.HasPrefix(line, "axisFormat "):
			d.AxisFormat = strings.TrimSpace(line[len("axisFormat "):])
		case strings.HasPrefix(line, "tickInterval "):
			value := strings.TrimSpace(line[len("tickInterval "):])
			i := 0
			for i < len(value) && value[i] >= '0' && value[i] <= '9' {
				i++
			}
			count, err := strconv.Atoi(value[:i])
			unit := value[i:]
			if err != nil || count < 1 || !validTickUnit(unit) {
				return nil, syntax.Errorf(lineNo, 1, "invalid tickInterval %q", value)
			}
			d.TickCount, d.TickUnit = count, unit
		case strings.HasPrefix(line, "weekday "):
			weekday, ok := parseWeekday(strings.TrimSpace(line[len("weekday "):]))
			if !ok {
				return nil, syntax.Errorf(lineNo, 1, "invalid weekday")
			}
			d.Weekday = weekday
		case strings.HasPrefix(line, "weekend "):
			weekday, ok := parseWeekday(strings.TrimSpace(line[len("weekend "):]))
			if !ok || (weekday != time.Friday && weekday != time.Saturday) {
				return nil, syntax.Errorf(lineNo, 1, "invalid weekend")
			}
			d.Weekend = weekday
		case strings.HasPrefix(line, "todayMarker "):
			value := strings.TrimSpace(line[len("todayMarker "):])
			if value != "off" {
				for _, property := range strings.Split(value, ",") {
					name, val, ok := strings.Cut(property, ":")
					if !ok || strings.TrimSpace(name) == "" || strings.TrimSpace(val) == "" {
						return nil, syntax.Errorf(lineNo, 1, "invalid todayMarker style")
					}
				}
			}
			d.TodayMarker = value
		case strings.HasPrefix(line, "section "):
			section = strings.TrimSpace(line[len("section "):])
			d.Sections = append(d.Sections, section)
		default:
			t, err := parseTask(line, section, lineNo)
			if err != nil {
				return nil, err
			}
			d.Tasks = append(d.Tasks, t)
		}
	}
	if !headerSeen {
		return nil, syntax.Errorf(1, 1, "expected 'gantt' header")
	}
	for _, token := range d.Excludes {
		if strings.EqualFold(token, "weekends") {
			continue
		}
		if _, ok := parseWeekday(token); ok {
			continue
		}
		date, err := parseDate(d.Layout, token)
		if err != nil {
			date, err = time.Parse("2006-01-02", token)
		}
		if err == nil {
			if d.excludedDates == nil {
				d.excludedDates = map[string]bool{}
			}
			d.excludedDates[date.Format("2006-01-02")] = true
			continue
		}
		return nil, syntax.Errorf(excludeLines[token], 1, "invalid exclusion %q", token)
	}
	if err := d.resolve(); err != nil {
		return nil, err
	}
	return d, nil
}

func parseTask(line, section string, lineNo int) (*Task, error) {
	name, rest, ok := strings.Cut(line, ":")
	if !ok {
		return nil, syntax.Errorf(lineNo, 1, "expected 'name: spec'")
	}
	t := &Task{Name: strings.TrimSpace(name), Section: section, Line: lineNo}
	var fields []string
	for _, field := range strings.Split(rest, ",") {
		f := strings.TrimSpace(field)
		if f == "" {
			continue
		}
		if isStatusTag(f) {
			if strings.EqualFold(f, "milestone") {
				t.Milestone = true
			} else {
				t.Status = strings.ToLower(f)
			}
		} else {
			fields = append(fields, f)
		}
	}
	if len(fields) == 0 && t.Milestone {
		return t, nil
	}
	if len(fields) < 1 || len(fields) > 3 {
		return nil, syntax.Errorf(lineNo, 1, "invalid task %q specification", t.Name)
	}
	if len(fields) == 3 {
		t.ID, fields = fields[0], fields[1:]
	}
	if len(fields) == 2 {
		if strings.HasPrefix(fields[0], "after ") {
			t.AfterID = strings.TrimSpace(strings.TrimPrefix(fields[0], "after "))
			if t.AfterID == "" {
				return nil, syntax.Errorf(lineNo, 1, "empty dependency")
			}
		} else {
			t.StartIn = fields[0]
		}
	}
	end := fields[len(fields)-1]
	if strings.HasPrefix(end, "until ") {
		t.UntilID = strings.TrimSpace(strings.TrimPrefix(end, "until "))
		if t.UntilID == "" {
			return nil, syntax.Errorf(lineNo, 1, "empty until dependency")
		}
	} else if duration, months, ok := parseDuration(end); ok {
		t.Duration, t.Months = duration, months
	} else {
		t.EndIn = end
	}
	return t, nil
}

// isStatusTag reports whether a field is one of the Mermaid task tags rather
// than an id, a date or a duration.
func isStatusTag(f string) bool {
	switch strings.ToLower(f) {
	case "done", "active", "crit", "milestone":
		return true
	}
	return false
}

// resolve computes task starts and ends, including forward dependencies.
func (d *Diagram) resolve() error {
	byID := map[string]*Task{}
	for _, task := range d.Tasks {
		if task.ID != "" {
			byID[task.ID] = task
		}
	}
	for {
		progress := false
		var pending *Task
		for i, task := range d.Tasks {
			if task.resolved {
				continue
			}
			pending = task
			var start time.Time
			if task.StartIn != "" {
				var err error
				start, err = parseDate(d.Layout, task.StartIn)
				if err != nil {
					return syntax.Errorf(task.Line, 1, "task %q has invalid start date %q", task.Name, task.StartIn)
				}
			} else if task.AfterID != "" {
				ready := true
				for _, id := range strings.Fields(task.AfterID) {
					dep, ok := byID[id]
					if !ok {
						return syntax.Errorf(task.Line, 1, "task %q depends on unknown task %q", task.Name, id)
					}
					if !dep.resolved {
						ready = false
						continue
					}
					if start.IsZero() || dep.End().After(start) {
						start = dep.End()
					}
				}
				if !ready {
					continue
				}
			} else if i > 0 {
				if !d.Tasks[i-1].resolved {
					continue
				}
				start = d.Tasks[i-1].End()
			} else {
				now := time.Now().UTC()
				start = time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
			}
			task.Start = start
			if task.UntilID != "" {
				ready := true
				for _, id := range strings.Fields(task.UntilID) {
					dep, ok := byID[id]
					if !ok {
						return syntax.Errorf(task.Line, 1, "task %q depends on unknown task %q", task.Name, id)
					}
					if !dep.resolved {
						ready = false
						continue
					}
					if task.end.IsZero() || dep.Start.Before(task.end) {
						task.end = dep.Start
					}
				}
				if !ready {
					task.end = time.Time{}
					continue
				}
			} else if task.EndIn != "" {
				var err error
				task.end, err = parseDate(d.Layout, task.EndIn)
				if err != nil {
					return syntax.Errorf(task.Line, 1, "task %q has invalid end date %q", task.Name, task.EndIn)
				}
			} else {
				task.end = start.AddDate(0, task.Months, 0).Add(task.Duration)
				if err := d.extendDuration(task); err != nil {
					return err
				}
			}
			if task.end.Before(start) {
				return syntax.Errorf(task.Line, 1, "task %q ends before it starts", task.Name)
			}
			task.resolved, progress = true, true
		}
		if pending == nil {
			return nil
		}
		if !progress {
			return syntax.Errorf(pending.Line, 1, "circular task dependency at %q", pending.Name)
		}
	}
}

// Duration exclusions extend the bar for each excluded day after its start.
func (d *Diagram) extendDuration(t *Task) error {
	if len(d.Excludes) == 0 || !t.end.After(t.Start) {
		return nil
	}
	for date, added := t.Start.AddDate(0, 0, 1), 0; !date.After(t.end); date = date.AddDate(0, 0, 1) {
		if d.excluded(date) {
			t.end = t.end.AddDate(0, 0, 1)
			added++
			if added > 10000 {
				return syntax.Errorf(t.Line, 1, "task %q cannot find a non-excluded date", t.Name)
			}
		}
	}
	return nil
}

func (d *Diagram) excluded(date time.Time) bool {
	if d.excludedDates[date.Format("2006-01-02")] {
		return true
	}
	for _, token := range d.Excludes {
		if strings.EqualFold(token, "weekends") && (date.Weekday() == d.Weekend || date.Weekday() == (d.Weekend+1)%7) {
			return true
		}
		if strings.EqualFold(token, date.Weekday().String()) || token == date.Format(d.Layout) || token == date.Format("2006-01-02") {
			return true
		}
	}
	return false
}

// Bounds returns the earliest start and latest end across tasks.
func (d *Diagram) Bounds() (lo, hi time.Time) {
	first := true
	for _, t := range d.Tasks {
		if t.Start.IsZero() {
			continue
		}
		if first {
			lo, hi = t.Start, t.End()
			first = false
			continue
		}
		if t.Start.Before(lo) {
			lo = t.Start
		}
		if t.End().After(hi) {
			hi = t.End()
		}
	}
	return lo, hi
}

func parseDuration(value string) (time.Duration, int, bool) {
	i := 0
	for i < len(value) && ((value[i] >= '0' && value[i] <= '9') || value[i] == '.') {
		i++
	}
	if i == 0 || i == len(value) {
		return 0, 0, false
	}
	n, err := strconv.ParseFloat(value[:i], 64)
	if err != nil || n < 0 {
		return 0, 0, false
	}
	var unit time.Duration
	switch value[i:] {
	case "ms":
		unit = time.Millisecond
	case "s":
		unit = time.Second
	case "m":
		unit = time.Minute
	case "h":
		unit = time.Hour
	case "d":
		unit = 24 * time.Hour
	case "w":
		unit = 7 * 24 * time.Hour
	case "M", "y":
		if value[i:] == "y" {
			n *= 12
		}
		months := int(n)
		return time.Duration((n - float64(months)) * 30 * float64(24*time.Hour)), months, true
	default:
		return 0, 0, false
	}
	if n*float64(unit) > float64(int64(^uint64(0)>>1)) {
		return 0, 0, false
	}
	return time.Duration(n * float64(unit)), 0, true
}

func validTickUnit(unit string) bool {
	switch unit {
	case "millisecond", "second", "minute", "hour", "day", "week", "month":
		return true
	}
	return false
}

func parseWeekday(s string) (time.Weekday, bool) {
	for day := time.Sunday; day <= time.Saturday; day++ {
		if strings.EqualFold(s, day.String()) {
			return day, true
		}
	}
	return 0, false
}

// Missing calendar fields in a time-only format use the current date, as in dayjs.
func parseDate(layout, value string) (time.Time, error) {
	if layout == "X" || layout == "x" {
		value, err := strconv.ParseFloat(value, 64)
		if err != nil {
			return time.Time{}, err
		}
		if layout == "x" {
			value /= 1000
		}
		seconds := int64(value)
		return time.Unix(seconds, int64((value-float64(seconds))*1e9)).UTC(), nil
	}
	for i := 1; i+1 < len(value); i++ {
		if value[i-1] < '0' || value[i-1] > '9' {
			continue
		}
		suffix := value[i : i+2]
		if suffix == "st" || suffix == "nd" || suffix == "rd" || suffix == "th" {
			value = value[:i] + value[i+2:]
			i--
		}
	}
	quarter := strings.Contains(layout, "@Q@")
	parseLayout := strings.ReplaceAll(layout, "@Q@", "1")
	t, err := time.Parse(parseLayout, value)
	if err != nil {
		return t, err
	}
	if quarter {
		if t.Month() > 4 {
			return time.Time{}, fmt.Errorf("invalid quarter")
		}
		t = t.AddDate(0, 2*(int(t.Month())-1), 0)
	}
	if t.Year() != 0 {
		return t, nil
	}
	now := time.Now().In(t.Location())
	month, day := t.Month(), t.Day()
	if !quarter && !strings.Contains(layout, "01") && !strings.Contains(layout, "Jan") {
		month = now.Month()
	}
	if !strings.Contains(layout, "02") && !strings.Contains(layout, "_2") {
		day = now.Day()
	}
	return time.Date(now.Year(), month, day, t.Hour(), t.Minute(), t.Second(), t.Nanosecond(), t.Location()), nil
}

// toGoLayout converts a Mermaid dateFormat to a Go time layout.
func toGoLayout(format string) string {
	r := strings.NewReplacer("YYYY", "2006", "YY", "06", "Q", "@Q@", "MMMM", "January", "MMM", "Jan", "MM", "01", "M", "1", "DDDD", "002", "DDD", "002", "Do", "2", "DD", "02", "D", "2", "HH", "15", "H", "15", "hh", "03", "h", "3", "mm", "04", "m", "4", "ss", "05", "s", "5", "SSS", "000", "A", "PM", "a", "pm", "ZZ", "-0700", "Z", "-07:00")
	return r.Replace(format)
}

func firstWord(s string) string {
	if i := strings.IndexAny(s, " \t"); i >= 0 {
		return s[:i]
	}
	return s
}

func stripComment(s string) string {
	if i := strings.Index(s, "%%"); i >= 0 {
		return s[:i]
	}
	return s
}
