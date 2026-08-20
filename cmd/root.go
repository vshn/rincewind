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

var cfgFlag string

// version is stamped by the build (see the Makefile's ldflags); "dev" is
// what a plain `go run .` shows.
var version = "dev"

var rootCmd = &cobra.Command{
	Use:   "aldediggi",
	Short: "Cluster diagnostic TUI driven by a YAML view config",
	Long: "aldediggi renders views defined in a YAML config file: each view runs a\n" +
		"script, reads a stream of JSON values from its stdout and shapes them\n" +
		"into a table with jq expressions.",
	SilenceUsage: true,
	RunE: func(cmd *cobra.Command, _ []string) error {
		views, err := config.Load(configPath())
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
	Short: "Validate the config file without starting the TUI",
	RunE: func(cmd *cobra.Command, _ []string) error {
		path := configPath()
		views, err := config.Load(path)
		if err != nil {
			return err
		}
		fmt.Printf("%s: %d views ok\n", path, len(views))
		return nil
	},
}

// configPath resolves where the views come from: the flag, then the
// environment, then the conventional place.
func configPath() string {
	if cfgFlag != "" {
		return cfgFlag
	}
	if env := os.Getenv("ALDEDIGGI_CONFIG"); env != "" {
		return env
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "config.yaml"
	}
	// The directory, not a file in it: loading a directory reads every
	// *.yaml in it, so both a single config.yaml and one file per group work
	// without the user having to say which they chose.
	return filepath.Join(home, ".config", "aldediggi")
}

func init() {
	rootCmd.PersistentFlags().StringVarP(&cfgFlag, "config", "c", "", "path to the view config file")
	rootCmd.AddCommand(checkCmd, versionCmd)
}

// Execute runs the root command. Errors are printed by cobra; printing them
// again here would double every message.
func Execute() {
	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}
