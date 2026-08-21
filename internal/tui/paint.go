package tui

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/vshn/rincewind/internal/render"
)

// Painting: everything that turns the model into lines on screen. Nothing
// here decides anything; by the time it runs, showResult has already laid
// the content out.

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
			descStyle.Render(render.Plural(len(j.steps), "step")))
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
	case (m.hasText() || m.hasChart()) && m.shownCount() > 0:
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
