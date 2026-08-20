package tui

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
)

func names(rows []any) []string {
	var out []string
	for _, r := range rows {
		out = append(out, r.(map[string]any)["name"].(string))
	}
	return out
}

// The rule the old tool did not have: a mutation acts on what was marked,
// not on whatever the filter happened to leave on screen.
func TestWorkingActionRefusesWithoutASelection(t *testing.T) {
	m, rec := drilling(t)

	m.Update(key("p"))

	if len(rec.planned) != 0 {
		t.Fatalf("the action planned %v without a selection", rec.planned)
	}
	if len(m.tabs) != 1 {
		t.Error("a job tab opened with nothing selected")
	}
	if !strings.Contains(m.View().Content, "select rows first") {
		t.Error("nothing on screen says why nothing happened")
	}
}

// The notice is for the keystroke that caused it and no longer.
func TestTheNoticeClearsOnTheNextKey(t *testing.T) {
	m, _ := drilling(t)
	m.Update(key("p"))
	m.Update(key("down"))

	if strings.Contains(m.View().Content, "select rows first") {
		t.Error("the notice outlived the keystroke it belonged to")
	}
}

// The point of the job tab: the exact invocation is on screen before the
// question is asked, so the question can actually be answered.
func TestActionShowsTheCommandBeforeAsking(t *testing.T) {
	m, _ := drilling(t)
	m.Update(key("space")) // select infra

	m.Update(key("p"))

	if len(m.tabs) != 2 || m.cur().job == nil {
		t.Fatalf("%d tabs, want a job tab open", len(m.tabs))
	}
	if m.cur().job.done || m.cur().running {
		t.Error("the action ran before it was confirmed")
	}

	content := visibleOnly(m.View().Content)
	for _, want := range []string{"name=infra", "sh -c", "echo", "Pause 1", "1 row", "[y/N]"} {
		if !strings.Contains(content, want) {
			t.Errorf("the screen is missing %q", want)
		}
	}
}

func TestOnlyYConfirms(t *testing.T) {
	m, _ := drilling(t)
	m.Update(key("space"))

	m.Update(key("p"))
	press(t, m, "j") // a stray key must cancel, not agree

	if len(m.tabs) != 1 {
		t.Errorf("%d tabs after cancelling, want the job tab closed", len(m.tabs))
	}
	if m.pending != nil {
		t.Error("the question is still pending")
	}

	m.Update(key("p"))
	press(t, m, "y")

	if m.cur().job == nil || !m.cur().job.done {
		t.Fatal("y did not run the action")
	}
}

func TestWorkingActionPlansEverySelectedRow(t *testing.T) {
	m, rec := drilling(t)
	m.Update(key("a")) // select all

	m.Update(key("p"))

	if len(rec.planned) != 1 {
		t.Fatalf("planned %d times, want once with every row", len(rec.planned))
	}
	if got := names(rec.planned[0]); len(got) != 2 || got[0] != "infra" || got[1] != "worker" {
		t.Errorf("action got %v, want both pools in view order", got)
	}
	// One invocation per row, each naming one object.
	if n := len(m.cur().job.steps); n != 2 {
		t.Errorf("%d steps, want one per row", n)
	}
}

// An action without a confirmation runs straight away, but still in a tab
// of its own so what it did is on the record.
func TestUnconfirmedActionRunsAtOnce(t *testing.T) {
	m, _ := drilling(t)
	m.Update(key("space"))

	press(t, m, "u")

	if m.cur().job == nil || !m.cur().job.done {
		t.Fatal("the action did not run")
	}
	if m.pending != nil {
		t.Error("it asked despite having no confirmation")
	}
}

// The output belongs in the job tab, beside the command that produced it —
// not under the table, where it would shift the rows about.
func TestOutputLandsInTheJobTab(t *testing.T) {
	m, _ := drilling(t)
	m.Update(key("space"))
	m.Update(key("p"))
	press(t, m, "y")

	content := visibleOnly(m.View().Content)
	if !strings.Contains(content, "patched infra") {
		t.Error("what the script printed is not on screen")
	}
	if !strings.Contains(content, "1 step ok") {
		t.Error("the job does not summarise how it went")
	}

	// Back on the view, the rows are untouched by any of it.
	m.Update(key("esc"))
	if got := visibleOnly(m.View().Content); strings.Contains(got, "patched infra") {
		t.Error("the action's output leaked into the view")
	}
}

// After a change it is the view that answers, not the patch message.
func TestReloadingActionRunsTheViewAgain(t *testing.T) {
	m, rec := drilling(t)
	m.Update(key("space"))
	before := rec.parent

	press(t, m, "u") // unpause: reload: true

	if rec.parent != before+1 {
		t.Errorf("the view ran %d times, want one more than %d", rec.parent, before)
	}
}

// Config keys must not fire from the menu, where enter means "run this view".
func TestActionKeysNeedTheOutputPane(t *testing.T) {
	m, rec := drilling(t)
	m.Update(key("space"))
	m.Update(key("tab")) // back to the menu

	m.Update(key("p"))

	if len(rec.planned) != 0 || m.pending != nil {
		t.Error("an action fired from the menu")
	}
}

// A job tab is a record of what was done; there is nothing to act on in it.
func TestJobTabOffersNoActions(t *testing.T) {
	m, _ := drilling(t)
	m.Update(key("space"))
	press(t, m, "u")

	if m.actionBoxHeight() != 0 {
		t.Error("the job tab still shows an action box")
	}
	if m.actionFor("p") != nil {
		t.Error("an action key still resolves inside a job tab")
	}
}

// actionBoxLine is the one line inside the action box: the box sits under
// the content box, and its text is the line before its bottom border.
func actionBoxLine(m *model) string {
	lines := strings.Split(m.View().Content, "\n")
	return visibleOnly(lines[titleHeight+m.bodyBoxHeight()+m.actionBoxHeight()-2])
}

// The actions get a framed box of their own, under the output.
func TestActionsAreShownInTheirOwnBox(t *testing.T) {
	m, _ := drilling(t)

	line := actionBoxLine(m)
	for _, want := range []string{"⏎ nodes", "p pause", "u unpause"} {
		if !strings.Contains(line, want) {
			t.Errorf("action box %q missing %q", line, want)
		}
	}

	// The box is framed: a border of its own above and below.
	lines := strings.Split(m.View().Content, "\n")
	top := visibleOnly(lines[titleHeight+m.bodyBoxHeight()])
	if !strings.Contains(top, "╭") {
		t.Errorf("the action box has no top border: %q", top)
	}
}

// A view with nothing to offer gets no empty frame: three lines to say so
// would be three lines of table.
func TestNoActionBoxWithoutActions(t *testing.T) {
	m := loaded(t) // demo.nodes has no actions
	if m.actionBoxHeight() != 0 {
		t.Error("a view without actions still reserved the box")
	}
	if strings.Contains(m.View().Content, "⏎ nodes") {
		t.Error("an action bar appeared for a view with no actions")
	}
}

// The question takes the box over, so it sits where the answer is expected.
func TestConfirmationTakesOverTheActionBox(t *testing.T) {
	m, _ := drilling(t)
	m.Update(key("space"))
	m.Update(key("p"))

	line := actionBoxLine(m)
	if !strings.Contains(line, "[y/N]") {
		t.Errorf("action box %q is not the question", line)
	}
	if strings.Contains(line, "p pause") {
		t.Error("the action legend is still there under the question")
	}
}

// The box costs the table its height, and nothing else on screen.
func TestActionBoxKeepsTheLayoutWhole(t *testing.T) {
	m, _ := drilling(t)
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
