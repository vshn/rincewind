package tui

import (
	"strings"

	"charm.land/lipgloss/v2"

	"github.com/vshn/rincewind/internal/engine"
)

// frameCorner is what a border writes before its top-left label: the corner
// and one dash. Hit-testing counts from it, so it must not drift from the
// literal below.
const frameCorner = 2

// box is a framed region of the screen. Labels ride in the borders rather
// than costing rows of the content they describe: top-left the tabs,
// top-right the scroll position, bottom-left what this view can do.
type box struct {
	w, h     int
	border   lipgloss.Style
	top      string
	topRight string
	bottom   string
}

// render draws the box around lines, rounded corners, one space of padding
// inside.
func (b box) render(lines []string) string {
	w, h := max(b.w, boxPad), max(b.h, 2)
	inner := w - 2    // between the border columns
	text := inner - 2 // minus the padding spaces
	clip := lipgloss.NewStyle().MaxWidth(text)

	out := make([]string, 0, h)
	out = append(out, b.edge("╭", "╮", inner, b.top, b.topRight))

	side := b.border.Render("│")
	for i := range h - 2 {
		line := ""
		if i < len(lines) {
			line = clip.Render(lines[i])
		}
		pad := max(text-lipgloss.Width(line), 0)
		out = append(out, side+" "+line+strings.Repeat(" ", pad)+" "+side)
	}

	out = append(out, b.edge("╰", "╯", inner, b.bottom, ""))
	return strings.Join(out, "\n")
}

// edge draws one horizontal border with its labels. Both are clipped before
// the fill is measured, so a narrow window can never push the corner off the
// end of the line.
func (b box) edge(l, r string, inner int, left, right string) string {
	left = lipgloss.NewStyle().MaxWidth(max(inner-1, 0)).Render(left)
	right = lipgloss.NewStyle().MaxWidth(max(inner-1-lipgloss.Width(left), 0)).Render(right)
	fill := max(inner-1-lipgloss.Width(left)-lipgloss.Width(right), 0)

	return b.border.Render(l+"─") + left +
		b.border.Render(strings.Repeat("─", fill)) + right + b.border.Render(r)
}

// tabHit is one tab's span within the bar, in columns from the bar's start.
type tabHit struct {
	tab    int
	x0, x1 int // half-open
}

// tabBar lays the tabs out to fit avail columns and reports where each one
// landed. Layout and hit-testing come from this one function, the way the
// sidebar's do, so what the mouse believes and what the screen shows cannot
// drift apart.
func (m *model) tabBar(avail int) (string, []tabHit) {
	if len(m.tabs) == 0 || avail <= 0 {
		return "", nil
	}

	// Chips are built plain and measured before being styled: an escape
	// sequence counted as width would put every hit one column out.
	plain := make([]string, len(m.tabs))
	for i, t := range m.tabs {
		label := t.label()
		if t.running {
			label += "…"
		}
		if i == m.act {
			label = "▸" + label
		}
		plain[i] = " " + label + " "
	}

	from, to := m.tabWindow(plain, avail)

	var b strings.Builder
	var hits []tabHit
	if from > 0 {
		b.WriteString(dividerStyle.Render("‹"))
	}
	for i := from; i < to; i++ {
		if i > from {
			b.WriteString(dividerStyle.Render("│"))
		}
		x0 := lipgloss.Width(visibleOnly(b.String()))
		if i == m.act {
			b.WriteString(tabStyle.Render(plain[i]))
		} else {
			b.WriteString(dividerStyle.Render(plain[i]))
		}
		hits = append(hits, tabHit{tab: i, x0: x0 + frameCorner, x1: x0 + frameCorner + lipgloss.Width(plain[i])})
	}
	if to < len(m.tabs) {
		b.WriteString(dividerStyle.Render("›"))
	}
	return b.String(), hits
}

// tabWindow picks the run of tabs to show. It grows outwards from the active
// one, which is therefore always visible however many tabs are open — so no
// cap on their number is needed.
func (m *model) tabWindow(plain []string, avail int) (from, to int) {
	total := 0
	for _, p := range plain {
		total += lipgloss.Width(p) + 1
	}
	if total <= avail {
		return 0, len(plain)
	}

	from, to = m.act, m.act+1
	used := lipgloss.Width(plain[m.act]) + 2 // the chip plus both markers
	for {
		grew := false
		if to < len(plain) && used+lipgloss.Width(plain[to])+1 <= avail {
			used += lipgloss.Width(plain[to]) + 1
			to, grew = to+1, true
		}
		if from > 0 && used+lipgloss.Width(plain[from-1])+1 <= avail {
			used += lipgloss.Width(plain[from-1]) + 1
			from, grew = from-1, true
		}
		if !grew {
			return from, to
		}
	}
}

// tabBarWidth is what the bar may use: the top border, less what the scroll
// position on the right has already claimed.
func (m *model) tabBarWidth() int {
	return max(m.contentWidth()-1-lipgloss.Width(m.scrollLabel()), 0)
}

// tabAt maps a screen position to a tab, or -1. Tabs live in the content
// box's top border line.
func (m *model) tabAt(x, y int) int {
	if y != titleHeight {
		return -1
	}
	_, hits := m.tabBar(m.tabBarWidth())
	for _, h := range hits {
		if x >= sidebarWidth+h.x0 && x < sidebarWidth+h.x1 {
			return h.tab
		}
	}
	return -1
}

// actionLine fills the action box: what can be done with what is on screen,
// or the question a pending action is waiting on. The question takes the
// box over rather than appearing elsewhere, so it sits where the answer is
// expected and cannot be missed.
func (m *model) actionLine() string {
	// The descriptions go before any action does: knowing that `p` pauses
	// beats knowing what pausing means, and a clipped line tells neither.
	acts := m.viewActions()
	keys := actionKeys(acts, true)
	if lipgloss.Width(fitKeys(keys, 1<<30)) > m.contentWidth() {
		keys = actionKeys(acts, false)
	}
	line := fitKeys(keys, m.contentWidth())

	t := m.cur()

	// Nothing to act on yet: show the bindings, but do not pretend they work.
	if t.res == nil || t.running || m.focus != paneOutput {
		return dividerStyle.Render(visibleOnly(line))
	}
	return line
}

// actionKeys renders the legend entries. Weights descend, so a narrow box
// sheds the last-declared action first and keeps the one the view leads with.
func actionKeys(acts []engine.Action, withShort bool) []helpKey {
	out := make([]helpKey, 0, len(acts))
	for i, a := range acts {
		label := keyStyle.Render(keyLabel(a.Key)) + " " + a.Name
		if withShort && a.Short != "" {
			label += dividerStyle.Render(" (" + a.Short + ")")
		}
		out = append(out, helpKey{label: label, weight: len(acts) - i})
	}
	return out
}

// keyLabel renders a key the way the help line does.
func keyLabel(key string) string {
	if key == "enter" {
		return "⏎"
	}
	return key
}
