package catalog

// Result is what a view returns. Deliberately data and not a formatted string:
// the CLI renders it through tabwriter and the TUI through a table widget, and
// neither can work backwards from finished text.
type Result interface{ isResult() }

// Text is unstructured output, such as logs.
type Text struct{ Lines []string }

// Emphasis marks a cell as worth noticing. Deciding this in the catalogue
// rather than in the renderers keeps "OutOfSync is bad" in one place, and lets
// the CLI and the TUI agree without sharing colours.
type Emphasis int

const (
	Plain Emphasis = iota
	Good
	Warn
	Bad
)

// Table is row-oriented output.
type Table struct {
	Cols []string
	Rows []Row
	// Emphasis maps a cell's exact value to how it should stand out. A value
	// that is not listed is Plain.
	Emphasis map[string]Emphasis
}

// EmphasisOf classifies one cell.
func (t Table) EmphasisOf(cell string) Emphasis { return t.Emphasis[cell] }

// Row is a rendered line plus the object it came from. Actions operate on Src,
// never on Cells: the cells are truncated for display, and a column the user
// never sees may still be what the action needs.
type Row struct {
	Cells []string
	Src   any
}

// Add appends a row. src may be nil for tables nothing acts on.
func (t *Table) Add(src any, cells ...string) {
	t.Rows = append(t.Rows, Row{Cells: cells, Src: src})
}

// Sources returns the objects behind the rows, in display order. This is an
// action's input: the filtered, sorted set the user was looking at.
func (t Table) Sources() []any {
	out := make([]any, 0, len(t.Rows))
	for _, r := range t.Rows {
		if r.Src != nil {
			out = append(out, r.Src)
		}
	}
	return out
}

// Navigate is returned by an action that moves to another view instead of
// producing output. Modelling it as a result keeps one Run signature: the TUI
// pushes the target onto its stack, the CLI just runs and prints it.
type Navigate struct {
	View string
	Args Args
}

func (Text) isResult()     {}
func (Table) isResult()    {}
func (Navigate) isResult() {}
