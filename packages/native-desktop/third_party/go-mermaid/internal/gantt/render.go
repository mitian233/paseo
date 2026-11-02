package gantt

import (
	"fmt"
	"strings"
	"time"

	"github.com/zkrebbekx/go-mermaid/internal/svgutil"
	"github.com/zkrebbekx/go-mermaid/internal/theme"
)

// RenderOptions controls gantt appearance.
type RenderOptions struct {
	Theme    string
	FontFace string
	FontSize float64
	Padding  float64
	Title    string
}

const (
	labelW = 170.0
	chartW = 560.0
	rowH   = 26.0
	axisH  = 22.0
)

var sectionColors = []string{"#5B8FF9", "#61DDAA", "#F6BD16", "#7262FD", "#F6903D", "#008685"}

// Render parses and renders gantt source to SVG.
func Render(src string, o RenderOptions) ([]byte, error) {
	d, err := Parse(src)
	if err != nil {
		return nil, err
	}
	if o.Title == "" {
		o.Title = d.Title
	}
	return svg(d, o), nil
}

func svg(d *Diagram, o RenderOptions) []byte {
	pal := theme.For(o.Theme)
	pad := o.Padding
	titleH := svgutil.TitleHeight(o.Title, o.FontSize)

	minT, maxT := d.Bounds()
	totalDays := maxT.Sub(minT).Hours() / 24
	if totalDays <= 0 {
		totalDays = 1
	}

	chartLeft := pad + labelW
	chartTop := pad + titleH + axisH
	w := chartLeft + chartW + pad
	h := chartTop + float64(len(d.Tasks))*rowH + pad

	dayX := func(t time.Time) float64 {
		days := t.Sub(minT).Hours() / 24
		return chartLeft + days/totalDays*chartW
	}

	var b strings.Builder
	fmt.Fprintf(&b, `<svg xmlns="http://www.w3.org/2000/svg" width="%s" height="%s" viewBox="0 0 %s %s" font-family="%s" font-size="%s">`,
		svgutil.Num(w), svgutil.Num(h), svgutil.Num(w), svgutil.Num(h), svgutil.Esc(o.FontFace), svgutil.Num(o.FontSize))
	b.WriteByte('\n')
	fmt.Fprintf(&b, `  <rect width="100%%" height="100%%" fill="%s"/>`, pal.Background)
	b.WriteByte('\n')
	if o.Title != "" {
		fmt.Fprintf(&b, `  <text x="%s" y="%s" fill="%s" text-anchor="middle" font-weight="bold">%s</text>`,
			svgutil.Num(w/2), svgutil.Num(pad+o.FontSize), pal.Text, svgutil.Esc(o.Title))
		b.WriteByte('\n')
	}
	if !minT.IsZero() && len(d.Excludes) > 0 {
		day := time.Date(minT.Year(), minT.Month(), minT.Day(), 0, 0, 0, 0, minT.Location())
		for ; day.Before(maxT); day = day.AddDate(0, 0, 1) {
			if !d.excluded(day) {
				continue
			}
			start, end := day, day.AddDate(0, 0, 1)
			if start.Before(minT) {
				start = minT
			}
			if end.After(maxT) {
				end = maxT
			}
			fmt.Fprintf(&b, `  <rect class="excluded-day" x="%s" y="%s" width="%s" height="%s" fill="%s" fill-opacity="0.18"/>`, svgutil.Num(dayX(start)), svgutil.Num(chartTop), svgutil.Num(dayX(end)-dayX(start)), svgutil.Num(float64(len(d.Tasks))*rowH), pal.NodeStroke)
			b.WriteByte('\n')
		}
	}

	if !minT.IsZero() {
		for _, tick := range axisTicks(minT, maxT, d) {
			x := dayX(tick)
			fmt.Fprintf(&b, `  <line x1="%s" y1="%s" x2="%s" y2="%s" stroke="%s" stroke-opacity="0.25"/>`, svgutil.Num(x), svgutil.Num(chartTop), svgutil.Num(x), svgutil.Num(chartTop+float64(len(d.Tasks))*rowH), pal.NodeStroke)
			anchor := "middle"
			if tick.Equal(minT) {
				anchor = "start"
			} else if tick.Equal(maxT) {
				anchor = "end"
			}
			fmt.Fprintf(&b, `  <text x="%s" y="%s" fill="%s" text-anchor="%s">%s</text>`, svgutil.Num(x), svgutil.Num(chartTop-6), pal.Text, anchor, svgutil.Esc(formatAxis(tick, d.AxisFormat)))
			b.WriteByte('\n')
		}
	}
	fmt.Fprintf(&b, `  <line x1="%s" y1="%s" x2="%s" y2="%s" stroke="%s"/>`,
		svgutil.Num(chartLeft), svgutil.Num(chartTop), svgutil.Num(chartLeft+chartW), svgutil.Num(chartTop), pal.NodeStroke)
	b.WriteByte('\n')

	sectionIndex := map[string]int{}
	for i, s := range d.Sections {
		sectionIndex[s] = i
	}

	for i, t := range d.Tasks {
		y := chartTop + float64(i)*rowH + 3
		bh := rowH - 6
		color := sectionColors[sectionIndex[t.Section]%len(sectionColors)]

		// Task name in the left gutter.
		fmt.Fprintf(&b, `  <text x="%s" y="%s" fill="%s">%s</text>`,
			svgutil.Num(pad), svgutil.Num(y+bh/2+o.FontSize*0.35), pal.Text, svgutil.Esc(t.Name))
		b.WriteByte('\n')

		if t.Start.IsZero() {
			continue
		}
		x1, x2 := dayX(t.Start), dayX(t.End())
		if t.Milestone {
			mid := (x1 + x2) / 2
			cy := y + bh/2
			r := bh / 2
			fmt.Fprintf(&b, `  <polygon points="%s,%s %s,%s %s,%s %s,%s" fill="%s" stroke="%s"/>`, svgutil.Num(mid), svgutil.Num(cy-r), svgutil.Num(mid+r), svgutil.Num(cy), svgutil.Num(mid), svgutil.Num(cy+r), svgutil.Num(mid-r), svgutil.Num(cy), color, pal.NodeStroke)
			b.WriteByte('\n')
			continue
		}
		bw := x2 - x1
		if bw < 2 {
			bw = 2
		}
		fmt.Fprintf(&b, `  <rect x="%s" y="%s" width="%s" height="%s" rx="3" fill="%s" stroke="%s"/>`,
			svgutil.Num(x1), svgutil.Num(y), svgutil.Num(bw), svgutil.Num(bh), color, pal.NodeStroke)
		b.WriteByte('\n')
	}
	if d.TodayMarker != "off" && !minT.IsZero() {
		now := time.Now().In(minT.Location())
		if !now.Before(minT) && !now.After(maxT) {
			style := d.TodayMarker
			if style == "" {
				style = "stroke:#f00;stroke-width:2px"
			} else {
				style = strings.ReplaceAll(style, ",", ";")
			}
			x := dayX(now)
			fmt.Fprintf(&b, `  <line class="today-marker" x1="%s" y1="%s" x2="%s" y2="%s" style="%s"/>`, svgutil.Num(x), svgutil.Num(chartTop), svgutil.Num(x), svgutil.Num(chartTop+float64(len(d.Tasks))*rowH), svgutil.Esc(style))
			b.WriteByte('\n')
		}
	}

	b.WriteString("</svg>\n")
	return []byte(b.String())
}

func axisTicks(start, end time.Time, d *Diagram) []time.Time {
	if d.TickUnit == "" {
		return []time.Time{start, end}
	}
	count := d.TickCount
	var tick time.Time
	advance := func(t time.Time) time.Time { return t }
	switch d.TickUnit {
	case "millisecond", "second", "minute", "hour":
		unit := map[string]time.Duration{"millisecond": time.Millisecond, "second": time.Second, "minute": time.Minute, "hour": time.Hour}[d.TickUnit]
		step := time.Duration(count) * unit
		if step <= 0 {
			return nil
		}
		tick = start.Truncate(step)
		advance = func(t time.Time) time.Time { return t.Add(step) }
	case "day":
		tick = time.Date(start.Year(), start.Month(), start.Day(), 0, 0, 0, 0, start.Location())
		tick = tick.AddDate(0, 0, -((tick.Day() - 1) % count))
		advance = func(t time.Time) time.Time { return t.AddDate(0, 0, count) }
	case "week":
		tick = time.Date(start.Year(), start.Month(), start.Day(), 0, 0, 0, 0, start.Location())
		tick = tick.AddDate(0, 0, -(int(tick.Weekday())-int(d.Weekday)+7)%7)
		origin := time.Date(1970, 1, 1, 0, 0, 0, 0, tick.Location())
		origin = origin.AddDate(0, 0, -(int(origin.Weekday())-int(d.Weekday)+7)%7)
		weeks := (tick.Unix() - origin.Unix()) / (7 * 24 * 60 * 60)
		remainder := weeks % int64(count)
		if remainder < 0 {
			remainder += int64(count)
		}
		tick = tick.AddDate(0, 0, -7*int(remainder))
		advance = func(t time.Time) time.Time { return t.AddDate(0, 0, 7*count) }
	case "month":
		month := (int(start.Month())-1)/count*count + 1
		tick = time.Date(start.Year(), time.Month(month), 1, 0, 0, 0, 0, start.Location())
		advance = func(t time.Time) time.Time { return t.AddDate(0, count, 0) }
	}
	if tick.Before(start) {
		tick = advance(tick)
	}
	var ticks []time.Time
	for !tick.After(end) {
		ticks = append(ticks, tick)
		next := advance(tick)
		if !next.After(tick) {
			break
		}
		tick = next
	}
	return ticks
}

// formatAxis implements the d3 time-format directives used by Mermaid.
func formatAxis(t time.Time, format string) string {
	if format == "" {
		return t.Format("2006-01-02")
	}
	var b strings.Builder
	for i := 0; i < len(format); i++ {
		if format[i] != '%' || i+1 == len(format) {
			b.WriteByte(format[i])
			continue
		}
		i++
		modifier := byte(0)
		if (format[i] == '-' || format[i] == '_' || format[i] == '0') && i+1 < len(format) {
			modifier = format[i]
			i++
		}
		var value string
		switch format[i] {
		case 'a':
			value = t.Format("Mon")
		case 'A':
			value = t.Format("Monday")
		case 'b':
			value = t.Format("Jan")
		case 'B':
			value = t.Format("January")
		case 'c':
			value = t.Format("Mon Jan _2 15:04:05 2006")
		case 'd':
			value = t.Format("02")
		case 'e':
			value = t.Format("_2")
		case 'H':
			value = t.Format("15")
		case 'I':
			value = t.Format("03")
		case 'j':
			value = fmt.Sprintf("%03d", t.YearDay())
		case 'm':
			value = t.Format("01")
		case 'M':
			value = t.Format("04")
		case 'L':
			value = fmt.Sprintf("%03d", t.Nanosecond()/1000000)
		case 'p':
			value = t.Format("PM")
		case 'S':
			value = t.Format("05")
		case 'U':
			value = fmt.Sprintf("%02d", (t.YearDay()-1+7-int(t.Weekday()))/7)
		case 'w':
			value = fmt.Sprint(int(t.Weekday()))
		case 'W':
			value = fmt.Sprintf("%02d", (t.YearDay()-1+7-(int(t.Weekday())+6)%7)/7)
		case 'x':
			value = t.Format("01/02/2006")
		case 'X':
			value = t.Format("15:04:05")
		case 'y':
			value = t.Format("06")
		case 'Y':
			value = t.Format("2006")
		case 'Z':
			value = t.Format("-0700")
		case '%':
			value = "%"
		default:
			value = string(format[i])
		}
		if modifier == '-' {
			value = strings.TrimLeft(value, " 0")
			if value == "" {
				value = "0"
			}
		}
		if modifier == '_' && len(value) > 1 && value[0] == '0' {
			value = " " + value[1:]
		}
		if modifier == '0' && len(value) > 1 && value[0] == ' ' {
			value = "0" + value[1:]
		}
		b.WriteString(value)
	}
	return b.String()
}
