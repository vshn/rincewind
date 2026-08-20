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

	"github.com/vshn/aldediggi/internal/engine"
	"github.com/vshn/aldediggi/internal/render"
)

const (
	sidebarWidth = 30
	chromeHeight = 4 // title and help line, each with a blank line inside
	contentPad   = 2 // gap between the divider and the output
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
	sidebarStyle = lipgloss.NewStyle().
			Border(lipgloss.NormalBorder(), false, true, false, false).
			BorderForeground(lipgloss.Color("8")).
			Width(sidebarWidth).
			PaddingRight(1)
	contentStyle = lipgloss.NewStyle().PaddingLeft(contentPad)
)

// entry is one line in the sidebar. A nil view marks a group header, which
// the cursor skips over.
type entry struct {
	label string
	view  *engine.View
}

// resultMsg carries a finished run. The seq is compared against the model's
// current one so that a slow run cannot overwrite the output of a newer one.
type resultMsg struct {
	seq int
	res engine.Table
	err error
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

type model struct {
	entries []entry
	cursor  int

	focus pane
	// row is the data row the output-pane cursor is on.
	row int

	// selection holds the keys of the selected rows. Keys rather than
	// indices, so a reload that reorders the table keeps the selection on
	// the same objects.
	selection map[string]bool

	width, height int

	spin      spinner.Model
	tickAlive bool

	vp    viewport.Model
	ready bool

	seq     int
	running bool
	res     *engine.Table
	err     error

	// cancel stops the run in flight, on escape or on quit.
	cancel context.CancelFunc

	// filter narrows what is shown.
	filter    textinput.Model
	filtering bool
}

// Run starts the TUI and blocks until the user quits.
func Run(views []engine.View) error {
	m := newModel(views)

	prog := tea.NewProgram(m)
	_, err := prog.Run()

	// Whatever was still running has no one left to report to.
	m.stop()
	return err
}

func newModel(views []engine.View) *model {
	m := &model{
		spin:      spinner.New(spinner.WithSpinner(spinner.MiniDot)),
		vp:        viewport.New(),
		selection: map[string]bool{},
	}
	m.filter = textinput.New()
	m.filter.Prompt = "/"

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

// selected is the view whose output is on screen.
func (m *model) selected() *engine.View {
	if m.cursor < 0 || m.cursor >= len(m.entries) {
		return nil
	}
	return m.entries[m.cursor].view
}

// visible is the result after filtering: what the output pane renders.
func (m *model) visible() engine.Table {
	if m.res == nil {
		return engine.Table{}
	}
	return filtered(*m.res, m.filter.Value())
}

func (m *model) rowCount() int { return len(m.visible().Rows) }

func (m *model) Init() tea.Cmd { return nil }

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
		m.running = false

		// Escape is a decision, not a failure. A deadline is a failure, but
		// one worth naming plainly.
		switch {
		case errors.Is(msg.err, context.Canceled):
			m.err = nil
		case errors.Is(msg.err, context.DeadlineExceeded):
			m.err = fmt.Errorf("timed out after %s", runTimeout)
		default:
			m.err = msg.err
		}

		if m.err != nil {
			m.res = nil
		} else {
			res := msg.res
			m.res = &res
			m.prune()
		}
		m.showResult()
		return m, m.scheduleRefresh()

	case refreshMsg:
		if msg.seq != m.seq || m.running {
			return m, nil
		}
		// While the filter is being typed a reload would fight the keyboard;
		// skip this tick but keep the chain alive.
		if m.filtering {
			return m, m.scheduleRefresh()
		}
		return m, m.reload()

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
		case m.running:
			m.stop()
		case m.filter.Value() != "":
			m.filter.SetValue("")
			m.showResult()
		case len(m.selection) > 0:
			m.selection = map[string]bool{}
			m.showResult()
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

	case "r":
		return m, m.reload()

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
	}

	// Anything else scrolls the output pane.
	var cmd tea.Cmd
	m.vp, cmd = m.vp.Update(msg)
	return m, cmd
}

// toggleFocus moves the keyboard between the list and the output. Only worth
// doing when the output has rows to land on.
func (m *model) toggleFocus() {
	if m.focus == paneList {
		if m.rowCount() == 0 {
			return
		}
		m.focus, m.row = paneOutput, 0
	} else {
		m.focus = paneList
	}
	m.showResult()
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

// toggleRow flips the selection of the row under the cursor and moves on,
// so marking a handful of rows is a run of spaces rather than a dance.
func (m *model) toggleRow() {
	vis := m.visible()
	if m.focus != paneOutput || len(vis.Rows) == 0 {
		return
	}
	m.clampRow()

	key := vis.Rows[m.row].Key
	if m.selection[key] {
		delete(m.selection, key)
	} else {
		m.selection[key] = true
	}
	m.row = min(m.row+1, len(vis.Rows)-1)
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
		if !m.selection[r.Key] {
			all = false
			break
		}
	}
	for _, r := range vis.Rows {
		if all {
			delete(m.selection, r.Key)
		} else {
			m.selection[r.Key] = true
		}
	}
	m.showResult()
}

// prune drops selected keys the current result no longer contains: a
// selection must never silently stand for rows that are gone.
func (m *model) prune() {
	if len(m.selection) == 0 || m.res == nil {
		return
	}
	present := map[string]bool{}
	for _, r := range m.res.Rows {
		present[r.Key] = true
	}
	for key := range m.selection {
		if !present[key] {
			delete(m.selection, key)
		}
	}
}

func (m *model) clampRow() {
	if n := m.rowCount(); n > 0 {
		m.row = min(max(m.row, 0), n-1)
	} else {
		m.row = 0
	}
}

// open runs the view under the sidebar cursor from scratch: a new view means
// a new filter, a new selection, a new cursor.
func (m *model) open() tea.Cmd {
	m.filter.SetValue("")
	m.selection = map[string]bool{}
	m.focus, m.row = paneList, 0
	m.res, m.err = nil, nil
	return m.launch()
}

// reload re-runs the current view but keeps the context a reload must not
// eat: the filter, the selection, the focused row.
func (m *model) reload() tea.Cmd {
	if m.res == nil && m.err == nil && !m.running {
		// Nothing has run yet; a reload is an open.
		return m.open()
	}
	return m.launch()
}

// launch starts the current view's pipeline inside a tea.Cmd. Nothing blocks
// here: the work happens in the command, which is what keeps the UI painting
// while it runs.
func (m *model) launch() tea.Cmd {
	cur := m.selected()
	if cur == nil {
		return nil
	}

	m.stop()
	m.seq++
	m.running, m.err = true, nil

	ctx, cancel := context.WithTimeout(context.Background(), runTimeout)
	m.cancel = cancel

	seq := m.seq
	run := func() tea.Msg {
		defer cancel()
		res, err := cur.Run(ctx)
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

// scheduleRefresh arms the auto-reload of a view that asked for one.
func (m *model) scheduleRefresh() tea.Cmd {
	cur := m.selected()
	if cur == nil || cur.Refresh <= 0 {
		return nil
	}
	seq := m.seq
	return tea.Tick(cur.Refresh, func(time.Time) tea.Msg { return refreshMsg{seq: seq} })
}

// contentWidth is the space the output widgets may actually paint on: the
// window minus the sidebar, its one-column border and the left padding.
// Sizing widgets to anything wider makes rows wrap and pushes the help line
// off screen.
func (m *model) contentWidth() int {
	if w := m.width - sidebarWidth - 1 - contentPad; w > 0 {
		return w
	}
	return 1
}

func (m *model) bodyHeight() int {
	if h := m.height - chromeHeight - m.statusLines(); h > 0 {
		return h
	}
	return 1
}

// statusLines is the room the status bar takes, so the body shrinks rather
// than pushing the help line off screen.
func (m *model) statusLines() int {
	if m.statusLine() == "" {
		return 0
	}
	return 1
}

func (m *model) resize() {
	m.vp.SetWidth(m.contentWidth())
	m.vp.SetHeight(m.bodyHeight())
	m.showResult()
}

// filtered narrows a table to the rows that match.
func filtered(t engine.Table, needle string) engine.Table {
	needle = strings.ToLower(strings.TrimSpace(needle))
	if needle == "" {
		return t
	}

	out := engine.Table{Cols: t.Cols, Notes: t.Notes}
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

// showResult feeds the current result into the viewport.
func (m *model) showResult() {
	vis := m.visible()

	cursor := -1
	if m.focus == paneOutput && len(vis.Rows) > 0 {
		m.clampRow()
		cursor = m.row
	}

	lines := render.Table(vis, m.tableStyle(vis, cursor))

	// Cell evaluation problems ride below the table instead of replacing
	// it: a half-broken view is still mostly a view.
	for _, note := range vis.Notes {
		lines = append(lines, noteStyle.Render("! "+note))
	}

	m.vp.SetContentLines(lines)
}

// tableStyle paints emphasis, the row cursor and the selection. Emphasis
// colours are dropped on highlighted rows: a highlight has to survive the
// whole line, and the reset at the end of a coloured cell would cut it short.
func (m *model) tableStyle(vis engine.Table, cursor int) render.Style {
	return render.Style{
		Header: func(s string) string { return headerStyle.Render(s) },
		Cell: func(row int, e engine.Emphasis, s string) string {
			switch {
			case row == cursor:
				return cursorRow.Render(s)
			case row < len(vis.Rows) && m.selection[vis.Rows[row].Key]:
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

	if !m.ready {
		v.Content = "loading…"
		return v
	}

	pane := lipgloss.NewStyle().
		Height(m.bodyHeight()).
		MaxHeight(m.bodyHeight()).
		Render(m.pane())
	if status := m.statusLine(); status != "" {
		pane = lipgloss.JoinVertical(lipgloss.Left, pane, status)
	}

	body := lipgloss.JoinHorizontal(
		lipgloss.Top,
		m.sidebarBorder().Height(m.bodyHeight()+m.statusLines()).Render(m.sidebar()),
		contentStyle.Render(pane),
	)

	v.Content = lipgloss.JoinVertical(lipgloss.Left, m.title(), "", body, "", m.help())
	return v
}

// title reads tool · id · title. The view title is the first thing dropped
// when space runs short: the id is what you navigate by.
func (m *model) title() string {
	sep := sepStyle.Render("  ·  ")

	name := titleStyle.Render("aldediggi")
	cur := m.selected()
	if cur == nil {
		return name
	}

	line := name + sep + titleStyle.Render(cur.ID)

	// Everything below needs a known width; before the first WindowSizeMsg
	// there is none to budget against.
	if m.width <= 0 {
		return line
	}

	if cur.Title != cur.ID {
		room := m.width - lipgloss.Width(line)
		if room > lipgloss.Width(sep)+lipgloss.Width(cur.Title) {
			line += sep + descStyle.Render(cur.Title)
		}
	}
	return lipgloss.NewStyle().MaxWidth(m.width).Render(line)
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
			b.WriteString(cursorStyle.Render("▸ "+e.label) + "\n")
		case i == m.cursor:
			b.WriteString(dividerStyle.Render("▸ "+e.label) + "\n")
		default:
			b.WriteString("  " + e.label + "\n")
		}
	}
	return b.String()
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
	if m.rowCount() == 0 {
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
	if m.res != nil && (m.filtering || m.filter.Value() != "") {
		parts = append(parts, dividerStyle.Render(fmt.Sprintf("%d of %d", m.rowCount(), len(m.res.Rows))))
	}
	if n := len(m.selection); n > 0 {
		parts = append(parts, warnStyle.Render(fmt.Sprintf("%d selected", n)))
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

func (m *model) help() string {
	switch {
	case m.filtering:
		return helpStyle.Render("⏎ keep filter  ·  esc clear")
	case m.running:
		return helpStyle.Render("esc cancel  ·  q quit")
	}

	keys := []string{"↑↓ move", "⇥ focus", "/ filter", "r reload", "q quit"}
	if m.focus == paneList {
		keys = append([]string{"⏎ run"}, keys...)
	} else {
		keys = append([]string{"␣ select", "a all"}, keys...)
	}
	if m.scrollable() {
		keys = append(keys, "<> scroll")
	}
	return helpStyle.Render(strings.Join(keys, "  ·  "))
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
