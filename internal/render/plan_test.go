package render

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"

	"github.com/vshn/rincewind/internal/engine"
)

func planSteps() []engine.Step {
	return []engine.Step{
		{Row: "worker", Env: engine.Args{"service": "worker", "replicas": "2"},
			Shell: []string{"sh", "-c"}, Script: `echo "$service"`},
		{Row: "api", Env: engine.Args{"service": "api", "replicas": "10"},
			Shell: []string{"sh", "-c"}, Script: `echo "$service"`},
	}
}

// boxOf cuts one chunk down to its frame lines.
func boxOf(chunk []string) []string {
	var out []string
	for _, line := range chunk {
		if strings.ContainsAny(line, "╭│╰") {
			out = append(out, line)
		}
	}
	return out
}

// The boxes of one plan are the same size, whatever each step happens to
// hold: the values are read down a column, and a column that changes width
// from one step to the next is not a column.
func TestEveryBoxInAPlanIsTheSameWidth(t *testing.T) {
	chunks := Plan("restart", planSteps(), PlanStyle{})

	first, second := boxOf(chunks[0]), boxOf(chunks[1])
	if len(first) != len(second) {
		t.Fatalf("%d lines against %d, want the same box twice", len(first), len(second))
	}
	for i := range first {
		if got, want := lipgloss.Width(second[i]), lipgloss.Width(first[i]); got != want {
			t.Errorf("line %d is %d wide, want %d:\n%s\n%s", i, got, want, second[i], first[i])
		}
	}
	if !strings.Contains(strings.Join(first, "\n"), "worker") {
		t.Errorf("the box lost its value:\n%s", strings.Join(first, "\n"))
	}
}

// A value holding a newline or a tab still lines up: the extra lines
// continue in the value column, tabs become spaces, and every line of every
// box in the plan stays the same width.
func TestAMultilineValueKeepsTheColumn(t *testing.T) {
	steps := []engine.Step{
		{Env: engine.Args{"reason": "first line\nsecond\tline"}, Shell: []string{"sh", "-c"}},
		{Env: engine.Args{"reason": "short"}, Shell: []string{"sh", "-c"}},
	}
	chunks := Plan("x", steps, PlanStyle{})

	widths := map[int]bool{}
	for _, chunk := range chunks {
		for _, line := range boxOf(chunk) {
			widths[lipgloss.Width(line)] = true
		}
	}
	if len(widths) != 1 {
		t.Errorf("the boxes hold %d different widths, want one:\n%s\n%s",
			len(widths), strings.Join(chunks[0], "\n"), strings.Join(chunks[1], "\n"))
	}
	joined := strings.Join(chunks[0], "\n")
	for _, want := range []string{"first line", "second", "line"} {
		if !strings.Contains(joined, want) {
			t.Errorf("the value lost %q:\n%s", want, joined)
		}
	}
	if strings.Contains(joined, "\t") {
		t.Errorf("a tab survived into the box:\n%s", joined)
	}
}

// One oversized value must not pad every box in the plan out past the pane:
// it wraps inside its cell, whole, and the plan stays readable.
func TestALongValueWrapsRatherThanInflating(t *testing.T) {
	long := strings.Repeat("x", 3*planValueWidth)
	steps := []engine.Step{
		{Env: engine.Args{"url": long}, Shell: []string{"sh", "-c"}},
		{Env: engine.Args{"url": "short"}, Shell: []string{"sh", "-c"}},
	}
	chunks := Plan("x", steps, PlanStyle{})

	for _, chunk := range chunks {
		for _, line := range boxOf(chunk) {
			if w := lipgloss.Width(line); w > planValueWidth+20 {
				t.Fatalf("a box line is %d wide, want the value wrapped:\n%s", w, line)
			}
		}
	}
	// Wrapped, not truncated: every character is still in the plan.
	if got := strings.Count(strings.Join(chunks[0], ""), "x"); got < 3*planValueWidth {
		t.Errorf("the value lost characters: %d of %d", got, 3*planValueWidth)
	}
}

// An action that takes no values gets no frame. An empty box reads as
// something missing, rather than as nothing wanted.
func TestNothingToShowIsNoBox(t *testing.T) {
	steps := []engine.Step{{Shell: []string{"sh", "-c"}, Script: "true"}}
	chunk := Plan("x", steps, PlanStyle{})[0]
	if joined := strings.Join(chunk, "\n"); strings.ContainsAny(joined, "╭╰│") {
		t.Errorf("drew a frame for a step with no values:\n%s", joined)
	}
}

// The chunk reads in the order the tab shows: heading, values, then the
// shell line and the script under it, indented as written.
func TestAChunkCarriesHeadBoxAndInvocation(t *testing.T) {
	chunk := Plan("restart", planSteps(), PlanStyle{})[0]
	if chunk[0] != "restart  worker" {
		t.Errorf("the heading is %q", chunk[0])
	}
	joined := strings.Join(chunk, "\n")
	shell := strings.Index(joined, "  sh -c")
	script := strings.Index(joined, `    echo "$service"`)
	box := strings.Index(joined, "│ service")
	if shell == -1 || script == -1 || box == -1 {
		t.Fatalf("the chunk is missing a part:\n%s", joined)
	}
	if box >= shell || shell >= script {
		t.Errorf("the parts are out of order:\n%s", joined)
	}
}

// The styles are what colour costs: unpainted, the plan carries no escape
// codes at all, which is what a pipe gets.
func TestAnUnpaintedPlanCarriesNoEscapeCodes(t *testing.T) {
	for _, chunk := range Plan("restart", planSteps(), PlanStyle{}) {
		if got := strings.Join(chunk, "\n"); strings.Contains(got, "\x1b") {
			t.Errorf("an unpainted plan is styled anyway: %q", got)
		}
	}
}

// Padding goes outside the paint: a styled line measures exactly what the
// plain one does, and no escape sequence wraps a run of trailing spaces.
func TestAPaintedPlanMeasuresLikeAPlainOne(t *testing.T) {
	dim := lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	st := PlanStyle{
		Head:   func(s string) string { return dim.Render(s) },
		Border: func(s string) string { return dim.Render(s) },
		Label:  func(s string) string { return dim.Render(s) },
	}
	plain := Plan("restart", planSteps(), PlanStyle{})
	painted := Plan("restart", planSteps(), st)

	for i := range plain {
		for l := range plain[i] {
			if got, want := lipgloss.Width(painted[i][l]), lipgloss.Width(plain[i][l]); got != want {
				t.Errorf("painted line measures %d, plain %d:\n%q", got, want, painted[i][l])
			}
			if strings.Contains(painted[i][l], " \x1b[0m") || strings.HasSuffix(painted[i][l], "  \x1b[0m") {
				t.Errorf("an escape sequence wraps padding: %q", painted[i][l])
			}
		}
	}
}

// The text plan is the same steps without the furniture: values as
// `name=value`, pasteable into a ticket or a shell as they stand.
func TestPlanTextIsPasteable(t *testing.T) {
	chunks := PlanText("restart", planSteps())
	joined := strings.Join(chunks[0], "\n")

	for _, want := range []string{"restart  worker", "  service=worker", "  replicas=2", "  sh -c"} {
		if !strings.Contains(joined, want) {
			t.Errorf("the text plan is missing %q:\n%s", want, joined)
		}
	}
	if strings.ContainsAny(joined, "╭│╰") {
		t.Errorf("the text plan carries box-drawing:\n%s", joined)
	}
}
