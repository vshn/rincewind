package tui

import (
	"context"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/vshn/aldediggi/internal/catalog"
)

func typed(m *model, s string) {
	for _, r := range s {
		m.Update(key(string(r)))
	}
}

// rows builds a view over three named rows.
func rowsView(seen *[]string) *catalog.Registry {
	r := catalog.NewRegistry()
	r.Describe("test", "test")
	r.Add(catalog.View{
		Group: "test", Name: "view", Short: "a view",
		Actions: []catalog.Action{{
			Name: "act", Key: "a",
			Run: func(_ context.Context, in catalog.Result, _ catalog.Args) (catalog.Result, error) {
				for _, src := range in.(catalog.Table).Sources() {
					*seen = append(*seen, src.(string))
				}
				return nil, nil
			},
		}},
		Run: func(context.Context, catalog.Args) (catalog.Result, error) {
			t := catalog.Table{Cols: []string{"NAME"}}
			t.Add("root", "root")
			t.Add("openshift-logging", "openshift-logging")
			t.Add("cluster-tools", "cluster-tools")
			return t, nil
		},
	})
	return r
}

func ready(t *testing.T, reg *catalog.Registry) *model {
	t.Helper()
	m := newModel(reg)
	next, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 24})
	m = next.(*model)

	m.Update(key("enter"))
	res, err := m.selected().Run(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	m.Update(resultMsg{seq: m.seq, res: res})
	return m
}

func TestFilterNarrowsTheView(t *testing.T) {
	var seen []string
	m := ready(t, rowsView(&seen))

	m.Update(key("/"))
	typed(m, "log")

	content := visibleOnly(m.View().Content)
	if !strings.Contains(content, "openshift-logging") {
		t.Error("the matching row is gone")
	}
	if strings.Contains(content, "cluster-tools") {
		t.Error("a non-matching row survived")
	}
	if !strings.Contains(content, "1 of 3") {
		t.Errorf("no count shown: %q", content)
	}
}

// This is the promise the whole no-row-selection design rests on.
func TestActionOperatesOnTheFilteredSet(t *testing.T) {
	var seen []string
	m := ready(t, rowsView(&seen))

	m.Update(key("/"))
	typed(m, "log")
	m.Update(key("enter")) // keep the filter, leave input mode

	_, cmd := m.Update(key("a"))
	if cmd == nil {
		t.Fatal("action did not fire")
	}
	cmd()

	if len(seen) != 1 || seen[0] != "openshift-logging" {
		t.Errorf("action saw %v, want just the filtered row", seen)
	}
}

// Otherwise "q" would quit halfway through typing a word.
func TestFilterInputOwnsTheKeyboard(t *testing.T) {
	var seen []string
	m := ready(t, rowsView(&seen))

	m.Update(key("/"))
	typed(m, "q")

	if m.filter.Value() != "q" {
		t.Errorf("filter is %q; q was treated as quit", m.filter.Value())
	}
}

func TestActionKeysAreInertWhileTyping(t *testing.T) {
	var seen []string
	m := ready(t, rowsView(&seen))

	m.Update(key("/"))
	typed(m, "a")

	if len(seen) > 0 {
		t.Error("typing the action key ran the action")
	}
	if m.filter.Value() != "a" {
		t.Errorf("filter is %q", m.filter.Value())
	}
}

func TestEscapeClearsTheFilter(t *testing.T) {
	var seen []string
	m := ready(t, rowsView(&seen))

	m.Update(key("/"))
	typed(m, "log")
	m.Update(key("esc"))

	if m.filter.Value() != "" {
		t.Errorf("filter survived escape: %q", m.filter.Value())
	}
	if !strings.Contains(visibleOnly(m.View().Content), "cluster-tools") {
		t.Error("rows did not come back")
	}
}

// A filter left over from the previous view would silently narrow the next one.
func TestRunningAViewClearsTheFilter(t *testing.T) {
	var seen []string
	m := ready(t, rowsView(&seen))

	m.Update(key("/"))
	typed(m, "log")
	m.Update(key("enter"))
	m.Update(key("r")) // reload

	if m.filter.Value() != "" {
		t.Errorf("filter carried over: %q", m.filter.Value())
	}
}
