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

	// Before the unknown keys, not after: a hat too new for this build will
	// have some, and listing them under the one line that actually explains
	// the situation would bury it.
	why, checked := hat.Unmet(version)
	if why != "" {
		return config.Hat{}, fmt.Errorf(
			"%s\n\nupgrade rincewind, or use a revision of %s written for this one", why, path)
	}
	// A build that cannot be measured is not one to refuse — but saying
	// nothing would let one test a hat here, ship it, and hand the refusal to
	// whoever is running a release. The stamp is named rather than
	// interpreted: "dev" and a "-dirty" tree fail to compare for different
	// reasons, and both are legible to whoever is looking at them.
	if !checked {
		say(w, "not checking that this hat wants rincewind >= %s: %s cannot be compared to it\n\n",
			hat.Requires, version)
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
	// The blank line closes this block the way the "not checking" note closes
	// its own, so no caller has to re-derive the spacing.
	say(w, "\nignored %s — a typo, or a hat written for a newer rincewind (this is %s)\n\n",
		engine.Plural(len(warnings), "unknown key"), version)
}
