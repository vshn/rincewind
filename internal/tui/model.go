// Package tui renders the configured views as an interactive two-pane
// browser: views on the left, the output of the selected view on the right.
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
	"github.com/charmbracelet/x/ansi"

	"github.com/vshn/rincewind/internal/engine"
	"github.com/vshn/rincewind/internal/render"
)

const (
	// sidebarWidth is the outer width of the sidebar box, and of the version
	// box below it: the two frames line up into one column.
	sidebarWidth = 30
	// titleHeight is the title box: border, text, border. Hit-testing counts
	// from it, so the mouse and the renderer share one idea of the layout.
	titleHeight = 3
	// frameChrome is the title box above and the footer boxes below the
	// body, three lines each.
	frameChrome = 2 * titleHeight
	// boxPad is what a frame adds around its text: border plus one space of
	// padding on each side.
	boxPad = 4
)

// runTimeout bounds a single script run. Without it an unreachable API server
// leaves the spinner turning with no way back.
const runTimeout = 30 * time.Second

// entry is one line in the sidebar. A nil view marks a group header, which
// the cursor skips over.
type entry struct {
	label string
	view  *engine.View
}

// resultMsg carries a finished view run. The seq names the tab it belongs
// to, so a slow run cannot overwrite the output of a newer one or of
// another tab.
type resultMsg struct {
	seq int
	res engine.Result
	err error
}

// jobMsg carries what an action's steps printed, one entry per step so the
// output stays beside the invocation that produced it.
type jobMsg struct {
	seq  int
	out  [][]string
	errs []error
}

// chunkMsg carries lines a followed view produced since the last one. The
// seq names the tab, exactly as a finished run does.
type chunkMsg struct {
	seq   int
	lines []string
	end   bool
	err   error
}

// refreshMsg re-runs a view with a refresh interval. It carries the seq of
// the run it was scheduled after: a tick outlived by a newer run is stale.
type refreshMsg struct {
	seq int
}

// pane names the half of the screen that has the keyboard.
type pane int

const (
	paneList pane = iota
	paneOutput
)

// tab is one view on screen in the content box: the view, the parameters it
// was opened with, and every piece of state that must be exactly where it
// was left when the user comes back to it. The previous generation of this
// tool kept a view stack and re-ran the parent on the way back, which lost
// the filter, the row cursor and the scroll position — that loss is what
// tabs exist to fix.
type tab struct {
	view *engine.View
	args engine.Args

	res *engine.Result
	err error

	// row is the data row the output-pane cursor is on.
	row int
	// filter is the widget's value, parked while another tab holds it.
	filter string
	// selection holds the keys of the selected rows. Keys rather than
	// indices, so a reload that reorders the table keeps the selection on
	// the same objects.
	selection map[string]bool
	// yOff and xOff park the scroll position of a tab in the background.
	yOff, xOff int

	// seq stamps the run in flight. Numbers come from one counter that never
	// reuses them, so a result finds its tab by it and finds none when the
	// tab has since started a newer run or was closed.
	seq     int
	running bool
	cancel  context.CancelFunc
	// fetched is when res arrived, so a tab whose refresh interval elapsed
	// in the background can reload on the way back instead of showing stale
	// rows.
	fetched time.Time

	// stream is the channel a followed view's lines arrive on. It is set
	// while following and cleared when the script ends or the tab is left:
	// a stream pauses in the background, the way a refresh does, so there is
	// never more than one script following.
	stream <-chan engine.Chunk
	// ended says the script stopped by itself rather than being cancelled —
	// the pod went away, which is a finding worth showing. endErr is kept
	// apart from err on purpose: err replaces the output, and the lines read
	// before the stream broke are exactly what one wants to look at.
	// stopped says following was ended by hand rather than by the script:
	// being told "the stream ended" after pressing esc explains nothing one
	// did not just do.
	ended   bool
	stopped bool
	endErr  error

	// job is set on a tab that runs an action rather than showing a view.
	// A tab holds one or the other, never both.
	job *job
}

// job is a working action given a tab of its own, and the whole of the
// interaction with it: the parameters it still needs, then what it is about
// to run spelled out, then the question, then what it had to say. Showing
// the command before it runs is the point — a confirmation with the
// invocation in plain sight above it asks a question one can answer.
type job struct {
	action engine.Action
	rows   []any
	keys   []string
	args   engine.Args // the view's parameters, to merge the collected ones into

	// vals and idx are the form: values collected so far, and the field
	// being asked for.
	vals map[string]string
	idx  int

	steps  []engine.Step
	prompt string     // set once planned and a confirmation is wanted
	out    [][]string // what each step printed, filled in as they finish
	errs   []error    // and how it went wrong, per step
	done   bool
	reload *tab // the view to run again afterwards, if the action asked
}

// The phases a job walks through, in order.
func (j *job) asking() bool     { return j.idx < len(j.action.Params) }
func (j *job) confirming() bool { return !j.asking() && j.prompt != "" }

// Whether the tab on screen is in one of those phases, for the help line.
func (m *model) jobAsking() bool {
	j := m.cur().job
	return j != nil && j.asking()
}

func (m *model) jobConfirming() bool {
	j := m.cur().job
	return j != nil && j.confirming()
}

// field is the parameter being asked for.
func (j *job) field() engine.Param { return j.action.Params[j.idx] }

func newTab(v *engine.View, args engine.Args) *tab {
	return &tab{view: v, args: args, selection: map[string]bool{}}
}

// label names the tab: the view's short name, or the action a job runs.
func (t *tab) label() string {
	switch {
	case t.job != nil:
		return t.job.label()
	case t.view == nil:
		return "?"
	case t.view.Name != "":
		return t.view.Name
	default:
		return t.view.ID
	}
}

// stop cancels this tab's run in flight, if any.
func (t *tab) stop() {
	if t.cancel != nil {
		t.cancel()
		t.cancel = nil
	}
}

type model struct {
	entries []entry
	cursor  int

	// version is shown in the footer; the binary stamps it at build time.
	version string

	// cluster is the kubectl context the scripts will run against, shown top
	// right wherever acting on the wrong one would be expensive. Empty when
	// there is no answer, and then nothing is shown at all.
	cluster string
	// clusterAt is when it was last asked, so a burst of view changes costs
	// one probe rather than one each.
	clusterAt time.Time
	// lookupCluster is the probe. Run sets it; newModel leaves it nil, so
	// the tests never spawn a process and never depend on a kubeconfig.
	lookupCluster func(context.Context) (string, error)

	// clipboard is the local clipboard writer, or nil for OSC 52 alone. Run
	// sets it; newModel leaves it nil, so running the tests cannot overwrite
	// what the person running them had on their clipboard.
	clipboard []string

	// tabs is never empty: the content box always shows something, even
	// before the first run. active indexes the one on screen.
	tabs   []*tab
	active int

	focus pane

	width, height int

	spin      spinner.Model
	tickAlive bool

	vp    viewport.Model
	ready bool

	// head is the table's header line. It is kept out of the viewport and
	// painted onto the frame's first row, so scrolling down never costs the
	// column names — which is most of what makes a wide table readable.
	head string
	// lines is how many lines the viewport holds, for the position label.
	lines int
	// widest is the widest of them, so the help line can say whether there
	// is anything past the right edge without measuring it all again.
	widest int
	// sideTop is the first sidebar line on screen, for menus taller than
	// their box.
	sideTop int

	// mouse reports whether the terminal's mouse is captured. Capturing it
	// takes away the terminal's own text selection, which for a tool people
	// copy node names out of is a real loss — hence the toggle.
	mouse bool

	// seq counts runs across every tab and never reuses a number, so one
	// rule covers all three kinds of stale message: a superseded run, a run
	// whose tab was closed, and a refresh tick for a backgrounded tab. Each
	// simply finds no tab.
	seq int

	// filter narrows what is shown. One widget; the value belongs to the
	// active tab and is parked on it when another takes over.
	filter    textinput.Model
	filtering bool

	// input collects an action's parameters, one field at a time. A second
	// widget rather than the filter's: that one's value belongs to a tab and
	// is parked when another takes over.
	input textinput.Model

	// notice is what an action had to say before running — most often that
	// nothing is selected. Cleared on the next keystroke, which is exactly
	// as long as it is worth reading.
	notice string
}

// cur is the tab the content box is showing.
func (m *model) cur() *tab { return m.tabs[m.active] }

// Run starts the TUI and blocks until the user quits, or until ctx is
// cancelled — which is what a signal does. Handing the context to bubbletea
// is what makes a SIGTERM come back through prog.Run, so the scripts still
// running are stopped rather than orphaned by a process that just ends.
func Run(ctx context.Context, views []engine.View, version string) error {
	m := newModel(views)
	m.version = version
	m.lookupCluster = kubeContext
	m.clipboard = localClipboard()

	prog := tea.NewProgram(m, tea.WithContext(ctx))
	_, err := prog.Run()

	// Whatever was still running has no one left to report to.
	m.stopAll()

	// Being asked to stop is not a failure to report; the tool was told to
	// go, and it went.
	if errors.Is(err, tea.ErrProgramKilled) {
		return nil
	}
	return err
}

func newModel(views []engine.View) *model {
	m := &model{
		spin:  spinner.New(spinner.WithSpinner(spinner.MiniDot)),
		vp:    viewport.New(),
		mouse: true,
	}
	m.filter = textinput.New()
	m.filter.Prompt = "/"
	m.input = textinput.New()

	// Long lines are not wrapped, so they can be scrolled instead. Wrapping a
	// table row would make the columns meaningless.
	m.vp.SoftWrap = false

	// Groups appear in order of first use; views keep the config file's
	// order within them. An empty group name lists its views without a
	// header.
	var order []string
	byGroup := map[string][]*engine.View{}
	for i := range views {
		v := &views[i]
		// A view needing a parameter cannot be started from the menu, so it
		// is not offered there; an action reaches it with one.
		if !v.Listed() {
			continue
		}
		if _, seen := byGroup[v.Group]; !seen {
			order = append(order, v.Group)
		}
		byGroup[v.Group] = append(byGroup[v.Group], v)
	}
	for _, g := range order {
		if g != "" {
			m.entries = append(m.entries, entry{label: g})
		}
		for _, v := range byGroup[g] {
			// The menu shows the id's name part; the title is description,
			// shown in the title bar once the view is selected.
			label := v.Name
			if label == "" {
				label = v.ID
			}
			m.entries = append(m.entries, entry{label: label, view: v})
		}
	}

	// Start on the first real view rather than on a header.
	m.cursor = m.next(-1, +1)
	// The content box always has a tab, even before anything has run.
	m.tabs = []*tab{newTab(m.selected(), nil)}

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

// around steps one selectable entry, going round the ends: on the last view
// a down lands on the first. The menu is a ring rather than a ladder because
// it is short and visible whole — the far end is nearer than the way back.
//
// Only single steps wrap. A page that wrapped would land somewhere in the
// middle after running off the end, which reads as a miss rather than as
// coming round; and the row cursor in the output pane never wraps, because a
// table of two hundred rows is not visible whole and losing one's place in it
// is not a shortcut.
func (m *model) around(i, step int) int {
	if j := m.next(i, step); j != i {
		return j
	}
	if step > 0 {
		return m.next(-1, +1)
	}
	return m.next(len(m.entries), -1)
}

// selected is the view whose output is on screen.
func (m *model) selected() *engine.View {
	if m.cursor < 0 || m.cursor >= len(m.entries) {
		return nil
	}
	return m.entries[m.cursor].view
}

// visible is the result after filtering: what the output pane renders.
func (m *model) visible() engine.Result {
	if m.cur().res == nil {
		return engine.Result{}
	}
	return filtered(*m.cur().res, m.filter.Value())
}

func (m *model) rowCount() int { return len(m.visible().Rows) }

// shownCount is how much the output pane is showing, of whichever kind: the
// filter's count and "nothing to show" mean the same thing either way.
func (m *model) shownCount() int {
	vis := m.visible()
	switch {
	case vis.IsText():
		return len(vis.Lines)
	case vis.IsChart():
		return len(vis.Series)
	}
	return len(vis.Rows)
}

// total is how much there was before the filter.
func (m *model) total() int {
	t := m.cur()
	switch {
	case t.res == nil:
		return 0
	case t.res.IsText():
		return len(t.res.Lines)
	case t.res.IsChart():
		return len(t.res.Series)
	default:
		return len(t.res.Rows)
	}
}

// Init asks which cluster this is before anything else: the answer belongs
// on screen from the first paint, not from the first view run.
func (m *model) Init() tea.Cmd { return m.refreshCluster() }

// stop cancels the run in flight, if any.
// stopAll cancels every run in flight, on quit.
func (m *model) stopAll() {
	for _, t := range m.tabs {
		t.stop()
	}
}

// anyRunning reports whether a tab, active or not, has work in flight. The
// spinner tick chain lives as long as one does.
func (m *model) anyRunning() bool {
	for _, t := range m.tabs {
		if !t.running {
			continue
		}
		// A followed view spins only until its first lines arrive. After
		// that nothing is turning on screen, and a tick chain outliving its
		// spinner would repaint ten times a second for as long as the log
		// runs.
		if t.stream != nil && t.res != nil && len(t.res.Lines) > 0 {
			continue
		}
		return true
	}
	return false
}

// tabBySeq finds the tab a message belongs to. Sequence numbers come from
// one counter and are never reused, so a run whose tab has since started a
// newer one, or whose tab was closed altogether, finds nothing — one rule
// covering every kind of staleness.
func (m *model) tabBySeq(seq int) *tab {
	for _, t := range m.tabs {
		if t.seq == seq {
			return t
		}
	}
	return nil
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

	case clusterMsg:
		m.cluster = msg.name
		return m, nil

	case copiedMsg:
		// Only a failure is worth saying: the notice already claimed the
		// copy, and it has to be taken back rather than left standing.
		if msg.err != nil {
			m.notice = "copy failed: " + msg.err.Error()
		}
		return m, nil

	case tea.MouseWheelMsg:
		m.onWheel(msg.Button, msg.X)
		return m, nil

	case tea.MouseClickMsg:
		if msg.Button == tea.MouseLeft {
			return m, m.onClick(msg.X, msg.Y)
		}
		return m, nil

	case resultMsg:
		// A superseded run, or one whose tab is gone, has no one to report to.
		t := m.tabBySeq(msg.seq)
		if t == nil {
			return m, nil
		}
		t.cancel, t.running = nil, false

		// Escape is a decision, not a failure. A deadline is a failure, but
		// one worth naming plainly.
		switch {
		case errors.Is(msg.err, context.Canceled):
			t.err = nil
		case errors.Is(msg.err, context.DeadlineExceeded):
			t.err = fmt.Errorf("timed out after %s", runTimeout)
		default:
			t.err = msg.err
		}

		// A run that failed replaces nothing. The rows from the last good
		// one stay on screen, with the failure said under them and their
		// age said beside them: one unreachable API server used to take a
		// standing table away for a whole refresh interval, and a cancelled
		// reload used to leave "nothing to show" behind without saying so.
		//
		// A view that has never produced anything has nothing to keep, and
		// there the error still has the pane to itself.
		if msg.err == nil {
			res := msg.res
			t.res, t.fetched = &res, time.Now()
			t.prune()
		}
		// A result for a tab in the background must not scribble on the one
		// the user is looking at.
		if t == m.cur() {
			m.showResult()
		}
		return m, m.scheduleRefresh(t)

	case jobMsg:
		t := m.tabBySeq(msg.seq)
		if t == nil || t.job == nil {
			return m, nil
		}
		t.cancel, t.running = nil, false
		t.job.out, t.job.errs, t.job.done = msg.out, msg.errs, true

		// The view the action changed is run again where it lives, so the
		// fresh rows are already there when one escapes back to them.
		var cmd tea.Cmd
		if to := t.job.reload; to != nil {
			cmd = m.launch(to)
		}
		if t == m.cur() {
			m.showResult()
		}
		return m, cmd

	case chunkMsg:
		t := m.tabBySeq(msg.seq)
		if t == nil {
			return m, nil
		}

		// Stay pinned to the end only if that is where the reader is; once
		// they have scrolled up, new lines must not yank the view away.
		follow := t == m.cur() && m.atBottom()
		t.appendLines(msg.lines)

		if msg.end {
			// stop rather than dropping the cancel func: the script is gone,
			// but its context still has to be released.
			t.stop()
			t.stream, t.running = nil, false
			t.ended, t.endErr = true, msg.err
		}
		if t == m.cur() {
			m.showResult()
			if follow {
				m.vp.GotoBottom()
			}
		}
		if msg.end {
			return m, nil
		}
		return m, m.readChunk(msg.seq, t.stream)

	case refreshMsg:
		// Only the tab on screen reloads on a tick: several tabs refreshing
		// in the background is several scripts against a cluster nobody is
		// watching. A backgrounded tab catches up when it comes forward.
		t := m.tabBySeq(msg.seq)
		if t == nil || t != m.cur() || t.running {
			return m, nil
		}
		// While the filter is being typed a reload would fight the keyboard;
		// skip this tick but keep the chain alive.
		if m.filtering {
			return m, m.scheduleRefresh(t)
		}
		return m, m.reload()

	case spinner.TickMsg:
		if !m.anyRunning() {
			m.tickAlive = false
			return m, nil
		}
		var cmd tea.Cmd
		m.spin, cmd = m.spin.Update(msg)
		return m, cmd
	}

	return m, nil
}

// The mouse acts on what it points at, not on what has the keyboard: the
// wheel over the table scrolls the table even while the menu is focused,
// which is the whole reason for reaching for the mouse in the first place.
const wheelStep = 3

func (m *model) onWheel(button tea.MouseButton, x int) {
	overList := x < sidebarWidth

	switch button {
	case tea.MouseWheelUp:
		if overList {
			m.scrollSidebar(-wheelStep)
		} else {
			m.vp.ScrollUp(wheelStep)
		}
	case tea.MouseWheelDown:
		if overList {
			m.scrollSidebar(+wheelStep)
		} else {
			m.vp.ScrollDown(wheelStep)
		}
	case tea.MouseWheelLeft:
		m.vp.ScrollLeft(wheelStep)
	case tea.MouseWheelRight:
		m.vp.ScrollRight(wheelStep)
	}
}

// onClick puts the cursor where it was pointed. In the menu that also runs
// the view: a menu entry one clicks is a thing one wants to see.
func (m *model) onClick(x, y int) tea.Cmd {
	if i := m.tabAt(x, y); i >= 0 {
		return m.activate(i)
	}

	if x < sidebarWidth {
		i := m.entryAt(y)
		if i < 0 {
			return nil
		}
		m.cursor, m.focus = i, paneList
		m.syncSidebar()
		return m.open()
	}

	row := m.rowAt(y)
	if row < 0 {
		return nil
	}
	m.focus, m.cur().row = paneOutput, row
	m.showResult()
	return nil
}

// prune drops selected keys the latest result no longer contains: a
// selection must never silently stand for rows that are gone.
func (t *tab) prune() {
	if len(t.selection) == 0 || t.res == nil {
		return
	}
	present := map[string]bool{}
	for _, r := range t.res.Rows {
		present[r.Key] = true
	}
	for key := range t.selection {
		if !present[key] {
			delete(t.selection, key)
		}
	}
}

// open runs the view under the sidebar cursor from scratch. Picking from the
// menu is starting over, not drilling: every tab opened by an action goes.
func (m *model) open() tea.Cmd {
	m.closeFrom(1)
	// The first tab is replaced rather than dropped, so its run has to be
	// cancelled by hand — a followed one would otherwise keep a script alive
	// with nothing left on screen to show for it.
	m.tabs[0].stop()
	m.tabs[0], m.active = newTab(m.selected(), nil), 0
	m.filter.SetValue("")
	m.focus = paneList
	return m.launch(m.cur())
}

// reload re-runs the active view but keeps the context a reload must not
// eat: the filter, the selection, the focused row.
func (m *model) reload() tea.Cmd {
	t := m.cur()
	if t.res == nil && t.err == nil && !t.running {
		// Nothing has run yet; a reload is an open.
		return m.open()
	}
	return m.launch(t)
}

// launch starts one tab's pipeline inside a tea.Cmd. Nothing blocks here:
// the work happens in the command, which is what keeps the UI painting while
// it runs.
//
// The closure captures the sequence number rather than the tab, because
// delivery is decided by that number alone: a tab closed mid-run simply has
// no one left to deliver to.
func (m *model) launch(t *tab) tea.Cmd {
	if t.view == nil {
		return nil
	}
	// A followed view is never finished, so it takes the other path — and
	// notably no deadline.
	if t.view.Follows() {
		return m.follow(t)
	}

	t.err = nil
	ctx, cancel, tick := m.begin(t, runTimeout)

	seq, view, args := t.seq, t.view, t.args
	run := func() tea.Msg {
		defer cancel()
		res, err := view.Run(ctx, args)
		return resultMsg{seq: seq, res: res, err: err}
	}
	return withTick(run, tick)
}

// begin puts a tab into the running state and hands back the context its
// work runs under: a superseded run is cancelled, the sequence number moves
// on so nothing it still sends finds a tab, and the tab remembers its cancel
// so leaving or closing it stops the script.
//
// A timeout of zero means no deadline — the followed case, which is never
// finished and would be cut off by runTimeout.
//
// The returned command is the spinner's first tick, or nil when a chain is
// already alive: two chains make the spinner turn twice as fast.
func (m *model) begin(t *tab, timeout time.Duration) (context.Context, context.CancelFunc, tea.Cmd) {
	t.stop()
	m.seq++
	t.seq, t.running = m.seq, true

	var (
		ctx    context.Context
		cancel context.CancelFunc
	)
	if timeout > 0 {
		ctx, cancel = context.WithTimeout(context.Background(), timeout)
	} else {
		ctx, cancel = context.WithCancel(context.Background())
	}
	t.cancel = cancel

	if m.tickAlive {
		return ctx, cancel, nil
	}
	m.tickAlive = true
	return ctx, cancel, m.spin.Tick
}

// withTick batches the work with the spinner tick begin handed out, if it
// handed out one at all.
func withTick(run tea.Cmd, tick tea.Cmd) tea.Cmd {
	if tick == nil {
		return run
	}
	return tea.Batch(run, tick)
}

// scheduleRefresh arms the auto-reload of a view that asked for one. Only
// the tab on screen gets a tick; see the refreshMsg branch.
func (m *model) scheduleRefresh(t *tab) tea.Cmd {
	if t != m.cur() || t.view == nil || t.view.Refresh <= 0 {
		return nil
	}
	seq := t.seq
	return tea.Tick(t.view.Refresh, func(time.Time) tea.Msg { return refreshMsg{seq: seq} })
}

// park saves the transient state of the tab about to be left. Anything not
// parked here is what the user will find missing on the way back.
func (m *model) park() {
	t := m.cur()
	t.yOff, t.xOff, t.filter = m.vp.YOffset(), m.vp.XOffset(), m.filter.Value()
}

// closeFrom drops the tabs from i on, cancelling their runs.
func (m *model) closeFrom(i int) {
	if i >= len(m.tabs) {
		return
	}
	for _, t := range m.tabs[i:] {
		t.stop()
	}
	m.tabs = m.tabs[:i]
	m.active = min(m.active, len(m.tabs)-1)
}

// activate switches the content box to tab i, parking the scroll position
// and the filter of the one being left: coming back has to land exactly
// where one left, which is the whole reason tabs replaced the view stack.
func (m *model) activate(i int) tea.Cmd {
	if i < 0 || i >= len(m.tabs) || i == m.active {
		return nil
	}
	m.park()
	m.pauseStream(m.cur())

	m.active = i
	return m.restore(m.cur())
}

// restore puts a tab back on screen exactly as it was left, which is the
// whole reason tabs replaced the view stack.
func (m *model) restore(t *tab) tea.Cmd {
	m.filter.SetValue(t.filter)

	// The offsets go back before showResult, whose follow() would otherwise
	// drag the position to the cursor it finds off screen.
	m.vp.SetYOffset(t.yOff)
	m.vp.SetXOffset(t.xOff)
	m.showResult()
	return m.resume(t)
}

// closeTab drops the active tab and falls back to the one it was opened
// from. The first tab stays: the content box always shows something.
func (m *model) closeTab() tea.Cmd {
	if m.active == 0 {
		return nil
	}
	to := m.active - 1
	m.closeFrom(m.active)
	m.active = to
	return m.restore(m.cur())
}

// resume re-arms a returning tab's refresh, reloading at once when the data
// aged past the interval while the tab sat in the background.
func (m *model) resume(t *tab) tea.Cmd {
	if t.view == nil || t.running {
		return nil
	}
	// A followed view was paused on the way out; start it again unless it
	// had already ended by itself, which is a finding to leave on screen.
	if t.view.Follows() {
		if t.ended {
			return nil
		}
		return m.follow(t)
	}
	if t.view.Refresh <= 0 {
		return nil
	}
	if !t.fetched.IsZero() && time.Since(t.fetched) >= t.view.Refresh {
		return m.launch(t)
	}
	return m.scheduleRefresh(t)
}

// viewActions is what the view on screen offers. A job tab offers nothing:
// it is a record of what was done, not something to act on.
func (m *model) viewActions() []engine.Action {
	if t := m.cur(); t.view != nil && t.job == nil {
		return t.view.Actions
	}
	return nil
}

// hasTable reports whether the content box is showing rows, as opposed to a
// job, a text view, a spinner or a note that there is nothing to see. Only
// rows get a sticky header and a row cursor.
//
// A failed run does not disqualify them: rows that stand while their view is
// unreachable are still rows, and are still what an action would act on.
func (m *model) hasTable() bool {
	t := m.cur()
	return t.job == nil && !t.running &&
		t.res != nil && !t.res.IsText() && m.rowCount() > 0
}

// hasText reports whether the content box is showing a text view's lines.
func (m *model) hasText() bool {
	t := m.cur()
	// A followed view has lines while it is still running, which is the
	// whole point; only an error hides them.
	return t.job == nil && t.res != nil && t.res.IsText() &&
		(!t.running || t.stream != nil)
}

// hasChart reports whether the content box is showing a chart. Like a text
// view it has no rows, so the arrows scroll rather than moving a cursor.
func (m *model) hasChart() bool {
	t := m.cur()
	return t.job == nil && !t.running && t.res != nil && t.res.IsChart()
}

// statusLines is the room the status bar takes, so the viewport shrinks
// rather than pushing rows out of the frame.
func (m *model) statusLines() int {
	if m.statusLine() == "" {
		return 0
	}
	return 1
}

// filtered narrows a table to the rows that match.
func filtered(t engine.Result, needle string) engine.Result {
	needle = strings.ToLower(strings.TrimSpace(needle))
	if needle == "" {
		return t
	}

	// Filtering a log is grep, and is most of what one does with one.
	if t.IsText() {
		out := engine.Result{Lines: []string{}, Notes: t.Notes}
		for _, line := range t.Lines {
			if strings.Contains(strings.ToLower(line), needle) {
				out.Lines = append(out.Lines, line)
			}
		}
		return out
	}

	// Filtering a chart picks series by name: /worker keeps the workers'
	// lines and drops the rest, which is how one node is read out of a pool.
	if t.IsChart() {
		out := engine.Result{Series: []engine.Series{}, Notes: t.Notes, YRange: t.YRange}
		for _, s := range t.Series {
			if strings.Contains(strings.ToLower(s.Name), needle) {
				out.Series = append(out.Series, s)
			}
		}
		return out
	}

	out := engine.Result{Cols: t.Cols, Notes: t.Notes}
	for _, row := range t.Rows {
		parts := make([]string, len(row.Cells))
		for i, c := range row.Cells {
			parts[i] = c.Text
		}
		if strings.Contains(strings.ToLower(strings.Join(parts, " ")), needle) {
			out.Rows = append(out.Rows, row)
		}
	}
	return out
}

// showResult feeds the current result into the viewport. The widget is
// re-sized here rather than only on window changes, because the header and
// the status line coming and going alter the viewport's share of the frame.
func (m *model) showResult() {
	m.vp.SetWidth(m.contentWidth())
	m.vp.SetHeight(m.vpHeight())

	// A job tab shows commands and their output, not rows.
	if t := m.cur(); t.job != nil {
		lines := t.job.lines(m.input.View())
		m.setContent("", lines)
		return
	}

	vis := m.visible()

	// A text view's lines go in as they came out of the script: no header,
	// no styling, no truncation. Whitespace is part of what is being read.
	if vis.IsText() {
		lines := vis.Lines
		// That a followed script stopped is itself a finding — the pod went
		// away — so it is said under the last line rather than swallowed.
		if t := m.cur(); t.ended {
			lines = append(append([]string{}, lines...), m.endMarker(t))
		}
		if note := m.staleNote(); len(note) > 0 {
			lines = append(append([]string{}, lines...), note...)
		}
		m.setContent("", lines)
		return
	}

	// A chart is drawn to the pane's size, so resizing re-renders it here.
	// Notes ride below it, exactly as they do under a table.
	if vis.IsChart() {
		var lines []string
		if len(vis.Series) > 0 {
			lines = render.Chart(vis, m.contentWidth(), m.vpHeight(), true)
		}
		for _, note := range vis.Notes {
			lines = append(lines, noteStyle.Render("! "+note))
		}
		lines = append(lines, m.staleNote()...)
		m.setContent("", lines)
		return
	}

	cursor := -1
	if m.focus == paneOutput && len(vis.Rows) > 0 {
		m.clampRow()
		cursor = m.cur().row
	}

	lines := render.Table(vis, m.tableStyle(vis, cursor))
	// The header comes out of the scrolling area and is painted onto the
	// frame's first row instead.
	head, lines := lines[0], lines[1:]

	// Cell evaluation problems ride below the table instead of replacing
	// it: a half-broken view is still mostly a view.
	for _, note := range vis.Notes {
		lines = append(lines, noteStyle.Render("! "+note))
	}
	lines = append(lines, m.staleNote()...)

	m.setContent(head, lines)
	m.followCursor(cursor)
}

// setContent hands the viewport what it is to show, and records what the
// chrome needs to know about it: how many lines there are, and how wide the
// widest of them is. The width is measured here, once per result, rather
// than by rendering the whole table again on every paint of the help line.
func (m *model) setContent(head string, lines []string) {
	m.head = head
	m.vp.SetContentLines(lines)
	m.lines = len(lines)

	m.widest = lipgloss.Width(head)
	for _, line := range lines {
		m.widest = max(m.widest, lipgloss.Width(line))
	}
}

// staleNote is the failed run said in full, under the rows it did not
// replace. A script's stderr is most of what a failure is worth, so it goes
// on screen whole rather than being cut to fit a status line — that one
// carries the short marker of the same thing, which is the half that has to
// stay visible from anywhere in a long table.
//
// Nothing to keep means nothing to note: there the error has the pane.
func (m *model) staleNote() []string {
	t := m.cur()
	if t.err == nil || t.res == nil {
		return nil
	}

	var out []string
	for i, line := range strings.Split(strings.TrimRight(t.err.Error(), "\n"), "\n") {
		prefix := "  "
		if i == 0 {
			prefix = "! "
		}
		out = append(out, errStyle.Render(prefix+line))
	}
	return out
}

// staleLabel says that what is on screen is not what the cluster last
// answered, when it did, and what went wrong.
//
// It names the time of the last good run rather than an age: the TUI paints
// on messages, not on a clock, so an age would stand still and lie between
// two of them.
//
// Of the failure it takes the first line, cut to half the box. The rest of
// the status line says what an action would act on — the filter, the
// selection — and a script that printed twenty lines of stderr must not push
// that off the edge. The whole of it stands under the rows.
func (m *model) staleLabel() string {
	t := m.cur()

	label := "⚠ stale"
	if !t.fetched.IsZero() {
		label += " — from " + t.fetched.Format("15:04:05")
	}
	if t.err == nil {
		return label
	}
	first, _, _ := strings.Cut(t.err.Error(), "\n")
	return label + ": " + ansi.Truncate(first, max(m.contentWidth()/2, 20), "…")
}

// endMarker says why a followed view stopped producing.
func (m *model) endMarker(t *tab) string {
	switch {
	case t.endErr != nil:
		return errStyle.Render("— stream failed: " + t.endErr.Error() + " —  (r to start again)")
	case t.stopped:
		return dividerStyle.Render("— stopped —  (r to start again)")
	}
	return dividerStyle.Render("— stream ended —  (r to start again)")
}

// tableStyle paints emphasis, the row cursor and the selection. Emphasis
// colours are dropped on highlighted rows: a highlight has to survive the
// whole line, and the reset at the end of a coloured cell would cut it short.
func (m *model) tableStyle(vis engine.Result, cursor int) render.Style {
	return render.Style{
		Header: func(s string) string { return headerStyle.Render(s) },
		Cell: func(row int, e engine.Emphasis, s string) string {
			switch {
			case row == cursor:
				return cursorRow.Render(s)
			case row < len(vis.Rows) && m.cur().selection[vis.Rows[row].Key]:
				return selectedRow.Render(s)
			}
			switch e {
			case engine.Bad:
				return badStyle.Render(s)
			case engine.Warn:
				return warnStyle.Render(s)
			case engine.Good:
				return goodStyle.Render(s)
			default:
				return s
			}
		},
	}
}

// scrollable reports whether anything reaches past the right edge, so the
// help only offers the keys that would do something.
func (m *model) scrollable() bool {
	// A chart is drawn to the pane's width; there is never more to the right.
	if m.hasChart() {
		return false
	}
	return m.vp.XOffset() > 0 || m.widest > m.contentWidth()
}
