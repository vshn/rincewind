package render

import (
	"fmt"
	"io"
	"os"

	"charm.land/lipgloss/v2"

	"github.com/vshn/rincewind/internal/engine"
)

// The CLI's own styles. Separate from the TUI's on purpose: the TUI paints
// for a screen it controls, this paints for whatever the output is pointed
// at, and half of those are a pipe.
var (
	cliHeader = lipgloss.NewStyle().Bold(true)
	cliGood   = lipgloss.NewStyle().Foreground(lipgloss.Color("10"))
	cliWarn   = lipgloss.NewStyle().Foreground(lipgloss.Color("11"))
	cliBad    = lipgloss.NewStyle().Foreground(lipgloss.Color("9"))
)

// CLI writes a result for non-interactive use: the same lines the TUI shows,
// without the frames. It replaces the `column -t` the shell scripts this tool
// grew out of piped through, whose flags differ between BSD and util-linux.
//
// A text view's lines go out verbatim, exactly as in the TUI: a log is read
// by its indentation.
func CLI(w io.Writer, res engine.Result) error {
	return cli(w, res, Colour(w))
}

// cli takes the styling decision as an argument so the tests can exercise the
// styled path against an ordinary buffer.
func cli(w io.Writer, res engine.Result, colour bool) error {
	lines := res.Lines
	if !res.Text() {
		style := Style{}
		if colour {
			style = Style{Header: func(s string) string { return cliHeader.Render(s) }, Cell: cliCell}
		}
		lines = Table(res, style)
	}

	for _, line := range lines {
		if _, err := fmt.Fprintln(w, line); err != nil {
			return err
		}
	}
	return nil
}

// cliCell paints one cell. The row index is what the TUI uses for its cursor
// and selection; out here there is neither, so only the emphasis matters.
func cliCell(_ int, e engine.Emphasis, s string) string {
	switch e {
	case engine.Good:
		return cliGood.Render(s)
	case engine.Warn:
		return cliWarn.Render(s)
	case engine.Bad:
		return cliBad.Render(s)
	}
	return s
}

// Colour reports whether w is a terminal, and so whether escape codes belong
// in what goes to it. Asked of the writer rather than of the platform: piping
// into a file is the case that matters, and it looks the same on every OS.
func Colour(w io.Writer) bool {
	f, ok := w.(*os.File)
	if !ok {
		return false
	}
	info, err := f.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}
