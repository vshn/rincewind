package tui

import (
	"context"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/vshn/aldediggi/internal/engine"
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

	res, err := m.selected().Run(context.Background())
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

	// Past the end the cursor must hold, not wrap onto a header.
	m.Update(key("down"))
	if got := m.selected().ID; got != "demo.nodes" {
		t.Fatalf("after second down: %q, want demo.nodes", got)
	}

	m.Update(key("up"))
	if got := m.selected().ID; got != "demo.quick" {
		t.Fatalf("after up: %q, want demo.quick", got)
	}
}

func TestEnterRunsWithoutBlocking(t *testing.T) {
	m := sized(t)

	_, cmd := m.Update(key("enter"))
	if !m.running {
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

	if m.running {
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

	stale := engine.Table{Cols: []string{"N"}, Rows: []engine.Row{row("stale")}}
	m.Update(resultMsg{seq: current - 1, res: stale})

	if !m.running {
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
	// The title may appear in the title bar (first line) as description,
	// but never below it as a menu entry.
	for i, line := range strings.Split(content, "\n")[1:] {
		if strings.Contains(visibleOnly(line), "One quick row") {
			t.Errorf("line %d shows the title as a menu entry: %q", i+1, visibleOnly(line))
		}
	}
}

func TestHelpLineSurvivesNarrowWindows(t *testing.T) {
	m := newModel(sampleViews())
	next, _ := m.Update(tea.WindowSizeMsg{Width: 60, Height: 12})
	m = next.(*model)

	m.Update(key("down"))
	res, err := m.selected().Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	m.Update(resultMsg{seq: m.seq, res: res})

	lines := strings.Split(m.View().Content, "\n")
	if len(lines) > 12 {
		t.Fatalf("view is %d lines, window is 12", len(lines))
	}
	if !strings.Contains(lines[len(lines)-1], "quit") {
		t.Errorf("last line is not the help line: %q", lines[len(lines)-1])
	}
}

func TestTitleShowsIDAndTitle(t *testing.T) {
	m := sized(t)

	title := strings.Split(m.View().Content, "\n")[0]
	for _, want := range []string{"aldediggi", "demo.quick", "One quick row"} {
		if !strings.Contains(title, want) {
			t.Errorf("title %q missing %q", visibleOnly(title), want)
		}
	}
}

func TestTitleDropsDescriptionWhenTooNarrow(t *testing.T) {
	m := newModel(sampleViews())
	next, _ := m.Update(tea.WindowSizeMsg{Width: 30, Height: 20})
	m = next.(*model)

	title := visibleOnly(strings.Split(m.View().Content, "\n")[0])
	if !strings.Contains(title, "demo.quick") {
		t.Errorf("title lost the id: %q", title)
	}
	if strings.Contains(title, "One quick row") {
		t.Errorf("title kept the description on a narrow window: %q", title)
	}
}

func TestTitleAndHelpAreSetOffByBlankLines(t *testing.T) {
	lines := strings.Split(sized(t).View().Content, "\n")

	if got := visibleOnly(lines[1]); strings.TrimSpace(got) != "" {
		t.Errorf("line below the title is %q, want blank", got)
	}
	if got := visibleOnly(lines[len(lines)-2]); strings.TrimSpace(got) != "" {
		t.Errorf("line above the help is %q, want blank", got)
	}
	if !strings.Contains(lines[len(lines)-1], "quit") {
		t.Errorf("last line is not the help line: %q", visibleOnly(lines[len(lines)-1]))
	}
}

func TestRefreshingViewSchedulesATick(t *testing.T) {
	views := sampleViews()
	views[0].Refresh = 1000000000 // 1s
	m := newModel(views)
	next, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 24})
	m = next.(*model)

	m.Update(key("enter"))
	res, _ := m.selected().Run(context.Background())
	_, cmd := m.Update(resultMsg{seq: m.seq, res: res})
	if cmd == nil {
		t.Fatal("a refreshing view scheduled no tick after its result")
	}

	// The tick re-runs the view — but only when it is not stale.
	m.Update(refreshMsg{seq: m.seq - 1})
	if m.running {
		t.Fatal("a stale tick re-ran the view")
	}
	m.Update(refreshMsg{seq: m.seq})
	if !m.running {
		t.Fatal("the tick did not re-run the view")
	}
}
