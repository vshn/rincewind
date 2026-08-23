package engine

import (
	"strings"
	"testing"
)

// The engine appends the count, so config cannot write a confirmation that
// hides how much it touches — and the TUI and `act` cannot drift into asking
// two different questions about the same rows.
func TestConfirmationCarriesTheRowCount(t *testing.T) {
	def := Def{
		ID: "demo.pools", Script: "true", Rows: ".",
		Columns: []ColumnDef{{Name: "N", Expr: ".name"}},
		Actions: []ActionDef{{
			Key: "p", Name: "pause",
			Confirm: `"Pause"`,
			Script:  "true",
		}},
	}

	v, err := Compile(def)
	if err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		rows []any
		want string
	}{
		{rows: []any{"a"}, want: "Pause (1 row)"},
		{rows: []any{"a", "b"}, want: "Pause (2 rows)"},
		{rows: nil, want: "Pause (0 rows)"},
	}
	for _, c := range cases {
		got, err := v.Actions[0].Confirm(c.rows, nil)
		if err != nil {
			t.Fatal(err)
		}
		if got != c.want {
			t.Errorf("%d rows: confirmation is %q, want %q", len(c.rows), got, c.want)
		}
	}
}

// A rowless action acts on nothing, so a count would be a fiction: it plans
// exactly one step, and that step is shown beside the question.
func TestRowlessConfirmationCountsNothing(t *testing.T) {
	def := Def{
		ID: "demo.pools", Script: "true", Rows: ".",
		Columns: []ColumnDef{{Name: "N", Expr: ".name"}},
		Actions: []ActionDef{{
			Key: "c", Name: "create", Scope: ScopeNone,
			Confirm: `"Create \($args.name)"`,
			Script:  "true",
		}},
	}

	v, err := Compile(def)
	if err != nil {
		t.Fatal(err)
	}

	got, err := v.Actions[0].Confirm(nil, Args{"name": "infra"})
	if err != nil {
		t.Fatal(err)
	}
	if got != "Create infra" {
		t.Errorf("confirmation is %q, want %q", got, "Create infra")
	}
	if strings.Contains(got, "row") {
		t.Errorf("a rowless action's confirmation counts rows: %q", got)
	}
}

// Keyed is what tells a caller whether a view's rows have names a person
// could type. Without a key expression the cells stand in, joined by a NUL.
func TestKeyedReportsWhetherRowsCanBeNamed(t *testing.T) {
	base := Def{
		ID: "demo.pools", Script: "true", Rows: ".",
		Columns: []ColumnDef{{Name: "N", Expr: ".name"}},
	}

	keyed := base
	keyed.Key = ".name"
	v, err := Compile(keyed)
	if err != nil {
		t.Fatal(err)
	}
	if !v.Keyed {
		t.Error("a view with a key expression is not marked keyed")
	}

	if v, err = Compile(base); err != nil {
		t.Fatal(err)
	}
	if v.Keyed {
		t.Error("a view without a key expression is marked keyed")
	}
}

// The values come out in name order, which is what lets a plan of several
// steps be read down a column: the same variable is in the same place every
// time, however the map happened to be built.
func TestValuesAreInNameOrder(t *testing.T) {
	s := Step{Env: Args{"service": "worker", "replicas": "2", "namespace": ""}}

	want := []Value{
		{Name: "namespace", Text: ""},
		{Name: "replicas", Text: "2"},
		{Name: "service", Text: "worker"},
	}
	got := s.Values()
	if len(got) != len(want) {
		t.Fatalf("%d values, want %d: %v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("value %d is %v, want %v", i, got[i], want[i])
		}
	}
}
