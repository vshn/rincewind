package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// withContexts is a minimal correct hat that says where it is, which the
// failure cases below are derived from.
const withContexts = `
contexts:
  - id: kubernetes
    source:
      script: echo '{"current-context":"demo-cluster"}'
    label: '."current-context"'
    tools: [kubectl]

  - id: cloudscale
    short: Cloudscale-managed clusters
    source:
      script: echo '{"current-context":"demo-cluster"}'
    active: '."current-context" | test("cloudscale")'
    label: '"cloudscale"'

views:
  - id: demo.list
    needs: kubernetes
    source:
      script: echo '{"items":[{"name":"a"}]}'
    rows: .items[]
    columns:
      - name: NAME
        expr: .name
`

func TestContextsCompile(t *testing.T) {
	loaded, err := Parse(strings.NewReader(withContexts))
	if err != nil {
		t.Fatal(err)
	}

	if len(loaded.Contexts) != 2 {
		t.Fatalf("%d contexts, want 2", len(loaded.Contexts))
	}
	// Declaration order survives: it is what decides which label the chrome
	// drops first.
	if loaded.Contexts[0].ID != "kubernetes" || loaded.Contexts[1].ID != "cloudscale" {
		t.Errorf("contexts are %q and %q, want them in declaration order",
			loaded.Contexts[0].ID, loaded.Contexts[1].ID)
	}
	if got := loaded.Contexts[1].Short; got != "Cloudscale-managed clusters" {
		t.Errorf("short is %q, want what the hat wrote", got)
	}
	if got := loaded.Contexts[0].Tools; len(got) != 1 || got[0] != "kubectl" {
		t.Errorf("tools are %v, want [kubectl]", got)
	}
	if got := loaded.Views[0].Needs; len(got) != 1 || got[0] != "kubernetes" {
		t.Errorf("needs is %v, want the one context written as a plain string", got)
	}
}

// Naming one context is the common case and must not have to look like a
// list; naming several must still work.
func TestNeedsTakesAListToo(t *testing.T) {
	yaml := strings.Replace(withContexts, "needs: kubernetes", "needs: [kubernetes, cloudscale]", 1)
	loaded, err := Parse(strings.NewReader(yaml))
	if err != nil {
		t.Fatal(err)
	}
	if got := loaded.Views[0].Needs; strings.Join(got, " ") != "kubernetes cloudscale" {
		t.Errorf("needs is %v, want both ids", got)
	}
}

// Everything a hat can get wrong about a context dies here, naming it.
func TestBrokenContextsFailWithContext(t *testing.T) {
	cases := []struct {
		name string
		yaml string
		want []string
	}{
		{
			name: "no id",
			yaml: strings.Replace(withContexts, "  - id: kubernetes", "  - short: nameless", 1),
			want: []string{"a context has no id"},
		},
		{
			name: "no script",
			yaml: strings.Replace(withContexts,
				"    source:\n      script: echo '{\"current-context\":\"demo-cluster\"}'\n    label: '.\"current-context\"'\n    tools: [kubectl]",
				"    label: '.\"current-context\"'", 1),
			want: []string{`context "kubernetes"`, "source.script is required"},
		},
		{
			name: "says nothing",
			yaml: strings.Replace(withContexts, `    label: '."current-context"'`+"\n    tools: [kubectl]", "", 1),
			want: []string{`context "kubernetes"`, "needs active or label"},
		},
		{
			name: "id is not a plain word",
			yaml: strings.Replace(withContexts, "id: kubernetes", "id: Kubernetes Cluster", 1),
			want: []string{`context "Kubernetes Cluster"`, "lowercase letters"},
		},
		{
			name: "nameless tool",
			yaml: strings.Replace(withContexts, "tools: [kubectl]", `tools: ["", kubectl]`, 1),
			want: []string{`context "kubernetes"`, "a tool has no name"},
		},
		{
			name: "broken predicate",
			yaml: strings.Replace(withContexts, `active: '."current-context" | test("cloudscale")'`, "active: '.[' ", 1),
			want: []string{`context "cloudscale"`, "active"},
		},
		{
			name: "broken label",
			yaml: strings.Replace(withContexts, `label: '."current-context"'`, "label: '.[' ", 1),
			want: []string{`context "kubernetes"`, "label"},
		},
		{
			name: "declared twice",
			yaml: strings.Replace(withContexts, "  - id: cloudscale", "  - id: kubernetes", 1),
			want: []string{`context "kubernetes"`, "declared twice"},
		},
		// The same rule as an open target that is not a view: a view naming a
		// context nobody declared would be shown always, which is the
		// opposite of what was meant.
		{
			name: "needs nothing that exists",
			yaml: strings.Replace(withContexts, "needs: kubernetes", "needs: cloudsclae", 1),
			want: []string{`view "demo.list"`, `needs "cloudsclae"`, "not a context"},
		},
		{
			name: "needs a nameless context",
			yaml: strings.Replace(withContexts, "needs: kubernetes", `needs: ""`, 1),
			want: []string{`view "demo.list"`, "needs"},
		},
		{
			name: "needs is neither a name nor a list",
			yaml: strings.Replace(withContexts, "needs: kubernetes", "needs: {a: b}", 1),
			want: []string{"context id or a list"},
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

// hatDir writes a hat of several files and loads it.
func hatDir(t *testing.T, files map[string]string) (Hat, error) {
	t.Helper()
	dir := t.TempDir()
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return Load(dir)
}

// A hat is one hat however it is split up: the contexts merge, and a view in
// one file may name a context declared in another.
func TestContextsMergeAcrossFiles(t *testing.T) {
	loaded, err := hatDir(t, map[string]string{
		"a.yaml": `
contexts:
  - id: kubernetes
    source: { script: "echo '{\"n\":\"demo\"}'" }
    label: .n
views:
  - id: a.one
    source: { script: "echo '{}'" }
    rows: .
    columns: [{name: X, expr: "1"}]
`,
		"b.yaml": `
contexts:
  - id: cloudscale
    source: { script: "echo '{\"n\":\"demo\"}'" }
    active: '.n | test("demo")'
    label: '"cloudscale"'
views:
  - id: b.two
    needs: [kubernetes, cloudscale]
    source: { script: "echo '{}'" }
    rows: .
    columns: [{name: X, expr: "1"}]
`,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded.Contexts) != 2 {
		t.Fatalf("%d contexts, want both files' merged", len(loaded.Contexts))
	}
	if len(loaded.Views) != 2 {
		t.Fatalf("%d views, want both files' merged", len(loaded.Views))
	}
}

// A clash across two files names both of them, or the reader is left grepping
// for the other half.
func TestContextIDClashNamesBothFiles(t *testing.T) {
	body := `
contexts:
  - id: kubernetes
    source: { script: "echo '{\"n\":\"demo\"}'" }
    label: .n
views:
  - id: %s
    source: { script: "echo '{}'" }
    rows: .
    columns: [{name: X, expr: "1"}]
`
	_, err := hatDir(t, map[string]string{
		"a.yaml": strings.Replace(body, "%s", "a.one", 1),
		"b.yaml": strings.Replace(body, "%s", "b.two", 1),
	})
	if err == nil {
		t.Fatal("loaded two hats declaring the same context")
	}
	for _, w := range []string{`context "kubernetes"`, "a.yaml", "b.yaml"} {
		if !strings.Contains(err.Error(), w) {
			t.Errorf("error %q does not mention %q", err, w)
		}
	}
}
