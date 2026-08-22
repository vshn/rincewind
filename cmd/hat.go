package cmd

import (
	"fmt"
	"io"

	"github.com/vshn/rincewind/internal/config"
	"github.com/vshn/rincewind/internal/engine"
)

// loadHat reads the hat and reports whatever this build did not understand.
// Every command that runs a hat goes through here, so none of them can
// quietly skip the report — and there is one place to hold the rule that a
// key from the future is news rather than a failure.
//
// The interpretation lives here rather than in config, because it is the
// binary that knows which version it is and therefore what "did not
// understand" is likely to mean.
func loadHat(w io.Writer, path string) (config.Hat, error) {
	hat, err := config.Load(path)
	if err != nil {
		return config.Hat{}, err
	}
	sayUnknown(w, hat.Warnings)
	return hat, nil
}

// sayUnknown lists the keys that were ignored, and says in one line what
// having ignored them probably means. A hat travels separately from the
// binary, so "you are older than this hat" is the common case and the one
// worth naming: the alternative reading, a typo, is the hat author's to
// catch with `check --strict`.
func sayUnknown(w io.Writer, warnings []string) {
	if len(warnings) == 0 {
		return
	}
	for _, warning := range warnings {
		say(w, "%s\n", warning)
	}
	say(w, "\nignored %s — a typo, or a hat written for a newer rincewind (this is %s)\n",
		engine.Plural(len(warnings), "unknown key"), version)
}

// unknownNote is the same thing as an error, for `check --strict`.
func unknownNote(warnings []string) error {
	if len(warnings) == 0 {
		return nil
	}
	return fmt.Errorf("%s", engine.Plural(len(warnings), "unknown key"))
}
