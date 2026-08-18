// Package cmd wires the view registry into cobra. Nothing here describes
// what a command does — that lives in the registry, so the CLI, the TUI and
// (later) shell completion cannot drift apart.
package cmd

import (
	"os"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	"github.com/vshn/aldediggi/internal/catalog"
	"github.com/vshn/aldediggi/internal/render"
	"github.com/vshn/aldediggi/internal/tui"
)

// registry is the demo set for now; the declarative loader replaces it.
var registry = catalog.Demo()

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
		return tui.Run(registry)
	},
	// Usage on every runtime error would bury the message under a flag list;
	// cobra still prints it for genuine usage mistakes.
	SilenceUsage: true,
}

func init() {
	cobra.OnInitialize(initConfig)
	addViews(RootCmd, registry)
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
			group.AddCommand(&cobra.Command{
				Use:   v.Name,
				Short: v.Short,
				Args:  cobra.NoArgs,
				RunE: func(cmd *cobra.Command, args []string) error {
					res, err := v.Run(cmd.Context())
					if err != nil {
						return err
					}
					return render.CLI(cmd.OutOrStdout(), res)
				},
			})
		}

		root.AddCommand(group)
	}
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
