package cmd

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"

	"github.com/vshn/rincewind/internal/config"
	"github.com/vshn/rincewind/internal/engine"
)

var (
	actParams []string
	actAll    bool
	actYes    bool
)

// act runs a view's action without the TUI. It is the same four movements the
// job tab makes — collect, plan, ask, run — laid out on a terminal instead of
// in a pane, and it goes through the same engine: Bind, Plan, Confirm and
// Step.Run are what both are made of.
//
// Rows are named rather than selected. In the TUI a mutation refuses without
// a selection, because a forgotten filter must not turn one loose on
// everything on screen; out here there is no filter to forget, and a key
// typed on the command line is the most deliberate selection there is. What
// carries over is that it must be deliberate: with no keys and no --all this
// refuses and prints the keys it would have accepted.
var actCmd = &cobra.Command{
	Use:   "act <view> <action> [key…]",
	Short: "Run one of a view's actions",
	Long: "Run a view's action from the command line.\n\n" +
		"The rows it acts on are named by their key — the same key the TUI's\n" +
		"selection is made of. Run it without any to see them. `-` reads them\n" +
		"from stdin instead, which then has nothing left to answer with, so that\n" +
		"form needs --yes.\n\n" +
		"What it is about to run is printed before it asks, one invocation per\n" +
		"row, exactly as the TUI shows it. The plan and the question go to\n" +
		"stderr and only what the scripts printed goes to stdout, so a redirect\n" +
		"catches the results and not the transcript.\n\n" +
		"Parameters are -p name=value. A terminal is asked for whatever is left;\n" +
		"a pipe is not, and a required parameter without a value is refused.",
	Args:              cobra.MinimumNArgs(2),
	ValidArgsFunction: completeAct,
	RunE: func(cmd *cobra.Command, args []string) error {
		path, err := hat()
		if err != nil {
			return err
		}
		views, err := config.Load(path)
		if err != nil {
			return err
		}
		v, err := find(views, args[0], path)
		if err != nil {
			return err
		}
		a, err := action(v, args[1])
		if err != nil {
			return err
		}
		return runAct(cmd, v, a, args[2:])
	},
}

func runAct(cmd *cobra.Command, v engine.View, a engine.Action, keys []string) error {
	out, errOut, in := cmd.OutOrStdout(), cmd.ErrOrStderr(), cmd.InOrStdin()

	// Navigating is looking, and looking is what `view` does. The refusal
	// spells out the command that does it, since the arguments are derived
	// from a row this one is not going to fetch.
	if a.Navigates() {
		return fmt.Errorf("action %s navigates to view %q rather than doing work\n\nprint that view instead:\n  rincewind view %s%s",
			a.Name, a.Open.ID, a.Open.ID, argHint(*a.Open))
	}

	keys, piped, err := actKeys(in, keys)
	if err != nil {
		return err
	}

	args, err := collect(errOut, in, v, a, piped)
	if err != nil {
		return err
	}

	rows, rowKeys, err := actRows(cmd.Context(), errOut, v, a, args, keys)
	if err != nil {
		return err
	}

	steps, full, err := plan(a, rows, rowKeys, args)
	if err != nil {
		return err
	}
	writePlan(errOut, a, steps)

	// The question is asked about the same values the commands were built
	// from — defaults applied — not about what was typed.
	ok, err := agreed(errOut, in, a, rows, full, len(steps))
	if err != nil {
		return err
	}
	if !ok {
		// Declining is a decision, not a failure: nothing ran, and nothing is
		// wrong.
		return nil
	}
	return work(cmd.Context(), out, errOut, steps)
}

// actKeys resolves the row names, reading them from stdin for `-`. That form
// leaves nothing to answer the question with, and a closed stdin is a no, so
// it would refuse every time — better to say why up front than to look like
// it did nothing.
func actKeys(in io.Reader, keys []string) (out []string, piped bool, err error) {
	for _, k := range keys {
		if k != "-" {
			out = append(out, k)
			continue
		}
		if piped {
			return nil, false, fmt.Errorf("- given twice")
		}
		// Both refusals come before anything is read. Without --yes this can
		// only end in one, and reading first would sit on a terminal's stdin
		// until EOF before saying so.
		if !actYes {
			return nil, false, fmt.Errorf(
				"keys read from stdin leave nothing to answer the question with\n\nadd --yes, having read the plan another way")
		}
		if interactive(in) {
			return nil, false, fmt.Errorf(
				"- reads keys from stdin, and stdin is this terminal\n\npipe them in, or name the rows instead")
		}
		piped = true

		text, err := io.ReadAll(reader(in))
		if err != nil {
			return nil, false, fmt.Errorf("reading keys: %w", err)
		}
		for _, line := range strings.Split(string(text), "\n") {
			if line = strings.TrimSpace(line); line != "" {
				out = append(out, line)
			}
		}
	}
	return out, piped, nil
}

// collect gathers the parameters. The view's and the action's share one
// namespace, which config guarantees is free of collisions — an action
// parameter may not shadow one of its view's — so a single -p is unambiguous.
func collect(w io.Writer, in io.Reader, v engine.View, a engine.Action, piped bool) (engine.Args, error) {
	declared := append(append([]engine.Param{}, v.Params...), a.Params...)

	given, err := parseParams(actParams)
	if err != nil {
		return nil, err
	}
	if err := checkDeclared(given, declared, fmt.Sprintf("view %q and action %s together", v.ID, a.Name)); err != nil {
		return nil, err
	}

	// A terminal is asked for the rest, one field at a time, the way the job
	// tab does — the default shown rather than applied invisibly. A pipe is
	// asked nothing: a question nobody can answer is a hang.
	if want := unset(given, declared); len(want) > 0 && !piped && interactive(in) {
		if err := promptParams(w, in, given, want); err != nil {
			return nil, err
		}
	}
	if want := missing(given, declared); len(want) > 0 {
		return nil, fmt.Errorf("%s needs %s\n\npass it with -p name=value\n\nit takes:\n%s",
			a.Name, strings.Join(want, ", "), paramListing(declared))
	}
	return given, nil
}

// actRows runs the view and picks out the rows that were named. A rowless
// action skips all of it: there is nothing for it to look at, so there is no
// reason to run a script to find it.
func actRows(ctx context.Context, errOut io.Writer, v engine.View, a engine.Action, args engine.Args, keys []string) ([]any, []string, error) {
	if !a.NeedsRows() {
		if len(keys) > 0 || actAll {
			return nil, nil, fmt.Errorf("action %s acts on nothing (scope: none), so it takes no rows", a.Name)
		}
		return nil, nil, nil
	}

	// Without a key expression the cells stand in for one, joined by a NUL.
	// The TUI selects with that happily; a person cannot type it.
	if !v.Keyed {
		return nil, nil, fmt.Errorf("view %q declares no key:, so its rows have no name to call them by\n\nadd one to the hat, and %s can act on them here",
			v.ID, a.Name)
	}
	if actAll && len(keys) > 0 {
		return nil, nil, fmt.Errorf("--all and named rows exclude each other")
	}

	viewArgs := engine.Args{}
	for _, p := range v.Params {
		if value, ok := args[p.Name]; ok {
			viewArgs[p.Name] = value
		}
	}
	res, err := v.Run(ctx, viewArgs)
	if err != nil {
		return nil, nil, err
	}
	for _, note := range res.Notes {
		say(errOut, "note: %s\n", note)
	}

	if !actAll && len(keys) == 0 {
		return nil, nil, fmt.Errorf("%s works on rows; name them, or --all\n\nrows of %s:\n%s",
			a.Name, v.ID, keyListing(res))
	}

	want := map[string]bool{}
	for _, k := range keys {
		want[k] = true
	}

	// The view's order, not the order they were typed: the rows arrive as the
	// hat sorted them, exactly as a selection does.
	var rows []any
	var got []string
	found := map[string]bool{}
	for _, r := range res.Rows {
		if r.Src == nil || (!actAll && !want[r.Key]) {
			continue
		}
		rows, got, found[r.Key] = append(rows, r.Src), append(got, r.Key), true
	}

	var unknown []string
	for _, k := range keys {
		if !found[k] {
			unknown = append(unknown, k)
		}
	}
	// Named and not there is a mistake about the world, so nothing runs —
	// not even the rows that were found.
	if len(unknown) > 0 {
		return nil, nil, fmt.Errorf("%s has no row %s\n\nrows of %s:\n%s",
			v.ID, strings.Join(quoted(unknown), ", "), v.ID, keyListing(res))
	}
	if len(rows) == 0 {
		return nil, nil, fmt.Errorf("%s has no rows to act on", v.ID)
	}
	return rows, got, nil
}

// plan turns the rows into the invocations, and labels each with the row it
// is about — the same two movements the job tab makes before it draws them.
func plan(a engine.Action, rows []any, keys []string, args engine.Args) ([]engine.Step, engine.Args, error) {
	bound, err := a.Bind(paramValues(a, args))
	if err != nil {
		return nil, nil, err
	}
	full := engine.Args{}
	for name, value := range args {
		full[name] = value
	}
	for name, value := range bound {
		full[name] = value
	}

	steps, err := a.Plan(rows, full)
	if err != nil {
		return nil, nil, err
	}
	for i := range steps {
		if i < len(keys) {
			steps[i].Row = keys[i]
		}
	}
	return steps, full, nil
}

// paramValues narrows the collected values to the ones the action declares,
// which is what Bind checks against.
func paramValues(a engine.Action, args engine.Args) map[string]string {
	out := map[string]string{}
	for _, p := range a.Params {
		if value, ok := args[p.Name]; ok {
			out[p.Name] = value
		}
	}
	return out
}

// writePlan prints what is about to run, one invocation per row. Not one
// shell-quoted line: the values travel beside the script, never inside it,
// and a quoted one-liner would misrepresent what runs.
func writePlan(w io.Writer, a engine.Action, steps []engine.Step) {
	for i, s := range steps {
		if i > 0 {
			say(w, "\n")
		}
		head := a.Name
		if s.Row != "" {
			head += "  " + s.Row
		}
		say(w, "%s\n", head)
		for _, line := range s.Command() {
			say(w, "%s\n", line)
		}
	}
}

// agreed asks. The hat's own question is used when it wrote one — the engine
// has already appended the count to it — and a plain one otherwise: out here
// nothing was selected by hand, so there is no deliberate act to stand in for
// the question the way pressing a key on chosen rows does in the TUI.
func agreed(w io.Writer, in io.Reader, a engine.Action, rows []any, args engine.Args, steps int) (bool, error) {
	question := "Run " + a.Name + "?"
	if a.NeedsRows() {
		question = fmt.Sprintf("Run %s on %s?", a.Name, engine.Plural(steps, "row"))
	}
	if a.Confirm != nil {
		asked, err := a.Confirm(rows, args)
		if err != nil {
			return false, fmt.Errorf("%s: %v", a.Name, err)
		}
		question = asked
	}

	say(w, "\n")
	if actYes {
		// Said, but not asked. The hat puts its warning in the confirmation —
		// "in-flight requests are dropped" — and --yes is agreement in
		// advance, not a reason never to have been told.
		say(w, "%s\n", question)
		return true, nil
	}
	return ask(w, in, question), nil
}

// work runs the steps in order. One step per row means a failure is
// attributable and the rest still run; what they printed goes to stdout, and
// how it went beside it, so a redirect keeps the results and not the report.
func work(ctx context.Context, out, errOut io.Writer, steps []engine.Step) error {
	failed := 0
	for _, s := range steps {
		lines, err := s.Run(ctx)
		if err != nil {
			failed++
			say(errOut, "%s%v\n", where(s), err)
		}
		for _, line := range lines {
			// A stdout that went away must not abort a run of mutations
			// half-way: the steps are the work, the printing is the report.
			fmt.Fprintln(out, line) //nolint:errcheck
		}
		// Being told to stop leaves the rest unrun rather than pressing on.
		if ctx.Err() != nil {
			return ctx.Err()
		}
	}

	if failed > 0 {
		// The summary is the error, so cobra prints it once and the exit
		// status carries it; the per-step lines above are the detail.
		return fmt.Errorf("%d of %s failed", failed, engine.Plural(len(steps), "step"))
	}
	say(errOut, "%s ok\n", engine.Plural(len(steps), "step"))
	return nil
}

// where names the row a failure belongs to, for a run of many.
func where(s engine.Step) string {
	if s.Row == "" {
		return ""
	}
	return s.Row + ": "
}

// action finds it by name. By name rather than by key: a key is what fits on
// an action bar, where out here there is room for the word, and `act pools
// pause` reads as the sentence it is.
func action(v engine.View, name string) (engine.Action, error) {
	for _, a := range v.Actions {
		if a.Name == name {
			return a, nil
		}
	}
	return engine.Action{}, fmt.Errorf("view %q has no action %q\n\nit offers:\n%s",
		v.ID, name, actionListing(v.Actions))
}

// actionListing spells out what a view can do, and of which kind, so the
// refusal answers the next question too.
func actionListing(actions []engine.Action) string {
	if len(actions) == 0 {
		return "  (none)"
	}
	width := 0
	for _, a := range actions {
		width = max(width, len(a.Name))
	}

	out := make([]string, 0, len(actions))
	for _, a := range actions {
		kind := "works on rows"
		switch {
		case a.Navigates():
			kind = "navigates to " + a.Open.ID
		case a.Rowless:
			kind = "acts on nothing"
		}
		line := fmt.Sprintf("  %-*s  %s", width, a.Name, kind)
		if a.Short != "" {
			line += "  — " + a.Short
		}
		out = append(out, line)
	}
	return strings.Join(out, "\n")
}

// keyListing is the names of the rows a view is showing: what one would have
// named, and what one can paste.
func keyListing(res engine.Result) string {
	if len(res.Rows) == 0 {
		return "  (none)"
	}
	out := make([]string, 0, len(res.Rows))
	for _, r := range res.Rows {
		out = append(out, "  "+r.Key)
	}
	return strings.Join(out, "\n")
}

// argHint spells out the -p a view would need, for a message that hands over
// a command line one can run.
func argHint(v engine.View) string {
	var out []string
	for _, p := range v.Params {
		if p.Required && p.Default == "" {
			out = append(out, " -p "+p.Name+"=…")
		}
	}
	return strings.Join(out, "")
}

// completeAct offers view ids, then that view's action names. Rows are not
// completed: finding them means running the view's script, and completion is
// not the place to run anything against a cluster.
func completeAct(_ *cobra.Command, args []string, _ string) ([]cobra.Completion, cobra.ShellCompDirective) {
	path, err := hat()
	if err != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	views, err := config.Load(path)
	if err != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	if len(args) == 0 {
		var out []cobra.Completion
		for _, v := range views {
			if len(v.Actions) > 0 {
				out = append(out, cobra.CompletionWithDesc(v.ID, v.Title))
			}
		}
		return out, cobra.ShellCompDirectiveNoFileComp
	}

	v, err := find(views, args[0], path)
	if err != nil || len(args) > 1 {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	var out []cobra.Completion
	for _, a := range v.Actions {
		if !a.Navigates() {
			out = append(out, cobra.CompletionWithDesc(a.Name, a.Short))
		}
	}
	return out, cobra.ShellCompDirectiveNoFileComp
}

func init() {
	actCmd.Flags().StringArrayVarP(&actParams, "param", "p", nil,
		"a parameter the view or the action declares, as name=value; repeatable")
	actCmd.Flags().BoolVar(&actAll, "all", false, "act on every row the view shows")
	actCmd.Flags().BoolVarP(&actYes, "yes", "y", false, "do not ask, having read the plan")
}
