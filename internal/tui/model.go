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

var (
	titleStyle   = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("6"))
	groupStyle   = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("8"))
	cursorStyle  = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("6"))
	descStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("7"))
	dividerStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	headerStyle  = lipgloss.NewStyle().Bold(true)
	cursorRow    = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("6"))
	selectedRow  = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("13"))
	badStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("9"))
	warnStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("11"))
	goodStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("10"))
	sepStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	helpStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	errStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("9"))
	noteStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("11"))
	borderStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	focusBorder  = lipgloss.NewStyle().Foreground(lipgloss.Color("6"))
	tabStyle     = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("6"))
	clusterStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("5"))
	keyStyle     = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("6"))
	promptStyle  = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("11"))
)

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
	// before the first run. act indexes the one on screen.
	tabs []*tab
	act  int

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
func (m *model) cur() *tab { return m.tabs[m.act] }

// Run starts the TUI and blocks until the user quits.
func Run(views []engine.View, version string) error {
	m := newModel(views)
	m.version = version
	m.lookupCluster = kubeContext
	m.clipboard = localClipboard()

	prog := tea.NewProgram(m)
	_, err := prog.Run()

	// Whatever was still running has no one left to report to.
	m.stopAll()
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
	if vis.Text() {
		return len(vis.Lines)
	}
	return len(vis.Rows)
}

// total is how much there was before the filter.
func (m *model) total() int {
	t := m.cur()
	switch {
	case t.res == nil:
		return 0
	case t.res.Text():
		return len(t.res.Lines)
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

		if t.err != nil {
			t.res = nil
		} else {
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

// onKey handles a key and re-probes the cluster when the view on screen has
// changed. Hooking it here rather than in open, drill, activate and closeTab
// keeps the four from drifting apart, and covers whatever is added next.
func (m *model) onKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	before := m.cur().view
	next, cmd := m.key(msg)
	if m.cur().view != before {
		if probe := m.refreshCluster(); probe != nil {
			return next, tea.Batch(cmd, probe)
		}
	}
	return next, cmd
}

func (m *model) key(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	key := msg.String()

	// A notice was written for the keystroke before this one.
	m.notice = ""

	// A job in the middle of its form or its question owns the keyboard.
	// Everything typed is part of the answer — including keys that are
	// otherwise bindings, so a stray j cannot agree to change things.
	if j := m.cur().job; j != nil {
		switch {
		case j.asking():
			switch key {
			case "enter":
				return m, m.accept(m.cur())
			case "esc":
				// Nothing has been planned and nothing has run.
				m.input.Blur()
				return m, m.closeTab()
			}
			var cmd tea.Cmd
			m.input, cmd = m.input.Update(msg)
			m.showResult()
			return m, cmd

		case j.confirming():
			return m, m.answer(key)
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
		m.clampRow()
		m.showResult()
		return m, cmd
	}

	// Config keys come first so that a view can bind enter — which in the
	// output pane means nothing else — as its one obvious action.
	if a := m.actionFor(key); a != nil {
		return m, m.fire(a)
	}

	switch key {
	case "q", "ctrl+c":
		m.stopAll()
		return m, tea.Quit

	case "/":
		if m.cur().res == nil {
			return m, nil
		}
		m.filtering = true
		return m, m.filter.Focus()

	case "tab":
		m.toggleFocus()
		return m, nil

	case "<", ">":
		// Columns are never cut, so anything past the right edge is reached
		// by scrolling rather than lost.
		step := m.contentWidth() / 3
		if key == "<" {
			m.vp.ScrollLeft(step)
		} else {
			m.vp.ScrollRight(step)
		}
		return m, nil

	case "esc":
		switch {
		case m.cur().stream != nil:
			m.stopFollow(m.cur())
			m.showResult()
		case m.cur().running:
			m.cur().stop()
		case m.filter.Value() != "":
			m.filter.SetValue("")
			m.showResult()
		case len(m.cur().selection) > 0:
			m.cur().selection = map[string]bool{}
			m.showResult()
		case m.act > 0:
			// A drill-down is closed by the key one already reaches for.
			return m, m.closeTab()
		case m.focus == paneOutput:
			m.focus = paneList
			m.showResult()
		}
		return m, nil

	case "up", "k":
		m.move(-1)
		return m, nil

	case "down", "j":
		m.move(+1)
		return m, nil

	case "pgdown", "ctrl+f":
		m.page(+1)
		return m, nil

	case "pgup", "ctrl+b":
		m.page(-1)
		return m, nil

	case "home", "g":
		m.jump(-1)
		return m, nil

	case "end", "G":
		m.jump(+1)
		return m, nil

	case "r":
		return m, m.reload()

	case "Y":
		return m, m.copyPane()

	case "enter":
		if m.focus == paneList {
			return m, m.open()
		}
		return m, nil

	case "space", " ":
		m.toggleRow()
		return m, nil

	case "a":
		m.toggleAll()
		return m, nil

	case "M":
		// Releasing the mouse hands text selection back to the terminal,
		// which is how one copies a node name out of a row. Upper case: the
		// lower-case letters belong to the views, which have far more use
		// for them than one global toggle does.
		m.mouse = !m.mouse
		return m, nil

	case "]":
		return m, m.activate(m.act + 1)

	case "[", "shift+tab":
		return m, m.activate(m.act - 1)

	case "x":
		return m, m.closeTab()
	}

	// Anything else scrolls the output pane.
	var cmd tea.Cmd
	m.vp, cmd = m.vp.Update(msg)
	return m, cmd
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

// scrollSidebar moves the menu window without moving the cursor: scrolling
// is looking around, not choosing.
func (m *model) scrollSidebar(step int) {
	lines, _ := m.sidebarRender()
	h := max(m.sidebarBoxHeight()-2, 1)
	if len(lines) <= h {
		return
	}
	m.sideTop = min(max(m.sideTop+step, 0), len(lines)-h)
}

// rowAt maps a screen line to a data row, or -1 where no row sits. The
// geometry is derived from the same numbers View draws with, so the two
// cannot drift apart.
func (m *model) rowAt(y int) int {
	if !m.hasTable() {
		return -1
	}
	// The first line inside the body box holds the sticky header; the rows
	// begin under it.
	top := titleHeight + 1 + 1
	if y < top || y >= top+m.vp.Height() {
		return -1
	}
	if i := y - top + m.vp.YOffset(); i < m.rowCount() {
		return i
	}
	return -1
}

// entryAt maps a screen line to a menu entry, or -1 for group headers, gaps
// and everything outside the box.
func (m *model) entryAt(y int) int {
	lines, owner := m.sidebarRender()
	h := max(m.sidebarBoxHeight()-2, 1)

	top := titleHeight + 1
	if y < top || y >= top+h {
		return -1
	}
	if i := y - top + sidebarOffset(len(lines), h, m.sideTop); i < len(owner) {
		return owner[i]
	}
	return -1
}

// toggleFocus moves the keyboard between the list and the output. Only worth
// doing when the output has something to land on — rows, or a log's lines.
func (m *model) toggleFocus() {
	if m.focus == paneList {
		if m.shownCount() == 0 {
			return
		}
		m.focus, m.cur().row = paneOutput, 0
	} else {
		m.focus = paneList
	}
	m.showResult()
}

// move steps the cursor of whichever pane has focus.
func (m *model) move(step int) {
	if m.focus == paneList {
		m.cursor = m.around(m.cursor, step)
		m.syncSidebar()
		return
	}

	// A text view has no row to move: the keys scroll it, which is what one
	// wants from a log anyway.
	if m.hasText() {
		if step < 0 {
			m.vp.ScrollUp(-step)
		} else {
			m.vp.ScrollDown(step)
		}
		return
	}

	if n := m.rowCount(); n > 0 {
		m.cur().row = min(max(m.cur().row+step, 0), n-1)
		m.showResult()
	}
}

// page moves a screenful in either direction. The cursor travels with it:
// scrolling the view out from under the cursor would leave the next space
// or action pointing at a row nobody is looking at.
func (m *model) page(dir int) {
	h := max(m.vp.Height(), 1)
	if m.focus == paneList {
		for range h {
			m.cursor = m.next(m.cursor, dir)
		}
		m.syncSidebar()
		return
	}
	m.move(dir * h)
}

// jump goes to the first or last entry.
func (m *model) jump(dir int) {
	if m.focus == paneList {
		if dir < 0 {
			m.cursor = m.next(-1, +1)
		} else {
			m.cursor = m.next(len(m.entries), -1)
		}
		m.syncSidebar()
		return
	}

	if m.hasText() {
		if dir < 0 {
			m.vp.GotoTop()
		} else {
			m.vp.GotoBottom()
		}
		return
	}

	if n := m.rowCount(); n > 0 {
		if dir < 0 {
			m.cur().row = 0
		} else {
			m.cur().row = n - 1
		}
		m.showResult()
	}
}

// followCursor scrolls the viewport just enough to keep the row cursor in sight,
// stopping at the edge rather than recentring: a table that jumps under the
// eye is harder to read than one that creeps.
func (m *model) followCursor(cursor int) {
	if cursor < 0 {
		return
	}
	h := max(m.vp.Height(), 1)
	switch top := m.vp.YOffset(); {
	case cursor < top:
		m.vp.SetYOffset(cursor)
	case cursor >= top+h:
		m.vp.SetYOffset(cursor - h + 1)
	}
}

// syncSidebar slides the menu window to keep the cursor on screen. Kept out
// of the render so that drawing the sidebar stays free of side effects.
func (m *model) syncSidebar() {
	lines, owner := m.sidebarRender()
	h := max(m.sidebarBoxHeight()-2, 1)

	cursor := -1
	for line, entry := range owner {
		if entry == m.cursor {
			cursor = line
			break
		}
	}

	if len(lines) <= h || cursor < 0 {
		m.sideTop = 0
		return
	}
	switch {
	case cursor < m.sideTop:
		m.sideTop = cursor
	case cursor >= m.sideTop+h:
		m.sideTop = cursor - h + 1
	}
	m.sideTop = min(max(m.sideTop, 0), len(lines)-h)
}

// toggleRow flips the selection of the row under the cursor and moves on,
// so marking a handful of rows is a run of spaces rather than a dance.
func (m *model) toggleRow() {
	vis := m.visible()
	if m.focus != paneOutput || len(vis.Rows) == 0 {
		return
	}
	m.clampRow()

	key := vis.Rows[m.cur().row].Key
	if m.cur().selection[key] {
		delete(m.cur().selection, key)
	} else {
		m.cur().selection[key] = true
	}
	m.cur().row = min(m.cur().row+1, len(vis.Rows)-1)
	m.showResult()
}

// toggleAll selects every visible row, or clears them when they already all
// are. Visible means filtered: what you see is what you mark.
func (m *model) toggleAll() {
	vis := m.visible()
	if len(vis.Rows) == 0 {
		return
	}

	all := true
	for _, r := range vis.Rows {
		if !m.cur().selection[r.Key] {
			all = false
			break
		}
	}
	for _, r := range vis.Rows {
		if all {
			delete(m.cur().selection, r.Key)
		} else {
			m.cur().selection[r.Key] = true
		}
	}
	m.showResult()
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

func (m *model) clampRow() {
	if n := m.rowCount(); n > 0 {
		m.cur().row = min(max(m.cur().row, 0), n-1)
	} else {
		m.cur().row = 0
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
	m.tabs[0], m.act = newTab(m.selected(), nil), 0
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

	t.stop()
	m.seq++
	t.seq, t.running, t.err = m.seq, true, nil

	ctx, cancel := context.WithTimeout(context.Background(), runTimeout)
	t.cancel = cancel

	seq, view, args := t.seq, t.view, t.args
	run := func() tea.Msg {
		defer cancel()
		res, err := view.Run(ctx, args)
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
	m.act = min(m.act, len(m.tabs)-1)
}

// activate switches the content box to tab i, parking the scroll position
// and the filter of the one being left: coming back has to land exactly
// where one left, which is the whole reason tabs replaced the view stack.
func (m *model) activate(i int) tea.Cmd {
	if i < 0 || i >= len(m.tabs) || i == m.act {
		return nil
	}
	m.park()
	m.pauseStream(m.cur())

	m.act = i
	t := m.cur()
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
	if m.act == 0 {
		return nil
	}
	to := m.act - 1
	m.closeFrom(m.act)
	m.act = to

	t := m.cur()
	m.filter.SetValue(t.filter)
	m.vp.SetYOffset(t.yOff)
	m.vp.SetXOffset(t.xOff)
	m.showResult()
	return m.resume(t)
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

// contentWidth is the space the output widgets may actually paint on: the
// window minus the sidebar column and the content box's own frame. Sizing
// widgets to anything wider makes rows wrap inside the box.
func (m *model) contentWidth() int {
	if w := m.width - sidebarWidth - boxPad; w > 0 {
		return w
	}
	return 1
}

// bodyBoxHeight is the outer height of the content box: whatever the title,
// the footer and the action box leave.
func (m *model) bodyBoxHeight() int {
	return max(m.height-frameChrome-m.actionBoxHeight(), 3)
}

// sidebarBoxHeight is the outer height of the menu, which runs the whole
// left side: the content box and the action box under it are one column,
// the menu beside them is one box.
func (m *model) sidebarBoxHeight() int {
	return m.bodyBoxHeight() + m.actionBoxHeight()
}

// actionBoxHeight is the room the action box takes, or none when the view
// has nothing to offer — an empty frame would cost three lines to say so.
func (m *model) actionBoxHeight() int {
	if len(m.viewActions()) == 0 {
		return 0
	}
	return titleHeight
}

// viewActions is what the view on screen offers. A job tab offers nothing:
// it is a record of what was done, not something to act on.
func (m *model) viewActions() []engine.Action {
	if t := m.cur(); t.view != nil && t.job == nil {
		return t.view.Actions
	}
	return nil
}

// vpHeight is the viewport's share of the content box: the inner height,
// minus the sticky header and the status line when those are showing.
func (m *model) vpHeight() int {
	h := m.bodyBoxHeight() - 2 - m.statusLines()
	if m.hasTable() {
		h--
	}
	return max(h, 1)
}

// hasTable reports whether the content box is showing rows, as opposed to a
// job, a text view, a spinner, an error or a note that there is nothing to
// see. Only rows get a sticky header and a row cursor.
func (m *model) hasTable() bool {
	t := m.cur()
	return t.job == nil && t.err == nil && !t.running &&
		t.res != nil && !t.res.Text() && m.rowCount() > 0
}

// hasText reports whether the content box is showing a text view's lines.
func (m *model) hasText() bool {
	t := m.cur()
	// A followed view has lines while it is still running, which is the
	// whole point; only an error hides them.
	return t.job == nil && t.err == nil && t.res != nil && t.res.Text() &&
		(!t.running || t.stream != nil)
}

// statusLines is the room the status bar takes, so the viewport shrinks
// rather than pushing rows out of the frame.
func (m *model) statusLines() int {
	if m.statusLine() == "" {
		return 0
	}
	return 1
}

func (m *model) resize() {
	m.showResult()
	m.syncSidebar()
}

// filtered narrows a table to the rows that match.
func filtered(t engine.Result, needle string) engine.Result {
	needle = strings.ToLower(strings.TrimSpace(needle))
	if needle == "" {
		return t
	}

	// Filtering a log is grep, and is most of what one does with one.
	if t.Text() {
		out := engine.Result{Lines: []string{}, Notes: t.Notes}
		for _, line := range t.Lines {
			if strings.Contains(strings.ToLower(line), needle) {
				out.Lines = append(out.Lines, line)
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
		m.head = ""
		m.vp.SetContentLines(lines)
		m.lines = len(lines)
		return
	}

	vis := m.visible()

	// A text view's lines go in as they came out of the script: no header,
	// no styling, no truncation. Whitespace is part of what is being read.
	if vis.Text() {
		lines := vis.Lines
		// That a followed script stopped is itself a finding — the pod went
		// away — so it is said under the last line rather than swallowed.
		if t := m.cur(); t.ended {
			lines = append(append([]string{}, lines...), m.endMarker(t))
		}
		m.head = ""
		m.vp.SetContentLines(lines)
		m.lines = len(lines)
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
	m.head, lines = lines[0], lines[1:]

	// Cell evaluation problems ride below the table instead of replacing
	// it: a half-broken view is still mostly a view.
	for _, note := range vis.Notes {
		lines = append(lines, noteStyle.Render("! "+note))
	}

	m.vp.SetContentLines(lines)
	m.lines = len(lines)
	m.followCursor(cursor)
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

func (m *model) View() tea.View {
	v := tea.NewView("")
	v.AltScreen = true

	// The mouse mode is a property of the frame, so the toggle is nothing
	// but this field following the model.
	v.MouseMode = tea.MouseModeNone
	if m.mouse {
		v.MouseMode = tea.MouseModeCellMotion
	}

	if !m.ready {
		v.Content = "loading…"
		return v
	}

	// The keyboard's side of the screen carries the bright border.
	sideBorder, outBorder := borderStyle, borderStyle
	if m.focus == paneList {
		sideBorder = focusBorder
	} else {
		outBorder = focusBorder
	}

	title := box{w: m.width, h: titleHeight, border: borderStyle}.
		render([]string{m.titleLine()})

	// The right side is a column: the output, and under it what can be done
	// with it. The menu beside them is one box spanning both.
	tabs, _ := m.tabBar(m.tabBarWidth())
	right := box{w: m.width - sidebarWidth, h: m.bodyBoxHeight(), border: outBorder,
		top: tabs, topRight: m.scrollLabel()}.render(m.contentLines())

	if h := m.actionBoxHeight(); h > 0 {
		right = lipgloss.JoinVertical(lipgloss.Left, right,
			box{w: m.width - sidebarWidth, h: h, border: borderStyle}.render([]string{m.actionLine()}))
	}

	body := lipgloss.JoinHorizontal(lipgloss.Top,
		box{w: sidebarWidth, h: m.sidebarBoxHeight(), border: sideBorder,
			topRight: m.sidebarMore()}.render(m.sidebarLines()),
		right,
	)

	footer := lipgloss.JoinHorizontal(lipgloss.Top,
		box{w: sidebarWidth, h: titleHeight, border: borderStyle}.
			render([]string{descStyle.Render(m.version)}),
		box{w: m.width - sidebarWidth, h: titleHeight, border: borderStyle}.
			render([]string{m.help()}),
	)

	v.Content = lipgloss.JoinVertical(lipgloss.Left, title, body, footer)
	return v
}

// titleLine reads tool · id args · title. It describes the tab on screen,
// not the entry the sidebar cursor happens to rest on. The title is the
// first thing dropped when space runs short: the id is what you navigate by,
// and the arguments are what tell two tabs of the same view apart.
func (m *model) titleLine() string {
	sep := sepStyle.Render("  ·  ")

	name := titleStyle.Render("rincewind")
	if j := m.cur().job; j != nil {
		if j.asking() {
			return m.withCluster(name + sep + titleStyle.Render(j.action.Name) + sep +
				descStyle.Render(j.action.Short))
		}
		return m.withCluster(name + sep + titleStyle.Render(j.action.Name) + sep +
			descStyle.Render(plural(len(j.steps), "step")))
	}
	cur := m.cur().view
	if cur == nil {
		return m.withCluster(name)
	}

	id := cur.ID
	if arg := m.argLabel(); arg != "" {
		id += " " + arg
	}
	line := name + sep + titleStyle.Render(id)

	// Everything below needs a known width; before the first WindowSizeMsg
	// there is none to budget against.
	if m.width <= 0 {
		return line
	}

	// The title yields to the cluster, not the other way round: what the
	// view is called matters less than which cluster it is showing.
	if cur.Title != cur.ID {
		room := m.width - boxPad - lipgloss.Width(line) - m.clusterRoom()
		if room > lipgloss.Width(sep)+lipgloss.Width(cur.Title) {
			line += sep + descStyle.Render(cur.Title)
		}
	}
	return m.withCluster(line)
}

// argLabel names the parameters the tab was opened with, so a drilled-into
// view says which pool or node it is about.
func (m *model) argLabel() string {
	t := m.cur()
	if t.view == nil {
		return ""
	}
	var vals []string
	for _, p := range t.view.Params {
		if v := t.args[p.Name]; v != "" {
			vals = append(vals, v)
		}
	}
	return strings.Join(vals, " ")
}

// scrollLabel says where in the table the view sits. It rides in the top
// border because a position indicator that costs a row of the content it
// describes is a poor trade.
func (m *model) scrollLabel() string {
	h := m.vp.Height()
	if !m.hasTable() || m.lines <= h {
		return ""
	}
	top := m.vp.YOffset()
	return dividerStyle.Render(fmt.Sprintf(" %d–%d/%d ", top+1, min(top+h, m.lines), m.lines))
}

// sidebarMore marks a menu taller than its box, so a view below the fold is
// not simply invisible.
func (m *model) sidebarMore() string {
	lines, _ := m.sidebarRender()
	if len(lines) <= max(m.sidebarBoxHeight()-2, 1) {
		return ""
	}
	return dividerStyle.Render(" ↕ ")
}

// contentLines is what the content frame holds: the sticky header, the
// scrolling pane, and the status line pinned to the frame's bottom edge
// when there is one.
func (m *model) contentLines() []string {
	var lines []string
	switch {
	case m.cur().job != nil:
		// A job shows its commands from the moment it opens, running or not.
		lines = strings.Split(m.vp.View(), "\n")
	case m.hasTable():
		lines = append(lines, m.headLine())
		lines = append(lines, strings.Split(m.vp.View(), "\n")...)
	case m.hasText() && m.shownCount() > 0:
		// No sticky header: there are no column names to keep.
		lines = strings.Split(m.vp.View(), "\n")
	default:
		lines = strings.Split(m.pane(), "\n")
	}

	if status := m.statusLine(); status != "" {
		inner := m.bodyBoxHeight() - 2
		for len(lines) < inner-1 {
			lines = append(lines, "")
		}
		lines = append(lines[:inner-1], status)
	}
	return lines
}

// headLine paints the table header, shifted along with whatever horizontal
// scrolling the viewport has done so the names stay over their columns.
func (m *model) headLine() string {
	head := m.head
	if x := m.vp.XOffset(); x > 0 {
		head = ansi.TruncateLeft(head, x, "")
	}
	return lipgloss.NewStyle().MaxWidth(m.contentWidth()).Render(head)
}

// sidebarRender draws the menu in full and reports, per line, which entry it
// belongs to (-1 for group headers and gaps). That mapping is what both the
// scroll window and the mouse need.
func (m *model) sidebarRender() (lines []string, owner []int) {
	add := func(line string, entry int) {
		lines = append(lines, line)
		owner = append(owner, entry)
	}

	for i, e := range m.entries {
		switch {
		case e.view == nil:
			if i > 0 {
				add("", -1)
			}
			add(groupStyle.Render(e.label), -1)
		case i == m.cursor && m.focus == paneList:
			add(cursorStyle.Render("▸ "+e.label), i)
		case i == m.cursor:
			add(dividerStyle.Render("▸ "+e.label), i)
		default:
			add("  "+e.label, i)
		}
	}
	return lines, owner
}

// sidebarOffset is the first menu line on screen, clamped to what the box
// can actually show.
func sidebarOffset(total, h, want int) int {
	if total <= h {
		return 0
	}
	return min(max(want, 0), total-h)
}

// sidebarLines is the slice of the menu that fits the box.
func (m *model) sidebarLines() []string {
	lines, _ := m.sidebarRender()
	h := max(m.sidebarBoxHeight()-2, 1)
	if len(lines) <= h {
		return lines
	}
	top := sidebarOffset(len(lines), h, m.sideTop)
	return lines[top : top+h]
}

func (m *model) pane() string {
	switch {
	case m.cur().err != nil:
		return errStyle.Render("error: " + m.cur().err.Error())

	case m.cur().running && m.cur().stream != nil && m.shownCount() == 0:
		return fmt.Sprintf("%s following…", m.spin.View())

	case m.cur().running && m.cur().stream == nil:
		return fmt.Sprintf("%s running…", m.spin.View())

	case m.cur().res == nil:
		return helpStyle.Render("press ⏎ to run")
	}

	// A table of nothing but headers, or a log with no lines, reads like a
	// failure. Say which it is.
	if m.shownCount() == 0 {
		if m.filter.Value() != "" {
			return helpStyle.Render("nothing matches " + m.filter.Value())
		}
		return helpStyle.Render("nothing to show")
	}

	return m.vp.View()
}

// statusLine shows the filter and the selection: the two pieces of state
// that change what a later action would operate on.
func (m *model) statusLine() string {
	var parts []string

	if m.filtering {
		parts = append(parts, m.filter.View())
	} else if m.filter.Value() != "" {
		parts = append(parts, dividerStyle.Render("/"+m.filter.Value()))
	}
	if m.cur().res != nil && (m.filtering || m.filter.Value() != "") {
		parts = append(parts, dividerStyle.Render(fmt.Sprintf("%d of %d", m.shownCount(), m.total())))
	}
	if n := len(m.cur().selection); n > 0 {
		parts = append(parts, warnStyle.Render(fmt.Sprintf("%d selected", n)))
	}
	if m.notice != "" {
		parts = append(parts, noteStyle.Render(m.notice))
	}

	if len(parts) == 0 {
		return ""
	}
	return lipgloss.NewStyle().MaxWidth(m.contentWidth()).Render(strings.Join(parts, "   "))
}

// scrollable reports whether anything reaches past the right edge, so the
// help only offers the keys that would do something.
func (m *model) scrollable() bool {
	if m.vp.XOffset() > 0 {
		return true
	}
	for _, line := range render.Table(m.visible(), render.Style{}) {
		if lipgloss.Width(line) > m.contentWidth() {
			return true
		}
	}
	return false
}

// helpKey is one entry of the key legend. Lower weights are dropped first
// when the footer box runs out of room.
type helpKey struct {
	label  string
	weight int
}

func (m *model) help() string {
	var keys []helpKey
	switch {
	case m.jobAsking():
		keys = []helpKey{{"⏎ next field", 4}, {"esc cancel", 3}}
	case m.jobConfirming():
		keys = []helpKey{{"y confirm", 4}, {"any other key cancels", 1}}
	case m.filtering:
		keys = []helpKey{{"⏎ keep filter", 1}, {"esc clear", 2}}
	// A followed view is read while it runs, so it keeps the full legend.
	case m.cur().running && m.cur().stream == nil:
		keys = []helpKey{{"esc cancel", 2}, {"q quit", 1}}
	default:
		if m.focus == paneList {
			keys = []helpKey{{"⏎ run", 3}}
		} else {
			keys = []helpKey{{"␣ select", 3}, {"a all", 1}}
		}
		keys = append(keys,
			helpKey{"↑↓ move", 1},
			helpKey{"⇥ focus", 2},
			helpKey{"/ filter", 2},
			helpKey{"q quit", 4},
		)
		// While a stream runs, esc stops it rather than going back, and
		// reloading it is what stopping and starting again would be. The
		// legend must not promise the other meanings.
		if m.cur().stream != nil {
			keys = append(keys, helpKey{"esc stop", 3})
		} else {
			keys = append(keys, helpKey{"r reload", 1})
			if len(m.tabs) > 1 {
				keys = append(keys, helpKey{"esc back", 4})
			}
		}
		if len(m.tabs) > 1 {
			keys = append(keys, helpKey{"][ tabs", 2})
		}
		if m.lines > m.vp.Height() {
			keys = append(keys, helpKey{"⇞⇟ page", 2})
		}
		if m.scrollable() {
			keys = append(keys, helpKey{"<> scroll", 1})
		}
		keys = append(keys, helpKey{"Y copy", 1})
		// Released is the surprising state, and the one you need the way
		// back from, so it holds on to its place in a narrow legend.
		if m.mouse {
			keys = append(keys, helpKey{"M release mouse", 0})
		} else {
			keys = append(keys, helpKey{"M grab mouse", 3})
		}
	}
	return helpStyle.Render(fitKeys(keys, m.contentWidth()))
}

// fitKeys joins the legend, shedding the lightest keys until it fits: a
// clipped "q qui" helps no one, a shorter legend still does.
func fitKeys(keys []helpKey, avail int) string {
	for {
		parts := make([]string, len(keys))
		for i, k := range keys {
			parts[i] = k.label
		}
		line := strings.Join(parts, "  ·  ")
		if lipgloss.Width(line) <= avail || len(keys) <= 1 {
			return line
		}
		drop := 0
		for i, k := range keys {
			if k.weight < keys[drop].weight {
				drop = i
			}
		}
		keys = append(keys[:drop], keys[drop+1:]...)
	}
}

// visibleOnly strips styling so tests can assert on what the user reads.
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
