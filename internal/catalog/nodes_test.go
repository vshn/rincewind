package catalog

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vshn/aldediggi/internal/kube"
)

const nodesFixture = `{"items":[
 {"metadata":{"name":"master-000000","labels":{"node-role.kubernetes.io/master":"","node-role.kubernetes.io/control-plane":""},
   "annotations":{"machineconfiguration.openshift.io/currentConfig":"rendered-master-9f8e7d",
                  "machineconfiguration.openshift.io/desiredConfig":"rendered-master-9f8e7d",
                  "machineconfiguration.openshift.io/state":"Done"}},
  "spec":{},
  "status":{"nodeInfo":{"kubeletVersion":"v1.31.5"},
   "conditions":[{"type":"MemoryPressure","status":"False","lastTransitionTime":"2026-04-28T09:00:00Z"},
                 {"type":"Ready","status":"True","reason":"KubeletReady","message":"kubelet is posting ready status","lastTransitionTime":"2026-04-28T09:00:00Z"}]}},
 {"metadata":{"name":"worker-01","labels":{"node-role.kubernetes.io/worker":""},
   "annotations":{"machineconfiguration.openshift.io/currentConfig":"rendered-worker-a1b2c3",
                  "machineconfiguration.openshift.io/desiredConfig":"rendered-worker-d4e5f6",
                  "machineconfiguration.openshift.io/state":"Done"}},
  "spec":{},
  "status":{"nodeInfo":{"kubeletVersion":"v1.31.4"},
   "conditions":[{"type":"Ready","status":"True","reason":"KubeletReady","message":"kubelet is posting ready status","lastTransitionTime":"2026-07-09T12:00:00Z"}]}},
 {"metadata":{"name":"worker-03","labels":{"node-role.kubernetes.io/worker":""},
   "annotations":{"machineconfiguration.openshift.io/state":"Working"}},
  "spec":{"unschedulable":true},
  "status":{"nodeInfo":{"kubeletVersion":"v1.31.4"},
   "conditions":[{"type":"Ready","status":"False","reason":"KubeletNotReady","message":"kubelet stopped posting node status","lastTransitionTime":"2026-08-18T11:48:00Z"}]}},
 {"metadata":{"name":"worker-07","labels":{"node-role.kubernetes.io/worker":""},
   "annotations":{"machineconfiguration.openshift.io/state":"Degraded",
                  "machineconfiguration.openshift.io/reason":"failed to drain node: blocked by PDB"}},
  "spec":{"unschedulable":true},
  "status":{"nodeInfo":{"kubeletVersion":"v1.31.4"},
   "conditions":[{"type":"Ready","status":"True","reason":"KubeletReady","message":"kubelet is posting ready status","lastTransitionTime":"2026-08-18T09:00:00Z"}]}},
 {"metadata":{"name":"infra-0","labels":{"node-role.kubernetes.io/infra":""}},
  "spec":{},
  "status":{"nodeInfo":{"kubeletVersion":"v1.31.5"},"conditions":[{"type":"MemoryPressure","status":"False"}]}}
]}`

// poolsForNodes targets a config the master node is not on, which is the case
// the state annotation cannot see.
const poolsForNodes = `{"items":[
 {"metadata":{"name":"master"},"spec":{"nodeSelector":{"matchLabels":{"node-role.kubernetes.io/master":""}}},
  "status":{"configuration":{"name":"rendered-master-NEWER"}}},
 {"metadata":{"name":"worker"},"spec":{"nodeSelector":{"matchLabels":{"node-role.kubernetes.io/worker":""}}},
  "status":{"configuration":{"name":"rendered-worker-d4e5f6"}}}
]}`

func fakeNodes(t *testing.T) (kube.Client, func() (args, stdin string)) {
	t.Helper()
	dir := t.TempDir()
	for name, body := range map[string]string{"nodes.json": nodesFixture, "pools.json": poolsForNodes} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	script := `D="$(dirname "$0")"
echo "$@" > "$D/args"
case "$*" in
  *patch*) cat > "$D/stdin"; echo ok ;;
  *machineconfigpool*) cat "$D/pools.json" ;;
  *)
    name=$(echo "$*" | sed -n "s/.*get nodes \([^ -][^ ]*\).*/\1/p")
    python3 -c "
import json,sys
d=json.load(open('$D/nodes.json'))
name='$name'
if name:
    for i in d['items']:
        if i['metadata']['name']==name: print(json.dumps(i)); sys.exit(0)
    sys.stderr.write('Error from server (NotFound): '+name+' not found\n'); sys.exit(1)
print(json.dumps(d))" ;;
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

func nodeRow(t *testing.T, res Result, name string) []string {
	t.Helper()
	for _, r := range res.(Table).Rows {
		if r.Cells[0] == name {
			return r.Cells
		}
	}
	t.Fatalf("no row for %q", name)
	return nil
}

// The zsh version bound to the Ready condition, so a node without one produced
// no line at all.
func TestNodeWithoutAReadyConditionStillAppears(t *testing.T) {
	c, _ := fakeNodes(t)

	res, err := nodeStatus(context.Background(), c, "")
	if err != nil {
		t.Fatal(err)
	}
	if got := len(res.(Table).Rows); got != 5 {
		t.Fatalf("got %d rows, want all 5 nodes", got)
	}
	if got := nodeRow(t, res, "infra-0")[2]; got != "Unknown" {
		t.Errorf("infra-0 ready = %q, want Unknown", got)
	}
}

// A forgotten cordon blocks a drain indefinitely and was invisible before.
func TestCordonIsVisible(t *testing.T) {
	c, _ := fakeNodes(t)

	res, _ := nodeStatus(context.Background(), c, "")
	if got := nodeRow(t, res, "worker-07")[4]; got != "cordoned" {
		t.Errorf("worker-07 sched = %q", got)
	}
	if got := nodeRow(t, res, "worker-01")[4]; got != "" {
		t.Errorf("worker-01 sched = %q, want empty", got)
	}
}

// currentConfig != desiredConfig with state Done is a node that has not picked
// the new config up yet — the question the old `nodes config` answered.
func TestConfigStateFoldsTheAnnotations(t *testing.T) {
	c, _ := fakeNodes(t)

	res, _ := nodeStatus(context.Background(), c, "")
	for name, want := range map[string]string{
		// The master's annotation says Done, but its pool wants a config it
		// has never been told about.
		"master-000000": "behind",
		"worker-01":     "pending",
		"worker-03":     "Working",
		"worker-07":     "Degraded",
		"infra-0":       "",
	} {
		if got := nodeRow(t, res, name)[5]; got != want {
			t.Errorf("%s config = %q, want %q", name, got, want)
		}
	}
}

// "failed to drain node" beats "kubelet is posting ready status" as an
// explanation for a node that is holding up a rollout.
func TestDegradedNodeShowsTheOperatorsReason(t *testing.T) {
	c, _ := fakeNodes(t)

	res, _ := nodeStatus(context.Background(), c, "")
	if got := nodeRow(t, res, "worker-07")[7]; !strings.Contains(got, "failed to drain") {
		t.Errorf("worker-07 message = %q", got)
	}
	if got := nodeRow(t, res, "worker-01")[7]; !strings.Contains(got, "kubelet is posting") {
		t.Errorf("worker-01 message = %q", got)
	}
}

func TestNodesSortByHowMuchTheyAreInTheWay(t *testing.T) {
	c, _ := fakeNodes(t)

	res, _ := nodeStatus(context.Background(), c, "")
	var got []string
	for _, r := range res.(Table).Rows {
		got = append(got, r.Cells[0])
	}

	// Not ready first, then config trouble, then merely cordoned, then fine.
	want := "worker-03,infra-0,worker-07,worker-01,master-000000"
	if strings.Join(got, ",") != want {
		t.Errorf("order = %v", got)
	}
}

// A node can hold several roles; on a single-node cluster it holds all of them.
func TestRolesAreReadFromLabels(t *testing.T) {
	c, _ := fakeNodes(t)

	res, _ := nodeStatus(context.Background(), c, "")
	if got := nodeRow(t, res, "master-000000")[1]; got != "control-plane,master" {
		t.Errorf("roles = %q", got)
	}
}

// The pool drill-down turns a nodeSelector into this call, so it has to reach
// kubectl unchanged.
func TestSelectorIsPassedThrough(t *testing.T) {
	c, read := fakeNodes(t)

	if _, err := listNodes(context.Background(), c, "node-role.kubernetes.io/worker=", ""); err != nil {
		t.Fatal(err)
	}
	if args, _ := read(); !strings.Contains(args, "-l node-role.kubernetes.io/worker=") {
		t.Errorf("args = %q", args)
	}
}

func TestCordonPatchesTheShownNodes(t *testing.T) {
	c, read := fakeNodes(t)

	res, _ := nodeStatus(context.Background(), c, "")
	one := Table{Cols: res.(Table).Cols, Rows: res.(Table).Rows[:1]}

	if _, err := cordonAction(c, true).Run(context.Background(), one, nil); err != nil {
		t.Fatal(err)
	}
	args, stdin := read()
	if !strings.Contains(args, `"unschedulable":true`) {
		t.Errorf("args = %q", args)
	}
	if !strings.Contains(stdin, "worker-03") {
		t.Errorf("stdin = %q", stdin)
	}

	if _, err := cordonAction(c, false).Run(context.Background(), one, nil); err != nil {
		t.Fatal(err)
	}
	if args, _ := read(); !strings.Contains(args, `"unschedulable":null`) {
		t.Errorf("uncordon args = %q", args)
	}
}

// Draining is deliberately not offered, so the prompt must not let anyone
// think the workload moves.
func TestCordonSaysWhatItDoesNotDo(t *testing.T) {
	c, _ := fakeNodes(t)

	res, _ := nodeStatus(context.Background(), c, "")
	got := cordonAction(c, true).Confirm(res, nil)
	if !strings.Contains(got, "Running pods stay where they are") {
		t.Errorf("confirmation = %q", got)
	}
}

func TestNodeConfigShowsTheFullNames(t *testing.T) {
	c, _ := fakeNodes(t)

	res, err := nodeConfig(context.Background(), c, "worker-01")
	if err != nil {
		t.Fatal(err)
	}

	joined := detailOf(res)
	for _, want := range []string{
		"node current=rendered-worker-a1b2c3",
		"node desired=rendered-worker-d4e5f6",
		"state=pending",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("missing %q in %q", want, joined)
		}
	}
}

func detailOf(res Result) string {
	var b strings.Builder
	for _, r := range res.(Table).Rows {
		b.WriteString(strings.Join(r.Cells, "=") + ";")
	}
	return b.String()
}

// The state annotation is not enough on its own: until the operator gets round
// to a node it leaves everything as it was, so a node that has never been told
// about a new config reads as "Done" while sitting on the old one.
func TestConfigStateIsJudgedAgainstThePoolTarget(t *testing.T) {
	var n node
	doc := `{"metadata":{"name":"x","annotations":{
	  "machineconfiguration.openshift.io/currentConfig":"rendered-old",
	  "machineconfiguration.openshift.io/desiredConfig":"rendered-old",
	  "machineconfiguration.openshift.io/state":"Done"}}}`
	if err := json.Unmarshal([]byte(doc), &n); err != nil {
		t.Fatal(err)
	}

	if got := n.configState("rendered-new"); got != "behind" {
		t.Errorf("with the pool ahead: %q, want behind", got)
	}
	if got := n.configState("rendered-old"); got != "Done" {
		t.Errorf("with the pool matching: %q, want Done", got)
	}
	// No pool to compare against: fall back to what the node says rather than
	// inventing a verdict.
	if got := n.configState(""); got != "Done" {
		t.Errorf("without a pool: %q", got)
	}
}

// A node told to change but not finished is a different situation from one
// that was never told, and the words have to distinguish them.
func TestPendingBeatsBehind(t *testing.T) {
	var n node
	doc := `{"metadata":{"name":"x","annotations":{
	  "machineconfiguration.openshift.io/currentConfig":"rendered-old",
	  "machineconfiguration.openshift.io/desiredConfig":"rendered-new",
	  "machineconfiguration.openshift.io/state":"Done"}}}`
	if err := json.Unmarshal([]byte(doc), &n); err != nil {
		t.Fatal(err)
	}

	if got := n.configState("rendered-new"); got != "pending" {
		t.Errorf("state = %q, want pending", got)
	}
}

// The detail view has to make the gap readable, which means showing all three
// values side by side.
func TestNodeConfigNamesThePoolAndItsTarget(t *testing.T) {
	c, _ := fakeNodes(t)

	res, err := nodeConfig(context.Background(), c, "master-000000")
	if err != nil {
		t.Fatal(err)
	}

	joined := detailOf(res)
	for _, want := range []string{
		"state=behind", "pool=master", "pool target=rendered-master-NEWER",
		"node current=rendered-master-9f8e7d", "annotation=Done",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("missing %q in %q", want, joined)
		}
	}
}

// Where several pools match, the most specific selector wins — that is how a
// custom pool takes precedence over the worker pool it is carved out of.
func TestPoolForPrefersTheMostSpecificSelector(t *testing.T) {
	var n node
	if err := json.Unmarshal([]byte(`{"metadata":{"name":"x","labels":{
	  "node-role.kubernetes.io/worker":"","node-role.kubernetes.io/storage":""}}}`), &n); err != nil {
		t.Fatal(err)
	}

	mk := func(name string, labels map[string]string, target string) machineConfigPool {
		var p machineConfigPool
		p.Metadata.Name = name
		p.Spec.NodeSelector.MatchLabels = labels
		p.Status.Configuration.Name = target
		return p
	}
	pools := []machineConfigPool{
		mk("worker", map[string]string{"node-role.kubernetes.io/worker": ""}, "rendered-worker"),
		mk("storage", map[string]string{
			"node-role.kubernetes.io/worker":  "",
			"node-role.kubernetes.io/storage": "",
		}, "rendered-storage"),
	}

	got, ok := poolFor(n, pools)
	if !ok {
		t.Fatal("no pool matched")
	}
	if got.Metadata.Name != "storage" {
		t.Errorf("matched %q, want the more specific pool", got.Metadata.Name)
	}
}

// Losing the whole view because MachineConfigPools cannot be read would be
// worse than a CONFIG column that falls back to the node's own account.
func TestNodesStillListWhenPoolsCannotBeRead(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "nodes.json"), []byte(nodesFixture), 0o644); err != nil {
		t.Fatal(err)
	}
	script := `D="$(dirname "$0")"
case "$*" in
  *machineconfigpool*) echo "forbidden" >&2; exit 1 ;;
  *) cat "$D/nodes.json" ;;
esac`
	bin := filepath.Join(dir, "kubectl")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\n"+script+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	res, err := nodeStatus(context.Background(), kube.Client{Bin: bin}, "")
	if err != nil {
		t.Fatalf("the view failed because pools were unreadable: %v", err)
	}
	if got := len(res.(Table).Rows); got != 5 {
		t.Errorf("got %d rows", got)
	}
	if got := nodeRow(t, res, "master-000000")[5]; got != "Done" {
		t.Errorf("without pools the fallback is %q, want the node's own Done", got)
	}
}

func TestNodeConditionsShowsEveryCondition(t *testing.T) {
	c, _ := fakeNodes(t)

	res, err := nodeConditions(context.Background(), c, "master-000000")
	if err != nil {
		t.Fatal(err)
	}
	if got := len(res.(Table).Rows); got != 2 {
		t.Errorf("got %d conditions, want MemoryPressure and Ready", got)
	}
	// "True" is good on Ready and bad on DiskPressure, so no colour here.
	if res.(Table).Emphasis != nil {
		t.Error("node conditions are coloured")
	}
}

func TestNodeDrillDownsRejectAMissingNode(t *testing.T) {
	c, _ := fakeNodes(t)

	if _, err := nodeConditions(context.Background(), c, "nope"); err == nil {
		t.Error("conditions accepted a missing node")
	}
	if _, err := nodeConfig(context.Background(), c, "nope"); err == nil {
		t.Error("config accepted a missing node")
	}
}

// The chain one actually walks: the pool is stuck, which nodes does it cover.
func TestPoolSelectorBecomesANodeQuery(t *testing.T) {
	var p machineConfigPool
	doc := `{"metadata":{"name":"worker"},"spec":{"nodeSelector":{"matchLabels":{"node-role.kubernetes.io/worker":""}}}}`
	if err := json.Unmarshal([]byte(doc), &p); err != nil {
		t.Fatal(err)
	}

	got, err := p.selector()
	if err != nil {
		t.Fatal(err)
	}
	if got != "node-role.kubernetes.io/worker=" {
		t.Errorf("selector = %q", got)
	}
}

// Listing the wrong nodes would be worse than saying no.
func TestPoolSelectorRefusesWhatItCannotRender(t *testing.T) {
	var expr machineConfigPool
	if err := json.Unmarshal([]byte(`{"metadata":{"name":"w"},"spec":{"nodeSelector":{"matchExpressions":[{}]}}}`), &expr); err != nil {
		t.Fatal(err)
	}
	if _, err := expr.selector(); err == nil {
		t.Error("matchExpressions were silently dropped")
	}

	var none machineConfigPool
	if err := json.Unmarshal([]byte(`{"metadata":{"name":"w"},"spec":{}}`), &none); err != nil {
		t.Fatal(err)
	}
	if _, err := none.selector(); err == nil {
		t.Error("a pool without a selector produced one")
	}
}

// The column exists to be lined up against job start times and log lines, so
// it carries a timestamp rather than an age.
func TestReadySinceIsAnAbsoluteTime(t *testing.T) {
	c, _ := fakeNodes(t)

	res, err := nodeStatus(context.Background(), c, "")
	if err != nil {
		t.Fatal(err)
	}

	got := nodeRow(t, res, "worker-03")[3]
	if _, err := time.ParseInLocation("2006-01-02 15:04", got, time.Local); err != nil {
		t.Errorf("SINCE = %q, which is not a timestamp", got)
	}

	// A node without a Ready condition has no transition to show.
	if got := nodeRow(t, res, "infra-0")[3]; got != "" {
		t.Errorf("infra-0 SINCE = %q, want empty", got)
	}
}

// mkPool builds a pool for the assignment tests.
func mkPool(name, label, target string) machineConfigPool {
	var p machineConfigPool
	p.Metadata.Name = name
	p.Spec.NodeSelector.MatchLabels = map[string]string{roleLabelPrefix + label: ""}
	p.Status.Configuration.Name = target
	return p
}

func mkNode(t *testing.T, roles ...string) node {
	t.Helper()
	labels := map[string]string{}
	for _, r := range roles {
		labels[roleLabelPrefix+r] = ""
	}
	raw, err := json.Marshal(map[string]any{"metadata": map[string]any{"name": "n", "labels": labels}})
	if err != nil {
		t.Fatal(err)
	}
	var n node
	if err := json.Unmarshal(raw, &n); err != nil {
		t.Fatal(err)
	}
	return n
}

// In these clusters the worker pool exists but holds nothing, and the real
// pools are named after an app or storage role. Both select on one label, so
// picking by selector specificity handed the node to worker.
func TestCustomPoolBeatsTheWorkerPool(t *testing.T) {
	pools := []machineConfigPool{
		mkPool("worker", "worker", "rendered-worker-EMPTY"),
		mkPool("x-app", "app", "rendered-x-app-1111"),
		mkPool("x-storage", "storage", "rendered-x-storage-3333"),
	}

	for _, tc := range []struct {
		roles []string
		want  string
	}{
		{[]string{"worker", "app"}, "x-app"},
		{[]string{"worker", "storage"}, "x-storage"},
		{[]string{"worker"}, "worker"}, // no custom pool claims it
	} {
		got, ok := poolFor(mkNode(t, tc.roles...), pools)
		if !ok {
			t.Fatalf("%v matched no pool", tc.roles)
		}
		if got.Metadata.Name != tc.want {
			t.Errorf("%v landed in %q, want %q", tc.roles, got.Metadata.Name, tc.want)
		}
	}
}

// A control-plane node is never in a worker pool, whatever else matches.
func TestMasterPoolWinsOutright(t *testing.T) {
	pools := []machineConfigPool{
		mkPool("x-app", "app", "rendered-x-app-1111"),
		mkPool("master", "master", "rendered-master-2222"),
	}

	got, ok := poolFor(mkNode(t, "master", "app"), pools)
	if !ok || got.Metadata.Name != "master" {
		t.Errorf("landed in %q", got.Metadata.Name)
	}
}

func TestPoolForFindsNothingWhenNoSelectorMatches(t *testing.T) {
	pools := []machineConfigPool{mkPool("x-app", "app", "t")}

	if _, ok := poolFor(mkNode(t, "storage"), pools); ok {
		t.Error("a node matched a pool that does not select it")
	}
}

// A pool selecting by expression cannot be evaluated, so it must not quietly
// claim nodes either.
func TestPoolWithExpressionsSelectsNothing(t *testing.T) {
	var p machineConfigPool
	p.Metadata.Name = "x-app"
	p.Spec.NodeSelector.MatchExpressions = []any{map[string]any{}}

	if selects(p, mkNode(t, "app")) {
		t.Error("a matchExpressions pool claimed a node")
	}
}
