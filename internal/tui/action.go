package tui

import (
	"context"
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/vshn/aldediggi/internal/engine"
)

// actionFor resolves a key to an action of the view on screen. It only
// answers while the output pane has focus, so a config key can never take
// "enter runs the highlighted view" away from the menu.
func (m *model) actionFor(key string) *engine.Action {
	t := m.cur()
	if m.focus != paneOutput || t.view == nil || t.res == nil || t.running {
		return nil
	}
	for i := range t.view.Actions {
		if t.view.Actions[i].Key == key {
			return &t.view.Actions[i]
		}
	}
	return nil
}

// fire starts an action. Both kinds open a tab: a navigation opens the view
// it points at, a working action opens the commands it is about to run.
func (m *model) fire(a *engine.Action) tea.Cmd {
	if a.Navigates() {
		return m.drill(a)
	}

	// A mutation acts on what was deliberately marked, never on whatever
	// happens to be filtered into view: a forgotten filter must not be able
	// to turn one loose on everything on screen.
	rows, keys := m.selectedRows()
	if len(rows) == 0 {
		m.notice = "select rows first (␣ or a)"
		return nil
	}

	steps, err := a.Plan(rows, m.cur().args)
	if err != nil {
		m.notice = fmt.Sprintf("%s: %v", a.Name, err)
		return nil
	}
	for i := range steps {
		steps[i].Row = keys[i]
	}

	// The commands go on screen before anything runs, so the question below
	// them is one that can actually be answered.
	from := m.cur()
	m.park()
	m.closeFrom(m.act + 1)

	t := &tab{job: &job{name: a.Name, steps: steps}}
	if a.Reload {
		t.job.reload = from
	}
	m.tabs = append(m.tabs, t)
	m.act = len(m.tabs) - 1
	m.filter.SetValue("")
	m.vp.SetYOffset(0)
	m.vp.SetXOffset(0)
	m.showResult()

	if a.Confirm == nil {
		return m.work(t)
	}
	prompt, err := a.Confirm(rows, from.args)
	if err != nil {
		m.notice = fmt.Sprintf("%s: %v", a.Name, err)
		return nil
	}
	// The count comes from here rather than from the config, so a
	// confirmation cannot be written that fails to say how much it touches.
	m.pending, m.pendingTab = a, t
	m.prompt = fmt.Sprintf("%s (%s)", prompt, plural(len(rows), "row"))
	return nil
}

// drill opens an action's target in a new tab, with its arguments derived
// from the row under the cursor. The selection is deliberately ignored:
// navigating to several places at once has no meaning, and leaving the
// selection to the mutations keeps it unambiguous.
func (m *model) drill(a *engine.Action) tea.Cmd {
	vis := m.visible()
	if len(vis.Rows) == 0 {
		return nil
	}
	m.clampRow()
	src := vis.Rows[m.cur().row].Src

	// Drilling from a tab in the middle abandons what was opened from it:
	// the tabs are one path, not a heap of windows.
	m.park()
	m.closeFrom(m.act + 1)

	args, err := a.Args(src, m.cur().args)

	t := newTab(a.Open, args)
	m.tabs = append(m.tabs, t)
	m.act = len(m.tabs) - 1
	m.filter.SetValue("")
	m.focus = paneOutput

	if err != nil {
		// A refusal written into the argument expression — a pool selecting
		// nodes by expression, say — is the answer to the attempt, so it is
		// shown where the attempt went.
		t.err = err
		m.showResult()
		return nil
	}
	return m.launch(t)
}

// work runs a job tab's steps, in order, and brings back what each printed.
// One step per row means a failure is attributable and the rest still run.
func (m *model) work(t *tab) tea.Cmd {
	t.stop()
	m.seq++
	t.seq, t.running = m.seq, true

	ctx, cancel := context.WithTimeout(context.Background(), runTimeout)
	t.cancel = cancel

	seq, steps := t.seq, t.job.steps
	run := func() tea.Msg {
		defer cancel()

		out := make([][]string, len(steps))
		errs := make([]error, len(steps))
		for i, s := range steps {
			out[i], errs[i] = s.Run(ctx)
			if ctx.Err() != nil {
				break
			}
		}
		return jobMsg{seq: seq, out: out, errs: errs}
	}

	if m.tickAlive {
		return run
	}
	m.tickAlive = true
	return tea.Batch(run, m.spin.Tick)
}

// selectedRows is what a working action operates on: the marked rows, in the
// order the view put them, with their raw source objects rather than the
// cells, which are shaped for reading. The keys come along so each step can
// say which object it is about.
func (m *model) selectedRows() (rows []any, keys []string) {
	t := m.cur()
	if t.res == nil {
		return nil, nil
	}
	for _, r := range t.res.Rows {
		if t.selection[r.Key] && r.Src != nil {
			rows = append(rows, r.Src)
			keys = append(keys, r.Key)
		}
	}
	return rows, keys
}

// answer resolves a pending confirmation. Only y proceeds; every other key
// cancels, and cancelling closes the tab that was opened to show the
// command — there is nothing left for it to report.
func (m *model) answer(key string) tea.Cmd {
	t := m.pendingTab
	m.pending, m.pendingTab, m.prompt = nil, nil, ""

	if key == "y" || key == "Y" {
		return m.work(t)
	}
	return m.closeTab()
}

// lines is what the job tab shows: for every row, the invocation and then
// what it said. The command is there from the moment the tab opens, before
// anything has run — seeing it is the reason the tab exists.
func (j *job) lines() []string {
	var out []string
	for i, s := range j.steps {
		if i > 0 {
			out = append(out, "")
		}

		head := j.name
		if s.Row != "" {
			head += "  " + s.Row
		}
		out = append(out, headerStyle.Render(head))
		out = append(out, dividerStyle.Render(strings.Join(s.Command(), "\n")))

		switch {
		case !j.done:
			out = append(out, helpStyle.Render("  …"))
		case i < len(j.errs) && j.errs[i] != nil:
			out = append(out, errStyle.Render("  "+j.errs[i].Error()))
		default:
			printed := []string{}
			if i < len(j.out) {
				printed = j.out[i]
			}
			if len(printed) == 0 {
				out = append(out, dividerStyle.Render("  (no output)"))
			}
			for _, line := range printed {
				out = append(out, "  "+line)
			}
		}
	}

	// The last word: how it went, in one line, for a run too long to read.
	if j.done {
		out = append(out, "", j.summary())
	}
	return out
}

// summary counts the steps that failed, because after acting on nine rows
// the count is the thing one checks.
func (j *job) summary() string {
	failed := 0
	for _, err := range j.errs {
		if err != nil {
			failed++
		}
	}
	if failed > 0 {
		return errStyle.Render(fmt.Sprintf("%d of %s failed", failed, plural(len(j.steps), "step")))
	}
	return goodStyle.Render(fmt.Sprintf("%s ok", plural(len(j.steps), "step")))
}

// label names the job tab.
func (j *job) label() string { return j.name }

func plural(n int, what string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, what)
	}
	return fmt.Sprintf("%d %ss", n, what)
}
