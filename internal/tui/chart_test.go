package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/vshn/rincewind/internal/engine"
)

// charting builds a model with a chart view loaded.
func charting(t *testing.T) *model {
	t.Helper()
	m := newModel(chartViews())
	next, _ := m.Update(tea.WindowSizeMsg{Width: 96, Height: 20})
	m = next.(*model)

	m.Update(key("enter"))
	m.Update(resultMsg{seq: m.seq, res: chartSeries()})
	return m
}

func brailleIn(s string) bool {
	for _, r := range s {
		if r >= 0x2800 && r <= 0x28FF {
			return true
		}
	}
	return false
}

// The point of the class: the pane holds a plot and a legend, not rows.
func TestChartShowsPlotAndLegend(t *testing.T) {
	m := charting(t)

	content := visibleOnly(m.View().Content)
	if !brailleIn(content) {
		t.Error("no braille rune on screen")
	}
	for _, name := range []string{"── worker-01", "── infra-0"} {
		if !strings.Contains(content, name) {
			t.Errorf("legend does not name %q", name)
		}
	}
}

// Filtering a chart picks series by name: one node read out of a pool.
func TestChartFilterPicksSeries(t *testing.T) {
	m := charting(t)
	typeFilter(m, "worker")

	content := visibleOnly(m.View().Content)
	if !strings.Contains(content, "── worker-01") {
		t.Error("the matching series is gone")
	}
	if strings.Contains(content, "infra-0") {
		t.Error("a non-matching series survived the filter")
	}
	if !strings.Contains(content, "1 of 2") {
		t.Errorf("the count is wrong or missing: %q", content)
	}
}

// Nothing that belongs to rows applies: no selection to build, nothing for a
// space to land on.
func TestChartHasNoSelection(t *testing.T) {
	m := charting(t)
	m.Update(key("tab"))

	m.Update(key("space"))
	m.Update(key("a"))

	if n := len(m.cur().selection); n != 0 {
		t.Errorf("%d rows selected in a view that has none", n)
	}
}

// An empty chart says so rather than showing empty axes.
func TestEmptyChartSaysSo(t *testing.T) {
	m := charting(t)
	m.Update(resultMsg{seq: m.cur().seq, res: engine.Result{Series: []engine.Series{}}})

	if !strings.Contains(m.View().Content, "nothing to show") {
		t.Error("an empty chart does not say it is empty")
	}
}

// The frames hold: the plot is drawn to the pane, never past it.
func TestChartLayoutKeepsItsWidth(t *testing.T) {
	m := charting(t)

	lines := strings.Split(m.View().Content, "\n")
	if len(lines) != 20 {
		t.Errorf("the screen is %d lines, the window is 20", len(lines))
	}
	for i, l := range lines {
		if got := lipgloss.Width(l); got != 96 {
			t.Errorf("line %d is %d wide, want 96", i, got)
		}
	}
}

// A resize redraws the plot to the new pane rather than clipping the old one.
func TestChartRedrawsOnResize(t *testing.T) {
	m := charting(t)

	next, _ := m.Update(tea.WindowSizeMsg{Width: 72, Height: 16})
	m = next.(*model)

	lines := strings.Split(m.View().Content, "\n")
	if len(lines) != 16 {
		t.Fatalf("the screen is %d lines, the window is 16", len(lines))
	}
	for i, l := range lines {
		if got := lipgloss.Width(l); got != 72 {
			t.Errorf("line %d is %d wide, want 72", i, got)
		}
	}
	if !brailleIn(visibleOnly(m.View().Content)) {
		t.Error("the plot did not survive the resize")
	}
}
