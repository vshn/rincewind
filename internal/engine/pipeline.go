package engine

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"sort"
	"strings"
	"time"
)

// run is the whole pipeline for one view. Sorting happens on the raw
// expression values, before formatting: an RFC 3339 timestamp orders
// chronologically as a string, a number orders numerically — the rendered
// cell would order neither.
func (cv *compiled) run(ctx context.Context, args Args) (Table, error) {
	env := cv.env(args)
	out, err := runScript(ctx, cv.shell, cv.def.Script, env, nil)
	if err != nil {
		return Table{}, err
	}

	docs, err := decodeStream(out)
	if err != nil {
		return Table{}, err
	}

	raws, err := cv.extract(ctx, docs, args)
	if err != nil {
		return Table{}, err
	}

	sort.SliceStable(raws, func(i, j int) bool { return cv.less(raws[i], raws[j]) })

	return cv.render(ctx, raws, args, time.Now()), nil
}

// rawRow is a row between extraction and rendering: the source object, the
// evaluated column values, and any per-column evaluation failure.
type rawRow struct {
	src  any
	vals []any
	bad  []error
}

// decodeStream reads stdout as a stream of JSON values — several
// concatenated documents are how a script feeds more than one source into
// one view. The rows expression runs once per value, jq-style.
func decodeStream(out []byte) ([]any, error) {
	dec := json.NewDecoder(bytes.NewReader(out))
	var docs []any
	for {
		var v any
		err := dec.Decode(&v)
		if errors.Is(err, io.EOF) {
			return docs, nil
		}
		if err != nil {
			return nil, fmt.Errorf("stdout is not a JSON stream: %v near %q", err, near(out, dec.InputOffset()))
		}
		docs = append(docs, v)
	}
}

// near shows where in the output the decoder gave up, because "invalid
// character" without context is useless against a pipeline of commands.
func near(out []byte, offset int64) string {
	start := max(int(offset)-10, 0)
	end := min(start+60, len(out))
	return oneLine(string(out[start:end]))
}

// extract runs the rows expression over every stream value. A failure here is
// fatal for the view: without rows there is nothing to salvage per cell.
func (cv *compiled) extract(ctx context.Context, docs []any, args Args) ([]rawRow, error) {
	var raws []rawRow
	for _, doc := range docs {
		iter := cv.rows.RunWithContext(ctx, doc, argsValue(args))
		for {
			v, ok := iter.Next()
			if !ok {
				break
			}
			if err, isErr := v.(error); isErr {
				return nil, fmt.Errorf("rows: %v", err)
			}
			row := rawRow{src: v, vals: make([]any, len(cv.cols)), bad: make([]error, len(cv.cols))}
			for i, col := range cv.cols {
				row.vals[i], row.bad[i] = firstValue(ctx, col.code, v, args)
			}
			raws = append(raws, row)
		}
	}
	return raws, nil
}

// render shapes the sorted raw rows into cells. Emphasis is matched against
// the raw value, before formatting, so a rule targets the data rather than
// however the cell happens to be displayed.
func (cv *compiled) render(ctx context.Context, raws []rawRow, args Args, now time.Time) Table {
	t := Table{Cols: make([]string, len(cv.cols))}
	for i, col := range cv.cols {
		t.Cols[i] = col.def.Name
	}

	// One note per broken column, not one per row: the first failure says
	// what is wrong, hundreds of copies of it say nothing more.
	noted := map[string]bool{}

	for _, raw := range raws {
		row := Row{Cells: make([]Cell, len(cv.cols)), Src: raw.src}
		for i, col := range cv.cols {
			if err := raw.bad[i]; err != nil {
				row.Cells[i] = Cell{Text: "!err"}
				if !noted[col.def.Name] {
					noted[col.def.Name] = true
					t.Notes = append(t.Notes, fmt.Sprintf("column %s: %v", col.def.Name, err))
				}
				continue
			}
			text := valueText(raw.vals[i])
			row.Cells[i] = Cell{
				Text: oneLine(formatCell(text, col.def.Format, now)),
				Emph: col.def.Emph[text],
			}
		}
		row.Key = cv.keyOf(ctx, raw.src, row.Cells, args)
		t.Rows = append(t.Rows, row)
	}
	return t
}

// keyOf names a row for selection purposes. Without a key expression the
// cells have to do, which means a selection may not survive a reload that
// changes what is displayed.
func (cv *compiled) keyOf(ctx context.Context, src any, cells []Cell, args Args) string {
	if cv.key != nil {
		if v, err := firstValue(ctx, cv.key, src, args); err == nil {
			return valueText(v)
		}
	}
	parts := make([]string, len(cells))
	for i, c := range cells {
		parts[i] = c.Text
	}
	return strings.Join(parts, "\x00")
}

// valueText renders one jq result as cell text. Composites become compact
// JSON: showing the structure beats hiding it behind a type name.
func valueText(v any) string {
	switch t := v.(type) {
	case nil:
		return ""
	case string:
		return t
	case bool:
		if t {
			return "true"
		}
		return "false"
	default:
		b, err := json.Marshal(v)
		if err != nil {
			return fmt.Sprintf("%v", v)
		}
		return string(b)
	}
}

func (cv *compiled) less(a, b rawRow) bool {
	for _, k := range cv.sort {
		var c int
		if len(k.rank) > 0 {
			c = cmp.Compare(rankIndex(k.rank, valueText(a.vals[k.col])), rankIndex(k.rank, valueText(b.vals[k.col])))
		} else {
			c = compareValues(a.vals[k.col], b.vals[k.col])
			if k.desc {
				c = -c
			}
		}
		if c != 0 {
			return c < 0
		}
	}
	return false
}

// rankIndex places a value in an explicit worst-to-best order; anything not
// listed sorts after everything that is.
func rankIndex(rank []string, v string) int {
	for i, r := range rank {
		if r == v {
			return i
		}
	}
	return len(rank)
}

// compareValues orders two raw jq values: numbers numerically, strings
// lexically, mixed types by kind so a sort never panics on ragged data.
func compareValues(a, b any) int {
	ka, kb := kindOf(a), kindOf(b)
	if ka != kb {
		return cmp.Compare(ka, kb)
	}
	switch ka {
	case kindBool:
		return cmp.Compare(boolInt(a.(bool)), boolInt(b.(bool)))
	case kindNumber:
		return cmp.Compare(toFloat(a), toFloat(b))
	case kindString:
		return strings.Compare(a.(string), b.(string))
	default: // nil or composite; composites compare as their JSON text
		return strings.Compare(valueText(a), valueText(b))
	}
}

const (
	kindNull = iota
	kindBool
	kindNumber
	kindString
	kindOther
)

func kindOf(v any) int {
	switch v.(type) {
	case nil:
		return kindNull
	case bool:
		return kindBool
	case int, float64, *big.Int:
		return kindNumber
	case string:
		return kindString
	default:
		return kindOther
	}
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

func toFloat(v any) float64 {
	switch t := v.(type) {
	case int:
		return float64(t)
	case float64:
		return t
	case *big.Int:
		f, _ := new(big.Float).SetInt(t).Float64()
		return f
	}
	return 0
}
