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
	Rows    string // jq: one stdout JSON value → row objects
	Key     string // jq: row → identity; empty means no stable identity
	Columns []ColumnDef
	Sort    []SortDef
}

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

	var err error
	if cv.rows, err = compileExpr(d.ID, "rows", d.Rows); err != nil {
		return View{}, err
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

	title := d.Title
	if title == "" {
		title = d.ID
	}
	group, name := "", d.ID
	if i := strings.Index(d.ID, "."); i >= 0 {
		group, name = d.ID[:i], d.ID[i+1:]
	}
	return View{
		ID:      d.ID,
		Name:    name,
		Title:   title,
		Group:   group,
		Refresh: d.Refresh,
		Run:     cv.run,
	}, nil
}

func compileExpr(view, field, src string) (*gojq.Code, error) {
	if src == "" {
		return nil, fmt.Errorf("view %q: %s: expression is required", view, field)
	}
	q, err := gojq.Parse(src)
	if err != nil {
		return nil, fmt.Errorf("view %q: %s: %v", view, field, err)
	}
	code, err := gojq.Compile(q)
	if err != nil {
		return nil, fmt.Errorf("view %q: %s: %v", view, field, err)
	}
	return code, nil
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
func firstValue(ctx context.Context, code *gojq.Code, in any) (any, error) {
	iter := code.RunWithContext(ctx, in)
	v, ok := iter.Next()
	if !ok {
		return nil, fmt.Errorf("expression produced no value")
	}
	if err, isErr := v.(error); isErr {
		return nil, err
	}
	return v, nil
}
