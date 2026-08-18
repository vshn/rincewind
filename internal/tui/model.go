// Package tui renders the registry as an interactive two-pane browser:
// views on the left, the output of the selected view on the right.
package tui

import (
	"context"
	"fmt"
	"strings"

	"charm.land/bubbles/v2/spinner"
	"charm.land/bubbles/v2/table"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/vshn/aldediggi/internal/catalog"
)

const (
	sidebarWidth = 30
	chromeHeight = 4 // title and help line, each with a blank line inside
	contentPad   = 2 // gap between the divider and the output
	minColWidth  = 3 // a shrunken column still has to show something
)

var (
	titleStyle    = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("6"))
	groupStyle    = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("8"))
	selectedStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("6"))
	descStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("7"))
	sepStyle      = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	helpStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	errStyle      = lipgloss.NewStyle().Foreground(lipgloss.Color("9"))
	sidebarStyle  = lipgloss.NewStyle().
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

type model struct {
	entries []entry
	cursor  int

	width, height int

	spin      spinner.Model
	tickAlive bool

	vp    viewport.Model
	tbl   table.Model
	ready bool

	seq     int
	running bool
	res     catalog.Result
	err     error
}

// Run starts the TUI and blocks until the user quits.
func Run(reg *catalog.Registry) error {
	m := newModel(reg)
	_, err := tea.NewProgram(m).Run()
	return err
}

func newModel(reg *catalog.Registry) *model {
	m := &model{
		spin: spinner.New(spinner.WithSpinner(spinner.MiniDot)),
		vp:   viewport.New(),
		tbl:  table.New(table.WithFocused(true)),
	}

	for _, group := range reg.Groups() {
		m.entries = append(m.entries, entry{label: group})
		for _, v := range reg.InGroup(group) {
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

func (m *model) selected() *catalog.View {
	if m.cursor < 0 || m.cursor >= len(m.entries) {
		return nil
	}
	return m.entries[m.cursor].view
}

func (m *model) Init() tea.Cmd { return nil }

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
		m.running, m.res, m.err = false, msg.res, msg.err
		m.showResult()
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
	switch msg.String() {
	case "q", "ctrl+c":
		return m, tea.Quit

	case "up", "k":
		m.cursor = m.next(m.cursor, -1)
		return m, nil

	case "down", "j":
		m.cursor = m.next(m.cursor, +1)
		return m, nil

	case "enter", "r":
		return m, m.start()
	}

	// Anything else scrolls the output pane.
	return m, m.forwardToPane(msg)
}

// start launches the selected view. Nothing blocks here: the work happens
// inside a tea.Cmd, which is what keeps the UI responsive while it runs.
func (m *model) start() tea.Cmd {
	cur := m.selected()
	if cur == nil {
		return nil
	}

	m.seq++
	m.running, m.res, m.err = true, nil, nil

	seq := m.seq
	run := func() tea.Msg {
		res, err := cur.Run(context.Background())
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

func (m *model) forwardToPane(msg tea.Msg) tea.Cmd {
	var cmd tea.Cmd
	switch m.res.(type) {
	case catalog.Table:
		m.tbl, cmd = m.tbl.Update(msg)
	case catalog.Text:
		m.vp, cmd = m.vp.Update(msg)
	}
	return cmd
}

func (m *model) bodyHeight() int {
	if h := m.height - chromeHeight; h > 0 {
		return h
	}
	return 1
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
	m.tbl.SetWidth(m.contentWidth())
	m.tbl.SetHeight(m.bodyHeight())
	m.showResult()
}

// showResult feeds the current Result into whichever widget can display it.
// The same Result also goes through render.CLI in non-interactive mode.
func (m *model) showResult() {
	switch r := m.res.(type) {
	case catalog.Text:
		m.vp.SetContent(strings.Join(r.Lines, "\n"))
	case catalog.Table:
		m.tbl.SetColumns(columns(r, m.contentWidth()))
		m.tbl.SetRows(rows(r))
	}
}

// columns sizes each column to its widest cell, then shrinks the last one if
// the total would overflow the pane.
func columns(t catalog.Table, avail int) []table.Column {
	widths := make([]int, len(t.Cols))
	for i, c := range t.Cols {
		widths[i] = lipgloss.Width(c)
	}
	for _, row := range t.Rows {
		for i, cell := range row {
			if i < len(widths) && lipgloss.Width(cell) > widths[i] {
				widths[i] = lipgloss.Width(cell)
			}
		}
	}

	// The table widget pads every cell by one column on each side.
	const gutter = 2
	total := 0
	for _, w := range widths {
		total += w + gutter
	}

	// Take the overflow off the last column, which is the free-text one.
	// Clamping rather than giving up keeps rows on a single line; an
	// unshrinkable table is truncated by the widget instead of wrapping.
	if last := len(widths) - 1; last >= 0 && total > avail {
		widths[last] = max(widths[last]-(total-avail), minColWidth)
	}

	cols := make([]table.Column, len(t.Cols))
	for i, c := range t.Cols {
		cols[i] = table.Column{Title: c, Width: widths[i]}
	}
	return cols
}

func rows(t catalog.Table) []table.Row {
	out := make([]table.Row, 0, len(t.Rows))
	for _, r := range t.Rows {
		out = append(out, table.Row(r))
	}
	return out
}

func (m *model) View() tea.View {
	v := tea.NewView("")
	v.AltScreen = true

	if !m.ready {
		v.Content = "loading…"
		return v
	}

	body := lipgloss.JoinHorizontal(
		lipgloss.Top,
		sidebarStyle.Height(m.bodyHeight()).Render(m.sidebar()),
		contentStyle.Height(m.bodyHeight()).Render(m.content()),
	)

	v.Content = lipgloss.JoinVertical(lipgloss.Left, m.title(), "", body, "", m.help())
	return v
}

// title reads tool · id · description, the third part being the same sentence
// `--help` prints for that command.
func (m *model) title() string {
	sep := sepStyle.Render("  ·  ")
	parts := []string{titleStyle.Render("aldediggi")}

	if cur := m.selected(); cur != nil {
		parts = append(parts, titleStyle.Render(cur.ID()))
		if cur.Short != "" {
			parts = append(parts, descStyle.Render(cur.Short))
		}
	}

	line := strings.Join(parts, sep)

	// Drop the description before the id scrolls off on a narrow window.
	if m.width > 0 && lipgloss.Width(line) > m.width && len(parts) == 3 {
		line = strings.Join(parts[:2], sep)
	}
	return line
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
		case i == m.cursor:
			b.WriteString(selectedStyle.Render("▸ "+e.label) + "\n")
		default:
			b.WriteString("  " + e.label + "\n")
		}
	}
	return b.String()
}

func (m *model) content() string {
	switch {
	case m.err != nil:
		return errStyle.Render("error: " + m.err.Error())

	case m.running:
		return fmt.Sprintf("%s running…", m.spin.View())

	case m.res == nil:
		return helpStyle.Render("press ⏎ to run")
	}

	switch m.res.(type) {
	case catalog.Table:
		return m.tbl.View()
	default:
		return m.vp.View()
	}
}

func (m *model) help() string {
	return helpStyle.Render("↑↓ navigate  ·  ⏎ run  ·  r reload  ·  q quit")
}
