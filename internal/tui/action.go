package tui

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/vshn/rincewind/internal/engine"
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
	// to turn one loose on everything on screen. An action that acts on
	// nothing is exempt — there are no rows for it to look at.
	var rows []any
	var keys []string
	if a.NeedsRows() {
		if rows, keys = m.selectedRows(); len(rows) == 0 {
			m.notice = "select rows first (␣ or a)"
			return nil
		}
	}

	// The tab opens now and carries the whole interaction: the form first if
	// the action needs values, then the commands, then the question.
	from := m.cur()
	m.park()
	m.closeFrom(m.act + 1)

	t := &tab{job: &job{
		action: *a, rows: rows, keys: keys, args: from.args,
		vals: map[string]string{},
	}}
	if a.Reload {
		t.job.reload = from
	}
	m.tabs = append(m.tabs, t)
	m.act = len(m.tabs) - 1
	m.filter.SetValue("")
	m.vp.SetYOffset(0)
	m.vp.SetXOffset(0)

	if t.job.asking() {
		return m.field(t.job)
	}
	return m.plan(t)
}

// field arms the input for the parameter now being asked for. A default is
// shown rather than applied invisibly, so plain enter takes it and the user
// can see what they took.
func (m *model) field(j *job) tea.Cmd {
	p := j.field()
	m.input.Reset()
	m.input.Prompt = p.Name + ": "
	m.input.SetValue(p.Default)
	m.input.CursorEnd()
	m.showResult()
	return m.input.Focus()
}

// accept records the field on screen and moves on, planning once the last
// one is in.
func (m *model) accept(t *tab) tea.Cmd {
	j := t.job
	p, value := j.field(), m.input.Value()

	if value == "" && p.Required {
		m.notice = p.Name + " is required"
		return nil
	}
	j.vals[p.Name] = value
	j.idx++

	if j.asking() {
		return m.field(j)
	}
	m.input.Blur()
	return m.plan(t)
}

// plan turns the job into the invocations it will make and asks about them.
// It runs after collection because the values are what make the command
// concrete — and a command one cannot see is not one to confirm.
func (m *model) plan(t *tab) tea.Cmd {
	j := t.job

	args := engine.Args{}
	for k, v := range j.args {
		args[k] = v
	}
	bound, err := j.action.Bind(j.vals)
	if err != nil {
		m.notice = fmt.Sprintf("%s: %v", j.action.Name, err)
		return m.closeTab()
	}
	for k, v := range bound {
		args[k] = v
	}
	j.args = args

	steps, err := j.action.Plan(j.rows, args)
	if err != nil {
		m.notice = fmt.Sprintf("%s: %v", j.action.Name, err)
		return m.closeTab()
	}
	for i := range steps {
		if i < len(j.keys) {
			steps[i].Row = j.keys[i]
		}
	}
	j.steps = steps

	if j.action.Confirm == nil {
		m.showResult()
		return m.work(t)
	}
	prompt, err := j.action.Confirm(j.rows, args)
	if err != nil {
		m.notice = fmt.Sprintf("%s: %v", j.action.Name, err)
		return m.closeTab()
	}
	// The count comes from here rather than from the config, so a
	// confirmation cannot be written that fails to say how much it touches.
	// A rowless action is exactly one step, and that step is on screen.
	j.prompt = prompt
	if j.action.NeedsRows() {
		j.prompt = fmt.Sprintf("%s (%s)", prompt, plural(len(j.rows), "row"))
	}
	m.showResult()
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
	ctx, cancel, tick := m.begin(t, runTimeout)

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
	return withTick(run, tick)
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

// answer resolves a job's confirmation. Only y proceeds; every other key
// cancels, and cancelling closes the tab that was opened to show the
// command — there is nothing left for it to report.
func (m *model) answer(key string) tea.Cmd {
	t := m.cur()
	t.job.prompt = ""

	if key == "y" || key == "Y" {
		return m.work(t)
	}
	return m.closeTab()
}

// lines is what the job tab shows, which is the whole interaction: the form
// while it is being filled in, then for every row the invocation and what it
// said. The command is there from the moment the values are in, before
// anything runs — seeing it is the reason the tab exists.
func (j *job) lines(input string) []string {
	if j.asking() {
		return j.form(input)
	}

	var out []string
	for i, s := range j.steps {
		if i > 0 {
			out = append(out, "")
		}

		head := j.action.Name
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

	switch {
	case j.confirming():
		// The question sits under the command it is about, so both are read
		// in one glance.
		out = append(out, "")
		for _, line := range strings.Split(j.prompt, "\n") {
			out = append(out, promptStyle.Render(line))
		}
		out = append(out, promptStyle.Render("[y/N]"))
	case j.done:
		// The last word: how it went, in one line, for a run too long to read.
		out = append(out, "", j.summary())
	}
	return out
}

// form is the tab while the action's parameters are being collected.
// Answered fields stay on screen: by the fourth one you have forgotten the
// first, and this is the last chance to catch a typo before confirming.
func (j *job) form(input string) []string {
	p := j.field()

	head := headerStyle.Render(j.action.Name)
	head += dividerStyle.Render(fmt.Sprintf("  (%d of %d)  %s", j.idx+1, len(j.action.Params), p.Short))

	out := []string{head, ""}
	for _, done := range j.action.Params[:j.idx] {
		out = append(out, dividerStyle.Render("  "+done.Name+": ")+j.vals[done.Name])
	}
	return append(out, "  "+input)
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
func (j *job) label() string { return j.action.Name }

func plural(n int, what string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, what)
	}
	return fmt.Sprintf("%d %ss", n, what)
}
