package config

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshn/aldediggi/internal/engine"
)

// nodesFixture is what the fake kubectl serves: a healthy master, a node
// whose custom pool is mid-rollout and degraded, and a cordoned NotReady
// worker whose pool has moved on without it.
const nodesFixture = `{"items":[
  {"metadata":{"name":"master-0",
     "labels":{"node-role.kubernetes.io/master":"","node-role.kubernetes.io/control-plane":""},
     "annotations":{"machineconfiguration.openshift.io/currentConfig":"rendered-master-2",
                    "machineconfiguration.openshift.io/desiredConfig":"rendered-master-2",
                    "machineconfiguration.openshift.io/state":"Done"}},
   "spec":{},
   "status":{"conditions":[{"type":"Ready","status":"True",
       "lastTransitionTime":"2026-08-01T10:00:00Z","message":"kubelet is posting ready status"}],
     "nodeInfo":{"kubeletVersion":"v1.31.1"}}},
  {"metadata":{"name":"infra-1",
     "labels":{"node-role.kubernetes.io/infra":"","infra":""},
     "annotations":{"machineconfiguration.openshift.io/currentConfig":"rendered-infra-1",
                    "machineconfiguration.openshift.io/desiredConfig":"rendered-infra-2",
                    "machineconfiguration.openshift.io/state":"Degraded",
                    "machineconfiguration.openshift.io/reason":"failed to drain node: pod xyz will not budge"}},
   "spec":{},
   "status":{"conditions":[{"type":"Ready","status":"True",
       "lastTransitionTime":"2026-08-10T08:00:00Z","message":"kubelet is posting ready status"}],
     "nodeInfo":{"kubeletVersion":"v1.31.1"}}},
  {"metadata":{"name":"worker-1",
     "labels":{"node-role.kubernetes.io/worker":""},
     "annotations":{"machineconfiguration.openshift.io/currentConfig":"rendered-worker-1",
                    "machineconfiguration.openshift.io/desiredConfig":"rendered-worker-1",
                    "machineconfiguration.openshift.io/state":"Done"}},
   "spec":{"unschedulable":true},
   "status":{"conditions":[{"type":"Ready","status":"False",
       "lastTransitionTime":"2026-08-19T22:00:00Z","message":"node is reporting a disk pressure condition"}],
     "nodeInfo":{"kubeletVersion":"v1.30.4"}}}
]}`

const poolsFixture = `{"items":[
  {"metadata":{"name":"master"},
   "spec":{"nodeSelector":{"matchLabels":{"node-role.kubernetes.io/master":""}}},
   "status":{"configuration":{"name":"rendered-master-2"}}},
  {"metadata":{"name":"worker"},
   "spec":{"nodeSelector":{"matchLabels":{"node-role.kubernetes.io/worker":""}}},
   "status":{"configuration":{"name":"rendered-worker-2"}}},
  {"metadata":{"name":"infra"},
   "spec":{"nodeSelector":{"matchLabels":{"infra":""}}},
   "status":{"configuration":{"name":"rendered-infra-2"}}}
]}`

// TestExampleNodesView runs the shipped nodes.status view against a fake
// kubectl, covering the whole pipeline: the script's jq pool join, the
// column expressions and the trouble-first sort.
func TestExampleNodesView(t *testing.T) {
	if _, err := exec.LookPath("jq"); err != nil {
		t.Skip("the nodes.status example needs jq on the PATH")
	}

	dir := t.TempDir()
	script := `#!/bin/sh
case "$*" in
  *nodes*) cat "` + dir + `/nodes.json" ;;
  *machineconfigpools*) cat "` + dir + `/pools.json" ;;
  *) echo "unexpected args: $*" >&2; exit 1 ;;
esac
`
	for name, content := range map[string]string{
		"kubectl": script, "nodes.json": nodesFixture, "pools.json": poolsFixture,
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	views, err := Load("../../examples/config.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var nodes *engine.View
	for i := range views {
		if views[i].ID == "nodes.status" {
			nodes = &views[i]
		}
	}
	if nodes == nil {
		t.Fatal("example config lost the nodes.status view")
	}

	res, err := nodes.Run(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Notes) > 0 {
		t.Fatalf("notes: %v", res.Notes)
	}
	if len(res.Rows) != 3 {
		t.Fatalf("got %d rows, want 3", len(res.Rows))
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

	// Trouble first: the NotReady worker, then the degraded rollout, then
	// the healthy master.
	for i, want := range []string{"worker-1", "infra-1", "master-0"} {
		if got := cell(i, "NAME"); got != want {
			t.Errorf("row %d is %q, want %q", i, got, want)
		}
	}

	// The worker sits on the old config and was never told about the new
	// one: "behind" is exactly the case the pool join exists to expose.
	if got := cell(0, "CONFIG"); got != "behind" {
		t.Errorf("worker CONFIG %q, want behind", got)
	}
	if got := cell(0, "SCHED"); got != "cordoned" {
		t.Errorf("worker SCHED %q, want cordoned", got)
	}
	if got := cell(1, "CONFIG"); got != "Degraded" {
		t.Errorf("infra CONFIG %q, want Degraded", got)
	}
	if got := cell(1, "MESSAGE"); got != "failed to drain node: pod xyz will not budge" {
		t.Errorf("infra MESSAGE %q, want the operator's reason", got)
	}
	if got := cell(2, "CONFIG"); got != "Done" {
		t.Errorf("master CONFIG %q, want Done", got)
	}
	if got := cell(2, "ROLE"); got != "control-plane,master" {
		t.Errorf("master ROLE %q, want control-plane,master", got)
	}
	// The timestamp is rendered in the local zone; asserting the date keeps
	// the test honest without pinning the machine's offset.
	if got := cell(2, "SINCE"); !strings.HasPrefix(got, "2026-08-01") {
		t.Errorf("master SINCE %q, want the ready transition timestamp", got)
	}
}
