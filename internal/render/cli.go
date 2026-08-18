// Package render turns a Result into output. The CLI path lives here; the TUI
// renders the same Result types into widgets, which is what keeps the two
// modes from growing separate code paths.
package render

import (
	"fmt"
	"io"
	"os"

	"github.com/vshn/aldediggi/internal/catalog"
)

// ANSI codes rather than lipgloss styles: the CLI decides on styling from the
// writer, not from a terminal it may not be attached to.
const (
	ansiReset  = "\x1b[0m"
	ansiBold   = "\x1b[1m"
	ansiRed    = "\x1b[31m"
	ansiYellow = "\x1b[33m"
	ansiGreen  = "\x1b[32m"
)

// CLI writes res to w for non-interactive use. Replaces the `column -t` the
// zsh script piped through, whose flags differ between BSD and util-linux.
func CLI(w io.Writer, res catalog.Result) error {
	return cli(w, res, styled(w))
}

// cli takes the styling decision as an argument so that tests can exercise the
// styled path against an ordinary buffer.
func cli(w io.Writer, res catalog.Result, style bool) error {
	switch r := res.(type) {
	case catalog.Text:
		for _, line := range r.Lines {
			if _, err := fmt.Fprintln(w, line); err != nil {
				return err
			}
		}
		return nil

	case catalog.Table:
		return writeTable(w, r, style)

	default:
		return fmt.Errorf("render: unsupported result type %T", res)
	}
}

func writeTable(w io.Writer, t catalog.Table, style bool) error {
	// Unbounded: a pipe has no width, and a terminal wraps on its own rather
	// than losing a column silently.
	for _, line := range Table(t, 0, cliStyle(style)) {
		if _, err := fmt.Fprintln(w, line); err != nil {
			return err
		}
	}
	return nil
}

func cliStyle(on bool) Style {
	if !on {
		return Style{}
	}
	return Style{
		Header: func(s string) string { return ansiBold + s + ansiReset },
		Cell: func(_ int, e catalog.Emphasis, s string) string {
			switch e {
			case catalog.Bad:
				return ansiRed + s + ansiReset
			case catalog.Warn:
				return ansiYellow + s + ansiReset
			case catalog.Good:
				return ansiGreen + s + ansiReset
			default:
				return s
			}
		},
	}
}

// styled reports whether w is a terminal we may decorate. Piped output must
// stay free of escape codes so it can be fed to grep and friends.
func styled(w io.Writer) bool {
	if _, set := os.LookupEnv("NO_COLOR"); set {
		return false
	}

	f, ok := w.(*os.File)
	if !ok {
		return false
	}

	info, err := f.Stat()
	if err != nil {
		return false
	}

	return info.Mode()&os.ModeCharDevice != 0
}
