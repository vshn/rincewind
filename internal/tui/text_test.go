package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/vshn/rincewind/internal/engine"
)

// showing builds a model with a text view loaded.
func showing(t *testing.T) *model {
	t.Helper()
	m := newModel(textViews())
	next, _ := m.Update(tea.WindowSizeMsg{Width: 96, Height: 20})
	m = next.(*model)

	m.Update(key("enter"))
	m.Update(resultMsg{seq: m.seq, res: engine.Result{Lines: logLines()}})
	return m
}

// The point of the class: the lines arrive as the script wrote them.
func TestTextLinesAreShownVerbatim(t *testing.T) {
	m := showing(t)

	// The four spaces are the discriminating part: a one-column table would
	// have collapsed them to one, which is why a log cannot be one.
	rows := body(m)
	if !strings.Contains(rows[0], "    indented continuation line") {
		t.Errorf("first line is %q, want its leading whitespace kept", rows[0])
	}
	// No sticky header: there are no column names to keep.
	if strings.Contains(rows[0], "LINE") || strings.Contains(rows[0], "LOG") {
		t.Error("a text view invented a column header")
	}
}

// Filtering a log is grep, and is most of what one does with one.
func TestTextFilterGrepsTheLines(t *testing.T) {
	m := showing(t)
	typeFilter(m, "error")

	content := visibleOnly(m.View().Content)
	if !strings.Contains(content, "liveness probe failed") {
		t.Error("the matching line is gone")
	}
	if strings.Contains(content, "event 01") {
		t.Error("a non-matching line survived the filter")
	}
	if !strings.Contains(content, "1 of 42") {
		t.Errorf("the count is wrong or missing: %q", content)
	}
}

// A text view has no rows, so the keys scroll it instead of moving a cursor
// — which is what one wants from a log anyway.
func TestTextViewFocusesAndScrolls(t *testing.T) {
	m := showing(t)

	m.Update(key("tab"))
	if m.focus != paneOutput {
		t.Fatal("a text view could not be focused, so it cannot be scrolled")
	}

	before := m.vp.YOffset()
	for range 5 {
		m.Update(key("down"))
	}
	if m.vp.YOffset() <= before {
		t.Errorf("the view did not scroll: offset %d", m.vp.YOffset())
	}

	m.Update(key("end"))
	if !strings.Contains(m.View().Content, "liveness probe failed") {
		t.Error("end did not reach the last line")
	}
	m.Update(key("home"))
	if m.vp.YOffset() != 0 {
		t.Errorf("home left the view at %d", m.vp.YOffset())
	}
}

// Nothing that belongs to rows applies: no selection, and no cursor row to
// act on.
func TestTextViewHasNoSelection(t *testing.T) {
	m := showing(t)
	m.Update(key("tab"))

	m.Update(key("space"))
	m.Update(key("a"))

	if n := len(m.cur().selection); n != 0 {
		t.Errorf("%d rows selected in a view that has none", n)
	}
	if strings.Contains(visibleOnly(m.View().Content), "selected") {
		t.Error("the status line offers a selection")
	}
}

// An empty log says so rather than showing an empty frame.
func TestEmptyTextViewSaysSo(t *testing.T) {
	m := showing(t)
	m.Update(resultMsg{seq: m.cur().seq, res: engine.Result{Lines: []string{}}})

	if !strings.Contains(m.View().Content, "nothing to show") {
		t.Error("an empty log does not say it is empty")
	}
}

// The frames hold, with no header line to account for.
func TestTextLayoutKeepsItsWidth(t *testing.T) {
	m := showing(t)
	m.Update(key("tab"))
	typeFilter(m, "event")

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
