package tui

import (
	"context"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/vshn/rincewind/internal/engine"
)

func key(s string) tea.KeyPressMsg {
	switch s {
	case "enter":
		return tea.KeyPressMsg{Code: tea.KeyEnter}
	case "down":
		return tea.KeyPressMsg{Code: tea.KeyDown}
	case "up":
		return tea.KeyPressMsg{Code: tea.KeyUp}
	case "esc":
		return tea.KeyPressMsg{Code: tea.KeyEscape}
	case "tab":
		return tea.KeyPressMsg{Code: tea.KeyTab}
	case "space":
		return tea.KeyPressMsg{Code: tea.KeySpace, Text: " "}
	default:
		return tea.KeyPressMsg{Code: rune(s[0]), Text: s}
	}
}

// sized returns a model that has already received a window size, since
// almost nothing renders before that.
func sized(t *testing.T) *model {
	t.Helper()
	m := newModel(sampleViews())
	next, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 24})
	return next.(*model)
}

// loaded runs the delayed nodes view synchronously and injects its result,
// bypassing the tea.Cmd machinery the way all these tests do.
func loaded(t *testing.T) *model {
	t.Helper()
	m := sized(t)
	m.Update(key("down")) // demo.nodes
	m.Update(key("enter"))

	res, err := m.selected().Run(context.Background(), nil)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	m.Update(resultMsg{seq: m.seq, res: res})
	return m
}

func TestStartsOnFirstViewNotHeader(t *testing.T) {
	m := sized(t)

	cur := m.selected()
	if cur == nil {
		t.Fatal("no view selected on start")
	}
	if cur.ID != "demo.quick" {
		t.Fatalf("selected %q, want demo.quick", cur.ID)
	}
}

func TestNavigationSkipsGroupHeaders(t *testing.T) {
	m := sized(t)

	m.Update(key("down"))
	if got := m.selected().ID; got != "demo.nodes" {
		t.Fatalf("after down: %q, want demo.nodes", got)
	}

	// Past the end it comes round to the first view — never onto the header
	// that sits above it.
	m.Update(key("down"))
	if got := m.selected().ID; got != "demo.quick" {
		t.Fatalf("after second down: %q, want it wrapped to demo.quick", got)
	}
	if m.entries[m.cursor].view == nil {
		t.Fatal("the cursor landed on a group header")
	}

	m.Update(key("up"))
	if got := m.selected().ID; got != "demo.nodes" {
		t.Fatalf("after up: %q, want it wrapped back to demo.nodes", got)
	}
}

func TestEnterRunsWithoutBlocking(t *testing.T) {
	m := sized(t)

	_, cmd := m.Update(key("enter"))
	if !m.cur().running {
		t.Fatal("model is not marked running")
	}
	if cmd == nil {
		t.Fatal("enter produced no command")
	}
	if !strings.Contains(m.View().Content, "running") {
		t.Error("view does not show the running state")
	}

	// The work happens in the command, so the model stayed responsive: the
	// cursor still moves while the run is in flight.
	m.Update(key("down"))
	if got := m.selected().ID; got != "demo.nodes" {
		t.Fatalf("cursor stuck during run: %q", got)
	}
}

func TestResultIsRenderedIntoTable(t *testing.T) {
	m := loaded(t)

	if m.cur().running {
		t.Error("still running after result")
	}

	content := m.View().Content
	for _, want := range []string{"NAME", "worker-02", "NotReady"} {
		if !strings.Contains(content, want) {
			t.Errorf("view missing %q", want)
		}
	}
}

func TestStaleResultIsIgnored(t *testing.T) {
	m := sized(t)
	m.Update(key("enter"))
	current := m.seq

	stale := engine.Result{Cols: []string{"N"}, Rows: []engine.Row{row("stale")}}
	m.Update(resultMsg{seq: current - 1, res: stale})

	if !m.cur().running {
		t.Error("a superseded result ended the current run")
	}
	if strings.Contains(m.View().Content, "stale") {
		t.Error("a superseded result reached the view")
	}
}

func TestErrorIsShown(t *testing.T) {
	m := sized(t)
	m.Update(key("enter"))
	m.Update(resultMsg{seq: m.seq, err: context.DeadlineExceeded})

	if !strings.Contains(m.View().Content, "error:") {
		t.Error("view does not show the error")
	}
	if !strings.Contains(m.View().Content, "timed out") {
		t.Error("a deadline is not named as a timeout")
	}
}

func TestNotesRideBelowTheTable(t *testing.T) {
	m := sized(t)
	m.Update(key("enter"))

	res := nodesTable()
	res.Notes = []string{"column AGE: expression produced no value"}
	m.Update(resultMsg{seq: m.seq, res: res})

	if !strings.Contains(m.View().Content, "column AGE") {
		t.Error("view does not show the note")
	}
}

func TestQuit(t *testing.T) {
	m := sized(t)

	_, cmd := m.Update(key("q"))
	if cmd == nil {
		t.Fatal("q produced no command")
	}
	if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Fatal("q did not quit")
	}
}

func TestSidebarListsGroupsAndViews(t *testing.T) {
	content := sized(t).View().Content

	// The menu shows the id's name part, not the title.
	for _, want := range []string{"demo", "quick", "nodes"} {
		if !strings.Contains(content, want) {
			t.Errorf("sidebar missing %q", want)
		}
	}
	// The title may appear in the title bar box as description, but never
	// below it as a menu entry.
	for i, line := range strings.Split(content, "\n")[2:] {
		if strings.Contains(ansi.Strip(line), "One quick row") {
			t.Errorf("line %d shows the title as a menu entry: %q", i+2, ansi.Strip(line))
		}
	}
}

func TestHelpLineSurvivesNarrowWindows(t *testing.T) {
	m := newModel(sampleViews())
	next, _ := m.Update(tea.WindowSizeMsg{Width: 60, Height: 12})
	m = next.(*model)

	m.Update(key("down"))
	res, err := m.selected().Run(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	m.Update(resultMsg{seq: m.seq, res: res})

	lines := strings.Split(m.View().Content, "\n")
	if len(lines) > 12 {
		t.Fatalf("view is %d lines, window is 12", len(lines))
	}
	// The help sits inside the footer box, one line above its border.
	if !strings.Contains(lines[len(lines)-2], "quit") {
		t.Errorf("footer is not the help line: %q", lines[len(lines)-2])
	}
}

func TestTitleShowsIDAndTitle(t *testing.T) {
	m := sized(t)

	// Line 0 is the title box's border; the text sits inside it.
	title := strings.Split(m.View().Content, "\n")[1]
	for _, want := range []string{"rincewind", "demo.quick", "One quick row"} {
		if !strings.Contains(title, want) {
			t.Errorf("title %q missing %q", ansi.Strip(title), want)
		}
	}
}

func TestTitleDropsDescriptionWhenTooNarrow(t *testing.T) {
	m := newModel(sampleViews())
	next, _ := m.Update(tea.WindowSizeMsg{Width: 30, Height: 20})
	m = next.(*model)

	title := ansi.Strip(strings.Split(m.View().Content, "\n")[1])
	if !strings.Contains(title, "demo.quick") {
		t.Errorf("title lost the id: %q", title)
	}
	if strings.Contains(title, "One quick row") {
		t.Errorf("title kept the description on a narrow window: %q", title)
	}
}

// Every rendered line must be exactly as wide as the window: a frame whose
// right border wanders is the first symptom of broken width math.
func TestEveryLineIsExactlyWindowWide(t *testing.T) {
	m := loaded(t)
	m.Update(key("tab"))
	m.Update(key("space")) // brings the status line in as well
	for i, l := range strings.Split(m.View().Content, "\n") {
		if w := lipgloss.Width(l); w != 100 {
			t.Errorf("line %d has width %d, want 100", i, w)
		}
	}
}

// The screen is four framed regions: title bar, sidebar and content side by
// side, and the split footer with the version on the sidebar's width and the
// global keys on the content's.
func TestEverythingIsFramed(t *testing.T) {
	m := sized(t)
	m.version = "v1.2.3"
	lines := strings.Split(m.View().Content, "\n")

	if got := ansi.Strip(lines[0]); !strings.HasPrefix(got, "╭") {
		t.Errorf("first line is no frame top: %q", got)
	}
	if got := ansi.Strip(lines[len(lines)-1]); !strings.HasSuffix(strings.TrimRight(got, " "), "╯") {
		t.Errorf("last line is no frame bottom: %q", got)
	}

	// The content box carries the current view as a tab in its top border.
	if got := ansi.Strip(lines[3]); !strings.Contains(got, "quick") {
		t.Errorf("body border %q carries no tab for the view", got)
	}

	// Footer: version left, keys right, on the same line inside the boxes.
	footer := ansi.Strip(lines[len(lines)-2])
	if !strings.Contains(footer, "v1.2.3") {
		t.Errorf("footer %q does not show the version", footer)
	}
	if !strings.Contains(footer, "quit") {
		t.Errorf("footer %q does not show the global keys", footer)
	}
	if strings.Index(footer, "v1.2.3") > strings.Index(footer, "quit") {
		t.Errorf("footer %q has version and keys in the wrong order", footer)
	}
}

func TestRefreshingViewSchedulesATick(t *testing.T) {
	views := sampleViews()
	views[0].Refresh = 1000000000 // 1s
	m := newModel(views)
	next, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 24})
	m = next.(*model)

	m.Update(key("enter"))
	res, _ := m.selected().Run(context.Background(), nil)
	_, cmd := m.Update(resultMsg{seq: m.seq, res: res})
	if cmd == nil {
		t.Fatal("a refreshing view scheduled no tick after its result")
	}

	// The tick re-runs the view — but only when it is not stale.
	m.Update(refreshMsg{seq: m.seq - 1})
	if m.cur().running {
		t.Fatal("a stale tick re-ran the view")
	}
	m.Update(refreshMsg{seq: m.seq})
	if !m.cur().running {
		t.Fatal("the tick did not re-run the view")
	}
}

// The menu is a ring: it is short and visible whole, so the far end is
// nearer than the way back.
func TestMenuWrapsAround(t *testing.T) {
	m := newModel(manyViews())
	next, _ := m.Update(tea.WindowSizeMsg{Width: 96, Height: 20})
	m = next.(*model)

	first := m.cursor
	m.Update(key("up"))
	last := m.cursor
	if last == first {
		t.Fatal("up on the first entry stayed put")
	}
	if m.entries[last].view == nil {
		t.Error("it wrapped onto a group header")
	}

	m.Update(key("down"))
	if m.cursor != first {
		t.Errorf("down from the last entry went to %d, want back to %d", m.cursor, first)
	}
}

// A page that wrapped would land in the middle after running off the end,
// which reads as a miss rather than as coming round.
func TestPagingStopsAtTheEnds(t *testing.T) {
	m := newModel(manyViews())
	next, _ := m.Update(tea.WindowSizeMsg{Width: 96, Height: 20})
	m = next.(*model)

	m.Update(key("pgup"))
	if got := m.cursor; got != m.next(-1, +1) {
		t.Errorf("pgup from the top moved to %d", got)
	}

	m.Update(key("end"))
	last := m.cursor
	m.Update(key("pgdown"))
	if m.cursor != last {
		t.Errorf("pgdown from the bottom moved to %d, want it to stay at %d", m.cursor, last)
	}
}

// The row cursor is not a ring: a table is not visible whole, and losing
// one's place in two hundred rows is not a shortcut.
func TestRowCursorDoesNotWrap(t *testing.T) {
	m := copying(t) // a table with four rows, output pane focused

	m.Update(key("up"))
	if m.cur().row != 0 {
		t.Errorf("up from the first row went to %d", m.cur().row)
	}
	m.Update(key("end"))
	last := m.cur().row
	m.Update(key("down"))
	if m.cur().row != last {
		t.Errorf("down from the last row went to %d, want it to stay at %d", m.cur().row, last)
	}
}
