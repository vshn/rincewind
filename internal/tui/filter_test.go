package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

func typeFilter(m *model, needle string) {
	m.Update(key("/"))
	for _, r := range needle {
		m.Update(key(string(r)))
	}
	m.Update(key("enter"))
}

func TestFilterNarrowsRows(t *testing.T) {
	m := loaded(t)
	typeFilter(m, "notready")

	content := m.View().Content
	if !strings.Contains(content, "worker-02") {
		t.Error("the matching row is gone")
	}
	if strings.Contains(content, "worker-01") {
		t.Error("a non-matching row survived the filter")
	}
	if !strings.Contains(visibleOnly(content), "1 of 4") {
		t.Error("the filter count is not shown")
	}
}

func TestEscClearsTheFilter(t *testing.T) {
	m := loaded(t)
	typeFilter(m, "notready")

	m.Update(key("esc"))
	if !strings.Contains(m.View().Content, "worker-01") {
		t.Error("clearing the filter did not bring the rows back")
	}
}

func TestQTypedIntoTheFilterDoesNotQuit(t *testing.T) {
	m := loaded(t)
	m.Update(key("/"))

	_, cmd := m.Update(key("q"))
	if cmd != nil {
		if _, quit := cmd().(tea.QuitMsg); quit {
			t.Fatal("q inside the filter quit the program")
		}
	}
	if got := m.filter.Value(); got != "q" {
		t.Fatalf("filter value %q, want q", got)
	}
}

func TestNoMatchSaysSo(t *testing.T) {
	m := loaded(t)
	typeFilter(m, "zzz")

	if !strings.Contains(m.View().Content, "nothing matches") {
		t.Error("an empty filter result does not say so")
	}
}

func TestRowCursorIsClampedByTheFilter(t *testing.T) {
	m := loaded(t)
	m.Update(key("tab"))
	m.Update(key("down"))
	m.Update(key("down"))
	m.Update(key("down")) // last of four rows

	typeFilter(m, "notready") // one row left

	m.Update(key("space"))
	if !m.selection["worker-02"] {
		t.Fatalf("selected %v, want worker-02: the cursor was not clamped", selectedKeys(m))
	}
}
