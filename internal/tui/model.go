// Package tui renders the registry as an interactive two-pane browser:
// views on the left, the output of the selected view on the right.
package tui

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"charm.land/bubbles/v2/spinner"
	"charm.land/bubbles/v2/textinput"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/vshn/aldediggi/internal/catalog"
	"github.com/vshn/aldediggi/internal/render"
)

const (
	sidebarWidth    = 30
	chromeHeight    = 4 // title and help line, each with a blank line inside
	actionBarHeight = 2 // divider plus the action list
	contentPad      = 2 // gap between the divider and the output
)

// runTimeout bounds a single kubectl call. Without it an unreachable API
// server leaves the spinner turning with no way back.
const runTimeout = 30 * time.Second

var (
	titleStyle       = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("6"))
	groupStyle       = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("8"))
	selectedStyle    = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("6"))
	descStyle        = lipgloss.NewStyle().Foreground(lipgloss.Color("7"))
	keyStyle         = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("6"))
	dividerStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	promptStyle      = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("11"))
	clusterStyle     = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("5"))
	headerStyle      = lipgloss.NewStyle().Bold(true)
	selectedRowStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("6"))
	badStyle         = lipgloss.NewStyle().Foreground(lipgloss.Color("9"))
	warnStyle        = lipgloss.NewStyle().Foreground(lipgloss.Color("11"))
	goodStyle        = lipgloss.NewStyle().Foreground(lipgloss.Color("10"))
	sepStyle         = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	helpStyle        = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	errStyle         = lipgloss.NewStyle().Foreground(lipgloss.Color("9"))
	sidebarStyle     = lipgloss.NewStyle().
				Border(lipgloss.NormalBorder(), false, true, false, false).
				BorderForeground(lipgloss.Color("8")).
				Width(sidebarWidth).
				PaddingRight(1)
	contentStyle = lipgloss.NewStyle().PaddingLeft(contentPad)
)

// entry is one line in the sidebar. A nil view marks a group header, which the
// cursor skips over.
type entry struct {
	label string
	view  *catalog.View
}

// resultMsg carries a finished run. The seq is compared against the model's
// current one so that a slow run cannot overwrite the output of a newer one.
type resultMsg struct {
	seq int
	res catalog.Result
	err error
}

// pane names the half of the screen that has the keyboard.
type pane int

const (
	paneList pane = iota
	paneOutput
)

// frame remembers a view so that drilling in can be undone.
type frame struct {
	view catalog.View
	args catalog.Args
}

type model struct {
	reg *catalog.Registry

	entries []entry
	cursor  int

	// current is what the output pane shows. It is not always the entry under
	// the sidebar cursor: drilling in reaches views that are not listed.
	current *catalog.View
	stack   []frame

	focus pane
	// row is the selected data row, and therefore what an action acts on
	// while the output pane has focus.
	row int

	width, height int

	spin      spinner.Model
	tickAlive bool

	vp    viewport.Model
	ready bool

	lookupCluster func(context.Context) (string, error)

	seq     int
	running bool
	res     catalog.Result
	err     error

	// args are the parameters the current result was produced with; an action
	// and a refresh have to use the same ones.
	args catalog.Args

	// cluster is shown wherever acting on the wrong one would be expensive.
	cluster string

	// cancel stops the run in flight, on escape or on quit.
	cancel context.CancelFunc

	// pending holds an action waiting for the user to answer its confirmation,
	// together with the parameters it will run with.
	pending     *catalog.Action
	pendingArgs catalog.Args
	prompt      string

	// asking collects an action's own parameters, one field at a time. A form
	// would be friendlier; this is enough to create something.
	asking    *catalog.Action
	askIdx    int
	askValues map[string]string
	input     textinput.Model

	// filter narrows what is shown — and therefore what an action operates
	// on, which is the whole point of not having row selection.
	filter    textinput.Model
	filtering bool
}

// clusterMsg carries the current kubectl context, looked up once at startup.
type clusterMsg struct{ name string }

// Run starts the TUI and blocks until the user quits. lookupCluster may be nil.
func Run(reg *catalog.Registry, lookupCluster func(context.Context) (string, error)) error {
	m := newModel(reg)
	m.lookupCluster = lookupCluster

	prog := tea.NewProgram(m)
	_, err := prog.Run()

	// Whatever was still running has no one left to report to.
	m.stop()
	return err
}

func newModel(reg *catalog.Registry) *model {
	m := &model{
		reg:  reg,
		spin: spinner.New(spinner.WithSpinner(spinner.MiniDot)),
		vp:   viewport.New(),
	}
	m.filter = textinput.New()
	m.filter.Prompt = "/"
	m.input = textinput.New()

	// Long lines are not wrapped, so they can be scrolled instead. Wrapping a
	// table row would make the columns meaningless.
	m.vp.SoftWrap = false

	for _, group := range reg.Groups() {
		listed := reg.Listed(group)
		if len(listed) == 0 {
			continue
		}
		m.entries = append(m.entries, entry{label: group})
		for _, v := range listed {
			m.entries = append(m.entries, entry{label: v.Name, view: &v})
		}
	}

	// Start on the first real view rather than on a header.
	m.cursor = m.next(-1, +1)

	return m
}

// next returns the index of the nearest selectable entry from i in direction
// step, or i itself when there is nothing to move to.
func (m *model) next(i, step int) int {
	for j := i + step; j >= 0 && j < len(m.entries); j += step {
		if m.entries[j].view != nil {
			return j
		}
	}
	if i >= 0 && i < len(m.entries) && m.entries[i].view != nil {
		return i
	}
	return 0
}

// listView is the entry under the sidebar cursor.
func (m *model) listView() *catalog.View {
	if m.cursor < 0 || m.cursor >= len(m.entries) {
		return nil
	}
	return m.entries[m.cursor].view
}

// selected is the view whose output is on screen.
func (m *model) selected() *catalog.View {
	if m.current != nil {
		return m.current
	}
	return m.listView()
}

// visible is the result after filtering: what the output pane renders.
func (m *model) visible() catalog.Result {
	return filtered(m.res, m.filter.Value())
}

// target is what an action operates on. With the output pane focused that is
// the selected row; otherwise it is everything the filter left. The CLI
// reaches the same two cases through the view's parameter and through no
// parameter at all.
func (m *model) target() catalog.Result {
	vis := m.visible()

	tbl, ok := vis.(catalog.Table)
	if !ok || m.focus != paneOutput || len(tbl.Rows) == 0 {
		return vis
	}

	row := min(max(m.row, 0), len(tbl.Rows)-1)
	return catalog.Table{Cols: tbl.Cols, Emphasis: tbl.Emphasis, Rows: tbl.Rows[row : row+1]}
}

// rowCount is how many rows the output pane is showing.
func (m *model) rowCount() int {
	if tbl, ok := m.visible().(catalog.Table); ok {
		return len(tbl.Rows)
	}
	return 0
}

func (m *model) Init() tea.Cmd {
	if m.lookupCluster == nil {
		return nil
	}
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), runTimeout)
		defer cancel()

		name, err := m.lookupCluster(ctx)
		if err != nil {
			// Not knowing the cluster is not worth an error pane; the label
			// simply stays empty.
			return clusterMsg{}
		}
		return clusterMsg{name: name}
	}
}

// stop cancels the run in flight, if any.
func (m *model) stop() {
	if m.cancel != nil {
		m.cancel()
		m.cancel = nil
	}
}

func (m *model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.resize()
		m.ready = true
		return m, nil

	case tea.KeyPressMsg:
		return m.onKey(msg)

	case resultMsg:
		// A superseded run finishing late must not clobber the current view.
		if msg.seq != m.seq {
			return m, nil
		}
		m.cancel = nil

		// An action that moves rather than reports: follow it instead of
		// trying to render it.
		if to, ok := msg.res.(catalog.Navigate); ok && msg.err == nil {
			m.running = false
			return m, m.navigate(to)
		}

		m.running, m.res, m.err = false, msg.res, msg.err

		// Escape is a decision, not a failure. A deadline is a failure, but
		// one worth naming plainly.
		switch {
		case errors.Is(msg.err, context.Canceled):
			m.err = nil
		case errors.Is(msg.err, context.DeadlineExceeded):
			m.err = fmt.Errorf("timed out after %s", runTimeout)
		}

		m.showResult()
		return m, nil

	case clusterMsg:
		m.cluster = msg.name
		return m, nil

	case spinner.TickMsg:
		if !m.running {
			m.tickAlive = false
			return m, nil
		}
		var cmd tea.Cmd
		m.spin, cmd = m.spin.Update(msg)
		return m, cmd
	}

	return m, nil
}

func (m *model) onKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	key := msg.String()

	// Collecting a value owns the keyboard: everything typed is part of the
	// answer, including keys that are otherwise bindings.
	if m.asking != nil {
		switch key {
		case "enter":
			return m, m.answer()
		case "esc":
			m.cancelAsking()
			return m, nil
		}
		var cmd tea.Cmd
		m.input, cmd = m.input.Update(msg)
		return m, cmd
	}

	// A pending confirmation swallows every key, so that a stray j or p cannot
	// answer a question about deleting things.
	if m.pending != nil {
		switch key {
		case "y", "Y":
			act, args := *m.pending, m.pendingArgs
			m.pending, m.pendingArgs, m.prompt = nil, nil, ""
			return m, m.runAction(act, args)
		default:
			m.pending, m.pendingArgs, m.prompt = nil, nil, ""
			return m, nil
		}
	}

	// While typing a filter the input owns the keyboard, or "q" would quit
	// halfway through a word.
	if m.filtering {
		switch key {
		case "enter":
			m.filtering = false
			m.filter.Blur()
			return m, nil
		case "esc":
			m.filtering = false
			m.filter.Blur()
			m.filter.SetValue("")
			m.showResult()
			return m, nil
		}
		var cmd tea.Cmd
		m.filter, cmd = m.filter.Update(msg)
		m.showResult()
		return m, cmd
	}

	switch key {
	case "q", "ctrl+c":
		m.stop()
		return m, tea.Quit

	case "/":
		if m.res == nil {
			return m, nil
		}
		m.filtering = true
		return m, m.filter.Focus()

	case "tab":
		return m, m.toggleFocus()

	case "<", ">":
		// Columns are never cut, so anything past the right edge is reached by
		// scrolling rather than lost.
		step := m.contentWidth() / 3
		if key == "<" {
			m.vp.ScrollLeft(step)
		} else {
			m.vp.ScrollRight(step)
		}
		return m, nil

	case "esc":
		switch {
		case m.running:
			m.stop()
		case m.filter.Value() != "":
			m.filter.SetValue("")
			m.showResult()
		case len(m.stack) > 0:
			return m, m.back()
		}
		return m, nil

	case "up", "k":
		m.move(-1)
		return m, nil

	case "down", "j":
		m.move(+1)
		return m, nil

	case "r":
		return m, m.start()

	case "enter":
		// In the list, enter runs the highlighted view. In the output pane it
		// drills in, if the view offers somewhere to go.
		if m.focus == paneList {
			m.current, m.stack = nil, nil
			return m, m.start()
		}
		if act, ok := m.actionFor("enter"); ok {
			return m, m.arm(act)
		}
		return m, nil
	}

	// Action bindings come from the registry, so they can only fire once a
	// result exists for them to act on.
	if act, ok := m.actionFor(key); ok {
		return m, m.arm(act)
	}

	// Anything else scrolls the output pane.
	return m, m.forwardToPane(msg)
}

// actionFor resolves a key to an action of the current view. Navigation and
// quit are matched first above, so config can never claim them.
func (m *model) actionFor(key string) (catalog.Action, bool) {
	cur := m.selected()
	if cur == nil || m.res == nil || m.running || m.filtering || m.asking != nil {
		return catalog.Action{}, false
	}
	for _, a := range cur.Actions {
		if a.Key == key {
			return a, true
		}
	}
	return catalog.Action{}, false
}

// toggleFocus moves the keyboard between the list and the output. Only worth
// doing when the output has rows to land on.
func (m *model) toggleFocus() tea.Cmd {
	if m.focus == paneList {
		if m.rowCount() == 0 {
			return nil
		}
		m.focus, m.row = paneOutput, 0
	} else {
		m.focus = paneList
	}
	m.showResult()
	return nil
}

// move steps the cursor of whichever pane has focus.
func (m *model) move(step int) {
	if m.focus == paneList {
		m.cursor = m.next(m.cursor, step)
		return
	}

	if n := m.rowCount(); n > 0 {
		m.row = min(max(m.row+step, 0), n-1)
		m.showResult()
	}
}

// back returns to the view that was drilled in from.
func (m *model) back() tea.Cmd {
	last := m.stack[len(m.stack)-1]
	m.stack = m.stack[:len(m.stack)-1]

	view := last.view
	m.current, m.args = &view, last.args
	return m.runCurrent(last.args)
}

// navigate drills into another view, remembering where it came from.
func (m *model) navigate(to catalog.Navigate) tea.Cmd {
	next, ok := m.reg.Get(to.View)
	if !ok {
		m.err = fmt.Errorf("no such view: %s", to.View)
		return nil
	}

	if cur := m.selected(); cur != nil {
		m.stack = append(m.stack, frame{view: *cur, args: m.args})
	}
	m.current, m.args = &next, to.Args
	return m.runCurrent(to.Args)
}

// arm collects whatever the action still needs, then asks, then runs.
func (m *model) arm(act catalog.Action) tea.Cmd {
	if len(act.Params) > 0 {
		m.asking, m.askIdx, m.askValues = &act, 0, map[string]string{}
		m.input.Reset()
		return m.input.Focus()
	}
	return m.confirmOrRun(act, m.args)
}

func (m *model) confirmOrRun(act catalog.Action, args catalog.Args) tea.Cmd {
	if act.Confirm != nil {
		if prompt := act.Confirm(m.target(), args); prompt != "" {
			m.pending, m.pendingArgs, m.prompt = &act, args, prompt
			return nil
		}
	}
	return m.runAction(act, args)
}

// answer records the current field and moves on, or finishes the collection.
func (m *model) answer() tea.Cmd {
	act := *m.asking
	param := act.Params[m.askIdx]
	m.askValues[param.Name] = m.input.Value()

	if m.askIdx+1 < len(act.Params) {
		m.askIdx++
		m.input.Reset()
		return nil
	}

	values := m.askValues
	m.cancelAsking()

	// The action's own parameters win over the view's: they are the more
	// specific answer to the same name.
	args := catalog.Args{}
	for k, v := range m.args {
		args[k] = v
	}
	bound, err := act.Bind(values)
	if err != nil {
		m.err = err
		return nil
	}
	for k, v := range bound {
		args[k] = v
	}

	return m.confirmOrRun(act, args)
}

func (m *model) cancelAsking() {
	m.asking, m.askIdx, m.askValues = nil, 0, nil
	m.input.Blur()
	m.input.Reset()
}

// runAction runs the action against the result currently on screen, then
// re-runs the view if the action changed anything.
func (m *model) runAction(act catalog.Action, args catalog.Args) tea.Cmd {
	cur := m.selected()
	if cur == nil || m.res == nil {
		return nil
	}

	m.stop()
	m.seq++
	m.running, m.err = true, nil

	ctx, cancel := context.WithTimeout(context.Background(), runTimeout)
	m.cancel = cancel

	seq, in := m.seq, m.target()
	run := func() tea.Msg {
		defer cancel()

		out, err := act.Run(ctx, in, args)
		if err != nil {
			return resultMsg{seq: seq, err: err}
		}
		if _, moving := out.(catalog.Navigate); moving || !act.Refresh {
			return resultMsg{seq: seq, res: out}
		}

		// Showing the view again matters more than showing what the action
		// printed: after a pause the AUTO column is the answer.
		res, err := cur.Run(ctx, args)
		return resultMsg{seq: seq, res: res, err: err}
	}

	if m.tickAlive {
		return run
	}
	m.tickAlive = true
	return tea.Batch(run, m.spin.Tick)
}

// start runs the view the sidebar cursor is on. Nothing blocks here: the work
// happens inside a tea.Cmd, which is what keeps the UI responsive while it
// runs.
func (m *model) start() tea.Cmd {
	cur := m.selected()
	if cur == nil {
		return nil
	}

	bound, err := cur.Bind(nil)
	if err != nil {
		// A view with a required parameter has nothing to run on. Saying so
		// beats running it with an empty value, which for syncstatus would
		// show some arbitrary application's resources as if they were the
		// answer. Such views are Hidden and reached by drilling in.
		m.running, m.res, m.err = false, nil, err
		return nil
	}
	return m.runCurrent(bound)
}

// runCurrent runs whatever view is selected with the given parameters.
func (m *model) runCurrent(args catalog.Args) tea.Cmd {
	cur := m.selected()
	if cur == nil {
		return nil
	}

	if cur.Available != nil {
		if err := cur.Available(); err != nil {
			m.running, m.res, m.err = false, nil, err
			return nil
		}
	}

	m.args = args

	m.stop()
	m.seq++
	m.running, m.res, m.err = true, nil, nil
	m.filter.SetValue("")
	m.focus, m.row = paneList, 0

	ctx, cancel := context.WithTimeout(context.Background(), runTimeout)
	m.cancel = cancel

	seq := m.seq
	run := func() tea.Msg {
		defer cancel()
		res, err := cur.Run(ctx, args)
		return resultMsg{seq: seq, res: res, err: err}
	}

	// Only one tick chain at a time, otherwise the spinner speeds up with
	// every run.
	if m.tickAlive {
		return run
	}
	m.tickAlive = true
	return tea.Batch(run, m.spin.Tick)
}

// forwardToPane scrolls the output. Both Text and Table live in the viewport,
// because the table is rendered to lines by internal/render rather than by a
// widget with a layout of its own.
func (m *model) forwardToPane(msg tea.Msg) tea.Cmd {
	var cmd tea.Cmd
	m.vp, cmd = m.vp.Update(msg)
	return cmd
}

func (m *model) bodyHeight() int {
	if h := m.height - chromeHeight - m.actionBarLines(); h > 0 {
		return h
	}
	return 1
}

// actionBarLines is the vertical space the bar takes. A confirmation gets as
// many lines as it needs: truncating it would drop the cluster name, which is
// the part most worth reading.
func (m *model) actionBarLines() int {
	if len(m.actions()) == 0 {
		return 0
	}
	if m.asking != nil {
		return 1 + lipgloss.Height(m.askBlock())
	}
	if m.pending != nil {
		return 1 + lipgloss.Height(m.promptBlock()) // divider plus the question
	}
	if m.filterLine() != "" {
		return actionBarHeight + 1
	}
	return actionBarHeight
}

// filterLine shows the filter and what it left, because an action acts on the
// remainder and the count is the only warning of that.
func (m *model) filterLine() string {
	if !m.filtering && m.filter.Value() == "" {
		return ""
	}

	line := m.filter.View()
	if !m.filtering {
		line = dividerStyle.Render("/" + m.filter.Value())
	}
	if n, ok := shownCount(m.visible()); ok {
		line += dividerStyle.Render(fmt.Sprintf("   %d of %d", n, mustCount(m.res)))
	}
	return line
}

func shownCount(res catalog.Result) (int, bool) {
	switch r := res.(type) {
	case catalog.Table:
		return len(r.Rows), true
	case catalog.Text:
		return len(r.Lines), true
	}
	return 0, false
}

func mustCount(res catalog.Result) int {
	n, _ := shownCount(res)
	return n
}

// askBlock shows which field is being collected, how far along it is, and what
// has been answered so far.
func (m *model) askBlock() string {
	act := *m.asking
	param := act.Params[m.askIdx]

	head := promptStyle.Render(fmt.Sprintf("%s (%d/%d)", act.Name, m.askIdx+1, len(act.Params)))
	if param.Short != "" {
		head += dividerStyle.Render("  " + param.Short)
	}

	lines := []string{head}

	// Answered fields stay visible: by the fourth one you have forgotten the
	// first, and this is the only chance to notice a typo before confirming.
	for _, done := range act.Params[:m.askIdx] {
		lines = append(lines, dividerStyle.Render("  "+done.Name+": ")+m.askValues[done.Name])
	}

	m.input.Prompt = param.Name + ": "
	lines = append(lines, "  "+m.input.View())

	return lipgloss.NewStyle().Width(m.contentWidth()).Render(
		lipgloss.JoinVertical(lipgloss.Left, lines...))
}

// promptBlock wraps the question and puts the cluster on its own line.
func (m *model) promptBlock() string {
	wrap := lipgloss.NewStyle().Width(m.contentWidth())

	// The answer goes on the last line, not appended to the question: a
	// multi-line prompt would otherwise bury it mid-paragraph.
	last := promptStyle.Render("[y/N]")
	if m.cluster != "" {
		last = dividerStyle.Render("cluster: ") + clusterStyle.Render(m.cluster) +
			dividerStyle.Render("   ") + last
	}

	return lipgloss.JoinVertical(lipgloss.Left,
		wrap.Render(promptStyle.Render(m.prompt)),
		last,
	)
}

func (m *model) actions() []catalog.Action {
	if cur := m.selected(); cur != nil {
		return cur.Actions
	}
	return nil
}

// contentWidth is the space the output widgets may actually paint on: the
// window minus the sidebar, its one-column border and the left padding. Sizing
// widgets to anything wider makes rows wrap and pushes the help line off screen.
func (m *model) contentWidth() int {
	if w := m.width - sidebarWidth - 1 - contentPad; w > 0 {
		return w
	}
	return 1
}

func (m *model) resize() {
	m.vp.SetWidth(m.contentWidth())
	m.vp.SetHeight(m.bodyHeight())
	m.showResult()
}

// filtered narrows a result to the rows or lines that match.
func filtered(res catalog.Result, needle string) catalog.Result {
	needle = strings.ToLower(strings.TrimSpace(needle))
	if needle == "" || res == nil {
		return res
	}

	switch r := res.(type) {
	case catalog.Table:
		out := catalog.Table{Cols: r.Cols, Emphasis: r.Emphasis}
		for _, row := range r.Rows {
			if strings.Contains(strings.ToLower(strings.Join(row.Cells, " ")), needle) {
				out.Rows = append(out.Rows, row)
			}
		}
		return out

	case catalog.Text:
		var out catalog.Text
		for _, line := range r.Lines {
			if strings.Contains(strings.ToLower(line), needle) {
				out.Lines = append(out.Lines, line)
			}
		}
		return out
	}
	return res
}

// showResult feeds the current Result into whichever widget can display it.
// The same Result also goes through render.CLI in non-interactive mode.
func (m *model) showResult() {
	switch r := m.visible().(type) {
	case catalog.Text:
		m.vp.SetContent(strings.Join(r.Lines, "\n"))
	case catalog.Table:
		selected := -1
		if m.focus == paneOutput && len(r.Rows) > 0 {
			m.row = min(max(m.row, 0), len(r.Rows)-1)
			selected = m.row
		}
		m.vp.SetContentLines(render.Table(r, tableStyle(selected)))
	}
}

// tableStyle mirrors the CLI palette and adds the selection. Both modes go
// through render.Style, so they cannot drift on which values count as trouble.
//
// selected is -1 when no row is highlighted. The emphasis colours are dropped
// on that row: a highlight has to survive the whole line, and the reset code
// at the end of a coloured cell would cut it short.
func tableStyle(selected int) render.Style {
	return render.Style{
		Header: func(s string) string { return headerStyle.Render(s) },
		Cell: func(row int, e catalog.Emphasis, s string) string {
			if row == selected {
				return selectedRowStyle.Render(s)
			}
			switch e {
			case catalog.Bad:
				return badStyle.Render(s)
			case catalog.Warn:
				return warnStyle.Render(s)
			case catalog.Good:
				return goodStyle.Render(s)
			default:
				return s
			}
		},
	}
}

func (m *model) View() tea.View {
	v := tea.NewView("")
	v.AltScreen = true

	if !m.ready {
		v.Content = "loading…"
		return v
	}

	pane := m.content()
	if bar := m.actionBar(); bar != "" {
		pane = lipgloss.JoinVertical(lipgloss.Left, pane, bar)
	}

	body := lipgloss.JoinHorizontal(
		lipgloss.Top,
		m.sidebarBorder().Height(m.bodyHeight()+m.actionBarLines()).Render(m.sidebar()),
		contentStyle.Render(pane),
	)

	v.Content = lipgloss.JoinVertical(lipgloss.Left, m.title(), "", body, "", m.help())
	return v
}

// title reads tool · id · description, with the cluster right-aligned. The
// description is the first thing dropped when space runs short: the id and the
// cluster are what you navigate and what you must not get wrong.
func (m *model) title() string {
	sep := sepStyle.Render("  ·  ")
	sepWidth := lipgloss.Width(sep)

	name := titleStyle.Render("aldediggi")
	cluster := m.clusterLabel()

	cur := m.selected()
	if cur == nil {
		return name
	}

	id := cur.ID()
	if len(m.stack) > 0 {
		id = m.stack[0].view.ID() + " › " + id
	}
	if arg := m.argLabel(); arg != "" {
		id += " " + arg
	}

	line := name + sep + titleStyle.Render(id)

	// Everything below needs a known width; before the first WindowSizeMsg
	// there is none to budget against.
	if m.width <= 0 {
		return line
	}

	room := m.width - lipgloss.Width(line)
	if cluster != "" {
		room -= lipgloss.Width(cluster) + 1
	}

	if cur.Short != "" && room > sepWidth+lipgloss.Width(cur.Short) {
		line += sep + descStyle.Render(cur.Short)
	}

	if cluster != "" {
		if gap := m.width - lipgloss.Width(line) - lipgloss.Width(cluster); gap > 0 {
			line += strings.Repeat(" ", gap) + cluster
		}
	}
	return lipgloss.NewStyle().MaxWidth(m.width).Render(line)
}

// argLabel names the parameters the current view was run with, so a drilled
// view says which application it is about.
func (m *model) argLabel() string {
	cur := m.selected()
	if cur == nil {
		return ""
	}
	var vals []string
	for _, p := range cur.Params {
		if v := m.args.Get(p.Name); v != "" {
			vals = append(vals, v)
		}
	}
	return strings.Join(vals, " ")
}

// scrollable reports whether anything reaches past the right edge, so the help
// only offers the keys that would do something.
func (m *model) scrollable() bool {
	if m.vp.XOffset() > 0 {
		return true
	}
	for _, line := range m.paneLines() {
		if lipgloss.Width(line) > m.contentWidth() {
			return true
		}
	}
	return false
}

// paneLines is what the output pane currently holds.
func (m *model) paneLines() []string {
	switch r := m.visible().(type) {
	case catalog.Text:
		return r.Lines
	case catalog.Table:
		return render.Table(r, render.Style{})
	}
	return nil
}

func (m *model) clusterLabel() string {
	if m.cluster == "" {
		return ""
	}
	return clusterStyle.Render("⎈ " + m.cluster)
}

// sidebarBorder brightens the divider on the side that has the keyboard, so
// tab has something visible to show for itself.
func (m *model) sidebarBorder() lipgloss.Style {
	if m.focus == paneList {
		return sidebarStyle.BorderForeground(lipgloss.Color("6"))
	}
	return sidebarStyle.BorderForeground(lipgloss.Color("8"))
}

func (m *model) sidebar() string {
	var b strings.Builder
	for i, e := range m.entries {
		switch {
		case e.view == nil:
			if i > 0 {
				b.WriteString("\n")
			}
			b.WriteString(groupStyle.Render(e.label) + "\n")
		case i == m.cursor && m.focus == paneList:
			b.WriteString(selectedStyle.Render("▸ "+e.label) + "\n")
		case i == m.cursor:
			b.WriteString(dividerStyle.Render("▸ "+e.label) + "\n")
		default:
			b.WriteString("  " + e.label + "\n")
		}
	}
	return b.String()
}

// content renders the output pane, padded to a fixed height so that the action
// bar underneath does not jump around as results change size.
func (m *model) content() string {
	// Height pads, MaxHeight clamps. Width is deliberately not set: the
	// widgets already size themselves to contentWidth, and re-wrapping their
	// output here would add lines and push the help line off screen.
	return lipgloss.NewStyle().
		Height(m.bodyHeight()).
		MaxHeight(m.bodyHeight()).
		Render(m.pane())
}

func (m *model) pane() string {
	switch {
	case m.err != nil:
		return errStyle.Render("error: " + m.err.Error())

	case m.running:
		return fmt.Sprintf("%s running…", m.spin.View())

	case m.res == nil:
		return helpStyle.Render("press ⏎ to run")
	}

	// A table of nothing but headers reads like a failure. Say which it is.
	if n, ok := shownCount(m.visible()); ok && n == 0 {
		if m.filter.Value() != "" {
			return helpStyle.Render("nothing matches " + m.filter.Value())
		}
		return helpStyle.Render("nothing to show")
	}

	return m.vp.View()
}

// actionBar lists what can be done with what is on screen. A pending
// confirmation replaces it, so the question sits where the answer is expected.
func (m *model) actionBar() string {
	acts := m.actions()
	if len(acts) == 0 {
		return ""
	}

	divider := dividerStyle.Render(strings.Repeat("─", max(m.contentWidth(), 1)))

	if m.asking != nil {
		return lipgloss.JoinVertical(lipgloss.Left, divider, m.askBlock())
	}
	if m.pending != nil {
		return lipgloss.JoinVertical(lipgloss.Left, divider, m.promptBlock())
	}

	parts := make([]string, 0, len(acts))
	for _, a := range acts {
		label := a.Name
		if a.Short != "" {
			label = a.Name
		}
		parts = append(parts, keyStyle.Render(a.Key)+" "+label)
	}

	line := lipgloss.NewStyle().MaxWidth(m.contentWidth()).Render(strings.Join(parts, dividerStyle.Render("  ·  ")))
	if m.res == nil || m.running {
		// Nothing to act on yet; show the bindings but do not pretend they work.
		line = dividerStyle.Render(visibleOnly(line))
	}

	if f := m.filterLine(); f != "" {
		return lipgloss.JoinVertical(lipgloss.Left, divider, f, line)
	}
	return lipgloss.JoinVertical(lipgloss.Left, divider, line)
}

// visibleOnly strips styling so a disabled bar can be recoloured as a whole.
func visibleOnly(s string) string {
	var b strings.Builder
	for {
		i := strings.Index(s, "\x1b[")
		if i < 0 {
			b.WriteString(s)
			return b.String()
		}
		b.WriteString(s[:i])
		j := strings.IndexByte(s[i:], 'm')
		if j < 0 {
			return b.String()
		}
		s = s[i+j+1:]
	}
}

func (m *model) help() string {
	switch {
	case m.asking != nil:
		return helpStyle.Render("⏎ next  ·  esc cancel")
	case m.pending != nil:
		return helpStyle.Render("y confirm  ·  any other key cancels")
	case m.filtering:
		return helpStyle.Render("⏎ keep filter  ·  esc clear")
	case m.running:
		return helpStyle.Render("esc cancel  ·  q quit")
	}

	keys := []string{"↑↓ move", "⇥ focus", "/ filter", "r reload", "q quit"}
	if m.scrollable() {
		keys = append(keys, "<> scroll")
	}
	if m.focus == paneList {
		keys = append([]string{"⏎ run"}, keys...)
	}
	if len(m.stack) > 0 {
		keys = append(keys, "esc back")
	}
	return helpStyle.Render(strings.Join(keys, "  ·  "))
}
