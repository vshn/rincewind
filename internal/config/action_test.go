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
	views, err := Parse(strings.NewReader(withActions))
	if err != nil {
		t.Fatal(err)
	}

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
