package tui

import (
	"context"
	"os/exec"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// The one place in this tool's Go code that names a producer. Everywhere else
// kubectl is hat business, and deliberately so — but which cluster one is
// pointed at cannot come from the hat: a hat is a set of views, and the
// answer changes without any of them changing. Reading it from the same
// kubeconfig the scripts read is what makes the two agree.
//
// current-context, not reachability. It reads ~/.kube/config and answers in
// tens of milliseconds, where asking the API server whether it is up costs a
// round trip on every view change — and would blank the display exactly when
// a cluster is unwell, which is when knowing which cluster it is matters
// most. Whether it answers at all is what running a view already tells you.
const (
	clusterTimeout = 2 * time.Second
	// clusterTTL keeps a held-down tab key from spawning a process per
	// repeat. A context does not change between two keystrokes.
	clusterTTL = 2 * time.Second
)

// clusterMsg carries what the probe found, empty when there was no answer.
type clusterMsg struct{ name string }

// kubeContext is the probe the binary runs. It is a field on the model rather
// than a call, so the tests drive the display without spawning a process —
// and so a build that has no business asking can leave it nil.
func kubeContext(ctx context.Context) (string, error) {
	out, err := exec.CommandContext(ctx, "kubectl", "config", "current-context").Output()
	return strings.TrimSpace(string(out)), err
}

// refreshCluster asks which context the scripts will run against, when the
// view on screen changes: often enough to catch a context switched in another
// terminal, rare enough to stay unnoticed. The TTL collapses a burst of tab
// switches into one run.
//
// A failure is an empty name rather than an error: no kubectl, or no context,
// means this is not a cluster session — a state to show nothing in, not one
// to complain about.
func (m *model) refreshCluster() tea.Cmd {
	if m.lookupCluster == nil {
		return nil
	}
	if !m.clusterAt.IsZero() && time.Since(m.clusterAt) < clusterTTL {
		return nil
	}
	m.clusterAt = time.Now()

	lookup := m.lookupCluster
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), clusterTimeout)
		defer cancel()

		name, err := lookup(ctx)
		if err != nil {
			return clusterMsg{}
		}
		return clusterMsg{name: name}
	}
}

// clusterGap is the least space between the title and the cluster. Fewer
// columns and the two read as one string.
const clusterGap = 3

// withCluster right-aligns the cluster in the title bar. Right rather than
// beside the view's name: it is not part of what one is looking at, it is
// where one is looking at it — and the right edge is the same place on every
// screen, which is what makes it readable without being read.
//
// Too narrow means it is dropped, like every other label in the chrome. A
// clipped cluster name is worse than none: half of two similar names is the
// same half.
func (m *model) withCluster(line string) string {
	if m.cluster == "" || m.width <= 0 {
		return line
	}
	right := clusterStyle.Render(m.cluster)
	gap := m.width - boxPad - lipgloss.Width(line) - lipgloss.Width(right)
	if gap < clusterGap {
		return line
	}
	return line + strings.Repeat(" ", gap) + right
}

// clusterRoom is what the cluster will take, so the title can be budgeted
// against it before either is placed.
func (m *model) clusterRoom() int {
	if m.cluster == "" {
		return 0
	}
	return lipgloss.Width(clusterStyle.Render(m.cluster)) + clusterGap
}
