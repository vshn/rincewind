package tui

import (
	"context"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/vshn/aldediggi/internal/catalog"
)

// twoLevel builds an apps view that drills into a hidden detail view, and
// records what each action was handed.
func twoLevel(seen *[]string) *catalog.Registry {
	r := catalog.NewRegistry()
	r.Describe("test", "test")

	r.Add(catalog.View{
		Group: "test", Name: "apps", Short: "apps",
		Actions: []catalog.Action{
			{
				Name: "detail", Key: "enter",
				Run: func(_ context.Context, in catalog.Result, _ catalog.Args) (catalog.Result, error) {
					rows := in.(catalog.Table).Sources()
					return catalog.Navigate{
						View: "test.detail",
						Args: catalog.Args{"app": rows[0].(string)},
					}, nil
				},
			},
			{
				Name: "act", Key: "a",
				Run: func(_ context.Context, in catalog.Result, _ catalog.Args) (catalog.Result, error) {
					for _, src := range in.(catalog.Table).Sources() {
						*seen = append(*seen, src.(string))
					}
					return nil, nil
				},
			},
		},
		Run: func(context.Context, catalog.Args) (catalog.Result, error) {
			t := catalog.Table{Cols: []string{"NAME"}}
			t.Add("root", "root")
			t.Add("tools", "tools")
			return t, nil
		},
	})

	r.Add(catalog.View{
		Group: "test", Name: "detail", Short: "detail",
		Hidden: true,
		Params: []catalog.Param{{Name: "app", Required: true}},
		Run: func(_ context.Context, args catalog.Args) (catalog.Result, error) {
			return catalog.Text{Lines: []string{"detail of " + args.Get("app")}}, nil
		},
	})

	return r
}

func loaded(t *testing.T, reg *catalog.Registry) *model {
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

// A view that needs a parameter has no business in the list.
func TestHiddenViewsAreNotListed(t *testing.T) {
	var seen []string
	m := loaded(t, twoLevel(&seen))

	for _, e := range m.entries {
		if e.view != nil && e.view.Name == "detail" {
			t.Fatal("a hidden view is in the sidebar")
		}
	}
}

func TestTabMovesFocusAndSelectsARow(t *testing.T) {
	var seen []string
	m := loaded(t, twoLevel(&seen))

	if m.focus != paneList {
		t.Fatal("focus does not start on the list")
	}
	m.Update(key("tab"))
	if m.focus != paneOutput {
		t.Fatal("tab did not move focus")
	}

	// With the output focused the arrows walk rows, not the sidebar.
	before := m.cursor
	m.Update(key("down"))
	if m.cursor != before {
		t.Error("the sidebar cursor moved while the output had focus")
	}
	if m.row != 1 {
		t.Errorf("row = %d, want 1", m.row)
	}
}

// The whole point: with a row selected, the action gets that row alone.
func TestActionTargetsTheSelectedRow(t *testing.T) {
	var seen []string
	m := loaded(t, twoLevel(&seen))

	m.Update(key("tab"))
	m.Update(key("down")) // second row
	_, cmd := m.Update(key("a"))
	if cmd == nil {
		t.Fatal("action did not fire")
	}
	cmd()

	if len(seen) != 1 || seen[0] != "tools" {
		t.Errorf("action saw %v, want just the selected row", seen)
	}
}

// With the list focused there is no selection, so the action gets everything.
func TestActionTargetsEverythingWhenTheListHasFocus(t *testing.T) {
	var seen []string
	m := loaded(t, twoLevel(&seen))

	if _, cmd := m.Update(key("a")); cmd != nil {
		cmd()
	}
	if len(seen) != 2 {
		t.Errorf("action saw %v, want both rows", seen)
	}
}

func TestEnterDrillsInAndEscapeComesBack(t *testing.T) {
	var seen []string
	m := loaded(t, twoLevel(&seen))

	m.Update(key("tab"))
	_, cmd := m.Update(key("enter"))
	if cmd == nil {
		t.Fatal("enter produced no command")
	}
	m.Update(cmd()) // the action returns Navigate

	if m.selected().Name != "detail" {
		t.Fatalf("current view is %q", m.selected().Name)
	}
	if len(m.stack) != 1 {
		t.Fatalf("stack depth %d", len(m.stack))
	}

	// The parameter travelled with the navigation.
	if got := m.args.Get("app"); got != "root" {
		t.Errorf("args carry %q", got)
	}

	// And the detail view actually ran with it.
	res, _ := m.selected().Run(context.Background(), m.args)
	m.Update(resultMsg{seq: m.seq, res: res})
	if !strings.Contains(visibleOnly(m.View().Content), "detail of root") {
		t.Error("the detail view did not render")
	}

	if _, cmd := m.Update(key("esc")); cmd != nil {
		cmd()
	}
	if m.selected().Name != "apps" {
		t.Errorf("escape landed on %q", m.selected().Name)
	}
	if len(m.stack) != 0 {
		t.Error("stack not popped")
	}
}

func TestBreadcrumbNamesWhereYouAre(t *testing.T) {
	var seen []string
	m := loaded(t, twoLevel(&seen))

	m.Update(key("tab"))
	_, cmd := m.Update(key("enter"))
	m.Update(cmd())

	title := visibleOnly(strings.Split(m.View().Content, "\n")[0])
	for _, want := range []string{"test.apps", "test.detail", "root"} {
		if !strings.Contains(title, want) {
			t.Errorf("title %q missing %q", title, want)
		}
	}
}

// Tabbing into an empty result would select a row that is not there.
func TestTabIsInertWithoutRows(t *testing.T) {
	r := catalog.NewRegistry()
	r.Describe("test", "test")
	r.Add(catalog.View{Group: "test", Name: "empty", Short: "empty",
		Run: func(context.Context, catalog.Args) (catalog.Result, error) {
			return catalog.Table{Cols: []string{"NAME"}}, nil
		}})

	m := loaded(t, r)
	m.Update(key("tab"))

	if m.focus != paneList {
		t.Error("focus moved into an empty table")
	}
}
