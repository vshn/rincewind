package catalog

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshn/aldediggi/internal/kube"
)

const (
	poolsFixture = `{"items":[
 {"metadata":{"name":"master"},"spec":{"paused":false},
  "status":{"configuration":{"name":"rendered-master-a1b2c3"},"machineCount":3,"updatedMachineCount":3,
   "readyMachineCount":3,"degradedMachineCount":0,
   "conditions":[{"type":"Updated","status":"True"},{"type":"Degraded","status":"False"}]}},
 {"metadata":{"name":"worker"},"spec":{"paused":true},
  "status":{"configuration":{"name":"rendered-worker-d4e5f6"},"machineCount":12,"updatedMachineCount":7,
   "readyMachineCount":11,"degradedMachineCount":1,
   "conditions":[{"type":"Updated","status":"False"},{"type":"Updating","status":"True"},
                 {"type":"Degraded","status":"False"},{"type":"NodeDegraded","status":"True"}]}},
 {"metadata":{"name":"infra"},"spec":{"paused":false},
  "status":{"configuration":{"name":"rendered-infra-9f8e7d"},"machineCount":2,"updatedMachineCount":2,
   "readyMachineCount":2,"degradedMachineCount":0,
   "conditions":[{"type":"Updated","status":"True"}]}}
]}`

	cvFixture = `{"spec":{"channel":"stable-4.20"},
 "status":{"desired":{"version":"4.20.32"},
  "conditions":[
   {"type":"Available","status":"True","message":"Done applying 4.20.32","lastTransitionTime":"2026-08-17T13:11:45Z"},
   {"type":"Failing","status":"False","lastTransitionTime":"2026-08-17T13:11:45Z"},
   {"type":"RetrievedUpdates","status":"True","lastTransitionTime":"2026-08-01T00:00:00Z"}]}}`
)

func fakeMCO(t *testing.T) (kube.Client, func() (args, stdin string)) {
	t.Helper()
	dir := t.TempDir()
	for name, body := range map[string]string{
		"pools.json": poolsFixture, "cv.json": cvFixture,
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	script := `D="$(dirname "$0")"
echo "$@" > "$D/args"
case "$*" in
  *patch*|*delete*) cat > "$D/stdin"; echo ok ;;
  *machineconfigpool*) cat "$D/pools.json" ;;
  *clusterversion*) cat "$D/cv.json" ;;
  *logs*) printf 'line one\nline two\n' ;;
esac`
	bin := filepath.Join(dir, "kubectl")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\n"+script+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	read := func() (string, string) {
		a, _ := os.ReadFile(filepath.Join(dir, "args"))
		in, _ := os.ReadFile(filepath.Join(dir, "stdin"))
		return strings.TrimSpace(string(a)), string(in)
	}
	return kube.Client{Bin: bin}, read
}

// Pools are objects with a health state, so trouble sorts first.
func TestPoolsSortTroubleFirst(t *testing.T) {
	c, _ := fakeMCO(t)

	res, err := machineConfigPools(context.Background(), c, "")
	if err != nil {
		t.Fatal(err)
	}

	want := []string{"worker", "infra", "master"}
	for i, name := range want {
		if got := cellAt(t, res, i, 0); got != name {
			t.Errorf("row %d is %q, want %q", i, got, name)
		}
	}
}

// A pool can be degraded through NodeDegraded or RenderDegraded without
// Degraded itself being true.
func TestPoolStateCoversEveryDegradedCondition(t *testing.T) {
	c, _ := fakeMCO(t)

	res, _ := machineConfigPools(context.Background(), c, "")
	if got := cellAt(t, res, 0, 1); got != "Degraded" {
		t.Errorf("worker state = %q, want Degraded (it has NodeDegraded)", got)
	}
	if got := cellAt(t, res, 2, 1); got != "Updated" {
		t.Errorf("master state = %q", got)
	}
}

// A pool left paused is the usual reason an upgrade never finishes, so it has
// to be visible.
func TestPausedPoolIsMarked(t *testing.T) {
	c, _ := fakeMCO(t)

	res, _ := machineConfigPools(context.Background(), c, "")
	if got := cellAt(t, res, 0, 2); got != "PAUSED" {
		t.Errorf("worker paused cell = %q", got)
	}
	if got := cellAt(t, res, 2, 2); got != "" {
		t.Errorf("master paused cell = %q, want empty", got)
	}
}

func TestPoolCountsAreShownAsProgress(t *testing.T) {
	c, _ := fakeMCO(t)

	res, _ := machineConfigPools(context.Background(), c, "")
	if got := cellAt(t, res, 0, 3); got != "7/12" {
		t.Errorf("updated = %q", got)
	}
	if got := cellAt(t, res, 0, 4); got != "11/12" {
		t.Errorf("ready = %q", got)
	}
	if got := cellAt(t, res, 0, 5); got != "1" {
		t.Errorf("degraded = %q", got)
	}
}

func TestPoolPauseSendsTheShownPools(t *testing.T) {
	c, read := fakeMCO(t)

	res, _ := machineConfigPools(context.Background(), c, "")
	if _, err := poolPauseAction(c, true).Run(context.Background(), res, nil); err != nil {
		t.Fatal(err)
	}

	args, stdin := read()
	if !strings.Contains(args, "patch -f - --type merge") {
		t.Errorf("args = %q", args)
	}
	if !strings.Contains(stdin, `"paused"`) {
		t.Errorf("stdin does not carry the pools: %q", stdin)
	}

	var sent struct {
		Items []struct {
			Metadata struct{ Name string } `json:"metadata"`
		} `json:"items"`
	}
	json.Unmarshal([]byte(stdin), &sent)
	if len(sent.Items) != 3 {
		t.Errorf("sent %d pools", len(sent.Items))
	}
}

func TestPoolPauseAndUnpauseDifferOnlyInTheValue(t *testing.T) {
	c, read := fakeMCO(t)
	res, _ := machineConfigPools(context.Background(), c, "")

	poolPauseAction(c, true).Run(context.Background(), res, nil)
	pauseArgs, _ := read()

	poolPauseAction(c, false).Run(context.Background(), res, nil)
	unpauseArgs, _ := read()

	if !strings.Contains(pauseArgs, `"paused":true`) {
		t.Errorf("pause sent %q", pauseArgs)
	}
	if !strings.Contains(unpauseArgs, `"paused":false`) {
		t.Errorf("unpause sent %q", unpauseArgs)
	}
}

// The version is the first question, so it goes above the conditions rather
// than being appended to them in columns that mean something else.
func TestProgressLeadsWithTheVersion(t *testing.T) {
	c, _ := fakeMCO(t)

	res, err := clusterVersion(context.Background(), c)
	if err != nil {
		t.Fatal(err)
	}

	if got := cellAt(t, res, 0, 1); got != "version" {
		t.Errorf("first row is %q", got)
	}
	if got := cellAt(t, res, 0, 2); got != "4.20.32" {
		t.Errorf("version = %q", got)
	}
	if got := cellAt(t, res, 1, 2); got != "stable-4.20" {
		t.Errorf("channel = %q", got)
	}
}

// "True" is good on Available and bad on Failing, so the bare value must not
// be coloured. Only the derived headline row is.
func TestConditionValuesAreNotColoured(t *testing.T) {
	c, _ := fakeMCO(t)

	res, _ := clusterVersion(context.Background(), c)
	for _, value := range []string{"True", "False", "Unknown"} {
		if got := res.(Table).EmphasisOf(value); got != Plain {
			t.Errorf("condition value %q is coloured", value)
		}
	}

	jobs := fakeJobs(t)
	conds, _ := upgradeConditions(context.Background(), jobs, "done-4-20-32")
	if got := conds.(Table).Emphasis; got != nil {
		t.Errorf("job conditions are coloured: %v", got)
	}
}

func TestLogsReadTheControllerDeployment(t *testing.T) {
	c, read := fakeMCO(t)

	if _, err := mcoLogs(context.Background(), c.In(mcoNamespace)); err != nil {
		t.Fatal(err)
	}
	if args, _ := read(); !strings.Contains(args, "deploy/machine-config-controller") {
		t.Errorf("args = %q", args)
	}
}

// Cluster-scoped resources must not be asked for inside a namespace.
func TestClusterScopedViewsUseNoNamespace(t *testing.T) {
	c, read := fakeMCO(t)

	machineConfigPools(context.Background(), c, "")
	if args, _ := read(); strings.Contains(args, "-n ") {
		t.Errorf("machineconfigpool was scoped to a namespace: %q", args)
	}

	clusterVersion(context.Background(), c)
	if args, _ := read(); strings.Contains(args, "-n ") {
		t.Errorf("clusterversion was scoped to a namespace: %q", args)
	}
}

// On a settled cluster the conditions say nothing much. Without a headline row
// that reads like the view is broken rather than like nothing is going on.
func TestProgressSaysIdleWhenNothingIsHappening(t *testing.T) {
	c, _ := fakeMCO(t)

	res, err := clusterVersion(context.Background(), c)
	if err != nil {
		t.Fatal(err)
	}

	if got := cellAt(t, res, 2, 1); got != "upgrade" {
		t.Fatalf("third row is %q", got)
	}
	if got := cellAt(t, res, 2, 2); got != "idle" {
		t.Errorf("state = %q, want idle", got)
	}
}

func TestProgressReportsWhatItIsWaitingOn(t *testing.T) {
	conds := []jobCondition{
		{Type: "Progressing", Status: "True", Message: "Working towards 4.20.33: 46% complete"},
		{Type: "Failing", Status: "False"},
	}

	state, detail := upgradeProgress(conds)
	if state != "in progress" {
		t.Errorf("state = %q", state)
	}
	if detail != "Working towards 4.20.33: 46% complete" {
		t.Errorf("detail = %q", detail)
	}
}

// Failing carries the actual blocker, which is the next question after "is it
// moving".
func TestProgressReportsBlocked(t *testing.T) {
	conds := []jobCondition{
		{Type: "Progressing", Status: "True", Message: "Working towards 4.20.33"},
		{Type: "Failing", Status: "True", Message: "machine-config is degraded"},
	}

	if state, _ := upgradeProgress(conds); state != "blocked" {
		t.Errorf("state = %q, want blocked", state)
	}

	// Blocked without progressing still explains itself.
	stuck := []jobCondition{{Type: "Failing", Status: "True", Message: "machine-config is degraded"}}
	state, detail := upgradeProgress(stuck)
	if state != "blocked" || detail != "machine-config is degraded" {
		t.Errorf("state = %q, detail = %q", state, detail)
	}
}
