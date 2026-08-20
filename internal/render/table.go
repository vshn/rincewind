package render

import (
	"strings"

	"charm.land/lipgloss/v2"

	"github.com/vshn/rincewind/internal/engine"
)

// gutter is the space between columns.
const gutter = 2

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
	Cell func(row int, e engine.Emphasis, s string) string
}

// Table lays a table out as lines, header first. Columns are as wide as their
// widest cell and nothing is cut: a diagnostic tool that hides the end of a
// message is worse than one you have to scroll. The terminal wraps, and the TUI
// scrolls sideways.
func Table(t engine.Result, style Style) []string {
	widths := layout(t)

	out := make([]string, 0, len(t.Rows)+1)
	out = append(out, join(t.Cols, widths, func(_ int, cell string) string {
		if style.Header == nil {
			return cell
		}
		return style.Header(cell)
	}))

	for i, row := range t.Rows {
		cells := make([]string, len(row.Cells))
		for c := range row.Cells {
			cells[c] = row.Cells[c].Text
		}
		out = append(out, join(cells, widths, func(c int, cell string) string {
			if style.Cell == nil {
				return cell
			}
			return style.Cell(i, row.Cells[c].Emph, cell)
		}))
	}
	return out
}

// layout sizes each column to its widest cell.
func layout(t engine.Result) []int {
	widths := make([]int, len(t.Cols))
	for i, c := range t.Cols {
		widths[i] = lipgloss.Width(c)
	}
	for _, row := range t.Rows {
		for i, cell := range row.Cells {
			if i < len(widths) && lipgloss.Width(cell.Text) > widths[i] {
				widths[i] = lipgloss.Width(cell.Text)
			}
		}
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
