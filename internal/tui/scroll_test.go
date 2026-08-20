package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"charm.land/lipgloss/v2"

	"github.com/vshn/aldediggi/internal/engine"
)

// scrolling builds a model showing a table taller and wider than its box,
// with the output pane focused.
func scrolling(t *testing.T) *model {
	t.Helper()
	views := []engine.View{sampleViews()[0]}
	m := newModel(views)
	next, _ := m.Update(tea.WindowSizeMsg{Width: 96, Height: 20})
	m = next.(*model)

	m.Update(key("enter"))
	m.Update(resultMsg{seq: m.seq, res: longTable()})
	m.Update(key("tab"))
	return m
}

// body returns the content box's lines, without the frames around them.
func body(m *model) []string {
	var out []string
	for _, line := range strings.Split(m.View().Content, "\n") {
		// The two boxes meet at "││"; everything after that is the content.
		if plain := visibleOnly(line); strings.Contains(plain, "││") {
			out = append(out, strings.SplitN(plain, "││", 2)[1])
		}
	}
	return out
}

func TestCursorPastTheBottomScrolls(t *testing.T) {
	m := scrolling(t)
	first := body(m)[1]

	for range 20 {
		m.Update(key("down"))
	}

	if got := body(m)[1]; got == first {
		t.Fatalf("the view never scrolled: still starts at %q", got)
	}
	// The cursor stays on screen, at the bottom edge.
	if !strings.Contains(m.View().Content, "worker-21") {
		t.Error("the cursor row scrolled out of sight")
	}
}

// The column names are the one line that must never scroll away: without
// them a wide table is a wall of unlabelled values.
func TestHeaderStaysPutWhileScrolling(t *testing.T) {
	m := scrolling(t)
	for range 25 {
		m.Update(key("down"))
	}

	rows := body(m)
	if !strings.HasPrefix(strings.TrimSpace(rows[0]), "NAME") {
		t.Errorf("first content line is %q, want the header", rows[0])
	}
	if strings.Contains(rows[1], "worker-01") {
		t.Error("the table did not scroll under the header")
	}
}

// Horizontal scrolling has to move the header with the rows, or the names
// end up over the wrong columns.
func TestHeaderFollowsHorizontalScroll(t *testing.T) {
	m := scrolling(t)
	before := body(m)[0]

	m.Update(key(">"))
	if after := body(m)[0]; after == before {
		t.Error("the header did not follow the horizontal scroll")
	}
}

func TestPageMovesAScreenful(t *testing.T) {
	m := scrolling(t)
	page := m.vp.Height()

	m.Update(key("pgdown"))
	if m.row != page {
		t.Errorf("cursor at row %d after a page, want %d", m.row, page)
	}

	m.Update(key("pgup"))
	if m.row != 0 {
		t.Errorf("cursor at row %d after paging back, want 0", m.row)
	}
}

func TestJumpToEndsOfTable(t *testing.T) {
	m := scrolling(t)

	m.Update(key("end"))
	if m.row != 39 {
		t.Errorf("cursor at row %d after end, want 39", m.row)
	}
	if !strings.Contains(m.View().Content, "worker-40") {
		t.Error("the last row is not on screen after end")
	}

	m.Update(key("home"))
	if m.row != 0 {
		t.Errorf("cursor at row %d after home, want 0", m.row)
	}
}

// The position label rides in the frame rather than costing a row of the
// table it describes.
func TestScrollPositionIsShownInTheFrame(t *testing.T) {
	m := scrolling(t)
	m.Update(key("end"))

	border := strings.Split(m.View().Content, "\n")[3]
	if got := visibleOnly(border); !strings.Contains(got, "/40") {
		t.Errorf("frame %q carries no scroll position", got)
	}
}

// A menu longer than its box must scroll too, or the views below the fold
// are simply unreachable to the eye.
func TestSidebarScrollsWithTheCursor(t *testing.T) {
	m := newModel(manyViews())
	next, _ := m.Update(tea.WindowSizeMsg{Width: 96, Height: 20})
	m = next.(*model)

	for range 25 {
		m.Update(key("down"))
	}

	content := m.View().Content
	if !strings.Contains(content, "view26") {
		t.Error("the sidebar cursor scrolled out of sight")
	}
	if strings.Contains(content, "view01") {
		t.Error("the sidebar did not scroll at all")
	}
}

// Scrolling must not disturb the frames: every line stays window-wide.
func TestScrolledLayoutKeepsItsWidth(t *testing.T) {
	m := scrolling(t)
	for range 25 {
		m.Update(key("down"))
	}
	m.Update(key(">"))

	for i, l := range strings.Split(m.View().Content, "\n") {
		if w := lipgloss.Width(l); w != 96 {
			t.Errorf("line %d has width %d, want 96", i, w)
		}
	}
}
