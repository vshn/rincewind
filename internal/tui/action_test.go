package tui

import (
	"context"
	"errors"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/vshn/aldediggi/internal/catalog"
)

// withAction builds a one-view registry whose action records that it ran.
func withAction(act catalog.Action, params ...catalog.Param) (*catalog.Registry, *catalog.Table) {
	result := &catalog.Table{Cols: []string{"NAME"}}
	result.Add("obj", "thing")

	r := catalog.NewRegistry()
	r.Describe("test", "test views")
	r.Add(catalog.View{
		Group: "test", Name: "view", Short: "a view",
		Params: params, Actions: []catalog.Action{act},
		Run: func(context.Context, catalog.Args) (catalog.Result, error) { return *result, nil },
	})
	return r, result
}

func run(t *testing.T, reg *catalog.Registry) *model {
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

func TestActionBarListsKeysAndNames(t *testing.T) {
	reg, _ := withAction(catalog.Action{Name: "pause", Key: "p"})

	content := visibleOnly(run(t, reg).View().Content)
	if !strings.Contains(content, "p pause") {
		t.Errorf("action bar missing: %q", content)
	}
}

// An action without a confirmation runs straight away.
func TestUnconfirmedActionRunsOnItsKey(t *testing.T) {
	ran := false
	reg, _ := withAction(catalog.Action{
		Name: "refresh", Key: "R",
		Run: func(context.Context, catalog.Result, catalog.Args) (catalog.Result, error) {
			ran = true
			return catalog.Text{Lines: []string{"done"}}, nil
		},
	})

	m := run(t, reg)
	_, cmd := m.Update(key("R"))
	if cmd == nil {
		t.Fatal("no command")
	}
	cmd()
	if !ran {
		t.Error("action did not run")
	}
}

func TestConfirmedActionWaitsForY(t *testing.T) {
	ran := false
	reg, _ := withAction(catalog.Action{
		Name: "pause", Key: "p",
		Confirm: func(catalog.Result) string { return "Pause 1 application?" },
		Run: func(context.Context, catalog.Result, catalog.Args) (catalog.Result, error) {
			ran = true
			return nil, nil
		},
	})

	m := run(t, reg)
	m.Update(key("p"))

	if ran {
		t.Fatal("action ran before the question was answered")
	}
	if !strings.Contains(visibleOnly(m.View().Content), "Pause 1 application?") {
		t.Error("prompt not shown")
	}

	_, cmd := m.Update(key("y"))
	if cmd == nil {
		t.Fatal("y produced no command")
	}
	cmd()
	if !ran {
		t.Error("y did not run the action")
	}
}

// A stray navigation key must not answer a question about changing things.
func TestAnyOtherKeyCancelsTheConfirmation(t *testing.T) {
	ran := false
	reg, _ := withAction(catalog.Action{
		Name: "pause", Key: "p",
		Confirm: func(catalog.Result) string { return "Pause?" },
		Run: func(context.Context, catalog.Result, catalog.Args) (catalog.Result, error) {
			ran = true
			return nil, nil
		},
	})

	m := run(t, reg)
	m.Update(key("p"))
	m.Update(key("j"))

	if ran {
		t.Error("j ran the action")
	}
	if m.pending != nil {
		t.Error("still pending after a cancel")
	}
	if strings.Contains(visibleOnly(m.View().Content), "Pause?") {
		t.Error("prompt still shown after a cancel")
	}
}

// While a confirmation is open the cursor must not move either, or the answer
// would apply to a view other than the one shown.
func TestConfirmationSwallowsNavigation(t *testing.T) {
	reg, _ := withAction(catalog.Action{
		Name: "pause", Key: "p",
		Confirm: func(catalog.Result) string { return "Pause?" },
	})

	m := run(t, reg)
	before := m.cursor
	m.Update(key("p"))
	m.Update(key("down"))

	if m.cursor != before {
		t.Error("cursor moved while a confirmation was open")
	}
}

func TestActionKeysDoNothingBeforeAResultExists(t *testing.T) {
	ran := false
	reg, _ := withAction(catalog.Action{
		Name: "pause", Key: "p",
		Run: func(context.Context, catalog.Result, catalog.Args) (catalog.Result, error) {
			ran = true
			return nil, nil
		},
	})

	m := newModel(reg)
	next, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 24})
	m = next.(*model)

	if _, cmd := m.Update(key("p")); cmd != nil {
		cmd()
	}
	if ran {
		t.Error("action ran with nothing to act on")
	}
}

func TestActionErrorIsShown(t *testing.T) {
	reg, _ := withAction(catalog.Action{
		Name: "pause", Key: "p",
		Run: func(context.Context, catalog.Result, catalog.Args) (catalog.Result, error) {
			return nil, errors.New("patch rejected")
		},
	})

	m := run(t, reg)
	_, cmd := m.Update(key("p"))
	m.Update(cmd())

	if !strings.Contains(visibleOnly(m.View().Content), "patch rejected") {
		t.Errorf("error not shown: %q", visibleOnly(m.View().Content))
	}
}

// A view whose parameter has no value must say so rather than run with an
// empty one, which for syncstatus would show an arbitrary app's resources.
func TestRequiredParamStopsTheRun(t *testing.T) {
	reg, _ := withAction(catalog.Action{Name: "x", Key: "x"},
		catalog.Param{Name: "app", Required: true})

	m := newModel(reg)
	next, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 24})
	m = next.(*model)
	m.Update(key("enter"))

	if m.running {
		t.Error("ran despite a missing parameter")
	}
	if !strings.Contains(visibleOnly(m.View().Content), "requires APP") {
		t.Errorf("no explanation shown: %q", visibleOnly(m.View().Content))
	}
}
