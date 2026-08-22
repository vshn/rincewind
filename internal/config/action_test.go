package config

import (
	"strings"
	"testing"
)

// withActions is a two-view config: a parent offering both kinds of action
// and the target one of them opens.
const withActions = `
views:
  - id: demo.pools
    source:
      script: echo '{"items":[]}'
    rows: .items[]
    columns:
      - name: NAME
        expr: .metadata.name
    actions:
      - key: enter
        name: nodes
        open: demo.nodes
        args:
          selector: .spec.selector
      - key: p
        name: pause
        confirm: '"Pause \(length)"'
        env:
          name: .metadata.name
        script: kubectl patch pool "$name"
        reload: true

  - id: demo.nodes
    params:
      - name: selector
        required: true
    source:
      script: echo '{"items":[]}'
    rows: .items[]
    columns:
      - name: NAME
        expr: .metadata.name
`

func TestActionsCompile(t *testing.T) {
	loaded, err := Parse(strings.NewReader(withActions))
	if err != nil {
		t.Fatal(err)
	}
	views := loaded.Views

	pools := views[0]
	if len(pools.Actions) != 2 {
		t.Fatalf("%d actions, want 2", len(pools.Actions))
	}

	open := pools.Actions[0]
	if !open.Navigates() || open.Open == nil || open.Open.ID != "demo.nodes" {
		t.Errorf("the navigating action did not resolve its target: %+v", open)
	}
	if pools.Actions[1].Navigates() {
		t.Error("the script action is marked as navigating")
	}
	if pools.Actions[1].Confirm == nil {
		t.Error("the confirmation was not compiled")
	}

	// A view needing a parameter is not offered in the menu: run bare it
	// would show some arbitrary object as if it were the answer.
	if views[1].Listed() {
		t.Error("a view with a required parameter is listed in the menu")
	}
	if !pools.Listed() {
		t.Error("a view without parameters is not listed")
	}
}

// Everything an action can get wrong dies at load time, naming the view and
// the action. This is what the old design could only discover at run time.
func TestBrokenActionsFailWithContext(t *testing.T) {
	cases := []struct {
		name string
		yaml string
		want []string
	}{
		{
			name: "opens an unknown view",
			yaml: strings.Replace(withActions, "open: demo.nodes", "open: demo.gone", 1),
			want: []string{"demo.pools", "nodes", "demo.gone", "not a view"},
		},
		{
			name: "passes an undeclared parameter",
			yaml: strings.Replace(withActions, "selector: .spec.selector", "selektor: .spec.selector", 1),
			want: []string{"demo.pools", "selektor", "does not declare"},
		},
		{
			name: "omits a required parameter",
			yaml: strings.Replace(withActions, "        args:\n          selector: .spec.selector\n", "", 1),
			want: []string{"demo.pools", "requires", "selector"},
		},
		{
			name: "claims a reserved key",
			yaml: strings.Replace(withActions, "key: p", "key: r", 1),
			want: []string{"demo.pools", "pause", "answers itself"},
		},
		{
			name: "two actions on one key",
			yaml: strings.Replace(withActions, "key: p", "key: enter", 1),
			want: []string{"demo.pools", "already taken"},
		},
		{
			name: "both open and script",
			yaml: strings.Replace(withActions, "        open: demo.nodes", "        open: demo.nodes\n        script: true", 1),
			want: []string{"demo.pools", "nodes", "either navigates or works"},
		},
		{
			name: "neither open nor script",
			yaml: strings.Replace(withActions, "        script: kubectl patch pool \"$name\"\n", "", 1),
			want: []string{"demo.pools", "pause", "either open or script"},
		},
		{
			name: "env on a navigating action",
			yaml: strings.Replace(withActions, "        open: demo.nodes", "        open: demo.nodes\n        env: { a: .b }", 1),
			want: []string{"demo.pools", "env belongs to a script"},
		},
		{
			name: "args on a working action",
			yaml: strings.Replace(withActions, "        script: kubectl patch pool \"$name\"", "        script: true\n        args: { a: .b }", 1),
			want: []string{"demo.pools", "args belong to open"},
		},
		{
			name: "broken argument expression",
			yaml: strings.Replace(withActions, "selector: .spec.selector", "selector: .spec.|", 1),
			want: []string{"demo.pools", "args.selector"},
		},
		{
			name: "broken confirm expression",
			yaml: strings.Replace(withActions, `confirm: '"Pause \(length)"'`, "confirm: '\"Pause \\(length'", 1),
			want: []string{"demo.pools", "confirm"},
		},
		{
			name: "uppercase env name could shadow PATH",
			yaml: strings.Replace(withActions, "          name: .metadata.name", "          PATH: .metadata.name", 1),
			want: []string{"demo.pools", "PATH", "lowercase"},
		},
		{
			name: "uppercase param name",
			yaml: strings.Replace(withActions, "      - name: selector\n        required: true", "      - name: HOME", 1),
			want: []string{"demo.nodes", "HOME", "lowercase"},
		},
		{
			name: "action without a name",
			yaml: strings.Replace(withActions, "        name: pause\n", "", 1),
			want: []string{"demo.pools", "no name"},
		},
		{
			name: "text view with rows",
			yaml: strings.Replace(valid, "    rows: .items[]", "    class: text\n    rows: .items[]", 1),
			want: []string{"demo.list", "rows is not read"},
		},
		{
			name: "text view with columns",
			yaml: strings.Replace(valid, "    rows: .items[]\n    key: .name\n", "    class: text\n", 1),
			want: []string{"demo.list", "columns are not read"},
		},
		{
			name: "unknown class",
			yaml: strings.Replace(valid, "    rows: .items[]", "    class: pie\n    rows: .items[]", 1),
			want: []string{"demo.list", "pie", "table, text or chart"},
		},
		{
			name: "chart view with columns",
			yaml: strings.Replace(valid, "    key: .name\n", "    class: chart\n", 1),
			want: []string{"demo.list", "columns are not read"},
		},
		{
			name: "chart view with key",
			yaml: strings.Replace(chart, "    rows: .", "    rows: .\n    key: .name", 1),
			want: []string{"demo.load", "key is not read"},
		},
		{
			name: "chart view with sort",
			yaml: strings.Replace(chart, "    rows: .", "    rows: .\n    sort: [{by: X}]", 1),
			want: []string{"demo.load", "sort is not read"},
		},
		{
			name: "chart view without rows",
			yaml: strings.Replace(chart, "    rows: .\n", "", 1),
			want: []string{"demo.load", "rows"},
		},
		{
			name: "follow on a chart",
			yaml: strings.Replace(chart, "    class: chart", "    class: chart\n    follow: true", 1),
			want: []string{"demo.load", "follow needs class: text"},
		},
		{
			name: "yrange on a table",
			yaml: strings.Replace(valid, "key: .name", "key: .name\n    yrange: [0, 100]", 1),
			want: []string{"demo.list", "yrange belongs to class chart"},
		},
		{
			name: "yrange with one number",
			yaml: strings.Replace(chart, "yrange: [0, 100]", "yrange: [100]", 1),
			want: []string{"demo.load", "two numbers"},
		},
		{
			name: "yrange upside down",
			yaml: strings.Replace(chart, "yrange: [0, 100]", "yrange: [100, 0]", 1),
			want: []string{"demo.load", "not below"},
		},
		{
			name: "chart action that navigates",
			yaml: strings.Replace(chart, "        scope: none\n", "", 1),
			want: []string{"demo.load", "refit", "scope: none"},
		},
		{
			name: "unknown scope",
			yaml: strings.Replace(withActions, "      - key: p", "      - scope: sideways\n        key: p", 1),
			want: []string{"demo.pools", "pause", "sideways", "not rows or none"},
		},
		{
			name: "scope on a navigating action",
			yaml: strings.Replace(withActions, "        open: demo.nodes", "        open: demo.nodes\n        scope: none", 1),
			want: []string{"demo.pools", "nodes", "scope belongs to a script"},
		},
		{
			name: "params on a navigating action",
			yaml: strings.Replace(withActions, "        open: demo.nodes", "        open: demo.nodes\n        params: [{name: x}]", 1),
			want: []string{"demo.pools", "nodes", "params belong to a script"},
		},
		{
			name: "uppercase param name",
			yaml: strings.Replace(withActions, "      - key: p", "      - params: [{name: PATH}]\n        key: p", 1),
			want: []string{"demo.pools", "pause", "PATH", "lowercase"},
		},
		{
			name: "param declared twice",
			yaml: strings.Replace(withActions, "      - key: p", "      - params: [{name: why}, {name: why}]\n        key: p", 1),
			want: []string{"demo.pools", "pause", "why", "twice"},
		},
		{
			name: "param shadows an env name",
			yaml: strings.Replace(withActions, "      - key: p", "      - params: [{name: name}]\n        key: p", 1),
			want: []string{"demo.pools", "pause", "also an env name"},
		},
		{
			name: "action without a key",
			yaml: strings.Replace(withActions, "      - key: p", "      - short: nothing binds this", 1),
			want: []string{"demo.pools", "pause", "no key"},
		},
		{
			name: "follow on a table",
			yaml: strings.Replace(valid, "    rows: .items[]", "    follow: true\n    rows: .items[]", 1),
			want: []string{"demo.list", "follow needs class: text"},
		},
		{
			name: "reload on an action of a followed view",
			yaml: `
views:
  - id: demo.log
    class: text
    follow: true
    source:
      script: echo hello
    actions:
      - key: p
        name: rotate
        scope: none
        script: echo rotate
        reload: true
`,
			want: []string{"demo.log", "rotate", "reload cannot re-run a followed view"},
		},
		{
			name: "follow with refresh",
			yaml: `
views:
  - id: demo.list
    class: text
    follow: true
    refresh: 30s
    source:
      script: echo hello
`,
			want: []string{"demo.list", "follow and refresh exclude each other"},
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
