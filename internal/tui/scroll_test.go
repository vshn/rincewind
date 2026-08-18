package tui

import (
	"context"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/vshn/aldediggi/internal/catalog"
)

func wideView(cells ...string) *catalog.Registry {
	r := catalog.NewRegistry()
	r.Describe("test", "test")
	r.Add(catalog.View{
		Group: "test", Name: "wide", Short: "wide",
		Run: func(context.Context, catalog.Args) (catalog.Result, error) {
			t := catalog.Table{Cols: []string{"A", "B", "C"}}
			t.Add(nil, cells...)
			return t, nil
		},
	})
	return r
}

func shown(t *testing.T, reg *catalog.Registry, w int) *model {
	t.Helper()
	m := newModel(reg)
	next, _ := m.Update(tea.WindowSizeMsg{Width: w, Height: 20})
	m = next.(*model)

	m.Update(key("enter"))
	res, err := m.selected().Run(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	m.Update(resultMsg{seq: m.seq, res: res})
	return m
}

// Nothing is cut, so a wide table reaches past the edge and is scrolled to.
func TestWideTablesScrollSideways(t *testing.T) {
	m := shown(t, wideView("aaa", "bbb", strings.Repeat("z", 200)), 60)

	if !m.scrollable() {
		t.Fatal("a 200-column cell does not register as scrollable")
	}
	if !strings.Contains(visibleOnly(m.View().Content), "<> scroll") {
		t.Error("the help does not offer the keys")
	}

	m.Update(key(">"))
	if m.vp.XOffset() == 0 {
		t.Error("> did not scroll")
	}

	before := m.vp.XOffset()
	m.Update(key("<"))
	if m.vp.XOffset() >= before {
		t.Error("< did not scroll back")
	}
}

// Offering keys that would do nothing is worse than not offering them.
func TestNarrowContentOffersNoScrolling(t *testing.T) {
	m := shown(t, wideView("a", "b", "c"), 100)

	if m.scrollable() {
		t.Error("a table that fits reports as scrollable")
	}
	if strings.Contains(visibleOnly(m.View().Content), "<> scroll") {
		t.Error("the help offers scrolling for content that fits")
	}
}

// A table of nothing but headers reads like a failure.
func TestEmptyTableSaysSo(t *testing.T) {
	r := catalog.NewRegistry()
	r.Describe("test", "test")
	r.Add(catalog.View{Group: "test", Name: "empty", Short: "empty",
		Run: func(context.Context, catalog.Args) (catalog.Result, error) {
			return catalog.Table{Cols: []string{"NAME"}}, nil
		}})

	m := shown(t, r, 80)
	if !strings.Contains(visibleOnly(m.View().Content), "nothing to show") {
		t.Errorf("an empty table shows no explanation: %q", visibleOnly(m.View().Content))
	}
}

func TestFilterThatMatchesNothingSaysWhy(t *testing.T) {
	m := shown(t, wideView("aaa", "bbb", "ccc"), 80)

	m.Update(key("/"))
	for _, r := range "zzz" {
		m.Update(key(string(r)))
	}

	if got := visibleOnly(m.View().Content); !strings.Contains(got, "nothing matches zzz") {
		t.Errorf("no explanation for an empty filter result: %q", got)
	}
}
