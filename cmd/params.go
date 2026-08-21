package cmd

import (
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	"github.com/vshn/rincewind/internal/engine"
)

// Parameters out here are `-p name=value`, repeated, rather than a flag per
// parameter. A generated flag would have to be declared before the hat is
// read, and it would put the hat's vocabulary into this tool's usage line:
// these are the hat's parameters, not rincewind's, and `-p` says so.
//
// They reach the script as environment variables, exactly as in the TUI —
// never templated into it.

// parseParams reads the pairs. A value may hold anything, `=` included, so
// only the first one separates.
func parseParams(pairs []string) (engine.Args, error) {
	out := engine.Args{}
	for _, pair := range pairs {
		name, value, ok := strings.Cut(pair, "=")
		if !ok || name == "" {
			return nil, fmt.Errorf("--param %q is not name=value", pair)
		}
		// Presence, not the value: `-p x=` is given, and a second x after it
		// is still the same mistake.
		if _, dup := out[name]; dup {
			return nil, fmt.Errorf("--param %s given twice", name)
		}
		out[name] = value
	}
	return out, nil
}

// checkDeclared refuses a value nothing asked for. A silently ignored
// parameter is the mistake this catches: the name is a typo, and the run
// would otherwise go ahead on the default.
func checkDeclared(given engine.Args, declared []engine.Param, what string) error {
	known := map[string]bool{}
	for _, p := range declared {
		known[p.Name] = true
	}

	var unknown []string
	for name := range given {
		if !known[name] {
			unknown = append(unknown, name)
		}
	}
	if len(unknown) == 0 {
		return nil
	}
	sort.Strings(unknown) // stable messages

	return fmt.Errorf("%s does not declare %s\n\nit declares:\n%s",
		what, strings.Join(quoted(unknown), ", "), paramListing(declared))
}

// unset is what -p did not supply, in declaration order: the fields to ask
// for when there is someone to ask.
func unset(given engine.Args, declared []engine.Param) []engine.Param {
	var out []engine.Param
	for _, p := range declared {
		if _, ok := given[p.Name]; !ok {
			out = append(out, p)
		}
	}
	return out
}

// missing names the required parameters that have neither a value nor a
// default — the ones that make running impossible rather than merely bare.
func missing(given engine.Args, declared []engine.Param) []string {
	var out []string
	for _, p := range declared {
		if p.Required && given[p.Name] == "" && p.Default == "" {
			out = append(out, p.Name)
		}
	}
	return out
}

// promptParams collects the fields one at a time, the way the job tab does:
// the default is shown rather than applied invisibly, so plain enter takes it
// and one can see what was taken.
//
// It writes to w rather than to stdout because what this collects is
// interaction, not output — see the note on `act`.
func promptParams(w io.Writer, in io.Reader, given engine.Args, params []engine.Param) error {
	for _, p := range params {
		label := p.Name
		if p.Short != "" {
			label += " (" + p.Short + ")"
		}
		if p.Default != "" {
			label += " [" + p.Default + "]"
		}
		say(w, "%s: ", label)

		line, err := reader(in).ReadString('\n')
		value := strings.TrimSpace(line)
		if value == "" {
			value = p.Default
		}
		if value == "" && p.Required {
			say(w, "\n")
			return fmt.Errorf("%s is required", p.Name)
		}
		given[p.Name] = value

		// A closed stdin answers nothing further; the loop would spin through
		// the rest taking defaults it was never offered.
		if err != nil && line == "" {
			say(w, "\n")
			return nil
		}
	}
	return nil
}

// interactive reports whether there is a person on the other end of in. Asked
// of the reader, the way Colour is asked of the writer: what matters is
// whether this invocation can hold a conversation, not which OS it is on.
func interactive(in io.Reader) bool {
	f, ok := in.(*os.File)
	if !ok {
		return false
	}
	info, err := f.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}

// paramListing spells out what a view or an action takes, indented for a
// message, so a refusal names what would work.
func paramListing(params []engine.Param) string {
	if len(params) == 0 {
		return "  (none)"
	}
	out := make([]string, 0, len(params))
	for _, p := range params {
		line := "  " + p.Name
		switch {
		case p.Required:
			line += "  (required)"
		case p.Default != "":
			line += "  [" + p.Default + "]"
		}
		if p.Short != "" {
			line += "  " + p.Short
		}
		out = append(out, line)
	}
	return strings.Join(out, "\n")
}

func quoted(names []string) []string {
	out := make([]string, len(names))
	for i, n := range names {
		out[i] = fmt.Sprintf("%q", n)
	}
	return out
}
