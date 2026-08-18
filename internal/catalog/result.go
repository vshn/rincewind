package catalog

// Result is what a view returns. Deliberately data and not a formatted string:
// the CLI renders it through tabwriter and the TUI through a table widget, and
// neither can work backwards from finished text.
type Result interface{ isResult() }

// Text is unstructured output, such as logs.
type Text struct{ Lines []string }

// Table is row-oriented output. Cells are plain strings for now; the style
// hints described in docs/architecture.md arrive with the config schema, and
// actions will need each row's source object alongside them.
type Table struct {
	Cols []string
	Rows [][]string
}

func (Text) isResult()  {}
func (Table) isResult() {}
