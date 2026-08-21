package tui

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// probing builds a model with a stubbed probe and counts how often it ran.
func probing(t *testing.T, name string) (*model, *int) {
	t.Helper()
	runs := 0
	m := newModel(sampleViews())
	m.lookupCluster = func(context.Context) (string, error) {
		runs++
		return name, nil
	}
	next, _ := m.Update(tea.WindowSizeMsg{Width: 96, Height: 20})
	return next.(*model), &runs
}

// The point: which cluster the scripts will act on, before anything acts.
func TestClusterShowsTopRight(t *testing.T) {
	m, _ := probing(t, "albedaran")
	m.Update(clusterMsg{name: "albedaran"})

	top := ansi.Strip(strings.Split(m.View().Content, "\n")[1])
	if !strings.Contains(top, "albedaran") {
		t.Fatalf("the title bar does not name the cluster: %q", top)
	}
	// The right edge, not merely somewhere after the title: it has to be in
	// the same column on every screen to be found without being read.
	if !strings.HasSuffix(strings.TrimRight(top, "│ "), "albedaran") {
		t.Errorf("the cluster is not at the right edge: %q", top)
	}
	if strings.Index(top, "albedaran") < strings.Index(top, "rincewind") {
		t.Errorf("the cluster is on the left: %q", top)
	}
}

// A bar too narrow for both drops the cluster rather than clipping it: half
// of two similar context names is the same half.
func TestNarrowBarDropsTheClusterWhole(t *testing.T) {
	m, _ := probing(t, "")
	next, _ := m.Update(tea.WindowSizeMsg{Width: 34, Height: 20})
	m = next.(*model)
	m.Update(clusterMsg{name: "a-rather-long-context-name"})

	top := ansi.Strip(strings.Split(m.View().Content, "\n")[1])
	if strings.Contains(top, "a-rather") {
		t.Errorf("a clipped cluster name survived: %q", top)
	}
}

// The title is what yields when the two compete, not the cluster.
func TestTitleYieldsToTheCluster(t *testing.T) {
	m, _ := probing(t, "")
	next, _ := m.Update(tea.WindowSizeMsg{Width: 52, Height: 20})
	m = next.(*model)
	m.Update(clusterMsg{name: "albedaran"})

	top := ansi.Strip(strings.Split(m.View().Content, "\n")[1])
	if !strings.Contains(top, "albedaran") {
		t.Errorf("the cluster gave way to the view title: %q", top)
	}
	if strings.Contains(top, "One quick row") {
		t.Errorf("the title and the cluster both fit where only one should: %q", top)
	}
}

// No kubectl, no context, no cluster — and then no room is taken for one.
func TestNoClusterShowsNothing(t *testing.T) {
	m, _ := probing(t, "")
	m.Update(clusterMsg{})

	// Nothing between the view's title and the frame's right edge.
	top := ansi.Strip(strings.Split(m.View().Content, "\n")[1])
	if !strings.HasSuffix(strings.TrimRight(top, "│ "), "One quick row") {
		t.Errorf("something sits where the cluster would be: %q", top)
	}
}

// A probe that fails says nothing rather than putting an error in the chrome:
// there is nothing to act on, and the views will report a broken cluster
// themselves.
func TestFailedProbeIsSilent(t *testing.T) {
	m := newModel(sampleViews())
	m.lookupCluster = func(context.Context) (string, error) {
		return "whatever it printed", fmt.Errorf("no kubectl")
	}
	next, _ := m.Update(tea.WindowSizeMsg{Width: 96, Height: 20})
	m = next.(*model)

	msg := m.refreshCluster()()
	if got := msg.(clusterMsg).name; got != "" {
		t.Errorf("a failed probe reported %q, want nothing", got)
	}
}

// Changing the view is what re-asks the question, since that is when one is
// about to act on something.
func TestViewChangeReprobes(t *testing.T) {
	m, runs := probing(t, "albedaran")
	drain(t, m, m.Init())
	if *runs != 1 {
		t.Fatalf("the probe ran %d times at startup, want once", *runs)
	}

	// Past the TTL, or a tab switch would be collapsed into the first run.
	m.clusterAt = m.clusterAt.Add(-time.Hour)

	// A different entry, so this is a change of view rather than a re-run of
	// the one already on screen.
	press(t, m, "down")
	press(t, m, "enter")
	if *runs != 2 {
		t.Errorf("the probe ran %d times, want it asked again on the new view", *runs)
	}
}

// A key that leaves the view alone must not spawn a process.
func TestUnrelatedKeysDoNotReprobe(t *testing.T) {
	m, runs := probing(t, "albedaran")
	drain(t, m, m.Init())
	m.clusterAt = m.clusterAt.Add(-time.Hour)

	for _, k := range []string{"down", "up", "tab", "M"} {
		press(t, m, k)
	}
	if *runs != 1 {
		t.Errorf("the probe ran %d times for keys that changed no view, want once", *runs)
	}
}

// A held-down tab key must not spawn a process per repeat.
func TestProbeIsRateLimited(t *testing.T) {
	m, runs := probing(t, "albedaran")
	for range 10 {
		if cmd := m.refreshCluster(); cmd != nil {
			cmd()
		}
	}
	if *runs != 1 {
		t.Errorf("the probe ran %d times in a burst, want the TTL to collapse it to one", *runs)
	}
}

// A long context name must not push the frame open.
func TestLongClusterNameKeepsTheWidth(t *testing.T) {
	m, _ := probing(t, "")
	m.Update(clusterMsg{name: strings.Repeat("very-long-context-name-", 8)})

	for i, l := range strings.Split(m.View().Content, "\n") {
		if got := lipgloss.Width(l); got != 96 {
			t.Fatalf("line %d is %d wide, want 96", i, got)
		}
	}
}
