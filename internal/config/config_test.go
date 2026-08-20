package config

import (
	"strings"
	"testing"
)

// valid is a minimal correct config the failure cases below are derived from.
const valid = `
views:
  - id: demo.list
    source:
      script: |
        echo '{"items":[{"name":"a"}]}'
    rows: .items[]
    key: .name
    columns:
      - name: NAME
        expr: .name
        emphasis: { a: good }
    sort:
      - by: NAME
`

func TestValidConfigCompiles(t *testing.T) {
	views, err := Parse(strings.NewReader(valid))
	if err != nil {
		t.Fatal(err)
	}
	if len(views) != 1 || views[0].ID != "demo.list" {
		t.Fatalf("got %+v, want one view demo.list", views)
	}
	if views[0].Title != "demo.list" {
		t.Errorf("title %q, want the id as fallback", views[0].Title)
	}
	// Group and name come out of the id; nothing declares them.
	if views[0].Group != "demo" || views[0].Name != "list" {
		t.Errorf("group %q name %q, want demo/list derived from the id", views[0].Group, views[0].Name)
	}
}

// Every broken config must fail at load time and name the culprit.
func TestBrokenConfigsFailWithContext(t *testing.T) {
	cases := []struct {
		name string
		yaml string
		want []string
	}{
		{
			name: "unknown field",
			yaml: strings.Replace(valid, "emphasis:", "emphsis:", 1),
			want: []string{"emphsis"},
		},
		{
			name: "duplicate id",
			yaml: valid + strings.TrimPrefix(valid, "\nviews:"),
			want: []string{"demo.list", "twice"},
		},
		{
			name: "broken rows expression",
			yaml: strings.Replace(valid, "rows: .items[]", "rows: .items[", 1),
			want: []string{"demo.list", "rows"},
		},
		{
			name: "broken column expression",
			yaml: strings.Replace(valid, "expr: .name", "expr: .name |", 1),
			want: []string{"demo.list", "NAME"},
		},
		{
			name: "sort names no column",
			yaml: strings.Replace(valid, "by: NAME", "by: NAM", 1),
			want: []string{"demo.list", "NAM"},
		},
		{
			name: "rank and order together",
			yaml: strings.Replace(valid, "- by: NAME", "- by: NAME\n        order: desc\n        rank: [a]", 1),
			want: []string{"demo.list", "exclude"},
		},
		{
			name: "bad order",
			yaml: strings.Replace(valid, "- by: NAME", "- by: NAME\n        order: down", 1),
			want: []string{"demo.list", "down"},
		},
		{
			name: "bad emphasis",
			yaml: strings.Replace(valid, "{ a: good }", "{ a: pink }", 1),
			want: []string{"demo.list", "pink"},
		},
		{
			name: "bad class",
			yaml: strings.Replace(valid, "key: .name", "key: .name\n    class: chart", 1),
			want: []string{"demo.list", "chart"},
		},
		{
			name: "refresh too fast",
			yaml: strings.Replace(valid, "key: .name", "key: .name\n    refresh: 100ms", 1),
			want: []string{"demo.list", "1s"},
		},
		{
			name: "refresh not a duration",
			yaml: strings.Replace(valid, "key: .name", "key: .name\n    refresh: soon", 1),
			want: []string{"demo.list", "refresh"},
		},
		{
			name: "id ends with a dot",
			yaml: strings.Replace(valid, "id: demo.list", "id: demo.", 1),
			want: []string{"demo.", "dot"},
		},
		{
			name: "missing id",
			yaml: strings.Replace(valid, "id: demo.list", "title: x", 1),
			want: []string{"no id"},
		},
		{
			name: "missing script",
			yaml: strings.Replace(valid, "script: |\n        echo '{\"items\":[{\"name\":\"a\"}]}'", "shell: [sh, -c]", 1),
			want: []string{"demo.list", "script"},
		},
		{
			name: "no columns",
			yaml: valid[:strings.Index(valid, "    columns:")],
			want: []string{"demo.list", "column"},
		},
		{
			name: "empty file",
			yaml: "views: []",
			want: []string{"no views"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Parse(strings.NewReader(tc.yaml))
			if err == nil {
				t.Fatal("parsed without error")
			}
			for _, w := range tc.want {
				if !strings.Contains(err.Error(), w) {
					t.Errorf("error %q does not mention %q", err, w)
				}
			}
		})
	}
}

// The end-to-end path: a parsed view actually runs its script.
func TestParsedViewRuns(t *testing.T) {
	views, err := Parse(strings.NewReader(valid))
	if err != nil {
		t.Fatal(err)
	}
	res, err := views[0].Run(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Rows) != 1 || res.Rows[0].Cells[0].Text != "a" {
		t.Fatalf("got %+v, want one row a", res.Rows)
	}
}
