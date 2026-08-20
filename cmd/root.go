// Package cmd wires the config loader and the TUI into a binary. It stays
// thin on purpose: everything a view can do is defined in the config file
// and executed by internal/engine.
package cmd

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/vshn/aldediggi/internal/config"
	"github.com/vshn/aldediggi/internal/tui"
)

// hatFlag names the hat: the directory the views are taken out of.
var hatFlag string

// version is stamped by the build (see the Makefile's ldflags); "dev" is
// what a plain `go run .` shows.
var version = "dev"

var rootCmd = &cobra.Command{
	Use:   "aldediggi",
	Short: "Cluster diagnostic TUI driven by a hat full of YAML views",
	Long: "aldediggi renders the views it pulls out of its hat — a directory of\n" +
		"YAML files. Each view runs a script, reads a stream of JSON values from\n" +
		"its stdout and shapes them into a table with jq expressions.",
	SilenceUsage: true,
	RunE: func(cmd *cobra.Command, _ []string) error {
		views, err := config.Load(hat())
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
		path := hat()
		views, err := config.Load(path)
		if err != nil {
			return err
		}
		fmt.Printf("%s: %d views ok\n", path, len(views))
		return nil
	},
}

// hat resolves where the views are pulled from: the flag, then the
// environment, then the conventional place. Every *.yaml in it is read, so
// the views can be kept one file per group.
func hat() string {
	if hatFlag != "" {
		return hatFlag
	}
	if env := os.Getenv("ALDEDIGGI_HAT"); env != "" {
		return env
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "."
	}
	return filepath.Join(home, ".config", "aldediggi")
}

func init() {
	// Cobra hands -h to --help unless the flag already exists, and the hat
	// has the better claim on it: help is asked for once, the hat on every
	// invocation. --help still works, spelled out.
	rootCmd.PersistentFlags().Bool("help", false, "show help")

	rootCmd.PersistentFlags().StringVarP(&hatFlag, "hat", "h", "",
		"directory the views are pulled out of")
	rootCmd.AddCommand(checkCmd, versionCmd)
}

// Execute runs the root command. Errors are printed by cobra; printing them
// again here would double every message.
func Execute() {
	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}
