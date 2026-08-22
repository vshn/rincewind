package engine

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/itchyny/gojq"
)

// Def is a validated, declarative view definition. The config loader builds
// it from YAML; Compile turns it into a runnable View. Splitting the two
// keeps gojq out of the config package and YAML out of this one.
type Def struct {
	ID      string // group and name are derived: everything up to the first dot groups
	Title   string
	Shell   []string // command the script is handed to; defaults to sh -c
	Script  string
	Refresh time.Duration
	// Text takes the script's stdout as lines rather than as JSON, which is
	// what a log is. Rows, Key, Columns and Sort are then all empty.
	Text bool
	// Chart reads the stream as series to draw rather than rows to list.
	// Rows shapes each stream value into series objects; Key, Columns and
	// Sort are empty, as they are for text.
	Chart bool
	// YRange fixes a chart's value axis; nil scales it to the data.
	YRange *[2]float64
	// Follow consumes that output as it is written instead of when the
	// script exits. Only a text view may.
	Follow  bool
	Rows    string // jq: one stdout JSON value → row objects
	Key     string // jq: row → identity; empty means no stable identity
	Params  []Param
	Columns []ColumnDef
	Sort    []SortDef
	Actions []ActionDef
}

// ActionDef declares one action. Exactly one of Open or Script is set.
type ActionDef struct {
	Key   string
	Name  string
	Short string

	// Open names the view a navigating action moves to; Args are jq
	// expressions producing its parameters from the row under the cursor.
	Open string
	Args map[string]string

	// Script is run once per selected row, with Env evaluated against that
	// row. Confirm is a jq expression over the array of selected rows
	// producing the question; empty runs unasked.
	Shell   []string
	Script  string
	Env     map[string]string
	Confirm string
	Reload  bool
	// Scope says what the action works on: ScopeRows, the default, or
	// ScopeNone for one that acts on nothing.
	Scope string
	// Params are asked for before the action is planned.
	Params []Param
}

// What an action's scope may be.
const (
	ScopeRows = "rows"
	ScopeNone = "none"
)

// ColumnDef projects one field of a row object into a cell.
type ColumnDef struct {
	Name   string
	Expr   string              // jq: row → cell value
	Format string              // FormatNone, FormatTimestamp or FormatAge
	Emph   map[string]Emphasis // exact raw value → emphasis
}

// SortDef orders rows by one column. Rank lists values from worst to best
// and replaces the natural order; values not listed sort last.
type SortDef struct {
	By   string
	Desc bool
	Rank []string
}

// Compile checks a definition against everything that can be checked without
// running it — every expression must parse, every sort key must name a
// column — and returns the runnable view. Errors carry the view id and the
// field, because a config mistake must die here, not as a nil somewhere in a
// running pipeline.
func Compile(d Def) (View, error) {
	cv := &compiled{def: d}

	if len(d.Shell) == 0 {
		cv.shell = []string{"sh", "-c"}
	} else {
		cv.shell = d.Shell
	}
	if d.Script == "" {
		return View{}, fmt.Errorf("view %q: source.script is required", d.ID)
	}

	// A text view has no expressions at all: its output is the answer.
	if d.Text {
		v, err := newView(d, cv.runText)
		if err == nil && d.Follow {
			v.Stream, v.Run = cv.runStream, nil
		}
		return v, err
	}

	var err error
	if cv.rows, err = compileExpr(d.ID, "rows", d.Rows); err != nil {
		return View{}, err
	}

	// A chart shapes the stream into series rather than rows; columns and
	// sort do not apply, and the config has already refused them.
	if d.Chart {
		return newView(d, cv.runChart)
	}
	if d.Key != "" {
		if cv.key, err = compileExpr(d.ID, "key", d.Key); err != nil {
			return View{}, err
		}
	}

	for _, c := range d.Columns {
		code, err := compileExpr(d.ID, "column "+c.Name, c.Expr)
		if err != nil {
			return View{}, err
		}
		switch c.Format {
		case FormatNone, FormatTimestamp, FormatAge:
		default:
			return View{}, fmt.Errorf("view %q: column %s: format %q is not one of timestamp, age", d.ID, c.Name, c.Format)
		}
		cv.cols = append(cv.cols, compiledColumn{def: c, code: code})
	}
	if len(cv.cols) == 0 {
		return View{}, fmt.Errorf("view %q: needs at least one column", d.ID)
	}

	for _, s := range d.Sort {
		idx := -1
		for i, c := range d.Columns {
			if c.Name == s.By {
				idx = i
				break
			}
		}
		if idx < 0 {
			return View{}, fmt.Errorf("view %q: sort by %q names no column", d.ID, s.By)
		}
		cv.sort = append(cv.sort, sortKey{col: idx, desc: s.Desc, rank: s.Rank})
	}

	return newView(d, cv.runTable)
}

// newView wraps a run function in everything a view carries besides it: the
// names derived from the id, the parameters, and the compiled actions.
func newView(d Def, run func(context.Context, Args) (Result, error)) (View, error) {
	title := d.Title
	if title == "" {
		title = d.ID
	}
	group, name := "", d.ID
	if i := strings.Index(d.ID, "."); i >= 0 {
		group, name = d.ID[:i], d.ID[i+1:]
	}
	actions, err := compileActions(d)
	if err != nil {
		return View{}, err
	}

	return View{
		ID:      d.ID,
		Name:    name,
		Title:   title,
		Group:   group,
		Refresh: d.Refresh,
		Params:  d.Params,
		Actions: actions,
		Keyed:   d.Key != "",
		Run:     run,
	}, nil
}

// argsVar is the name under which a view's current parameters are exposed to
// action expressions. It is what makes "the view's parameter, else the row"
// sayable in config: `$args.node // .metadata.name`.
const argsVar = "$args"

func compileExpr(view, field, src string) (*gojq.Code, error) {
	return compileExprIn("view", view, field, src)
}

// compileExprIn is that for whatever declared the expression. A context is
// not a view and must not be named as one, but every other word of the
// message — and every rule about when it is written — is the same.
func compileExprIn(kind, name, field, src string) (*gojq.Code, error) {
	if src == "" {
		return nil, fmt.Errorf("%s %q: %s: expression is required", kind, name, field)
	}
	q, err := gojq.Parse(src)
	if err != nil {
		return nil, fmt.Errorf("%s %q: %s: %v", kind, name, field, err)
	}
	code, err := gojq.Compile(q, gojq.WithVariables([]string{argsVar}))
	if err != nil {
		return nil, fmt.Errorf("%s %q: %s: %v", kind, name, field, err)
	}
	return code, nil
}

// argsValue renders the current parameters the way a jq expression sees
// them: a plain object, so `$args.node` works and a missing one is null.
func argsValue(cur Args) any {
	out := map[string]any{}
	for k, v := range cur {
		out[k] = v
	}
	return out
}

// env is what the view's script sees. Every declared parameter is set, empty
// if it was not given, so a script can test it with [ -n "$x" ] rather than
// having to guess whether the variable exists at all.
func (cv *compiled) env(args Args) Args {
	out := Args{}
	for _, p := range cv.def.Params {
		value := args[p.Name]
		if value == "" {
			value = p.Default
		}
		out[p.Name] = value
	}
	return out
}

// compiled is the runnable form: every expression parsed, every sort key
// resolved to a column index.
type compiled struct {
	def   Def
	shell []string
	rows  *gojq.Code
	key   *gojq.Code
	cols  []compiledColumn
	sort  []sortKey
}

type compiledColumn struct {
	def  ColumnDef
	code *gojq.Code
}

type sortKey struct {
	col  int
	desc bool
	rank []string
}

// firstValue runs a compiled expression against one input and returns the
// first result. A query yielding nothing is an error: a column that silently
// produces no value would be indistinguishable from an empty one.
//
// Every expression is compiled with the same one variable, so every run must
// hand it the current parameters — see argsVar.
func firstValue(ctx context.Context, code *gojq.Code, in any, cur Args) (any, error) {
	iter := code.RunWithContext(ctx, in, argsValue(cur))
	v, ok := iter.Next()
	if !ok {
		return nil, fmt.Errorf("expression produced no value")
	}
	if err, isErr := v.(error); isErr {
		return nil, err
	}
	return v, nil
}
