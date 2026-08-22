package render

import (
	"strings"

	"charm.land/lipgloss/v2"

	"github.com/vshn/rincewind/internal/engine"
)

// PlanStyle paints a plan. Nil funcs draw it plain — the pipe case — the
// same convention Style follows for a table. Named fields rather than
// positional styles, so a border and a label cannot be transposed silently.
type PlanStyle struct {
	Head   func(string) string // the step's heading: action name and row
	Border func(string) string // the box frame and the shell line
	Label  func(string) string // the value names
	Script func(string) string // the script itself
}

// planValueWidth caps the value column. A longer value wraps inside its
// cell rather than truncating — every value is still printed whole, which
// the confirmation depends on — so one oversized value cannot pad every box
// in the plan out past the pane.
const planValueWidth = 80

// Plan renders what an action is about to run, one chunk per step: the
// heading, the values in a light frame, then the shell and the script as
// written. One renderer for the TUI and for `act`, so the shape cannot
// depend on which half draws it; the style is the only difference between
// the screen and the pipe.
//
// The frame is what separates the values from the script under them — the
// two are read differently, one for what it says and one for what it will
// do. Deliberately not one shell-quoted line: the values never enter the
// script text, and a quoted one-liner would misrepresent what runs.
func Plan(name string, steps []engine.Step, st PlanStyle) [][]string {
	b := measure(steps)

	out := make([][]string, 0, len(steps))
	for _, s := range steps {
		chunk := []string{apply(st.Head, heading(name, s))}
		chunk = append(chunk, b.draw(s.Values(), st)...)
		chunk = append(chunk, invocation(s, st)...)
		out = append(out, chunk)
	}
	return out
}

// PlanText is the same plan as pasteable text: the values as plain
// `name=value` lines instead of a frame. The frame reads well on a screen
// and pastes as furniture — this is what a copy is made of, so what the
// tool just ran goes into a ticket or a shell as it stands.
func PlanText(name string, steps []engine.Step) [][]string {
	out := make([][]string, 0, len(steps))
	for _, s := range steps {
		chunk := []string{heading(name, s)}
		for _, v := range s.Values() {
			chunk = append(chunk, "  "+v.Name+"="+v.Text)
		}
		chunk = append(chunk, invocation(s, PlanStyle{})...)
		out = append(out, chunk)
	}
	return out
}

// heading and invocation are shared between the drawn plan and the text
// one, so the two cannot drift apart in anything but the values' shape.
func heading(name string, s engine.Step) string {
	if s.Row == "" {
		return name
	}
	return name + "  " + s.Row
}

func invocation(s engine.Step, st PlanStyle) []string {
	out := []string{apply(st.Border, "  "+strings.Join(s.Shell, " "))}
	for _, line := range strings.Split(strings.TrimRight(s.Script, "\n"), "\n") {
		out = append(out, apply(st.Script, "    "+line))
	}
	return out
}

func apply(f func(string) string, s string) string {
	if f == nil {
		return s
	}
	return f(s)
}

// box holds the column widths of one plan. Measured across every step at
// once, because the boxes have to come out the same size: a plan of many
// rows is read downwards, and a column that changes width from one step to
// the next is not a column.
type box struct{ name, text int }

func measure(steps []engine.Step) box {
	var b box
	for _, s := range steps {
		for _, v := range s.Values() {
			b.name = max(b.name, lipgloss.Width(v.Name))
			for _, line := range cellLines(v.Text) {
				b.text = max(b.text, lipgloss.Width(line))
			}
		}
	}
	// A value column of nothing but empty strings still gets one space, so
	// the frame stays a frame.
	if b.name > 0 {
		b.text = max(b.text, 1)
	}
	return b
}

// cellLines shapes one value for its cell, keeping all of it: newlines
// continue in the value column, tabs become spaces — a cell is aligned by
// measured width, and a terminal's tab stop is not one — and a line past
// the cap wraps to the next.
func cellLines(text string) []string {
	var out []string
	for _, line := range strings.Split(strings.ReplaceAll(text, "\t", "    "), "\n") {
		out = append(out, wrapLine(line)...)
	}
	return out
}

func wrapLine(line string) []string {
	if lipgloss.Width(line) <= planValueWidth {
		return []string{line}
	}
	var out []string
	var cur strings.Builder
	w := 0
	for _, r := range line {
		rw := lipgloss.Width(string(r))
		if w+rw > planValueWidth && cur.Len() > 0 {
			out = append(out, cur.String())
			cur.Reset()
			w = 0
		}
		cur.WriteRune(r)
		w += rw
	}
	return append(out, cur.String())
}

// draw renders one step's values. Nothing to show is no box at all: an
// action that takes no values would otherwise get an empty frame, which
// reads as something missing rather than as nothing wanted.
//
// Padding goes outside the paint, the same rule join follows for a table:
// the cell is painted first and padded after, so no escape sequence ever
// wraps a run of spaces and a styled line measures the same as a plain one.
func (b box) draw(vals []engine.Value, st PlanStyle) []string {
	if len(vals) == 0 {
		return nil
	}

	bar := func(l, m, r string) string {
		return apply(st.Border, "  "+l+strings.Repeat("─", b.name+2)+m+strings.Repeat("─", b.text+2)+r)
	}
	cell := func(f func(string) string, s string, w int) string {
		if s == "" {
			return strings.Repeat(" ", w)
		}
		return apply(f, s) + strings.Repeat(" ", w-lipgloss.Width(s))
	}

	out := []string{bar("╭", "┬", "╮")}
	for _, v := range vals {
		for i, text := range cellLines(v.Text) {
			name := ""
			if i == 0 {
				name = v.Name
			}
			out = append(out, "  "+apply(st.Border, "│ ")+cell(st.Label, name, b.name)+
				apply(st.Border, " │ ")+cell(nil, text, b.text)+apply(st.Border, " │"))
		}
	}
	return append(out, bar("╰", "┴", "╯"))
}
