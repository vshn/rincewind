package engine

import (
	"context"
	"strings"
	"testing"
	"time"
)

// runDef compiles and runs a definition against a real shell; the scripts
// only echo fixture JSON, so the tests cover the same path production does.
func runDef(t *testing.T, d Def) Table {
	t.Helper()
	v, err := Compile(d)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	res, err := v.Run(context.Background())
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	return res
}

func cellTexts(r Row) []string {
	out := make([]string, len(r.Cells))
	for i, c := range r.Cells {
		out[i] = c.Text
	}
	return out
}

func TestRowsRunOverEveryStreamValue(t *testing.T) {
	// Two JSON documents on stdout, as a script pulling from two sources
	// would produce them. Rows from both must land in one table.
	res := runDef(t, Def{
		ID: "t",
		Script: `echo '{"items":[{"name":"a"}]}'
echo '{"items":[{"name":"b"},{"name":"c"}]}'`,
		Rows:    ".items[]",
		Columns: []ColumnDef{{Name: "NAME", Expr: ".name"}},
	})

	if len(res.Rows) != 3 {
		t.Fatalf("got %d rows, want 3", len(res.Rows))
	}
	if got := res.Rows[2].Cells[0].Text; got != "c" {
		t.Errorf("last row is %q, want c", got)
	}
}

func TestScriptFailureCarriesStderr(t *testing.T) {
	v, err := Compile(Def{
		ID:      "t",
		Script:  `echo "the disk is on fire" >&2; exit 3`,
		Rows:    ".",
		Columns: []ColumnDef{{Name: "X", Expr: "."}},
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = v.Run(context.Background())
	if err == nil || !strings.Contains(err.Error(), "the disk is on fire") {
		t.Fatalf("error %v does not carry stderr", err)
	}
}

func TestBrokenOutputNamesTheSpot(t *testing.T) {
	v, err := Compile(Def{
		ID:      "t",
		Script:  `echo 'not json at all'`,
		Rows:    ".",
		Columns: []ColumnDef{{Name: "X", Expr: "."}},
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = v.Run(context.Background())
	if err == nil || !strings.Contains(err.Error(), "JSON stream") {
		t.Fatalf("error %v does not explain the contract", err)
	}
}

func TestCellErrorSpoilsOneCellNotTheView(t *testing.T) {
	res := runDef(t, Def{
		ID:     "t",
		Script: `echo '{"items":[{"name":"a","tags":"x"},{"name":"b","tags":"y"}]}'`,
		Rows:   ".items[]",
		Columns: []ColumnDef{
			{Name: "NAME", Expr: ".name"},
			// keys on a string fails per row; the NAME column must survive.
			{Name: "TAG", Expr: ".tags | keys"},
		},
	})

	if len(res.Rows) != 2 {
		t.Fatalf("got %d rows, want 2", len(res.Rows))
	}
	if got := res.Rows[0].Cells[1].Text; got != "!err" {
		t.Errorf("broken cell shows %q, want !err", got)
	}
	if got := res.Rows[0].Cells[0].Text; got != "a" {
		t.Errorf("healthy cell shows %q, want a", got)
	}
	if len(res.Notes) != 1 || !strings.Contains(res.Notes[0], "TAG") {
		t.Errorf("notes %v, want one naming column TAG", res.Notes)
	}
}

func TestSortByRankThenName(t *testing.T) {
	res := runDef(t, Def{
		ID: "t",
		Script: `echo '{"items":[
			{"name":"b","sync":"Synced"},
			{"name":"a","sync":"OutOfSync"},
			{"name":"c","sync":"Synced"},
			{"name":"d","sync":"Weird"}]}'`,
		Rows: ".items[]",
		Columns: []ColumnDef{
			{Name: "NAME", Expr: ".name"},
			{Name: "SYNC", Expr: ".sync"},
		},
		Sort: []SortDef{
			{By: "SYNC", Rank: []string{"OutOfSync", "Synced"}},
			{By: "NAME"},
		},
	})

	var names []string
	for _, r := range res.Rows {
		names = append(names, r.Cells[0].Text)
	}
	// Trouble first, then alphabet; the unranked value last.
	if got := strings.Join(names, " "); got != "a b c d" {
		t.Errorf("order %q, want a b c d", got)
	}
}

func TestSortComparesRawNumbers(t *testing.T) {
	// Rendered as strings, 9 would sort after 10. The raw values must not.
	res := runDef(t, Def{
		ID:      "t",
		Script:  `echo '[{"n":10},{"n":9},{"n":101}]'`,
		Rows:    ".[]",
		Columns: []ColumnDef{{Name: "N", Expr: ".n"}},
		Sort:    []SortDef{{By: "N"}},
	})

	if got := strings.Join(cellTexts(res.Rows[0]), ""); got != "9" {
		t.Errorf("first row %q, want 9", got)
	}
	if got := res.Rows[2].Cells[0].Text; got != "101" {
		t.Errorf("last row %q, want 101", got)
	}
}

func TestSortDescending(t *testing.T) {
	res := runDef(t, Def{
		ID:      "t",
		Script:  `echo '[{"t":"2026-01-01T00:00:00Z"},{"t":"2026-08-01T00:00:00Z"}]'`,
		Rows:    ".[]",
		Columns: []ColumnDef{{Name: "AT", Expr: ".t", Format: FormatTimestamp}},
		Sort:    []SortDef{{By: "AT", Desc: true}},
	})

	if got := res.Rows[0].Cells[0].Text; !strings.HasPrefix(got, "2026-08-01") {
		t.Errorf("first row %q, want the later timestamp", got)
	}
}

func TestFormats(t *testing.T) {
	if got := formatCell("2026-08-18T09:48:00Z", FormatTimestamp, time.Now()); !strings.Contains(got, "2026-08-18") || strings.Contains(got, "T") {
		t.Errorf("timestamp rendered as %q", got)
	}

	now := time.Date(2026, 8, 20, 12, 0, 0, 0, time.UTC)
	if got := formatCell("2026-08-18T10:00:00Z", FormatAge, now); got != "2d2h" {
		t.Errorf("age rendered as %q, want 2d2h", got)
	}

	// A value that is no time passes through rather than vanishing.
	if got := formatCell("pending", FormatAge, now); got != "pending" {
		t.Errorf("non-time rendered as %q, want passthrough", got)
	}
}

func TestEmphasisMatchesRawValue(t *testing.T) {
	res := runDef(t, Def{
		ID:     "t",
		Script: `echo '[{"s":"Bad"},{"s":"Fine"}]'`,
		Rows:   ".[]",
		Columns: []ColumnDef{
			{Name: "S", Expr: ".s", Emph: map[string]Emphasis{"Bad": Bad, "Fine": Good}},
		},
	})

	if got := res.Rows[0].Cells[0].Emph; got != Bad {
		t.Errorf("emphasis %v, want Bad", got)
	}
	if got := res.Rows[1].Cells[0].Emph; got != Good {
		t.Errorf("emphasis %v, want Good", got)
	}
}

func TestKeyExpressionNamesRows(t *testing.T) {
	res := runDef(t, Def{
		ID:      "t",
		Script:  `echo '[{"meta":{"uid":"u1"},"name":"a"}]'`,
		Rows:    ".[]",
		Key:     ".meta.uid",
		Columns: []ColumnDef{{Name: "NAME", Expr: ".name"}},
	})

	if got := res.Rows[0].Key; got != "u1" {
		t.Errorf("key %q, want u1", got)
	}
}

func TestKeyFallsBackToCells(t *testing.T) {
	res := runDef(t, Def{
		ID:      "t",
		Script:  `echo '[{"name":"a","s":"x"}]'`,
		Rows:    ".[]",
		Columns: []ColumnDef{{Name: "NAME", Expr: ".name"}, {Name: "S", Expr: ".s"}},
	})

	if got := res.Rows[0].Key; got == "" {
		t.Error("keyless row has no identity at all")
	}
}

func TestCompositeValuesRenderAsJSON(t *testing.T) {
	res := runDef(t, Def{
		ID:     "t",
		Script: `echo '[{"labels":{"a":"b"},"n":3,"ok":true,"gone":null}]'`,
		Rows:   ".[]",
		Columns: []ColumnDef{
			{Name: "L", Expr: ".labels"},
			{Name: "N", Expr: ".n"},
			{Name: "OK", Expr: ".ok"},
			{Name: "GONE", Expr: ".gone"},
		},
	})

	got := cellTexts(res.Rows[0])
	want := []string{`{"a":"b"}`, "3", "true", ""}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("cell %d: %q, want %q", i, got[i], want[i])
		}
	}
}

func TestMultilineValuesStayOnOneLine(t *testing.T) {
	res := runDef(t, Def{
		ID:      "t",
		Script:  `printf '%s' '[{"msg":"first line\nsecond line"}]'`,
		Rows:    ".[]",
		Columns: []ColumnDef{{Name: "MSG", Expr: ".msg"}},
	})

	if got := res.Rows[0].Cells[0].Text; strings.Contains(got, "\n") {
		t.Errorf("cell %q contains a newline", got)
	}
}

func TestCancelledContextWinsOverExitStatus(t *testing.T) {
	v, err := Compile(Def{
		ID:      "t",
		Script:  `sleep 5`,
		Rows:    ".",
		Columns: []ColumnDef{{Name: "X", Expr: "."}},
	})
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	_, err = v.Run(ctx)
	if err != context.DeadlineExceeded {
		t.Fatalf("got %v, want context.DeadlineExceeded", err)
	}
}
