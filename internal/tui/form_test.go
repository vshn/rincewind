package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// typeText sends a word one keystroke at a time, the way the user does.
func typeText(m *model, s string) {
	for _, r := range s {
		m.Update(key(string(r)))
	}
}

// An action that acts on nothing must not demand a selection first. Making
// someone select rows for a create would be asking for something it will
// never look at.
func TestRowlessActionNeedsNoSelection(t *testing.T) {
	m, _ := drilling(t)

	m.Update(key("n"))

	if strings.Contains(m.View().Content, "select rows first") {
		t.Fatal("a rowless action demanded a selection")
	}
	if len(m.tabs) != 2 || m.cur().job == nil {
		t.Fatalf("%d tabs, want the job tab open", len(m.tabs))
	}
}

// The form comes before the plan, because the values are what make the
// command concrete — there is nothing to show until they are in.
func TestFormIsAskedBeforePlanning(t *testing.T) {
	m, rec := drilling(t)

	m.Update(key("n"))

	if !m.cur().job.asking() {
		t.Fatal("the job did not start with its form")
	}
	if len(rec.planArgs) != 0 {
		t.Errorf("planned %v before the form was filled in", rec.planArgs)
	}
	content := visibleOnly(m.View().Content)
	for _, want := range []string{"create", "(1 of 2)", "pool name", "name:"} {
		if !strings.Contains(content, want) {
			t.Errorf("the form is missing %q", want)
		}
	}
}

// One field at a time, with the answered ones still on screen: by the fourth
// field you have forgotten the first, and this is the last chance to catch a
// typo before confirming.
func TestAnsweredFieldsStayOnScreen(t *testing.T) {
	m, _ := drilling(t)
	m.Update(key("n"))

	typeText(m, "infra")
	m.Update(key("enter"))

	content := visibleOnly(m.View().Content)
	if !strings.Contains(content, "name: infra") {
		t.Error("the answered field is not on screen")
	}
	if !strings.Contains(content, "(2 of 2)") {
		t.Error("the form did not move to the next field")
	}
}

// A default is shown rather than applied invisibly, so plain enter takes it
// and one can see what was taken.
func TestDefaultIsPrefilledAndCarried(t *testing.T) {
	m, rec := drilling(t)
	m.Update(key("n"))
	typeText(m, "infra")
	m.Update(key("enter"))

	if got := m.input.Value(); got != "3" {
		t.Errorf("the field holds %q, want the default 3", got)
	}
	m.Update(key("enter")) // take it

	if len(rec.planArgs) != 1 {
		t.Fatalf("planned %d times, want once", len(rec.planArgs))
	}
	if got := rec.planArgs[0]["size"]; got != "3" {
		t.Errorf("planned with size %q, want the default", got)
	}
}

// While a field is open the input owns the keyboard, or `q` would quit
// halfway through a word.
func TestTheFieldOwnsTheKeyboard(t *testing.T) {
	m, _ := drilling(t)
	m.Update(key("n"))

	_, cmd := m.Update(key("q"))
	if cmd != nil {
		if _, quit := cmd().(tea.QuitMsg); quit {
			t.Fatal("q inside a field quit the program")
		}
	}
	if got := m.input.Value(); got != "q" {
		t.Fatalf("the field holds %q, want q", got)
	}
	// And an action key does not fire a second action.
	m.Update(key("p"))
	if len(m.tabs) != 2 {
		t.Errorf("%d tabs, want the one form still open", len(m.tabs))
	}
}

func TestARequiredFieldRefusesToBeEmpty(t *testing.T) {
	m, rec := drilling(t)
	m.Update(key("n"))

	m.Update(key("enter")) // name is required and empty

	if !m.cur().job.asking() || m.cur().job.idx != 0 {
		t.Error("the form moved on from an empty required field")
	}
	if len(rec.planArgs) != 0 {
		t.Error("it planned anyway")
	}
	if !strings.Contains(m.View().Content, "name is required") {
		t.Error("nothing on screen says why enter did nothing")
	}
}

// esc abandons the whole thing: nothing was planned, nothing ran, and the
// tab that was opened to ask goes with it.
func TestEscAbandonsTheForm(t *testing.T) {
	m, rec := drilling(t)
	m.Update(key("n"))
	typeText(m, "infra")

	m.Update(key("esc"))

	if len(m.tabs) != 1 {
		t.Errorf("%d tabs after esc, want the form closed", len(m.tabs))
	}
	if len(rec.planArgs) != 0 {
		t.Error("it planned despite being abandoned")
	}
}

// The whole point of collecting first: the command on screen carries the
// values, so the question below it can be answered.
func TestCollectedValuesReachTheCommand(t *testing.T) {
	m, _ := drilling(t)
	m.Update(key("n"))
	typeText(m, "infra")
	m.Update(key("enter"))
	m.Update(key("enter")) // take the default

	content := visibleOnly(m.View().Content)
	for _, want := range []string{"name=infra", "size=3", "Create pool infra", "[y/N]"} {
		if !strings.Contains(content, want) {
			t.Errorf("the screen is missing %q", want)
		}
	}
	if m.cur().job.done {
		t.Error("it ran before the question was answered")
	}

	press(t, m, "y")
	if !m.cur().job.done {
		t.Fatal("y did not run it")
	}
	if !strings.Contains(visibleOnly(m.View().Content), "created infra/3") {
		t.Error("the output is not on screen")
	}
}

// A rowless confirmation says nothing about rows, because there are none —
// and the one step it will make is on screen above the question.
func TestRowlessConfirmationCountsNoRows(t *testing.T) {
	m, _ := drilling(t)
	m.Update(key("n"))
	typeText(m, "infra")
	m.Update(key("enter"))
	m.Update(key("enter"))

	if got := m.cur().job.prompt; strings.Contains(got, "row") {
		t.Errorf("prompt %q counts rows for a rowless action", got)
	}
}

// The form and the question are tab content, which scrolls — so neither can
// push the footer off a short window.
func TestFormKeepsTheLayoutWhole(t *testing.T) {
	m, _ := drilling(t)
	m.Update(key("n"))
	typeText(m, "infra")
	m.Update(key("enter"))

	for _, size := range []struct{ w, h int }{{96, 20}, {60, 12}} {
		next, _ := m.Update(tea.WindowSizeMsg{Width: size.w, Height: size.h})
		m = next.(*model)
		lines := strings.Split(m.View().Content, "\n")
		if len(lines) != size.h {
			t.Errorf("%dx%d: %d lines", size.w, size.h, len(lines))
		}
		for i, l := range lines {
			if got := lipgloss.Width(l); got != size.w {
				t.Errorf("%dx%d: line %d is %d wide", size.w, size.h, i, got)
			}
		}
	}
}
