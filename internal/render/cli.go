// Package render turns a Result into output. The CLI path lives here; the TUI
// renders the same Result types into widgets, which is what keeps the two
// modes from growing separate code paths.
package render

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/vshn/aldediggi/internal/catalog"
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
	// Laid out into a buffer first. tabwriter counts an escape sequence as
	// cell content (see the Escape docs in text/tabwriter), so styling before
	// the layout shifts the styled row out of alignment with the rest.
	var buf bytes.Buffer
	tw := tabwriter.NewWriter(&buf, 0, 0, 2, ' ', 0)

	if _, err := fmt.Fprintln(tw, strings.Join(t.Cols, "\t")); err != nil {
		return err
	}
	for _, row := range t.Rows {
		if _, err := fmt.Fprintln(tw, strings.Join(row, "\t")); err != nil {
			return err
		}
	}
	if err := tw.Flush(); err != nil {
		return err
	}

	if !style {
		_, err := w.Write(buf.Bytes())
		return err
	}

	// Per-cell colours from the config schema will need the column widths
	// computed here rather than by tabwriter; the header is a single span, so
	// it can still be wrapped after the fact.
	head, rest, _ := strings.Cut(buf.String(), "\n")
	if _, err := fmt.Fprintf(w, "\x1b[1m%s\x1b[0m\n", head); err != nil {
		return err
	}
	_, err := io.WriteString(w, rest)
	return err
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
