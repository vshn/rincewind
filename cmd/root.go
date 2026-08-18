// Package cmd wires the view registry into cobra. Nothing here describes what
// a view does — that lives in the registry, so the CLI, the TUI and shell
// completion cannot drift apart.
package cmd

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	"github.com/vshn/aldediggi/internal/catalog"
	"github.com/vshn/aldediggi/internal/kube"
	"github.com/vshn/aldediggi/internal/render"
	"github.com/vshn/aldediggi/internal/tui"
)

const (
	flagAction = "action"
	flagYes    = "yes"
)

// RootCmd starts the TUI when invoked bare, and otherwise dispatches to the
// generated subcommands.
var RootCmd = &cobra.Command{
	Use:   "aldediggi",
	Short: "Cluster diagnostic helper",
	Long: "Cluster diagnostic helper.\n\n" +
		"Run without a subcommand to browse everything interactively; run a\n" +
		"subcommand to print just its output.",
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		return tui.Run(registry(), kube.Client{}.CurrentContext)
	},
	// Usage on every runtime error would bury the message under a flag list;
	// cobra still prints it for genuine usage mistakes.
	SilenceUsage: true,
}

// registry is built once; Navigate needs to look views up by id at run time.
var registry = sync.OnceValue(buildRegistry)

// buildRegistry is called lazily so that config is in place first.
func buildRegistry() *catalog.Registry {
	client := kube.Client{}

	reg := catalog.NewRegistry()
	reg.Merge(catalog.Argo(client))
	reg.Merge(catalog.Upgrade(client))
	reg.Merge(catalog.Nodes(client))
	reg.Merge(catalog.Alerts(client))
	return reg
}

func init() {
	cobra.OnInitialize(initConfig)
	initConfig() // registry construction below already needs the defaults
	addViews(RootCmd, registry())
}

// addViews turns every registry entry into a `<group> <name>` subcommand.
func addViews(root *cobra.Command, reg *catalog.Registry) {
	for _, name := range reg.Groups() {
		group := &cobra.Command{
			Use:   name,
			Short: reg.GroupShort(name),
			// NoArgs so that a mistyped view is a usage error rather than a
			// silent success that only prints help.
			Args: cobra.NoArgs,
			// A group on its own does nothing; showing its views beats
			// printing success while having done no work.
			RunE: func(cmd *cobra.Command, args []string) error {
				return cmd.Help()
			},
		}

		for _, v := range reg.InGroup(name) {
			group.AddCommand(viewCmd(v))
		}

		root.AddCommand(group)
	}
}

func viewCmd(v catalog.View) *cobra.Command {
	cmd := &cobra.Command{
		Use:   useLine(v),
		Short: v.Short,
		Args:  cobra.MaximumNArgs(len(v.Params)),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runView(cmd, v, args)
		},
		ValidArgsFunction: completeParams(v),
	}

	if len(v.Actions) > 0 {
		cmd.Flags().String(flagAction, "", "run an action on this view: "+strings.Join(v.ActionNames(), ", "))
		cmd.Flags().BoolP(flagYes, "y", false, "skip the confirmation prompt")

		// An action's own parameters become flags. Two actions of one view
		// must not claim the same name; pflag would panic, so say what is
		// wrong instead.
		claimed := map[string]string{}
		for _, a := range v.Actions {
			for _, p := range a.Params {
				if owner, taken := claimed[p.Name]; taken {
					panic(fmt.Sprintf("%s: actions %q and %q both define --%s", v.ID(), owner, a.Name, p.Name))
				}
				claimed[p.Name] = a.Name
				cmd.Flags().String(p.Name, p.Default, fmt.Sprintf("%s (--action %s)", p.Short, a.Name))
			}
		}

		// Completion for the flag's values, so actions are as discoverable on
		// the command line as they are in the TUI.
		names := v.ActionNames()
		_ = cmd.RegisterFlagCompletionFunc(flagAction,
			func(*cobra.Command, []string, string) ([]string, cobra.ShellCompDirective) {
				return names, cobra.ShellCompDirectiveNoFileComp
			})
	}

	return cmd
}

// useLine spells out the parameters, so `--help` shows `syncstatus APP`.
func useLine(v catalog.View) string {
	parts := []string{v.Name}
	for _, p := range v.Params {
		if p.Required {
			parts = append(parts, strings.ToUpper(p.Name))
		} else {
			parts = append(parts, "["+strings.ToUpper(p.Name)+"]")
		}
	}
	return strings.Join(parts, " ")
}

func completeParams(v catalog.View) func(*cobra.Command, []string, string) ([]string, cobra.ShellCompDirective) {
	return func(cmd *cobra.Command, args []string, _ string) ([]string, cobra.ShellCompDirective) {
		if len(args) >= len(v.Params) {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		p := v.Params[len(args)]
		if p.Complete == nil {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		out, err := p.Complete(cmd.Context())
		if err != nil {
			return nil, cobra.ShellCompDirectiveError
		}
		return out, cobra.ShellCompDirectiveNoFileComp
	}
}

func runView(cmd *cobra.Command, v catalog.View, args []string) error {
	if v.Available != nil {
		if err := v.Available(); err != nil {
			return err
		}
	}

	bound, err := v.Bind(args)
	if err != nil {
		return err
	}

	res, err := v.Run(cmd.Context(), bound)
	if err != nil {
		return err
	}

	name, _ := cmd.Flags().GetString(flagAction)
	if name == "" {
		return render.CLI(cmd.OutOrStdout(), res)
	}

	action, ok := v.Lookup(name)
	if !ok {
		return fmt.Errorf("unknown action %q for %s; available: %s",
			name, v.ID(), strings.Join(v.ActionNames(), ", "))
	}

	// The action's own parameters win over the view's: they are the more
	// specific answer to the same name.
	values := map[string]string{}
	for _, p := range action.Params {
		values[p.Name], _ = cmd.Flags().GetString(p.Name)
	}
	extra, err := action.Bind(values)
	if err != nil {
		return err
	}
	for k, val := range extra {
		bound[k] = val
	}

	// The view's output is the action's input, so print it first: you see what
	// you are about to act on before you are asked to confirm.
	if err := render.CLI(cmd.OutOrStdout(), res); err != nil {
		return err
	}

	yes, _ := cmd.Flags().GetBool(flagYes)
	if action.Confirm != nil {
		if prompt := action.Confirm(res, bound); prompt != "" {
			// Fetched here rather than at startup: it costs a subprocess, and
			// only a mutation is worth spending it on.
			cluster, _ := kube.Client{}.CurrentContext(cmd.Context())
			prompt = catalog.Prompt(prompt, cluster)

			// --yes skips the asking, not the telling. A warning such as
			// "root is not in this set" is the reason to show it at all.
			if yes {
				_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "\n%s\n", prompt)
			} else {
				ok, err := confirm(cmd, prompt)
				if err != nil {
					return err
				}
				if !ok {
					return errors.New("aborted")
				}
			}
		}
	}

	out, err := action.Run(cmd.Context(), res, bound)
	if err != nil {
		return err
	}
	if out == nil {
		return nil
	}

	// An action that navigates is the CLI's drill-down: run the target and
	// print it, which is what the TUI shows after pushing it on the stack.
	if to, ok := out.(catalog.Navigate); ok {
		return runNavigated(cmd, to)
	}
	return render.CLI(cmd.OutOrStdout(), out)
}

func runNavigated(cmd *cobra.Command, to catalog.Navigate) error {
	next, ok := registry().Get(to.View)
	if !ok {
		return fmt.Errorf("no such view: %s", to.View)
	}

	res, err := next.Run(cmd.Context(), to.Args)
	if err != nil {
		return err
	}
	return render.CLI(cmd.OutOrStdout(), res)
}

// confirm asks on stderr, so that piping stdout to a file still shows the
// question. A non-interactive stdin is a refusal, not a silent yes.
func confirm(cmd *cobra.Command, prompt string) (bool, error) {
	in, ok := cmd.InOrStdin().(*os.File)
	if !ok {
		return false, errors.New("cannot ask for confirmation here; pass --yes")
	}
	info, err := in.Stat()
	if err != nil || info.Mode()&os.ModeCharDevice == 0 {
		return false, errors.New("not a terminal; pass --yes to confirm")
	}

	_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "\n%s [y/N] ", prompt)

	line, err := bufio.NewReader(in).ReadString('\n')
	if err != nil {
		// /dev/null passes the character-device test above but answers
		// nothing. Say so rather than reporting a bare "aborted".
		return false, errors.New("no answer on stdin; pass --yes to confirm")
	}
	answer := strings.ToLower(strings.TrimSpace(line))
	return answer == "y" || answer == "yes", nil
}

// initConfig wires the environment side of the vocabulary.
func initConfig() {
	// Prefixed, so the bound keys claim ALDEDIGGI_* rather than bare names.
	viper.SetEnvPrefix("ALDEDIGGI")
	viper.SetEnvKeyReplacer(strings.NewReplacer(".", "_", "-", "_"))
	viper.AutomaticEnv()
}

// Execute adds all child commands to the root command and sets flags appropriately.
// This is called by main.main(). It only needs to happen once to the rootCmd.
func Execute() {
	// Cobra has already reported the error on stderr by the time it comes back;
	// printing it again here produced every failure twice. All that is left to
	// do is fail the exit status.
	if err := RootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}
