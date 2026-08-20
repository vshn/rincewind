package cmd

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"

	"github.com/vshn/rincewind/internal/config"
	"github.com/vshn/rincewind/internal/engine"
	"github.com/vshn/rincewind/internal/render"
)

var viewCmd = &cobra.Command{
	Use:   "view <id>",
	Short: "Run one view and print its output",
	Long: "Run one view and print what it produces, without the TUI.\n\n" +
		"A table goes out column-aligned, a text view verbatim, and a followed\n" +
		"view streams until its script ends or it is interrupted. Colour is used\n" +
		"only when the output is a terminal, so piping into a file gives plain\n" +
		"text.\n\n" +
		"Views are run as they stand: there is no way to pass a parameter yet, so\n" +
		"one that requires an argument is refused rather than run on a value it\n" +
		"made up.",
	Args:              cobra.ExactArgs(1),
	ValidArgsFunction: completeViews,
	RunE: func(cmd *cobra.Command, args []string) error {
		views, err := config.Load(hat())
		if err != nil {
			return err
		}
		v, err := pick(views, args[0], hat())
		if err != nil {
			return err
		}
		return runView(cmd.Context(), cmd.OutOrStdout(), cmd.ErrOrStderr(), v)
	},
}

// pick finds the view by id and refuses the ones this mode cannot run. Both
// refusals name what would work instead: a list of ids is the answer to
// either mistake.
func pick(views []engine.View, id, from string) (engine.View, error) {
	for _, v := range views {
		if v.ID != id {
			continue
		}
		if !v.Listed() {
			return engine.View{}, fmt.Errorf(
				"view %q needs %s, and `view` has no way to pass one yet\n\nviews that run without arguments:\n%s",
				id, needed(v), listing(views))
		}
		return v, nil
	}
	return engine.View{}, fmt.Errorf("no view %q in %s\n\nviews that run without arguments:\n%s",
		id, from, listing(views))
}

// needed names the required parameters, so the refusal says what is missing
// rather than only that something is.
func needed(v engine.View) string {
	var names []string
	for _, p := range v.Params {
		if p.Required {
			names = append(names, p.Name)
		}
	}
	return strings.Join(names, ", ")
}

// listing is the ids one can actually ask for, indented for a message.
func listing(views []engine.View) string {
	var out []string
	for _, v := range views {
		if v.Listed() {
			out = append(out, "  "+v.ID)
		}
	}
	if len(out) == 0 {
		return "  (none)"
	}
	return strings.Join(out, "\n")
}

// runView runs it and writes what it produced. Notes — a broken expression in
// one cell — go to stderr: they belong with the diagnostics, not in the table
// someone is piping into a file.
func runView(ctx context.Context, out, errOut io.Writer, v engine.View) error {
	if v.Follows() {
		return followView(ctx, out, v)
	}

	res, err := v.Run(ctx, nil)
	if err != nil {
		return err
	}
	for _, note := range res.Notes {
		// A note that cannot be written changes nothing about the table,
		// which is what was asked for; the write below reports its own.
		_, _ = fmt.Fprintln(errOut, "note: "+note)
	}
	return render.CLI(out, res)
}

// followView streams a followed view's lines as they are written, the way
// `kubectl logs -f` does. It ends when the script does, or when the context
// is cancelled — which is what Ctrl-C does.
func followView(ctx context.Context, out io.Writer, v engine.View) error {
	ch, err := v.Stream(ctx, nil)
	if err != nil {
		return err
	}
	for c := range ch {
		for _, line := range c.Lines {
			if _, err := fmt.Fprintln(out, line); err != nil {
				// A closed pipe — `| head` — ends the stream rather than
				// filling a log nobody reads.
				return err
			}
		}
		if c.End {
			return c.Err
		}
	}
	return nil
}

// completeViews offers the ids a shell can complete to. A hat that does not
// load offers nothing rather than an error: completion is not the place to
// report a broken config.
func completeViews(*cobra.Command, []string, string) ([]cobra.Completion, cobra.ShellCompDirective) {
	views, err := config.Load(hat())
	if err != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	var out []cobra.Completion
	for _, v := range views {
		if v.Listed() {
			out = append(out, cobra.CompletionWithDesc(v.ID, v.Title))
		}
	}
	return out, cobra.ShellCompDirectiveNoFileComp
}
