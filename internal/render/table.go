package render

import (
	"strings"

	"charm.land/lipgloss/v2"

	"github.com/vshn/aldediggi/internal/catalog"
)

const (
	gutter      = 2 // spaces between columns
	minColWidth = 3 // a shrunken column still has to show something
	ellipsis    = "…"
)

// Style paints a table. Either field may be nil, which leaves that part
// unstyled. Styling happens after the layout is fixed, because escape
// sequences count towards the width of everything that measures naively —
// text/tabwriter and bubbles/table both get this wrong.
type Style struct {
	Header func(string) string
	// Cell receives the zero-based data row, so a caller that highlights a
	// selection can override the emphasis colours for that one row. Painting
	// over a finished line instead would not work: the reset codes inside it
	// end the highlight partway across.
	Cell func(row int, e catalog.Emphasis, s string) string
}

// Table lays a table out as lines no wider than width, header first. One
// renderer for both modes, so the CLI and the TUI cannot disagree about where
// a column ends.
func Table(t catalog.Table, width int, style Style) []string {
	widths := layout(t, width)

	out := make([]string, 0, len(t.Rows)+1)
	out = append(out, clipLine(join(t.Cols, widths, func(_ int, cell string) string {
		if style.Header == nil {
			return cell
		}
		return style.Header(cell)
	}), width))

	for i, row := range t.Rows {
		out = append(out, clipLine(join(row.Cells, widths, func(_ int, cell string) string {
			if style.Cell == nil {
				return cell
			}
			return style.Cell(i, t.EmphasisOf(strings.TrimSpace(cell)), cell)
		}), width))
	}
	return out
}

// clipLine is the backstop for a window too narrow to hold even the minimum
// columns. width <= 0 means unbounded, which is what a pipe gets.
func clipLine(line string, width int) string {
	if width <= 0 || lipgloss.Width(line) <= width {
		return line
	}
	return lipgloss.NewStyle().MaxWidth(width).Render(line)
}

// layout sizes each column to its widest cell, then takes any overflow off the
// last one, which is the free-text column.
func layout(t catalog.Table, width int) []int {
	widths := make([]int, len(t.Cols))
	for i, c := range t.Cols {
		widths[i] = lipgloss.Width(c)
	}
	for _, row := range t.Rows {
		for i, cell := range row.Cells {
			if i < len(widths) && lipgloss.Width(cell) > widths[i] {
				widths[i] = lipgloss.Width(cell)
			}
		}
	}

	if len(widths) == 0 || width <= 0 {
		return widths
	}

	total := gutter * (len(widths) - 1)
	for _, w := range widths {
		total += w
	}

	// The last column is the free-text one by convention, so it gives way
	// first. Once it is at the minimum, keep taking from whichever column is
	// widest, otherwise a narrow window overflows however much the last one
	// shrinks.
	shrink := func(i int) {
		give := min(widths[i]-minColWidth, total-width)
		widths[i] -= give
		total -= give
	}

	if last := len(widths) - 1; total > width && widths[last] > minColWidth {
		shrink(last)
	}
	for total > width {
		widest := -1
		for i, w := range widths {
			if w > minColWidth && (widest < 0 || w > widths[widest]) {
				widest = i
			}
		}
		if widest < 0 {
			break // every column is at the minimum; the line gets clipped
		}
		shrink(widest)
	}

	return widths
}

func join(cells []string, widths []int, paint func(i int, cell string) string) string {
	var b strings.Builder
	for i, w := range widths {
		cell := ""
		if i < len(cells) {
			cell = cells[i]
		}
		cell = clip(cell, w)

		// Padding goes outside the paint. Inside it, trailing spaces would be
		// wrapped in escape sequences and no longer trimmable, so a styled
		// line would measure differently from the same line unstyled.
		pad := max(w-lipgloss.Width(cell), 0)
		// An empty cell is left alone. Painting it would emit an escape
		// sequence with nothing between the codes, which then blocks the
		// trailing padding from being trimmed off the end of the line.
		if cell != "" {
			b.WriteString(paint(i, cell))
		}
		b.WriteString(strings.Repeat(" ", pad))

		if i < len(widths)-1 {
			b.WriteString(strings.Repeat(" ", gutter))
		}
	}
	return strings.TrimRight(b.String(), " ")
}

// clip shortens a cell to w columns, marking that something was cut.
func clip(s string, w int) string {
	if lipgloss.Width(s) <= w {
		return s
	}
	if w <= 1 {
		return ellipsis
	}

	runes := []rune(s)
	for len(runes) > 0 && lipgloss.Width(string(runes))+1 > w {
		runes = runes[:len(runes)-1]
	}
	return string(runes) + ellipsis
}
