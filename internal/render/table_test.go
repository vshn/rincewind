package render

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"

	"github.com/vshn/aldediggi/internal/catalog"
)

func sample() catalog.Table {
	t := catalog.Table{
		Cols:     []string{"NAME", "SYNC", "MESSAGE"},
		Emphasis: map[string]catalog.Emphasis{"OutOfSync": catalog.Bad, "Synced": catalog.Good},
	}
	t.Add(nil, "cluster-tools", "OutOfSync", strings.Repeat("x", 80))
	t.Add(nil, "root", "Synced", "")
	return t
}

// Rows wider than the pane wrap, which pushes everything below off screen.
func TestLinesNeverExceedTheWidth(t *testing.T) {
	for _, width := range []int{20, 40, 63, 120, 400} {
		for i, line := range Table(sample(), width, Style{}) {
			if got := lipgloss.Width(line); got > width {
				t.Errorf("width=%d line %d is %d wide", width, i, got)
			}
		}
	}
}

func TestColumnsStayAlignedAcrossRows(t *testing.T) {
	lines := Table(sample(), 100, Style{})

	col := strings.Index(lines[0], "SYNC")
	for i, line := range lines[1:] {
		if got := strings.Index(line, strings.Fields(line)[1]); got != col {
			t.Errorf("row %d: second column at %d, want %d", i, got, col)
		}
	}
}

func TestCrampedColumnsStayUsable(t *testing.T) {
	for _, line := range Table(sample(), 10, Style{}) {
		if strings.TrimSpace(line) == "" {
			t.Error("a cramped table produced an empty line")
		}
	}
}

func TestClippedCellsAreMarked(t *testing.T) {
	lines := Table(sample(), 40, Style{})
	if !strings.Contains(lines[1], ellipsis) {
		t.Errorf("no clip marker: %q", lines[1])
	}
}

// Styling happens after the layout, so escape sequences must not shift
// anything. This is what tabwriter and bubbles/table both get wrong.
func TestStylingDoesNotDisturbAlignment(t *testing.T) {
	plain := Table(sample(), 100, Style{})
	styled := Table(sample(), 100, Style{
		Header: func(s string) string { return "\x1b[1m" + s + "\x1b[0m" },
		Cell:   func(_ int, _ catalog.Emphasis, s string) string { return "\x1b[31m" + s + "\x1b[0m" },
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

func TestEmphasisIsLookedUpPerCell(t *testing.T) {
	var seen []catalog.Emphasis
	Table(sample(), 100, Style{
		Cell: func(_ int, e catalog.Emphasis, s string) string {
			seen = append(seen, e)
			return s
		},
	})

	var bad, good int
	for _, e := range seen {
		switch e {
		case catalog.Bad:
			bad++
		case catalog.Good:
			good++
		}
	}
	if bad != 1 || good != 1 {
		t.Errorf("bad=%d good=%d, want one each", bad, good)
	}
}
