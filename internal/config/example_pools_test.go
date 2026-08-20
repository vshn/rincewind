package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshn/rincewind/internal/engine"
)

const poolsFixtureFull = `{"items":[
  {"metadata":{"name":"worker"},
   "spec":{"nodeSelector":{"matchLabels":{"node-role.kubernetes.io/worker":"","zone":"b"}}},
   "status":{"machineCount":3,"readyMachineCount":3,
     "configuration":{"name":"rendered-worker-2"},
     "conditions":[{"type":"Updated","status":"True"}]}},
  {"metadata":{"name":"infra"},
   "spec":{"paused":true,"nodeSelector":{"matchLabels":{"infra":""}}},
   "status":{"machineCount":2,"readyMachineCount":1,
     "configuration":{"name":"rendered-infra-2"},
     "conditions":[{"type":"Degraded","status":"True"}]}},
  {"metadata":{"name":"odd"},
   "spec":{"nodeSelector":{"matchExpressions":[{"key":"x","operator":"Exists"}]}},
   "status":{"machineCount":1,"readyMachineCount":1,
     "configuration":{"name":"rendered-odd-1"},
     "conditions":[{"type":"Updating","status":"True"}]}}
]}`

func viewByID(t *testing.T, views []engine.View, id string) *engine.View {
	t.Helper()
	for i := range views {
		if views[i].ID == id {
			return &views[i]
		}
	}
	t.Fatalf("example config lost the %s view", id)
	return nil
}

// The shipped nodes.pools view, its drill-down and its mutation, run against
// a fake kubectl. This is what keeps the jq in examples/config.yaml honest:
// the expressions here are the ones users copy.
func TestExamplePoolsViewAndItsActions(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "pools.json"), []byte(poolsFixtureFull), 0o644); err != nil {
		t.Fatal(err)
	}
	// The fake records what it was asked to do, so the mutation can be
	// checked without a cluster.
	script := `#!/bin/sh
case "$1" in
  get)   cat "` + dir + `/pools.json" ;;
  patch) echo "$@" >> "` + dir + `/patched"; echo "machineconfigpool/$3 patched" ;;
esac
`
	if err := os.WriteFile(filepath.Join(dir, "kubectl"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	views, err := Load("../../examples")
	if err != nil {
		t.Fatal(err)
	}
	pools := viewByID(t, views, "nodes.pools")

	res, err := pools.Run(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Notes) > 0 {
		t.Fatalf("notes: %v", res.Notes)
	}

	cell := func(row int, col string) string {
		for i, c := range res.Cols {
			if c == col {
				return res.Rows[row].Cells[i].Text
			}
		}
		t.Fatalf("no column %q", col)
		return ""
	}

	// Trouble first: the degraded pool, then the updating one, then the
	// settled one.
	for i, want := range []string{"infra", "odd", "worker"} {
		if got := cell(i, "NAME"); got != want {
			t.Errorf("row %d is %q, want %q", i, got, want)
		}
	}
	if got := cell(0, "PAUSED"); got != "paused" {
		t.Errorf("infra PAUSED %q, want paused", got)
	}
	if got := cell(0, "READY"); got != "1/2" {
		t.Errorf("infra READY %q, want 1/2", got)
	}

	// The drill-down renders the selector the way kubectl -l takes it,
	// sorted so the same pool always yields the same call.
	drill := pools.Actions[0]
	if !drill.Navigates() || drill.Open.ID != "nodes.status" {
		t.Fatalf("the first action does not open nodes.status: %+v", drill)
	}
	args, err := drill.Args(rowSrc(t, res, 2), nil) // worker
	if err != nil {
		t.Fatal(err)
	}
	if got := args["selector"]; got != "node-role.kubernetes.io/worker=,zone=b" {
		t.Errorf("selector %q, want both labels sorted", got)
	}

	// A pool selecting by expression is refused rather than half-rendered:
	// listing the wrong nodes is worse than saying no.
	if _, err := drill.Args(rowSrc(t, res, 1), nil); err == nil {
		t.Error("a matchExpressions pool was not refused")
	} else if !strings.Contains(err.Error(), "by expression") {
		t.Errorf("refusal %q does not explain itself", err)
	}

	// The mutation plans one invocation per row, each naming one object,
	// and the plan spells out what would run before anything does.
	pause := pools.Actions[1]
	steps, err := pause.Plan([]any{rowSrc(t, res, 0), rowSrc(t, res, 2)}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(steps) != 2 {
		t.Fatalf("%d steps, want one per row", len(steps))
	}
	if got := steps[0].Env["name"]; got != "infra" {
		t.Errorf("first step is about %q, want infra", got)
	}
	shown := strings.Join(steps[0].Command(), "\n")
	for _, want := range []string{"name=infra", "sh -c", "kubectl patch"} {
		if !strings.Contains(shown, want) {
			t.Errorf("the command shown %q is missing %q", shown, want)
		}
	}

	// Running them is what actually touches the cluster.
	for _, s := range steps {
		if _, err := s.Run(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
	patched, err := os.ReadFile(filepath.Join(dir, "patched"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"infra", "worker"} {
		if !strings.Contains(string(patched), want) {
			t.Errorf("kubectl was never asked to patch %s: %q", want, patched)
		}
	}
}

func rowSrc(t *testing.T, res engine.Table, i int) any {
	t.Helper()
	if i >= len(res.Rows) {
		t.Fatalf("row %d of %d", i, len(res.Rows))
	}
	return res.Rows[i].Src
}
