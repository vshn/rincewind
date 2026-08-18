package tui

import (
	"context"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/vshn/aldediggi/internal/catalog"
)

func key(s string) tea.KeyPressMsg {
	switch s {
	case "enter":
		return tea.KeyPressMsg{Code: tea.KeyEnter}
	case "down":
		return tea.KeyPressMsg{Code: tea.KeyDown}
	case "up":
		return tea.KeyPressMsg{Code: tea.KeyUp}
	default:
		return tea.KeyPressMsg{Code: rune(s[0]), Text: s}
	}
}

// sized returns a model that has already received a window size, since almost
// nothing renders before that.
func sized(t *testing.T) *model {
	t.Helper()
	m := newModel(sampleRegistry())
	next, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 24})
	return next.(*model)
}

func TestStartsOnFirstViewNotHeader(t *testing.T) {
	m := sized(t)

	cur := m.selected()
	if cur == nil {
		t.Fatal("no view selected on start")
	}
	if cur.ID() != "demo.text" {
		t.Fatalf("selected %q, want demo.text", cur.ID())
	}
}

func TestNavigationSkipsGroupHeaders(t *testing.T) {
	m := sized(t)

	m.Update(key("down"))
	if got := m.selected().ID(); got != "demo.table" {
		t.Fatalf("after down: %q, want demo.table", got)
	}

	// Past the end the cursor must hold, not wrap onto a header.
	m.Update(key("down"))
	if got := m.selected().ID(); got != "demo.table" {
		t.Fatalf("after second down: %q, want demo.table", got)
	}

	m.Update(key("up"))
	if got := m.selected().ID(); got != "demo.text" {
		t.Fatalf("after up: %q, want demo.text", got)
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
	if got := m.selected().ID(); got != "demo.table" {
		t.Fatalf("cursor stuck during run: %q", got)
	}
}

func TestResultIsRenderedIntoTable(t *testing.T) {
	m := sized(t)
	m.Update(key("down")) // demo.table
	m.Update(key("enter"))

	res, err := m.selected().Run(context.Background(), nil)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	m.Update(resultMsg{seq: m.seq, res: res})

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

	m.Update(resultMsg{seq: current - 1, res: catalog.Text{Lines: []string{"stale"}}})

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

	for _, want := range []string{"demo", "text", "table"} {
		if !strings.Contains(content, want) {
			t.Errorf("sidebar missing %q", want)
		}
	}
}

func TestHelpLineSurvivesNarrowWindows(t *testing.T) {
	m := newModel(sampleRegistry())
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
	if !strings.Contains(lines[len(lines)-1], "quit") {
		t.Errorf("last line is not the help line: %q", lines[len(lines)-1])
	}
}

// The third part of the title is the same sentence `--help` prints, so both
// have to come from the one field in the registry.
func TestTitleShowsIDAndDescription(t *testing.T) {
	m := sized(t)

	cur := m.selected()
	title := strings.Split(m.View().Content, "\n")[0]

	for _, want := range []string{"aldediggi", cur.ID(), cur.Short} {
		if !strings.Contains(title, want) {
			t.Errorf("title %q missing %q", visibleOnly(title), want)
		}
	}
}

// A long description must not push the id off screen.
func TestTitleDropsDescriptionWhenTooNarrow(t *testing.T) {
	m := newModel(sampleRegistry())
	next, _ := m.Update(tea.WindowSizeMsg{Width: 30, Height: 20})
	m = next.(*model)

	title := visibleOnly(strings.Split(m.View().Content, "\n")[0])
	if !strings.Contains(title, "demo.text") {
		t.Errorf("title lost the id: %q", title)
	}
	if strings.Contains(title, "Echo a line") {
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
