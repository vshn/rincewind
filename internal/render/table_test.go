package render

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"

	"github.com/vshn/rincewind/internal/engine"
)

func row(emphs []engine.Emphasis, cells ...string) engine.Row {
	r := engine.Row{}
	for i, c := range cells {
		e := engine.Plain
		if i < len(emphs) {
			e = emphs[i]
		}
		r.Cells = append(r.Cells, engine.Cell{Text: c, Emph: e})
	}
	return r
}

func sample() engine.Table {
	return engine.Table{
		Cols: []string{"NAME", "SYNC", "MESSAGE"},
		Rows: []engine.Row{
			row([]engine.Emphasis{engine.Plain, engine.Bad}, "cluster-tools", "OutOfSync", strings.Repeat("x", 80)),
			row([]engine.Emphasis{engine.Plain, engine.Good}, "root", "Synced", ""),
		},
	}
}

func TestColumnsStayAlignedAcrossRows(t *testing.T) {
	lines := Table(sample(), Style{})

	col := strings.Index(lines[0], "SYNC")
	for i, line := range lines[1:] {
		if got := strings.Index(line, strings.Fields(line)[1]); got != col {
			t.Errorf("row %d: second column at %d, want %d", i, got, col)
		}
	}
}

// Styling happens after the layout, so escape sequences must not shift
// anything. This is what tabwriter and bubbles/table both get wrong.
func TestStylingDoesNotDisturbAlignment(t *testing.T) {
	plain := Table(sample(), Style{})
	styled := Table(sample(), Style{
		Header: func(s string) string { return "\x1b[1m" + s + "\x1b[0m" },
		Cell:   func(_ int, _ engine.Emphasis, s string) string { return "\x1b[31m" + s + "\x1b[0m" },
	})

	if len(plain) != len(styled) {
		t.Fatalf("%d plain lines vs %d styled", len(plain), len(styled))
	}
	for i := range plain {
		if got := lipgloss.Width(styled[i]); got != lipgloss.Width(plain[i]) {
			t.Errorf("line %d: styled width %d, plain %d", i, got, lipgloss.Width(plain[i]))
		}
	}
}

func TestEmphasisReachesTheRightCell(t *testing.T) {
	var seen []engine.Emphasis
	Table(sample(), Style{
		Cell: func(_ int, e engine.Emphasis, s string) string {
			seen = append(seen, e)
			return s
		},
	})

	var bad, good int
	for _, e := range seen {
		switch e {
		case engine.Bad:
			bad++
		case engine.Good:
			good++
		}
	}
	if bad != 1 || good != 1 {
		t.Errorf("bad=%d good=%d, want one each", bad, good)
	}
}
