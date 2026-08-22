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

// viewParams are the -p pairs this command was given.
var viewParams []string

var viewCmd = &cobra.Command{
	Use:   "view <id>",
	Short: "Run one view and print its output",
	Long: "Run one view and print what it produces, without the TUI.\n\n" +
		"A table goes out column-aligned, a text view verbatim, and a followed\n" +
		"view streams until its script ends or it is interrupted. Colour is used\n" +
		"only when the output is a terminal, so piping into a file gives plain\n" +
		"text.\n\n" +
		"A view that declares parameters takes them as -p name=value, the way an\n" +
		"action in the TUI supplies them. They reach the script as environment\n" +
		"variables. Nothing is asked for here — this command prints and exits, so\n" +
		"a required parameter without a value is refused by name.",
	Args:              cobra.ExactArgs(1),
	ValidArgsFunction: completeViews,
	RunE: func(cmd *cobra.Command, args []string) error {
		path, err := hat()
		if err != nil {
			return err
		}
		loaded, err := config.Load(path)
		if err != nil {
			return err
		}
		v, err := find(loaded.Views, args[0], path)
		if err != nil {
			return err
		}

		args2, err := parseParams(viewParams)
		if err != nil {
			return err
		}
		if err := checkDeclared(args2, v.Params, fmt.Sprintf("view %q", v.ID)); err != nil {
			return err
		}
		// Nothing is prompted for: this command's output is meant to be piped,
		// and a question on the way there is a hang.
		if want := missing(args2, v.Params); len(want) > 0 {
			return fmt.Errorf("view %q needs %s\n\npass it with -p name=value\n\nit declares:\n%s",
				v.ID, strings.Join(want, ", "), paramListing(v.Params))
		}
		return runView(cmd.Context(), cmd.OutOrStdout(), cmd.ErrOrStderr(), v, args2)
	},
}

// find locates the view by id, naming what else there is when it is not
// there. Every view can be reached now that -p exists, so the listing no
// longer hides the ones that take parameters — it marks them instead.
func find(views []engine.View, id, from string) (engine.View, error) {
	for _, v := range views {
		if v.ID == id {
			return v, nil
		}
	}
	return engine.View{}, fmt.Errorf("no view %q in %s\n\nviews:\n%s", id, from, listing(views))
}

// listing is the ids one can ask for, indented for a message, each with what
// it insists on being given.
func listing(views []engine.View) string {
	var out []string
	for _, v := range views {
		line := "  " + v.ID
		if want := required(v); want != "" {
			line += "  (needs " + want + ")"
		}
		out = append(out, line)
	}
	if len(out) == 0 {
		return "  (none)"
	}
	return strings.Join(out, "\n")
}

// required names a view's required parameters, so a listing says which ones
// need a -p before they will run.
func required(v engine.View) string {
	var names []string
	for _, p := range v.Params {
		if p.Required && p.Default == "" {
			names = append(names, p.Name)
		}
	}
	return strings.Join(names, ", ")
}

// runView runs it and writes what it produced. Notes — a broken expression in
// one cell — go to stderr: they belong with the diagnostics, not in the table
// someone is piping into a file.
func runView(ctx context.Context, out, errOut io.Writer, v engine.View, args engine.Args) error {
	if v.Follows() {
		return followView(ctx, out, v, args)
	}

	res, err := v.Run(ctx, args)
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
func followView(ctx context.Context, out io.Writer, v engine.View, args engine.Args) error {
	ch, err := v.Stream(ctx, args)
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
	path, err := hat()
	if err != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	loaded, err := config.Load(path)
	if err != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	var out []cobra.Completion
	for _, v := range loaded.Views {
		out = append(out, cobra.CompletionWithDesc(v.ID, v.Title))
	}
	return out, cobra.ShellCompDirectiveNoFileComp
}

func init() {
	viewCmd.Flags().StringArrayVarP(&viewParams, "param", "p", nil,
		"a parameter the view declares, as name=value; repeatable")
}
