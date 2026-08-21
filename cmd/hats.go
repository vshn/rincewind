package cmd

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/vshn/rincewind/internal/config"
	"github.com/vshn/rincewind/internal/hats"
)

// hatsDirFlag overrides where the store lives.
var hatsDirFlag string

// parseSource is hats.ParseSource, behind a variable so the install command
// can be exercised against a local repository. Nothing but a test replaces
// it: installing is GitHub only, and that is the point of parsing at all.
var parseSource = hats.ParseSource

// yesFlag skips the questions, for scripts. Every question this command asks
// is about running someone else's code, so the way past them is explicit.
var yesFlag bool

// warning is said once, at install, and is the whole reason installing asks
// at all. A hat is not data.
const warning = "A hat is a directory of scripts that rincewind runs as you, against your\n" +
	"cluster. Install one only from a source you would run a shell script from."

var hatsCmd = &cobra.Command{
	Use:   "hats",
	Short: "Find, install and update hats",
	Long: "Manage the store of installed hats: a directory of git clones.\n\n" +
		"Nothing here resolves a hat by name — `--hat` takes a path, and `hats list`\n" +
		"prints the paths. Which hat runs by default is $RINCEWIND_HAT.",
}

var hatsSearchCmd = &cobra.Command{
	Use:   "search [words]",
	Short: "Find repositories of hats on GitHub",
	Long: "Search GitHub for repositories carrying the " + hats.Topic + " topic.\n\n" +
		"The topic is self-assigned: anyone can put it on a repository, so this is\n" +
		"discovery and not a registry. Read a hat before you install it.\n\n" +
		"Set GITHUB_TOKEN to raise the ten-per-minute limit.",
	Args: cobra.ArbitraryArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		repos, err := hats.Searcher{}.Search(cmd.Context(), strings.Join(args, " "))
		if err != nil {
			return err
		}
		out := cmd.OutOrStdout()
		if len(repos) == 0 {
			say(out, "nothing carries the %s topic yet\n", hats.Topic)
			return nil
		}

		say(out, "\n")
		for _, r := range repos {
			say(out, "  %-34s %5d★  %s\n", r.Name, r.Stars, r.Description)
		}
		say(out, "\nanyone can add the %s topic, so this is discovery and not a registry\n\n  rincewind hats install %s\n",
			hats.Topic, repos[0].Name)
		return nil
	},
}

var hatsInstallCmd = &cobra.Command{
	Use:   "install <owner/repo | github-url>",
	Short: "Clone a repository of hats into the store",
	Long: "Clone a repository into the store and report the hats in it.\n\n" +
		"Cloned over ssh where ssh works, and over https where it does not — which\n" +
		"is how a private repository is installed, since an ssh key is a credential\n" +
		"you already have and this command will not prompt for one. Both urls are\n" +
		"shown before it asks.",
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		src, err := parseSource(args[0])
		if err != nil {
			return err
		}
		store := hats.Store{Dir: hats.DefaultDir(hatsDirFlag)}
		out := cmd.OutOrStdout()

		// Both spellings, in the order they will be tried, from the same
		// function that tries them: a plan worked out a second time could
		// differ from what runs, which is the one thing a plan may not do.
		urls := hats.CloneURLs(cmd.Context(), src)
		if len(urls) == 0 {
			return fmt.Errorf("%s has no url to clone from", src.Slug)
		}
		say(out, "\n  from   %s\n", urls[0])
		for _, url := range urls[1:] {
			say(out, "  else   %s\n", url)
		}
		say(out, "  into   %s\n\n%s\n\n", short(store.Path(src)), warning)

		if !confirm(cmd, "Install?") {
			return nil
		}

		clone, err := store.Clone(cmd.Context(), src)
		if err != nil {
			return err
		}
		say(out, "\n  cloned at %s\n\n", hats.Head(cmd.Context(), clone.Path))

		// The check runs here, not the first time a key is pressed: a hat
		// that does not load must fail while you are installing it.
		if err := report(out, clone); err != nil {
			// Nothing usable came out of it, so nothing is left behind.
			_ = store.Remove(clone)
			return err
		}
		return nil
	},
}

var hatsListCmd = &cobra.Command{
	Use:   "list",
	Short: "Show the installed hats and where they are",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		store := hats.Store{Dir: hats.DefaultDir(hatsDirFlag)}
		clones, err := store.List()
		if err != nil {
			return err
		}
		out := cmd.OutOrStdout()
		if len(clones) == 0 {
			say(out, "nothing installed in %s\n\ntry `rincewind hats search`\n", short(store.Dir))
			return nil
		}

		var last string
		for _, c := range clones {
			say(out, "\n%s @ %s\n", c.Slug, hats.Head(cmd.Context(), c.Path))
			for _, h := range c.Hats {
				say(out, "  %-14s %s\n", h.Name, short(h.Path))
				last = h.Path
			}
			if len(c.Hats) == 0 {
				say(out, "  (no hats found in it)\n")
			}
		}
		if last != "" {
			say(out, "\nrun one with --hat, or make it the default:\n\n  export RINCEWIND_HAT=%s\n", short(last))
		}
		return nil
	},
}

var hatsUpdateCmd = &cobra.Command{
	Use:   "update [repo]",
	Short: "Bring installed hats up to date",
	Long: "Fetch and show what an update would change, then ask before applying it.\n\n" +
		"Without an argument every installed repository is offered. A repository\n" +
		"with local changes is reported and left alone.",
	Args: cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		store := hats.Store{Dir: hats.DefaultDir(hatsDirFlag)}

		var clones []hats.Clone
		if len(args) == 1 {
			c, err := store.Find(args[0])
			if err != nil {
				return err
			}
			clones = []hats.Clone{c}
		} else {
			var err error
			if clones, err = store.List(); err != nil {
				return err
			}
		}

		out := cmd.OutOrStdout()
		for _, c := range clones {
			diff, err := hats.Incoming(cmd.Context(), c.Path)
			if err != nil {
				say(cmd.ErrOrStderr(), "%s: %v\n", c.Slug, err)
				continue
			}
			if diff == "" {
				say(out, "%s is up to date\n", c.Slug)
				continue
			}

			// The diff before the question, for the same reason an action's
			// command is on screen before it runs: this is code that will
			// run against a cluster.
			say(out, "\n%s\n\n%s\n\n", c.Slug, diff)
			if !confirm(cmd, "Apply?") {
				continue
			}
			if err := store.Pull(cmd.Context(), c.Path); err != nil {
				say(cmd.ErrOrStderr(), "%v\n", err)
				continue
			}
			say(out, "  now at %s\n", hats.Head(cmd.Context(), c.Path))
		}
		return nil
	},
}

var hatsRemoveCmd = &cobra.Command{
	Use:   "remove <repo>",
	Short: "Delete an installed repository from the store",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		store := hats.Store{Dir: hats.DefaultDir(hatsDirFlag)}
		c, err := store.Find(args[0])
		if err != nil {
			return err
		}

		say(cmd.OutOrStdout(), "\n  %s\n  %s\n\n", c.Slug, short(c.Path))
		if !confirm(cmd, "Delete?") {
			return nil
		}
		return store.Remove(c)
	},
}

// report loads each hat, which is both the check and the view count. A hat
// that does not load is named rather than left to fail on the first
// keystroke; the others still count, because one broken hat in a repository
// of five is not a reason to have none.
func report(out io.Writer, c hats.Clone) error {
	if len(c.Hats) == 0 {
		return fmt.Errorf("%s holds no hats\n\nexpected a directory of *.yaml under hats/, or *.yaml at the root", c.Slug)
	}

	ok := 0
	for _, h := range c.Hats {
		views, err := config.Load(h.Path)
		if err != nil {
			say(out, "  %-14s does not load: %v\n", h.Name, err)
			continue
		}
		ok++
		say(out, "  %-14s %-10s %s\n", h.Name, plural(len(views), "view"), short(h.Path))
	}
	if ok == 0 {
		return fmt.Errorf("no hat in %s loads", c.Slug)
	}

	say(out, "\nrun one with --hat, or make it the default:\n\n  export RINCEWIND_HAT=%s\n", short(c.Hats[0].Path))
	return nil
}

// say writes one of this command's own lines. The write error is ignored on
// purpose: if stdout has gone away there is nobody left to tell, and the work
// itself reports its failures through the error it returns.
func say(w io.Writer, format string, args ...any) {
	_, _ = fmt.Fprintf(w, format, args...)
}

func plural(n int, what string) string {
	if n == 1 {
		return fmt.Sprintf("1 %s", what)
	}
	return fmt.Sprintf("%d %ss", n, what)
}

// short writes a path the way one would say it. The store lives under the
// home directory, so spelling it out puts the interesting half off the edge
// of a narrow terminal.
func short(path string) string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" || !strings.HasPrefix(path, home+string(filepath.Separator)) {
		return path
	}
	return "~" + path[len(home):]
}

// confirmIn and confirmRd cache one buffered reader per stdin. A fresh
// bufio.Reader per question reads ahead and drops what it buffered, so a
// piped `y\ny\n` answers the first question and leaves the second at EOF.
// The pair is rebuilt when stdin changes, which is what a test does when it
// swaps the reader between runs.
var (
	confirmIn io.Reader
	confirmRd *bufio.Reader
)

// confirm asks, unless --yes said not to. Anything but y is no, and so is a
// closed stdin: a pipe that cannot answer has not agreed to anything.
func confirm(cmd *cobra.Command, question string) bool {
	if yesFlag {
		return true
	}
	say(cmd.OutOrStdout(), "%s [y/N] ", question)

	line, err := reader(cmd.InOrStdin()).ReadString('\n')
	if err != nil && line == "" {
		say(cmd.OutOrStdout(), "\n")
		return false
	}
	return strings.EqualFold(strings.TrimSpace(line), "y")
}

// reader hands out the buffered reader belonging to in, making one the first
// time it is seen.
func reader(in io.Reader) *bufio.Reader {
	if confirmRd == nil || confirmIn != in {
		confirmIn, confirmRd = in, bufio.NewReader(in)
	}
	return confirmRd
}

func init() {
	hatsCmd.PersistentFlags().StringVarP(&hatsDirFlag, "dir", "d", "",
		"directory the hats are installed into (default $"+hats.EnvDir+", else ~/.config/rincewind)")
	hatsCmd.PersistentFlags().BoolVarP(&yesFlag, "yes", "y", false, "do not ask")

	hatsCmd.AddCommand(hatsSearchCmd, hatsInstallCmd, hatsListCmd, hatsUpdateCmd, hatsRemoveCmd)
}
