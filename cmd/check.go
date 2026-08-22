package cmd

import (
	"context"
	"errors"
	"io"
	"os/exec"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/spf13/cobra"

	"github.com/vshn/rincewind/internal/config"
	"github.com/vshn/rincewind/internal/engine"
)

// gutter is the space between the report's columns, the same as the one
// between a table's.
const gutter = 2

var checkCmd = &cobra.Command{
	Use:   "check",
	Short: "Validate the hat and report the environment it would run in",
	Long: "Load every view in the hat, which is what catches a config mistake, and\n" +
		"then say where the hat thinks it is: each context, what it resolved to,\n" +
		"the label it contributes, whether the binaries it names are on PATH, and\n" +
		"how many views it gates.\n\n" +
		"Only a hat that does not load is a failure. An inactive context and a\n" +
		"missing binary are both ordinary — one may well be checking a hat for a\n" +
		"platform one is not on — so neither changes the exit status.",
	RunE: func(cmd *cobra.Command, _ []string) error {
		path, err := hat()
		if err != nil {
			return err
		}
		// The report is what this command is for, so the unknown keys go to
		// stdout with the rest of it rather than off to the side.
		out := cmd.OutOrStdout()
		loaded, err := loadHat(out, path)
		if err != nil {
			return err
		}

		say(out, "%s: %s ok\n", path, engine.Plural(len(loaded.Views), "view"))
		// Said even when it is met: a hat that pins a version is saying
		// something about itself worth reading before one edits it. The
		// comparison half is left off when nothing was compared — loadHat's
		// "not checking" note just said so, and repeating the version here
		// would read as if it had been measured.
		if loaded.Requires != "" {
			if _, checked := loaded.Unmet(version); checked {
				say(out, "requires rincewind >= %s, and this is %s\n", loaded.Requires, version)
			} else {
				say(out, "requires rincewind >= %s\n", loaded.Requires)
			}
		}
		reportContexts(cmd.Context(), out, loaded)

		// Strict is for the hat's own pipeline: an unknown key there is a
		// typo, because the author is by definition running the version they
		// wrote it for.
		if checkStrict && len(loaded.Warnings) > 0 {
			return errors.New(engine.Plural(len(loaded.Warnings), "unknown key"))
		}
		return nil
	},
}

// checkStrict makes an unknown key an error rather than a note.
var checkStrict bool

func init() {
	checkCmd.Flags().BoolVar(&checkStrict, "strict", false,
		"treat a key this build does not understand as an error")
}

// reportContexts resolves the hat's contexts and lays them out one to a line:
// what each became, what it contributes to the chrome, what it expects to
// find on PATH, and how many views it gates — so the report answers "what
// would I lose here" as well as "where am I".
func reportContexts(ctx context.Context, w io.Writer, loaded config.Hat) {
	if len(loaded.Contexts) == 0 {
		return
	}
	found := engine.Resolve(ctx, loaded.Contexts)

	gates := map[string]int{}
	for _, v := range loaded.Views {
		for _, id := range v.Needs {
			gates[id]++
		}
	}

	cols := make([][]string, 0, len(loaded.Contexts))
	for _, c := range loaded.Contexts {
		views := ""
		if n := gates[c.ID]; n > 0 {
			views = engine.Plural(n, "view")
		}
		cols = append(cols, []string{
			c.ID, found.State[c.ID].String(), found.Label[c.ID], tools(c.Tools), views,
		})
	}

	// Measured the way everything here is measured, not by counting bytes:
	// the ticks and crosses below are three bytes and one column each.
	//
	// The last column is not padded, so a context that gates nothing ends the
	// line rather than trailing spaces across it.
	width := make([]int, len(cols[0])-1)
	for _, row := range cols {
		for i := range width {
			width[i] = max(width[i], lipgloss.Width(row[i]))
		}
	}

	say(w, "\n")
	for _, row := range cols {
		line := "  "
		for i, cell := range row[:len(row)-1] {
			line += cell + strings.Repeat(" ", width[i]-lipgloss.Width(cell)+gutter)
		}
		say(w, "%s\n", strings.TrimRight(line+row[len(row)-1], " "))
	}

	// A probe that could not answer hides nothing, so the TUI stays quiet
	// about it. This is where it is said instead.
	for _, note := range found.Notes {
		say(w, "  ! %s\n", note)
	}
}

// tools probes each binary a context names — capabilities, not platform
// names, and the same way the clipboard writer and git are probed.
//
// It is a report and never a gate. A missing binary is actionable news —
// install it — and a view that hid itself over one would hide the finding;
// the script's own "command not found" does the rest.
func tools(names []string) string {
	out := make([]string, 0, len(names))
	for _, name := range names {
		if _, err := exec.LookPath(name); err != nil {
			out = append(out, name+" ✗ not on PATH")
			continue
		}
		out = append(out, name+" ✓")
	}
	return strings.Join(out, "  ")
}
