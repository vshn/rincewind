package tui

import tea "charm.land/bubbletea/v2"

// Keys: what a keystroke does, and the movement it causes. The switch in
// key is the whole binding table; boundKeys beside it is what a test holds
// against the reserved set in internal/config.

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

	// Every key answered below is listed in boundKeys, which a test holds
	// against config's reserved set. Adding a case here means adding it
	// there.
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
		case m.active > 0:
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
		return m, m.activate(m.active + 1)

	case "[", "shift+tab":
		return m, m.activate(m.active - 1)

	case "x":
		return m, m.closeTab()
	}

	// Anything else scrolls the output pane.
	var cmd tea.Cmd
	m.vp, cmd = m.vp.Update(msg)
	return m, cmd
}

// boundKeys is what the switch in onKey answers, written out because a
// switch cannot be read by a test. Config's reserved set is the other half
// of the contract: everything here is refused to a hat, except enter, which
// is deliberately left free — in the output pane it means nothing else, so a
// view may bind it as its one obvious action.
//
// Keep this beside the switch, in both directions.
var boundKeys = []string{
	"q", "ctrl+c", "/", "tab", "<", ">", "esc",
	"up", "k", "down", "j", "pgdown", "ctrl+f", "pgup", "ctrl+b",
	"home", "g", "end", "G", "r", "Y", "enter", "space", " ", "a", "M",
	"]", "[", "shift+tab", "x",
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
	// wants from a log anyway. A chart is the same — only its notes and
	// legend can even reach past the pane.
	if m.hasText() || m.hasChart() {
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

	if m.hasText() || m.hasChart() {
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
	if m.focus != paneOutput || len(vis.Rows) == 0 {
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

func (m *model) clampRow() {
	if n := m.rowCount(); n > 0 {
		m.cur().row = min(max(m.cur().row, 0), n-1)
	} else {
		m.cur().row = 0
	}
}
