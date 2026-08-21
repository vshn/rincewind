package render

import (
	"strings"
	"testing"
	"time"

	"github.com/vshn/rincewind/internal/engine"
)

func chartResult() engine.Result {
	base := time.Unix(1755600000, 0)
	mk := func(name string, vs ...float64) engine.Series {
		s := engine.Series{Name: name}
		for i, v := range vs {
			s.Points = append(s.Points, engine.Point{T: base.Add(time.Duration(i) * time.Hour), V: v})
		}
		return s
	}
	return engine.Result{Series: []engine.Series{
		mk("api", 10, 40, 90, 40, 10),
		mk("db", 5, 5, 6, 5, 5),
	}}
}

func hasBraille(lines []string) bool {
	for _, line := range lines {
		for _, r := range line {
			if r >= 0x2800 && r <= 0x28FF {
				return true
			}
		}
	}
	return false
}

func TestChartFillsItsBoxAndNamesItsSeries(t *testing.T) {
	lines := Chart(chartResult(), 60, 12, false)

	// The plot plus its one legend line is exactly the height asked for:
	// the pane it fills does not grow.
	if len(lines) != 12 {
		t.Fatalf("got %d lines, want 12", len(lines))
	}
	if !hasBraille(lines) {
		t.Error("no braille rune in the plot")
	}
	joined := strings.Join(lines, "\n")
	for _, name := range []string{"── api", "── db"} {
		if !strings.Contains(joined, name) {
			t.Errorf("legend does not name %q", name)
		}
	}
	if strings.Contains(joined, "\x1b[") {
		t.Error("escape codes in an uncoloured chart")
	}
}

func TestChartColoursOnlyWhenAsked(t *testing.T) {
	lines := Chart(chartResult(), 60, 12, true)
	if !strings.Contains(strings.Join(lines, "\n"), "\x1b[") {
		t.Error("no escape codes in a coloured chart")
	}
}

func TestChartWithNothingToDrawIsNothing(t *testing.T) {
	empty := engine.Result{Series: []engine.Series{{Name: "api"}}}
	if lines := Chart(empty, 60, 12, false); lines != nil {
		t.Fatalf("got %d lines for a chart with no points, want none", len(lines))
	}
}
