package tui

import (
	"strings"
	"testing"

	"charm.land/bubbles/v2/spinner"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/vshn/rincewind/internal/engine"
)

// drilling builds a model showing the pools view with the output focused,
// ready for an action key.
func drilling(t *testing.T) (*model, *record) {
	t.Helper()
	rec := &record{}
	m := newModel(drillViews(rec))
	next, _ := m.Update(tea.WindowSizeMsg{Width: 96, Height: 20})
	m = next.(*model)

	m.Update(key("enter")) // run demo.pools
	m.Update(resultMsg{seq: m.seq, res: poolTable()})
	m.Update(key("tab"))
	return m, rec
}

// deliver runs whatever the active tab has in flight and injects its result,
// standing in for the tea.Cmd the program would have run.
func deliver(t *testing.T, m *model, res engine.Table) {
	t.Helper()
	m.Update(resultMsg{seq: m.cur().seq, res: res})
}

// press sends a key and runs the commands it produced — and the ones their
// messages produce in turn, since an action's result goes on to reload the
// view it changed. The fixtures return at once, so this stays synchronous.
func press(t *testing.T, m *model, k string) {
	t.Helper()
	_, cmd := m.Update(key(k))
	drain(t, m, cmd)
}

func drain(t *testing.T, m *model, cmd tea.Cmd) {
	t.Helper()
	for range 8 {
		if cmd == nil {
			return
		}
		msg := cmd()
		switch msg := msg.(type) {
		case nil:
			return
		case spinner.TickMsg:
			// The tick chain never ends; the tests do not need it.
			return
		case tea.BatchMsg:
			cmd = nil
			for _, c := range msg {
				if c == nil {
					continue
				}
				if _, tick := c().(spinner.TickMsg); !tick {
					cmd = c
				}
			}
		default:
			_, cmd = m.Update(msg)
		}
	}
	t.Fatal("the command chain did not settle")
}

// colOf is the display column a piece of text sits at. Byte offsets will not
// do: the frames are drawn with multi-byte box characters.
func colOf(t *testing.T, line, needle string) int {
	t.Helper()
	i := strings.Index(line, needle)
	if i < 0 {
		t.Fatalf("%q is not in %q", needle, line)
	}
	return lipgloss.Width(line[:i])
}

func TestDrillOpensTheTargetInANewTab(t *testing.T) {
	m, _ := drilling(t)

	m.Update(key("enter"))

	if len(m.tabs) != 2 {
		t.Fatalf("%d tabs after drilling, want 2", len(m.tabs))
	}
	if m.act != 1 {
		t.Errorf("active tab is %d, want the new one", m.act)
	}
	if got := m.cur().view.ID; got != "demo.nodes" {
		t.Errorf("new tab shows %q, want demo.nodes", got)
	}
	if !m.cur().running {
		t.Error("the new tab did not start its view")
	}
}

// The whole point of the arguments: the target is opened about the row the
// cursor was on, not about the view in general.
func TestDrillPassesTheRowsArguments(t *testing.T) {
	m, rec := drilling(t)
	m.Update(key("down")) // second row: worker

	press(t, m, "enter")

	if len(rec.args) != 1 {
		t.Fatalf("target ran %d times, want once", len(rec.args))
	}
	if got := rec.args[0]["selector"]; got != "pool=worker" {
		t.Errorf("target got selector %q, want pool=worker", got)
	}
}

// A selection is the mutations' business; navigating follows the cursor.
func TestDrillIgnoresTheSelection(t *testing.T) {
	m, rec := drilling(t)
	m.Update(key("space")) // selects infra, moves to worker
	press(t, m, "enter")

	if len(rec.args) != 1 || rec.args[0]["selector"] != "pool=worker" {
		t.Fatalf("target got %v, want one call about the cursor row", rec.args)
	}
}

// A refusal written into an argument expression is the answer to the
// attempt, so it shows where the attempt went.
func TestDrillArgumentErrorBecomesTheNewTabsError(t *testing.T) {
	m, _ := drilling(t)
	broken := engine.Table{Cols: []string{"NAME", "STATE"}, Rows: []engine.Row{poolRow("odd", "broken")}}
	m.Update(resultMsg{seq: m.cur().seq, res: broken})

	m.Update(key("enter"))

	if len(m.tabs) != 2 {
		t.Fatalf("%d tabs, want the failed attempt to have opened one", len(m.tabs))
	}
	if m.cur().running {
		t.Error("the target ran despite the refusal")
	}
	if !strings.Contains(m.View().Content, "selects nodes by expression") {
		t.Error("the refusal is not on screen")
	}
}

func TestDrillingFromAMiddleTabTruncatesTheRest(t *testing.T) {
	m, _ := drilling(t)
	m.Update(key("enter")) // tab 1
	deliver(t, m, longTable())
	m.Update(key("["))     // back to tab 0
	m.Update(key("enter")) // drill again

	if len(m.tabs) != 2 {
		t.Fatalf("%d tabs, want the old branch replaced", len(m.tabs))
	}
}

func TestPickingAViewInTheMenuResetsToOneTab(t *testing.T) {
	m, _ := drilling(t)
	m.Update(key("enter"))
	deliver(t, m, longTable())

	m.Update(key("tab")) // back to the menu
	m.Update(key("enter"))

	if len(m.tabs) != 1 {
		t.Fatalf("%d tabs after picking from the menu, want 1", len(m.tabs))
	}
}

// The headline: coming back has to land exactly where one left. The old
// view stack re-ran the parent and lost all three of these.
func TestSwitchingTabsKeepsFilterCursorAndScroll(t *testing.T) {
	m, _ := drilling(t)
	m.Update(key("down")) // cursor on worker
	typeFilter(m, "w")    // narrows to worker

	m.Update(key("enter")) // drill
	deliver(t, m, longTable())
	m.Update(key("["))

	if got := m.filter.Value(); got != "w" {
		t.Errorf("filter is %q after coming back, want w", got)
	}
	if got := m.cur().row; got != 0 {
		t.Errorf("row cursor is %d, want the filtered row", got)
	}
	if !strings.Contains(m.View().Content, "worker") {
		t.Error("the parent's rows are gone")
	}
}

func TestSwitchingTabsStartsNoRun(t *testing.T) {
	m, rec := drilling(t)
	m.Update(key("enter"))
	deliver(t, m, longTable())

	before := len(rec.args)
	press(t, m, "[")
	press(t, m, "]")

	if len(rec.args) != before {
		t.Errorf("switching tabs re-ran the view: %d runs, want %d", len(rec.args), before)
	}
	if m.cur().running {
		t.Error("switching tabs left a run in flight")
	}
}

func TestTabKeysStopAtTheEnds(t *testing.T) {
	m, _ := drilling(t)
	m.Update(key("enter"))
	deliver(t, m, longTable())

	m.Update(key("]"))
	if m.act != 1 {
		t.Errorf("next tab past the end moved to %d", m.act)
	}
	m.Update(key("["))
	m.Update(key("["))
	if m.act != 0 {
		t.Errorf("previous tab past the start moved to %d", m.act)
	}
}

func TestEscClosesTheDrillTabBeforeUnfocusing(t *testing.T) {
	m, _ := drilling(t)
	m.Update(key("enter"))
	deliver(t, m, longTable())

	m.Update(key("esc"))
	if len(m.tabs) != 1 {
		t.Fatalf("%d tabs after esc, want the drill closed", len(m.tabs))
	}
	if m.focus != paneOutput {
		t.Error("esc dropped the focus together with the tab")
	}

	// On the last tab the ladder is what it always was.
	m.Update(key("esc"))
	if m.focus != paneList {
		t.Error("esc on the last tab did not return to the menu")
	}
}

func TestClosingATabCancelsItsRun(t *testing.T) {
	m, _ := drilling(t)
	m.Update(key("enter"))
	if !m.cur().running {
		t.Fatal("the drill did not start a run")
	}

	m.Update(key("x"))
	if len(m.tabs) != 1 {
		t.Fatalf("%d tabs after x, want 1", len(m.tabs))
	}
	// The cancelled run's result must find no tab to land in.
	if m.tabBySeq(2) != nil {
		t.Error("a closed tab still answers to its sequence number")
	}
}

func TestResultForAClosedTabIsDropped(t *testing.T) {
	m, _ := drilling(t)
	m.Update(key("enter"))
	orphan := m.cur().seq
	m.Update(key("x"))

	m.Update(resultMsg{seq: orphan, res: longTable()})

	if len(m.tabs) != 1 || m.cur().view.ID != "demo.pools" {
		t.Error("a result for a closed tab reached the screen")
	}
}

func TestBackgroundResultLandsInItsOwnTab(t *testing.T) {
	m, _ := drilling(t)
	m.Update(key("enter"))
	waiting := m.cur().seq
	m.Update(key("[")) // leave it running and go back

	m.Update(resultMsg{seq: waiting, res: longTable()})

	if m.tabs[1].res == nil {
		t.Error("the background tab did not receive its result")
	}
	if strings.Contains(m.View().Content, "worker-01") {
		t.Error("a background result scribbled on the visible tab")
	}
}

func TestTabsAreShownInTheContentBorder(t *testing.T) {
	m, _ := drilling(t)
	m.Update(key("enter"))
	deliver(t, m, longTable())

	border := visibleOnly(strings.Split(m.View().Content, "\n")[titleHeight])
	for _, want := range []string{"pools", "nodes", "▸"} {
		if !strings.Contains(border, want) {
			t.Errorf("tab bar %q missing %q", border, want)
		}
	}
}

func TestClickOnATabActivatesIt(t *testing.T) {
	m, _ := drilling(t)
	m.Update(key("enter"))
	deliver(t, m, longTable())

	border := visibleOnly(strings.Split(m.View().Content, "\n")[titleHeight])
	m.Update(click(colOf(t, border, "pools"), titleHeight))

	if m.act != 0 {
		t.Errorf("active tab is %d after clicking the first, want 0", m.act)
	}
}

// The frames must survive several tabs on a narrow window.
func TestTabbedLayoutKeepsItsWidth(t *testing.T) {
	m, _ := drilling(t)
	m.Update(key("enter"))
	deliver(t, m, longTable())

	for _, w := range []int{96, 60, 44} {
		next, _ := m.Update(tea.WindowSizeMsg{Width: w, Height: 20})
		m = next.(*model)
		for i, l := range strings.Split(m.View().Content, "\n") {
			if got := lipgloss.Width(l); got != w {
				t.Errorf("width %d: line %d is %d wide", w, i, got)
			}
		}
	}
}
