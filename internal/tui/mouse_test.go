package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

func click(x, y int) tea.MouseClickMsg {
	return tea.MouseClickMsg{X: x, Y: y, Button: tea.MouseLeft}
}

func wheel(x int, button tea.MouseButton) tea.MouseWheelMsg {
	return tea.MouseWheelMsg{X: x, Y: 5, Button: button}
}

// lineOf finds the screen line a piece of text was rendered on. The mouse
// tests aim at what is actually on screen rather than at a line number
// worked out twice — once in the code and once in the test.
func lineOf(t *testing.T, m *model, needle string) int {
	t.Helper()
	for i, line := range strings.Split(m.View().Content, "\n") {
		if strings.Contains(visibleOnly(line), needle) {
			return i
		}
	}
	t.Fatalf("%q is not on screen", needle)
	return -1
}

const overContent = sidebarWidth + 5

func TestWheelScrollsTheTableItPointsAt(t *testing.T) {
	m := scrolling(t)
	m.Update(key("tab"))
	m.focus = paneList // the wheel acts on what it points at, not on focus

	m.Update(wheel(overContent, tea.MouseWheelDown))
	if m.vp.YOffset() != wheelStep {
		t.Fatalf("viewport at %d after a wheel down, want %d", m.vp.YOffset(), wheelStep)
	}

	m.Update(wheel(overContent, tea.MouseWheelUp))
	if m.vp.YOffset() != 0 {
		t.Errorf("viewport at %d after a wheel up, want 0", m.vp.YOffset())
	}
}

func TestWheelOverTheMenuScrollsTheMenu(t *testing.T) {
	m := newModel(manyViews())
	next, _ := m.Update(tea.WindowSizeMsg{Width: 96, Height: 20})
	m = next.(*model)
	m.Update(key("enter"))
	m.Update(resultMsg{seq: m.seq, res: longTable()})

	m.Update(wheel(1, tea.MouseWheelDown))

	if m.sideTop != wheelStep {
		t.Errorf("menu at line %d, want %d", m.sideTop, wheelStep)
	}
	if m.vp.YOffset() != 0 {
		t.Error("the wheel over the menu scrolled the table as well")
	}
	// Scrolling is looking around, not choosing.
	if m.cursor != 1 {
		t.Errorf("cursor moved to %d; the wheel must not select", m.cursor)
	}
}

func TestClickPutsTheCursorOnTheRowUnderIt(t *testing.T) {
	m := scrolling(t)

	m.Update(click(overContent, lineOf(t, m, "worker-03")))

	if m.focus != paneOutput {
		t.Error("clicking a row did not focus the output")
	}
	if m.cur().row != 2 {
		t.Errorf("cursor on row %d, want row 2 (worker-03)", m.cur().row)
	}
}

// The same click has to land on the same row once the table has scrolled,
// which is where an off-by-one in the offset would show up.
func TestClickHitsTheRightRowAfterScrolling(t *testing.T) {
	m := scrolling(t)
	for range 25 {
		m.Update(key("down"))
	}

	m.Update(click(overContent, lineOf(t, m, "worker-20")))

	if m.cur().row != 19 {
		t.Errorf("cursor on row %d, want row 19 (worker-20)", m.cur().row)
	}
}

func TestClickOnTheHeaderOrFrameDoesNothing(t *testing.T) {
	m := scrolling(t)
	m.Update(key("tab"))
	before := m.cur().row

	for _, y := range []int{
		0,                      // the title box
		titleHeight,            // the body's top border
		lineOf(t, m, "NAME"),   // the sticky header
		m.bodyBoxHeight() + 10, // the footer
	} {
		m.Update(click(overContent, y))
		if m.cur().row != before {
			t.Fatalf("a click on line %d moved the cursor to %d", y, m.cur().row)
		}
	}
}

func TestClickOnAMenuEntryRunsIt(t *testing.T) {
	m := newModel(manyViews())
	next, _ := m.Update(tea.WindowSizeMsg{Width: 96, Height: 20})
	m = next.(*model)

	m.Update(click(1, lineOf(t, m, "view04")))

	// Entry 0 is the group header, so the fourth view is entry 4.
	if m.cursor != 4 {
		t.Fatalf("cursor on entry %d, want 4 (view04)", m.cursor)
	}
	if !m.cur().running {
		t.Error("clicking a menu entry did not run it")
	}
}

// The mirror of the scrolled-table case: once the menu has scrolled, a
// click must still land on the entry it points at.
func TestClickHitsTheRightEntryAfterScrolling(t *testing.T) {
	m := newModel(manyViews())
	next, _ := m.Update(tea.WindowSizeMsg{Width: 96, Height: 20})
	m = next.(*model)

	m.Update(wheel(1, tea.MouseWheelDown))
	m.Update(wheel(1, tea.MouseWheelDown))

	m.Update(click(1, lineOf(t, m, "view12")))

	if m.cursor != 12 {
		t.Errorf("cursor on entry %d, want 12 (view12)", m.cursor)
	}
}

func TestClickOnAGroupHeaderIsIgnored(t *testing.T) {
	m := sized(t)
	before := m.cursor

	m.Update(click(1, lineOf(t, m, "demo")))

	if m.cursor != before {
		t.Errorf("a click on the group header moved the cursor to %d", m.cursor)
	}
}

// Capturing the mouse costs the terminal's own text selection, so the state
// has to be reachable both ways.
func TestMouseCanBeReleasedAndGrabbedAgain(t *testing.T) {
	m := sized(t)

	if m.View().MouseMode != tea.MouseModeCellMotion {
		t.Fatal("the mouse is not captured by default")
	}

	m.Update(key("M"))
	if m.View().MouseMode != tea.MouseModeNone {
		t.Error("m did not release the mouse")
	}
	if !strings.Contains(m.View().Content, "grab mouse") {
		t.Error("the help does not say how to get the mouse back")
	}

	m.Update(key("M"))
	if m.View().MouseMode != tea.MouseModeCellMotion {
		t.Error("m did not grab the mouse again")
	}
}
