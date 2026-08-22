package tui

import (
	"context"
	"os/exec"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/vshn/rincewind/internal/engine"
)

// Where one is, shown **top right** in the title bar, because acting on the
// wrong cluster is the expensive mistake this tool can help make. The right
// edge is the same column on every screen, where a left-aligned label would
// shift with the length of the view id beside it — which is what lets it be
// found without being read.
//
// The answer comes from the hat's contexts: a script that prints JSON, and
// jq over it. The hat asks the question and the world answers it, so this is
// not the hat declaring where it is — which it may never do.
//
// A hat that declares no contexts falls back to `kubectl config
// current-context`, which is what the chrome read before there were any.
// That keeps every existing hat's display; taking the Go special case away
// altogether is a later decision, to be taken on its own.

const (
	// clusterTimeout bounds the fallback probe. The contexts carry their own
	// deadline, one per script; see engine.Resolve.
	clusterTimeout = 2 * time.Second
	// bearingsTTL keeps a held-down tab key from spawning a process per
	// repeat. Neither the cluster nor the platform changes between two
	// keystrokes.
	bearingsTTL = 2 * time.Second
)

// bearingsMsg carries what a round of resolution found. An empty one is the
// answer when nothing answered, and then nothing is shown at all.
type bearingsMsg struct{ found engine.Bearings }

// kubeContext is the fallback probe the binary runs. It is a field on the
// model rather than a call, so the tests drive the display without spawning
// a process — and so a build that has no business asking can leave it nil.
//
// The context, never reachability. `current-context` reads ~/.kube/config and
// answers in tens of milliseconds, where asking the API server whether it is
// up costs a round trip on every view change — and would blank the display
// exactly when a cluster is unwell, which is when knowing which cluster it is
// matters most. Whether it answers at all is what running a view already
// tells you.
func kubeContext(ctx context.Context) (string, error) {
	out, err := exec.CommandContext(ctx, "kubectl", "config", "current-context").Output()
	return strings.TrimSpace(string(out)), err
}

// refreshBearings asks where the scripts will run, when the view on screen
// changes: often enough to catch a context switched in another terminal, rare
// enough to stay unnoticed. The TTL collapses a burst of tab switches into
// one round.
//
// One cadence for every context, and deliberately no cache key. A context is
// expected to be cheap — the platform usually falls out of the same local
// kubeconfig read as the cluster name, and identical sources already run once
// per round — and a hat that writes an expensive probe can cache in its own
// script.
//
// The work happens in the command, never here: the menu is complete until the
// answer lands, which is what makes failing open fall out of the ordering
// rather than needing a rule of its own.
func (m *model) refreshBearings() tea.Cmd {
	if len(m.contexts) == 0 && m.lookupCluster == nil {
		return nil
	}
	if !m.bearingsAt.IsZero() && time.Since(m.bearingsAt) < bearingsTTL {
		return nil
	}
	m.bearingsAt = time.Now()

	if len(m.contexts) == 0 {
		return m.probeCluster()
	}

	cs := m.contexts
	return func() tea.Msg {
		return bearingsMsg{found: engine.Resolve(context.Background(), cs)}
	}
}

// probeCluster is the fallback, for a hat that declares no contexts.
//
// A failure is no label rather than an error: no kubectl, or no current
// context, means this is not a cluster session — a state to show nothing in,
// not one to complain about.
func (m *model) probeCluster() tea.Cmd {
	lookup := m.lookupCluster
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), clusterTimeout)
		defer cancel()

		name, err := lookup(ctx)
		if err != nil || name == "" {
			return bearingsMsg{}
		}
		return bearingsMsg{found: engine.Bearings{Labels: []string{name}}}
	}
}

// bearingsGap is the least space between the title and the labels. Fewer
// columns and the two read as one string.
const bearingsGap = 3

// withBearings right-aligns the labels in the title bar. Right rather than
// beside the view's name: they are not part of what one is looking at, they
// are where one is looking at it.
//
// Too narrow sheds a label whole, from the right, rather than clipping one:
// half of two similar context names is the same half. Which is what makes the
// order the hat declared them in worth having — the first is the last to go,
// so the hat decides what survives a narrow window.
func (m *model) withBearings(line string) string {
	if m.width <= 0 {
		return line
	}
	left := m.width - boxPad - lipgloss.Width(line)

	for labels := m.bearings.Labels; len(labels) > 0; labels = labels[:len(labels)-1] {
		right := renderLabels(labels)
		if w := lipgloss.Width(right); w <= left-bearingsGap {
			return line + strings.Repeat(" ", left-w) + right
		}
	}
	return line
}

// bearingsRoom is what they would take with nothing shed, so the title can be
// budgeted against them before either is placed.
func (m *model) bearingsRoom() int {
	if len(m.bearings.Labels) == 0 {
		return 0
	}
	return lipgloss.Width(renderLabels(m.bearings.Labels)) + bearingsGap
}

// renderLabels joins them the way the title bar separates everything else.
func renderLabels(labels []string) string {
	parts := make([]string, len(labels))
	for i, l := range labels {
		parts[i] = bearingStyle.Render(l)
	}
	return strings.Join(parts, sepStyle.Render(chromeSep))
}

// ruledOut reports whether every context a view names has resolved away.
//
// Every one of them: a view naming two contexts belongs here if either is
// active, and one context that could not be found out shows it too. Failing
// open is the rule — a view disappears only when the world has definitively
// said it does not belong here, never because a probe was slow, broken or has
// not run yet.
func (m *model) ruledOut(v *engine.View) bool {
	if len(v.Needs) == 0 {
		return false
	}
	for _, id := range v.Needs {
		if m.bearings.State[id] != engine.StateInactive {
			return false
		}
	}
	return true
}
