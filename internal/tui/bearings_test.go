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

	"github.com/vshn/rincewind/internal/engine"
)

// contexts compiles definitions the way a hat's would be compiled.
func contexts(t *testing.T, defs ...engine.ContextDef) []engine.Context {
	t.Helper()
	var out []engine.Context
	for _, d := range defs {
		c, err := engine.CompileContext(d)
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, c)
	}
	return out
}

// says builds a context that always answers with the given label.
func says(id, label string) engine.ContextDef {
	return engine.ContextDef{ID: id, Script: `echo '{}'`, Label: `"` + label + `"`}
}

// chrome builds a model of the given width, with the window message
// delivered, since everything about the title bar is a question of room.
func chrome(t *testing.T, width int) *model {
	t.Helper()
	m := newModel(sampleViews())
	next, _ := m.Update(tea.WindowSizeMsg{Width: width, Height: 20})
	return next.(*model)
}

// probing builds a model with a stubbed fallback probe and counts how often
// it ran. A hat that declares no contexts is what keeps that path alive.
func probing(t *testing.T, name string) (*model, *int) {
	t.Helper()
	runs := 0
	m := chrome(t, 96)
	m.lookupCluster = func(context.Context) (string, error) {
		runs++
		return name, nil
	}
	return m, &runs
}

// top is the title bar, stripped of colour.
func top(m *model) string {
	return ansi.Strip(strings.Split(m.View().Content, "\n")[1])
}

// The point: which cluster the scripts will act on, before anything acts.
func TestBearingsShowTopRight(t *testing.T) {
	m := chrome(t, 96)
	m.Update(bearingsMsg{found: engine.Bearings{Labels: []string{"albedaran"}}})

	if !strings.Contains(top(m), "albedaran") {
		t.Fatalf("the title bar does not name the cluster: %q", top(m))
	}
	// The right edge, not merely somewhere after the title: it has to be in
	// the same column on every screen to be found without being read.
	if !strings.HasSuffix(strings.TrimRight(top(m), "│ "), "albedaran") {
		t.Errorf("the cluster is not at the right edge: %q", top(m))
	}
	if strings.Index(top(m), "albedaran") < strings.Index(top(m), "rincewind") {
		t.Errorf("the cluster is on the left: %q", top(m))
	}
}

// Several contexts read as one line, joined the way the title bar joins
// everything, in the order the hat declared them.
func TestLabelsJoinInDeclarationOrder(t *testing.T) {
	m := chrome(t, 96)
	m.Update(bearingsMsg{found: engine.Bearings{Labels: []string{"demo-cluster", "cloudscale"}}})

	if !strings.Contains(top(m), "demo-cluster  ·  cloudscale") {
		t.Errorf("the chrome reads %q, want the labels joined in order", top(m))
	}
}

// A round is what actually resolves them: the hat's contexts, run together.
func TestARoundResolvesEveryContext(t *testing.T) {
	m := chrome(t, 96)
	m.contexts = contexts(t,
		engine.ContextDef{ID: "cluster", Script: `echo '{"n":"demo-cluster"}'`, Label: ".n"},
		engine.ContextDef{ID: "cloudy", Script: `echo '{"n":"demo-cluster"}'`,
			Active: `.n | test("demo")`, Label: `"cloudy"`},
		engine.ContextDef{ID: "elsewhere", Script: `echo '{"n":"demo-cluster"}'`,
			Active: `.n | test("nowhere")`, Label: `"elsewhere"`},
	)

	next, _ := m.Update(m.refreshBearings()())
	m = next.(*model)

	if got := m.bearings.State["elsewhere"]; got != engine.StateInactive {
		t.Errorf("elsewhere is %v, want inactive", got)
	}
	if !strings.Contains(top(m), "demo-cluster  ·  cloudy") {
		t.Errorf("the chrome reads %q, want both active labels", top(m))
	}
	if strings.Contains(top(m), "elsewhere") {
		t.Errorf("an inactive context reached the chrome: %q", top(m))
	}
}

// A probe that cannot answer leaves the chrome exactly as the others left it:
// nothing is hidden on its account, so there is nothing to explain here.
// `check` is where a failed probe is reported.
func TestAFailedProbeLeavesTheChromeAsItWas(t *testing.T) {
	m := chrome(t, 96)
	m.contexts = contexts(t,
		engine.ContextDef{ID: "cluster", Script: `echo '{"n":"demo-cluster"}'`, Label: ".n"},
		engine.ContextDef{ID: "broken", Script: `echo "no kubeconfig" >&2; exit 1`, Label: `"broken"`},
	)

	next, _ := m.Update(m.refreshBearings()())
	m = next.(*model)

	if got := m.bearings.State["broken"]; got != engine.StateUnknown {
		t.Errorf("a probe that failed is %v, want unknown", got)
	}
	if !strings.Contains(top(m), "demo-cluster") {
		t.Errorf("the chrome reads %q, want the context that did answer", top(m))
	}
	for _, word := range []string{"broken", "kubeconfig", "error"} {
		if strings.Contains(top(m), word) {
			t.Errorf("the chrome complains about a failed probe: %q", top(m))
		}
	}
}

// A bar too narrow sheds a label whole rather than clipping it, from the
// right: half of two similar context names is the same half, and the first
// declared is the last to go.
func TestNarrowBarShedsLabelsWhole(t *testing.T) {
	m := chrome(t, 60)
	m.Update(bearingsMsg{found: engine.Bearings{Labels: []string{"demo-cluster", "a-rather-long-platform-name"}}})

	if strings.Contains(top(m), "a-rather") {
		t.Errorf("a clipped label survived: %q", top(m))
	}
	if !strings.Contains(top(m), "demo-cluster") {
		t.Errorf("the first-declared label went first: %q", top(m))
	}
}

// Narrower still and there is nothing to say.
func TestATooNarrowBarDropsThemAll(t *testing.T) {
	m := chrome(t, 34)
	m.Update(bearingsMsg{found: engine.Bearings{Labels: []string{"a-rather-long-context-name"}}})

	if strings.Contains(top(m), "a-rather") {
		t.Errorf("a clipped label survived: %q", top(m))
	}
}

// The title is what yields when the two compete, not the labels.
func TestTitleYieldsToTheLabels(t *testing.T) {
	m := chrome(t, 52)
	m.Update(bearingsMsg{found: engine.Bearings{Labels: []string{"albedaran"}}})

	if !strings.Contains(top(m), "albedaran") {
		t.Errorf("the labels gave way to the view title: %q", top(m))
	}
	if strings.Contains(top(m), "One quick row") {
		t.Errorf("the title and the labels both fit where only one should: %q", top(m))
	}
}

// Nothing answered, so no room is taken for an answer.
func TestNoBearingsShowNothing(t *testing.T) {
	m := chrome(t, 96)
	m.Update(bearingsMsg{})

	// Nothing between the view's title and the frame's right edge.
	if !strings.HasSuffix(strings.TrimRight(top(m), "│ "), "One quick row") {
		t.Errorf("something sits where the labels would be: %q", top(m))
	}
}

// A hat that declares no contexts keeps the display it always had.
func TestTheFallbackIsTheKubectlContext(t *testing.T) {
	m, runs := probing(t, "albedaran")

	next, _ := m.Update(m.refreshBearings()())
	m = next.(*model)

	if *runs != 1 {
		t.Fatalf("the fallback probe ran %d times, want once", *runs)
	}
	if !strings.Contains(top(m), "albedaran") {
		t.Errorf("the chrome reads %q, want the kubectl context", top(m))
	}
}

// A hat that declares contexts does not run it at all.
func TestContextsReplaceTheFallback(t *testing.T) {
	m, runs := probing(t, "albedaran")
	m.contexts = contexts(t, says("cloudy", "cloudy"))

	next, _ := m.Update(m.refreshBearings()())
	m = next.(*model)

	if *runs != 0 {
		t.Errorf("the fallback probe ran %d times beside the hat's contexts", *runs)
	}
	if !strings.Contains(top(m), "cloudy") {
		t.Errorf("the chrome reads %q, want the hat's own answer", top(m))
	}
}

// A fallback probe that fails says nothing rather than putting an error in
// the chrome.
func TestFailedFallbackIsSilent(t *testing.T) {
	m := chrome(t, 96)
	m.lookupCluster = func(context.Context) (string, error) {
		return "whatever it printed", fmt.Errorf("no kubectl")
	}

	msg := m.refreshBearings()()
	if got := msg.(bearingsMsg).found.Labels; len(got) > 0 {
		t.Errorf("a failed probe reported %q, want nothing", got)
	}
}

// Changing the view is what asks again, since that is when one is about to
// act on something.
func TestViewChangeResolvesAgain(t *testing.T) {
	m, runs := probing(t, "albedaran")
	drain(t, m, m.Init())
	if *runs != 1 {
		t.Fatalf("the probe ran %d times at startup, want once", *runs)
	}

	// Past the TTL, or a tab switch would be collapsed into the first round.
	m.bearingsAt = m.bearingsAt.Add(-time.Hour)

	// A different entry, so this is a change of view rather than a re-run of
	// the one already on screen.
	press(t, m, "down")
	press(t, m, "enter")
	if *runs != 2 {
		t.Errorf("the probe ran %d times, want it asked again on the new view", *runs)
	}
}

// A key that leaves the view alone must not spawn a process.
func TestUnrelatedKeysDoNotResolveAgain(t *testing.T) {
	m, runs := probing(t, "albedaran")
	drain(t, m, m.Init())
	m.bearingsAt = m.bearingsAt.Add(-time.Hour)

	for _, k := range []string{"down", "up", "tab", "M"} {
		press(t, m, k)
	}
	if *runs != 1 {
		t.Errorf("the probe ran %d times for keys that changed no view, want once", *runs)
	}
}

// A held-down tab key must not spawn a process per repeat.
func TestRoundsAreRateLimited(t *testing.T) {
	m, runs := probing(t, "albedaran")
	for range 10 {
		if cmd := m.refreshBearings(); cmd != nil {
			cmd()
		}
	}
	if *runs != 1 {
		t.Errorf("the probe ran %d times in a burst, want the TTL to collapse it to one", *runs)
	}
}

// A long label must not push the frame open.
func TestALongLabelKeepsTheWidth(t *testing.T) {
	m := chrome(t, 96)
	m.Update(bearingsMsg{found: engine.Bearings{Labels: []string{strings.Repeat("very-long-context-name-", 8)}}})

	for i, l := range strings.Split(m.View().Content, "\n") {
		if got := lipgloss.Width(l); got != 96 {
			t.Fatalf("line %d is %d wide, want 96", i, got)
		}
	}
}
