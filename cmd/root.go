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

	"github.com/vshn/rincewind/internal/config"
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
		views, err := config.Load(path)
		if err != nil {
			return err
		}
		return tui.Run(views, version)
	},
}

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Print the version",
	Run: func(*cobra.Command, []string) {
		fmt.Println(version)
	},
}

var checkCmd = &cobra.Command{
	Use:   "check",
	Short: "Validate the hat without starting the TUI",
	RunE: func(cmd *cobra.Command, _ []string) error {
		path, err := hat()
		if err != nil {
			return err
		}
		views, err := config.Load(path)
		if err != nil {
			return err
		}
		say(cmd.OutOrStdout(), "%s: %d views ok\n", path, len(views))
		return nil
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
		"  -h <path>                     for one run\n" +
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
	// Cobra hands -h to --help unless the flag already exists, and the hat
	// has the better claim on it: help is asked for once, the hat on every
	// invocation. --help still works, spelled out.
	rootCmd.PersistentFlags().Bool("help", false, "show help")

	rootCmd.PersistentFlags().StringVarP(&hatFlag, "hat", "h", "",
		"directory the views are pulled out of")
	rootCmd.AddCommand(checkCmd, versionCmd, viewCmd, hatsCmd)
}

// Execute runs the root command. Errors are printed by cobra; printing them
// again here would double every message.
//
// Ctrl-C cancels the context rather than killing the process outright, so a
// followed view stops its script and a run in flight is not left behind.
func Execute() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := rootCmd.ExecuteContext(ctx); err != nil {
		os.Exit(1)
	}
}
