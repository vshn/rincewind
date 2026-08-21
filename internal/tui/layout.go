package tui

// Layout: how the window is divided into boxes, which line of the screen
// belongs to which row or menu entry, and where the scroll positions have to
// end up after a resize.

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

// vpHeight is the viewport's share of the content box: the inner height,
// minus the sticky header and the status line when those are showing.
func (m *model) vpHeight() int {
	h := m.bodyBoxHeight() - 2 - m.statusLines()
	if m.hasTable() {
		h--
	}
	return max(h, 1)
}

func (m *model) resize() {
	m.showResult()
	m.syncSidebar()
}

// sidebarOffset is the first menu line on screen, clamped to what the box
// can actually show.
func sidebarOffset(total, h, want int) int {
	if total <= h {
		return 0
	}
	return min(max(want, 0), total-h)
}
