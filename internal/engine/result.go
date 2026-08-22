// Package engine turns a view definition into a runnable pipeline:
// script → JSON value stream → rows expression → column projection → sort →
// Result. It knows nothing about kubectl or any other producer; a source is
// whatever prints JSON to stdout.
package engine

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"
)

// Emphasis marks a cell as worth noticing. Deciding this in the pipeline
// rather than in the renderers keeps "OutOfSync is bad" in one place, and lets
// every consumer agree without sharing colours.
type Emphasis int

const (
	Plain Emphasis = iota
	Good
	Warn
	Bad
)

// ParseEmphasis reads the value the config file uses.
func ParseEmphasis(s string) (Emphasis, error) {
	switch s {
	case "good":
		return Good, nil
	case "warn":
		return Warn, nil
	case "bad":
		return Bad, nil
	}
	return Plain, fmt.Errorf("%q is not an emphasis; use good, warn or bad", s)
}

// Cell is one rendered table cell. The emphasis is resolved while the table
// is built, so renderers need no lookup of their own.
type Cell struct {
	Text string
	Emph Emphasis
}

// Row is a rendered line plus the object it came from. Anything acting on a
// row later must use Src, never the cells: the cells are shaped for display.
// Key identifies the row across reloads, which is what lets a selection
// survive a refresh that reorders everything.
type Row struct {
	Cells []Cell
	Src   any
	Key   string
}

// Result is what a view produces: rows shaped into columns, plain lines for
// a text view, or series for a chart. Notes carry per-value evaluation
// problems: one broken expression must not take down the other 199 rows.
type Result struct {
	Cols  []string
	Rows  []Row
	Notes []string

	// Lines is a text view's output, verbatim: no columns, no jq, and no
	// collapsing of whitespace, because in a log the indentation is part of
	// what is being read. A result carries exactly one of rows, lines or
	// series.
	Lines []string

	// Series is a chart view's output: named lines over time. YRange is the
	// axis the config asked for; nil scales to the data.
	Series []Series
	YRange *[2]float64
}

// IsText reports which shape this is.
func (r Result) IsText() bool { return r.Lines != nil }

// IsChart reports whether this is a chart view's result.
func (r Result) IsChart() bool { return r.Series != nil }

// Series is one named line of a chart: measurements in time order. The name
// is the legend label, and what the filter matches.
type Series struct {
	Name   string
	Points []Point
}

// Point is one measurement.
type Point struct {
	T time.Time
	V float64
}

// Args are the parameters a view instance was opened with. They reach the
// view's script as environment variables, never as text templated into it:
// a value spliced into code is an injection hole, an environment variable is
// not.
type Args map[string]string

func (a Args) Get(name string) string { return a[name] }

// Param is a value a view needs before it can run. A view with a required
// parameter cannot be started from the menu — it is reached by an action
// that supplies one.
type Param struct {
	Name     string
	Short    string
	Default  string
	Required bool
}

// Action is something a view offers to do with the rows it is showing.
// Exactly one of Open or Run is set: the first navigates to another view,
// the second does work.
type Action struct {
	Key   string
	Name  string
	Short string

	// Open is the view a navigating action moves to, resolved at load time
	// so nothing has to look a view up by id at run time. Args derives its
	// parameters from the row under the cursor.
	Open *View
	Args func(src any, cur Args) (Args, error)

	// Plan turns the selected rows into the invocations the action would
	// make, one per row, *without* running any of them. Producing them up
	// front is what lets the tool show exactly what it is about to do —
	// and lets the question be asked with the command in plain sight.
	Plan    func(rows []any, cur Args) ([]Step, error)
	Confirm func(rows []any, cur Args) (string, error)
	Reload  bool
	// Rowless marks an action that acts on nothing — a create, or one that
	// works from the view's own parameters. It plans exactly one step.
	Rowless bool

	// Params are values the action needs that the view cannot supply, such
	// as the name and dates of something being created. They are asked for
	// before anything is planned.
	Params []Param

	// openID and argNames survive compilation so Link can resolve the target
	// and check the arguments against the parameters it declares.
	openID   string
	argNames []string
}

// Navigates reports which kind of action this is.
func (a Action) Navigates() bool { return a.Open != nil }

// NeedsRows reports whether the action works on the selected rows. One that
// creates something acts on nothing, so demanding a selection from it would
// be asking for rows it will never look at.
func (a Action) NeedsRows() bool { return !a.Navigates() && !a.Rowless }

// Step is one invocation an action will make: one row, the environment it
// gets and the script handed to the shell. Nothing here is interpolated —
// the script is the text from the config, the values travel beside it.
type Step struct {
	Row    string // the row's key, to say which object this is about
	Env    Args
	Shell  []string
	Script string
	Stdin  []byte
}

// Command renders the invocation for reading: the values, the shell, then
// the script as written. It is deliberately not one shell-quoted line —
// that would be a fiction, since the values never enter the script text.
func (s Step) Command() []string {
	return append(s.Values(), s.Invocation()...)
}

// Values renders the environment as two columns, name then value. Aligned
// because a plan of many steps is read downwards: the same variable stands
// in the same place for every row, so what differs between two rows can be
// seen without reading the names again.
//
// len is the right measure here, not lipgloss.Width: a name is held to
// lowercase ASCII when the hat is loaded, and the values are the last
// column, which is never padded.
func (s Step) Values() []string {
	names := make([]string, 0, len(s.Env))
	width := 0
	for name := range s.Env {
		names = append(names, name)
		width = max(width, len(name))
	}
	sort.Strings(names)

	out := make([]string, 0, len(names))
	for _, name := range names {
		line := fmt.Sprintf("  %-*s  %s", width, name, s.Env[name])
		out = append(out, strings.TrimRight(line, " "))
	}
	return out
}

// Invocation renders the shell and the script as written, indented under the
// values that will be handed to it.
func (s Step) Invocation() []string {
	out := []string{"  " + strings.Join(s.Shell, " ")}
	for _, line := range strings.Split(strings.TrimRight(s.Script, "\n"), "\n") {
		out = append(out, "    "+line)
	}
	return out
}

// Run executes one step and returns what it printed.
func (s Step) Run(ctx context.Context) ([]string, error) {
	out, err := runScript(ctx, s.Shell, s.Script, s.Env, s.Stdin)
	if err != nil {
		return nil, err
	}
	var lines []string
	for _, line := range strings.Split(strings.TrimRight(string(out), "\n"), "\n") {
		if line != "" {
			lines = append(lines, line)
		}
	}
	return lines, nil
}

// View is a compiled, runnable view. Run owns the whole pipeline; callers
// impose their own deadline through ctx.
//
// Group and Name are derived from the id: everything before the first dot
// groups the sidebar, the rest is the entry's label. An id without a dot is
// ungrouped and stands as its own label.
type View struct {
	ID      string
	Name    string
	Title   string
	Group   string
	Refresh time.Duration
	// Needs names the contexts this view belongs to: it is hidden only where
	// every one of them is definitively inactive. Empty means always shown.
	Needs   []string
	Params  []Param
	Actions []Action
	Run     func(ctx context.Context, args Args) (Result, error)

	// Keyed says the view declared a `key:` expression, so its rows have
	// names a person could type. Without one the cells stand in, joined by a
	// NUL — which the TUI selects with happily and a command line cannot name
	// at all.
	Keyed bool

	// Stream is set instead of Run on a followed view: its output arrives as
	// it is written rather than when the script exits. Cancelling the context
	// kills the script, which is how one stops following.
	Stream func(ctx context.Context, args Args) (<-chan Chunk, error)
}

// Follows reports whether the view streams rather than running once.
func (v View) Follows() bool { return v.Stream != nil }

// Bind resolves collected values against the declared parameters: the value
// if given, else the default, else an error when the parameter is required.
// One place decides "required and missing".
func (a Action) Bind(values map[string]string) (Args, error) {
	out := Args{}
	for _, p := range a.Params {
		switch v := values[p.Name]; {
		case v != "":
			out[p.Name] = v
		case p.Default != "":
			out[p.Name] = p.Default
		case p.Required:
			return nil, fmt.Errorf("%s needs %s", a.Name, p.Name)
		default:
			out[p.Name] = ""
		}
	}
	return out, nil
}

// Listed reports whether the view can be started from the menu. One that
// needs a parameter cannot: running it with an empty value would show some
// arbitrary object's data as if it were the answer.
func (v View) Listed() bool {
	for _, p := range v.Params {
		if p.Required {
			return false
		}
	}
	return true
}
