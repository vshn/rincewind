package render

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/NimbleMarkets/ntcharts/v2/linechart/timeserieslinechart"

	"github.com/vshn/rincewind/internal/engine"
)

// seriesColours is the palette a chart cycles through, in drawing order.
// ANSI indices rather than RGB, so the terminal's own theme decides what
// they look like — the same reason the table styles use them.
var seriesColours = []string{"6", "2", "3", "5", "4", "1", "14", "10", "11", "13", "12", "9"}

var chartAxis = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))

// Chart draws a chart result as lines: the plot in braille, then a legend
// naming each series in its colour. The block is at most w wide and exactly
// h tall while there is room, because the TUI pane it fills does not grow.
// colour off yields plain runes, for a pipe and for the clipboard.
//
// Series are drawn in the order the engine kept them — the script's order —
// so a hat that wants its avg line on top of its max band emits max first.
func Chart(res engine.Result, w, h int, colour bool) []string {
	drawn := make([]engine.Series, 0, len(res.Series))
	for _, s := range res.Series {
		if len(s.Points) > 0 {
			drawn = append(drawn, s)
		}
	}
	if len(drawn) == 0 {
		return nil
	}

	legend := legendLines(drawn, w, colour)
	chartH := max(h-len(legend), 3)

	minT, maxT := drawn[0].Points[0].T, drawn[0].Points[0].T
	minV, maxV := drawn[0].Points[0].V, drawn[0].Points[0].V
	for _, s := range drawn {
		for _, p := range s.Points {
			if p.T.Before(minT) {
				minT = p.T
			}
			if p.T.After(maxT) {
				maxT = p.T
			}
			minV, maxV = min(minV, p.V), max(maxV, p.V)
		}
	}
	// A flat range would make the scale factor divide by zero.
	if !maxT.After(minT) {
		maxT = minT.Add(time.Minute)
	}
	lo, hi := min(minV, 0), maxV
	if res.YRange != nil {
		lo, hi = res.YRange[0], res.YRange[1]
	}
	if hi <= lo {
		hi = lo + 1
	}

	axis := lipgloss.NewStyle()
	if colour {
		axis = chartAxis
	}
	// Ranges go in as options: the y label column is sized at New, and a
	// range set later can leave a wider label clipped.
	c := timeserieslinechart.New(w, chartH,
		timeserieslinechart.WithAxesStyles(axis, axis),
		timeserieslinechart.WithXLabelFormatter(timeLabel(maxT.Sub(minT))),
		timeserieslinechart.WithYLabelFormatter(yFormatter(lo, hi, chartH-2)),
		timeserieslinechart.WithTimeRange(minT, maxT),
		timeserieslinechart.WithYRange(lo, hi),
	)

	names := make([]string, len(drawn))
	for i, s := range drawn {
		names[i] = s.Name
		if colour {
			c.SetDataSetStyle(s.Name, colourFor(i))
		}
		for _, p := range s.Points {
			c.PushDataSet(s.Name, timeserieslinechart.TimePoint{Time: p.T, Value: p.V})
		}
	}
	// Not DrawBrailleAll: that iterates a map, and where two series share a
	// cell the one drawn last owns it — an order worth deciding, not rolling.
	c.DrawBrailleDataSets(names)

	return append(strings.Split(c.View(), "\n"), legend...)
}

// timeLabel picks the x axis label for the span on show: days of history are
// told apart by date, hours by clock.
func timeLabel(span time.Duration) func(int, float64) string {
	layout := "15:04"
	if span > 48*time.Hour {
		layout = "01-02"
	}
	return func(_ int, v float64) string {
		return time.Unix(int64(v), 0).Format(layout)
	}
}

// yFormatter renders y axis labels at one fixed width. ntcharts sizes the
// label column from the values its width pass happens to sample, and at odd
// graph heights the draw pass can produce a wider label than any sampled —
// "100" clipped to "00". Same-width labels make the passes agree by
// construction.
func yFormatter(lo, hi float64, graphH int) func(int, float64) string {
	// Whole numbers once the range has room for them: the label orients, it
	// does not measure. Tight ranges keep three significant digits.
	show := func(v float64) string { return strconv.FormatFloat(v, 'g', 3, 64) }
	if hi-lo >= 10 {
		show = func(v float64) string { return strconv.Itoa(int(math.Round(v))) }
	}

	graphH = max(graphH, 1)
	width := 0
	for i := 0; i <= graphH; i++ {
		v := lo + (hi-lo)*float64(i)/float64(graphH)
		width = max(width, len(show(v)))
	}
	return func(_ int, v float64) string {
		return fmt.Sprintf("%*s", width, show(v))
	}
}

func colourFor(i int) lipgloss.Style {
	return lipgloss.NewStyle().Foreground(lipgloss.Color(seriesColours[i%len(seriesColours)]))
}

// legendLines names the series under the plot, each mark in its series'
// colour, packed into as few lines of width w as they go.
func legendLines(drawn []engine.Series, w int, colour bool) []string {
	const sep = "   "
	var lines []string
	line, width := "", 0
	for i, s := range drawn {
		mark := "──"
		if colour {
			mark = colourFor(i).Render(mark)
		}
		entry := mark + " " + s.Name
		ew := 3 + lipgloss.Width(s.Name)
		switch {
		case line == "":
			line, width = entry, ew
		case width+len(sep)+ew <= w:
			line, width = line+sep+entry, width+len(sep)+ew
		default:
			lines = append(lines, line)
			line, width = entry, ew
		}
	}
	return append(lines, line)
}
