// Package cmd wires the config loader and the TUI into a binary. It stays
// thin on purpose: everything a view can do is defined in the config file
// and executed by internal/engine.
package cmd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/vshn/rincewind/internal/hats"
	"github.com/vshn/rincewind/internal/tui"
)

// hatFlag names the hat: the directory the views are taken out of.
var hatFlag string

// envHat is the hat used when the flag is absent. There is no other default:
// no config file, no symlink, no conventional directory that doubles as a
// hat. One variable, which the shell already knows how to scope to a project
// or a terminal.
const envHat = "RINCEWIND_HAT"

// version is stamped by the build (see the Makefile's ldflags); "dev" is
// what a plain `go run .` shows.
var version = "dev"

var rootCmd = &cobra.Command{
	Use:   "rincewind",
	Short: "Cluster diagnostic TUI driven by a hat full of YAML views",
	Long: "rincewind renders the views it pulls out of its hat — a directory of\n" +
		"YAML files. Each view runs a script, reads a stream of JSON values from\n" +
		"its stdout and shapes them into a table with jq expressions.",
	SilenceUsage: true,
	RunE: func(cmd *cobra.Command, _ []string) error {
		path, err := hat()
		if err != nil {
			return err
		}
		loaded, err := loadHat(cmd.ErrOrStderr(), path)
		if err != nil {
			return err
		}
		return tui.Run(cmd.Context(), loaded.Views, loaded.Contexts, version)
	},
}

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Print the version",
	Run: func(*cobra.Command, []string) {
		fmt.Println(version)
	},
}

// hat resolves where the views are pulled from: the flag, then the
// environment, and then nothing. Every *.yaml in it is read, so the views
// can be kept one file per group.
//
// There is deliberately no fallback. ~/.config/rincewind used to be a hat
// itself and is now the store, which cannot be both: a directory of hats is
// not a directory of views. Guessing one of the installed hats would be
// worse than asking, since which cluster one is pointed at is exactly the
// thing this tool should not decide quietly.
func hat() (string, error) {
	if hatFlag != "" {
		return hatFlag, nil
	}
	if env := os.Getenv(envHat); env != "" {
		return env, nil
	}
	return "", noHat()
}

// noHat is the first thing a new user sees, so it does the work: it names
// both ways to choose one and, if anything is installed, the line to paste.
func noHat() error {
	var b strings.Builder
	b.WriteString("no hat chosen\n\n" +
		"  --hat <path>                    for one run\n" +
		"  export " + envHat + "=<path>   for every run\n")

	store := hats.Store{Dir: hats.DefaultDir("")}
	clones, err := store.List()
	if err != nil || len(clones) == 0 {
		b.WriteString("\nnothing is installed yet:\n\n  rincewind hats search\n  rincewind hats install <owner/repo>\n")
		return errors.New(b.String())
	}

	b.WriteString("\ninstalled:\n")
	for _, c := range clones {
		for _, h := range c.Hats {
			fmt.Fprintf(&b, "  %s\n", short(h.Path))
		}
	}
	return errors.New(b.String())
}

func init() {
	// --hat has no short form: -h is help everywhere, and a tool that takes
	// it for something else costs every newcomer the same moment of
	// confusion. The hat is named rarely anyway, now that $RINCEWIND_HAT is
	// how one is chosen for good.
	rootCmd.PersistentFlags().StringVar(&hatFlag, "hat", "",
		"directory the views are pulled out of (default $"+envHat+")")
	rootCmd.AddCommand(checkCmd, versionCmd, viewCmd, actCmd, hatsCmd)
}

// Execute runs the root command. Errors are printed by cobra; printing them
// again here would double every message.
//
// A signal cancels the context rather than killing the process outright, so
// the TUI comes back from bubbletea and stops the scripts it had running.
// Ctrl-C inside the TUI is not one of these: the terminal is in raw mode, so
// it arrives as a keystroke — this is the path a SIGTERM takes, and the one
// a Ctrl-C takes when there is no terminal to be raw.
func Execute() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := rootCmd.ExecuteContext(ctx); err != nil {
		os.Exit(1)
	}
}
