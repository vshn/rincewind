package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/vshn/rincewind/internal/engine"
)

// gated builds a model over the gated views and delivers one round's
// findings, the way a resolution would.
func gated(t *testing.T, state map[string]engine.State) *model {
	t.Helper()
	m := newModel(gatedViews())
	next, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 24})
	m = next.(*model)
	next, _ = m.Update(bearingsMsg{found: engine.Bearings{State: state}})
	return next.(*model)
}

// menu is the sidebar as it reads on screen.
func menu(m *model) string {
	var out []string
	for _, line := range strings.Split(m.View().Content, "\n") {
		out = append(out, ansi.Strip(line)[:sidebarWidth])
	}
	return strings.Join(out, "\n")
}

// The point: views for a platform this session is not on are noise, and the
// menu leaves them out.
func TestViewsTheContextsRuledOutAreHidden(t *testing.T) {
	m := gated(t, map[string]engine.State{
		"elsewhere": engine.StateInactive,
		"cloudy":    engine.StateInactive,
	})

	if strings.Contains(menu(m), "volumes") {
		t.Errorf("a view its context ruled out is in the menu:\n%s", menu(m))
	}
	if !strings.Contains(menu(m), "services") {
		t.Errorf("a view that names no context went too:\n%s", menu(m))
	}
	// Two hidden, and the sidebar says so rather than quietly dropping them.
	if !strings.Contains(menu(m), "2 hidden") {
		t.Errorf("the sidebar does not say how many it left out:\n%s", menu(m))
	}
}

// Every context it names, not any: one active one is enough to show it.
func TestOneActiveContextIsEnough(t *testing.T) {
	m := gated(t, map[string]engine.State{
		"elsewhere": engine.StateInactive,
		"cloudy":    engine.StateActive,
	})

	if !strings.Contains(menu(m), "either") {
		t.Errorf("a view naming two contexts went with the inactive one:\n%s", menu(m))
	}
	if strings.Contains(menu(m), "volumes") {
		t.Errorf("the view naming only the inactive context stayed:\n%s", menu(m))
	}
	if !strings.Contains(menu(m), "1 hidden") {
		t.Errorf("the count is not one:\n%s", menu(m))
	}
}

// Fail open: a probe that could not answer must hide nothing, and nothing is
// hidden before the first round either.
func TestUnknownHidesNothing(t *testing.T) {
	for _, tc := range []struct {
		name  string
		state map[string]engine.State
	}{
		{"nothing resolved yet", nil},
		{"the probe could not answer", map[string]engine.State{"elsewhere": engine.StateUnknown}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := gated(t, tc.state)
			for _, name := range []string{"services", "volumes", "either", "events"} {
				if !strings.Contains(menu(m), name) {
					t.Errorf("%s is missing:\n%s", name, menu(m))
				}
			}
			if strings.Contains(menu(m), "hidden") {
				t.Errorf("the sidebar claims something is hidden:\n%s", menu(m))
			}
		})
	}
}

// Hiding is a filter, not a deletion: one key brings them back, marked with
// what they are waiting for.
func TestHBringsHiddenViewsBack(t *testing.T) {
	m := gated(t, map[string]engine.State{
		"elsewhere": engine.StateInactive,
		"cloudy":    engine.StateInactive,
	})
	press(t, m, "H")

	if !strings.Contains(menu(m), "volumes") {
		t.Fatalf("H did not bring the hidden views back:\n%s", menu(m))
	}
	// Marked rather than passed off as ordinary, and it names why it waits.
	if !strings.Contains(menu(m), "volumes  elsewhere") {
		t.Errorf("a view brought back reads as an ordinary entry:\n%s", menu(m))
	}
	if !strings.Contains(menu(m), "H hides 2") {
		t.Errorf("the sidebar does not offer the way back:\n%s", menu(m))
	}

	press(t, m, "H")
	if strings.Contains(menu(m), "volumes") {
		t.Errorf("H did not put them away again:\n%s", menu(m))
	}
}

// The cursor must never come to rest on a line that is not in the list.
func TestTheCursorNeverLandsOnAHiddenView(t *testing.T) {
	m := gated(t, map[string]engine.State{
		"elsewhere": engine.StateInactive,
		"cloudy":    engine.StateInactive,
	})

	// Down the whole menu twice over, which wraps: every stop must be a view
	// that is actually listed.
	for range 2 * len(m.entries) {
		press(t, m, "down")
		v := m.selected()
		if v == nil {
			t.Fatal("the cursor came to rest on a group header")
		}
		if m.ruledOut(v) {
			t.Fatalf("the cursor landed on %s, which is hidden", v.ID)
		}
	}
}

// A round can rule out every listed view, and then there is nothing under
// the cursor. Enter must be a no-op: it used to replace the first tab with
// an empty one, throwing away the result on screen.
func TestEnterOnAnEmptiedMenuKeepsTheTab(t *testing.T) {
	var views []engine.View
	for _, v := range gatedViews() {
		if len(v.Needs) > 0 {
			views = append(views, v)
		}
	}
	m := newModel(views)
	next, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 24})
	m = next.(*model)

	press(t, m, "enter") // run the view under the cursor
	if m.cur().res == nil {
		t.Fatal("the opened view produced nothing")
	}

	m.Update(bearingsMsg{found: engine.Bearings{State: map[string]engine.State{
		"elsewhere": engine.StateInactive,
		"cloudy":    engine.StateInactive,
	}}})
	if m.selected() != nil {
		t.Fatal("something is still listed; this test needs an empty menu")
	}

	press(t, m, "enter")
	if m.cur().view == nil || m.cur().res == nil {
		t.Errorf("enter on an empty menu replaced the tab: view %v, res %v",
			m.cur().view, m.cur().res)
	}
}

// A round that hides the view under the cursor moves it; one that hides
// others leaves it exactly where it was.
func TestResolutionKeepsTheCursorOnItsView(t *testing.T) {
	m := gated(t, nil)
	press(t, m, "down") // demo.volumes, the second entry under the group header
	was := m.selected()
	if was.ID != "demo.volumes" {
		t.Fatalf("the cursor is on %s, want demo.volumes", was.ID)
	}

	m.Update(bearingsMsg{found: engine.Bearings{State: map[string]engine.State{
		"elsewhere": engine.StateInactive,
	}}})
	if got := m.selected(); got == nil || got.ID == "demo.volumes" {
		t.Fatalf("the cursor stayed on the view that was hidden under it: %v", got)
	}

	// And the other way round: hiding nothing leaves the cursor put.
	m = gated(t, nil)
	press(t, m, "down")
	press(t, m, "down") // demo.either
	m.Update(bearingsMsg{found: engine.Bearings{State: map[string]engine.State{
		"elsewhere": engine.StateActive,
	}}})
	if got := m.selected(); got == nil || got.ID != "demo.either" {
		t.Errorf("the cursor moved for a round that hid nothing: %v", got)
	}
}
