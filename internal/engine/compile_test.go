package engine

import (
	"strings"
	"testing"
)

// A config mistake must die at compile time with the view and field named —
// this is the whole answer to "an expression engine gives a stack of nils".
func TestCompileErrorsNameViewAndField(t *testing.T) {
	cases := []struct {
		name string
		def  Def
		want []string
	}{
		{
			name: "broken rows expression",
			def: Def{ID: "argo.apps", Script: "true", Rows: ".items[",
				Columns: []ColumnDef{{Name: "N", Expr: ".n"}}},
			want: []string{"argo.apps", "rows"},
		},
		{
			name: "broken column expression",
			def: Def{ID: "argo.apps", Script: "true", Rows: ".",
				Columns: []ColumnDef{{Name: "SYNC", Expr: ".a |"}}},
			want: []string{"argo.apps", "SYNC"},
		},
		{
			name: "sort names no column",
			def: Def{ID: "argo.apps", Script: "true", Rows: ".",
				Columns: []ColumnDef{{Name: "N", Expr: ".n"}},
				Sort:    []SortDef{{By: "SYNK"}}},
			want: []string{"argo.apps", "SYNK"},
		},
		{
			name: "unknown format",
			def: Def{ID: "argo.apps", Script: "true", Rows: ".",
				Columns: []ColumnDef{{Name: "N", Expr: ".n", Format: "iso"}}},
			want: []string{"argo.apps", "N", "iso"},
		},
		{
			name: "no script",
			def: Def{ID: "argo.apps", Rows: ".",
				Columns: []ColumnDef{{Name: "N", Expr: ".n"}}},
			want: []string{"argo.apps", "script"},
		},
		{
			name: "no columns",
			def:  Def{ID: "argo.apps", Script: "true", Rows: "."},
			want: []string{"argo.apps", "column"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Compile(tc.def)
			if err == nil {
				t.Fatal("compiled without error")
			}
			for _, w := range tc.want {
				if !strings.Contains(err.Error(), w) {
					t.Errorf("error %q does not mention %q", err, w)
				}
			}
		})
	}
}

func TestCompileDefaults(t *testing.T) {
	v, err := Compile(Def{ID: "x", Script: "true", Rows: ".",
		Columns: []ColumnDef{{Name: "N", Expr: ".n"}}})
	if err != nil {
		t.Fatal(err)
	}
	if v.Title != "x" {
		t.Errorf("title %q, want the id as fallback", v.Title)
	}
}
