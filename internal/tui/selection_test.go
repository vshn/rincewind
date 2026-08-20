package tui

import (
	"strings"
	"testing"

	"github.com/vshn/aldediggi/internal/engine"
)

func selectedKeys(m *model) []string {
	var out []string
	for k := range m.cur().selection {
		out = append(out, k)
	}
	return out
}

func TestSpaceTogglesRowAndMovesOn(t *testing.T) {
	m := loaded(t)
	m.Update(key("tab")) // focus the output pane

	m.Update(key("space"))
	if !m.cur().selection["worker-01"] {
		t.Fatalf("selected %v, want worker-01", selectedKeys(m))
	}
	if m.cur().row != 1 {
		t.Fatalf("cursor at %d after space, want 1", m.cur().row)
	}

	// A second space on the same row deselects.
	m.Update(key("up"))
	m.Update(key("space"))
	if len(m.cur().selection) != 0 {
		t.Fatalf("selected %v, want none", selectedKeys(m))
	}
}

func TestSelectionCountIsShown(t *testing.T) {
	m := loaded(t)
	m.Update(key("tab"))
	m.Update(key("space"))
	m.Update(key("space"))

	if !strings.Contains(m.View().Content, "2 selected") {
		t.Error("view does not show the selection count")
	}
}

func TestSpaceNeedsTheOutputPane(t *testing.T) {
	m := loaded(t)

	m.Update(key("space"))
	if len(m.cur().selection) != 0 {
		t.Fatalf("space selected %v from the sidebar", selectedKeys(m))
	}
}

func TestAToggleAllRespectsTheFilter(t *testing.T) {
	m := loaded(t)

	m.Update(key("/"))
	for _, r := range "worker" {
		m.Update(key(string(r)))
	}
	m.Update(key("enter"))

	m.Update(key("a"))
	if len(m.cur().selection) != 2 || !m.cur().selection["worker-01"] || !m.cur().selection["worker-02"] {
		t.Fatalf("selected %v, want the two workers", selectedKeys(m))
	}

	// All visible rows are selected, so a again clears them.
	m.Update(key("a"))
	if len(m.cur().selection) != 0 {
		t.Fatalf("selected %v after second a, want none", selectedKeys(m))
	}
}

func TestSelectionSurvivesAReloadThatReorders(t *testing.T) {
	m := loaded(t)
	m.Update(key("tab"))
	m.Update(key("space")) // worker-01

	m.Update(key("r"))
	reordered := nodesTable()
	for i, j := 0, len(reordered.Rows)-1; i < j; i, j = i+1, j-1 {
		reordered.Rows[i], reordered.Rows[j] = reordered.Rows[j], reordered.Rows[i]
	}
	m.Update(resultMsg{seq: m.seq, res: reordered})

	if !m.cur().selection["worker-01"] {
		t.Fatalf("selected %v after reload, want worker-01", selectedKeys(m))
	}
}

func TestSelectionIsPrunedWhenRowsVanish(t *testing.T) {
	m := loaded(t)
	m.Update(key("tab"))
	m.Update(key("space")) // worker-01

	m.Update(key("r"))
	shrunk := engine.Table{Cols: nodesTable().Cols, Rows: nodesTable().Rows[1:]}
	m.Update(resultMsg{seq: m.seq, res: shrunk})

	if len(m.cur().selection) != 0 {
		t.Fatalf("selection %v survived rows that are gone", selectedKeys(m))
	}
}

func TestEscClearsSelectionBeforeUnfocusing(t *testing.T) {
	m := loaded(t)
	m.Update(key("tab"))
	m.Update(key("space"))

	m.Update(key("esc"))
	if len(m.cur().selection) != 0 {
		t.Fatal("esc did not clear the selection")
	}
	if m.focus != paneOutput {
		t.Fatal("esc dropped the focus together with the selection")
	}

	m.Update(key("esc"))
	if m.focus != paneList {
		t.Fatal("second esc did not return to the list")
	}
}

func TestOpeningAViewDropsTheSelection(t *testing.T) {
	m := loaded(t)
	m.Update(key("tab"))
	m.Update(key("space"))

	m.Update(key("tab")) // back to the list
	m.Update(key("up"))  // demo/quick
	m.Update(key("enter"))

	if len(m.cur().selection) != 0 {
		t.Fatalf("selection %v leaked into another view", selectedKeys(m))
	}
}
