// Package engine turns a view definition into a runnable pipeline:
// script → JSON value stream → rows expression → column projection → sort →
// Table. It knows nothing about kubectl or any other producer; a source is
// whatever prints JSON to stdout.
package engine

import (
	"context"
	"fmt"
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

// Table is what a view produces. Notes carry per-cell evaluation problems:
// one broken expression must not take down the other 199 rows.
type Table struct {
	Cols  []string
	Rows  []Row
	Notes []string
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
	Run     func(ctx context.Context) (Table, error)
}
