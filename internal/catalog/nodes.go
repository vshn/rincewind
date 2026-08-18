package catalog

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/vshn/aldediggi/internal/kube"
)

// The machine-config operator records a node's rollout in annotations. The
// reason is where "failed to drain node: …" ends up, which is the answer one
// is usually after and which the zsh version never showed.
const (
	mcoCurrentConfig = "machineconfiguration.openshift.io/currentConfig"
	mcoDesiredConfig = "machineconfiguration.openshift.io/desiredConfig"
	mcoState         = "machineconfiguration.openshift.io/state"
	mcoReason        = "machineconfiguration.openshift.io/reason"

	roleLabelPrefix = "node-role.kubernetes.io/"

	// The two pools the machine-config operator ships with. Every other pool
	// is a custom one carved out of worker, and takes precedence over it.
	masterPool = "master"
	workerPool = "worker"
)

type node struct {
	Metadata struct {
		Name        string            `json:"name"`
		Labels      map[string]string `json:"labels"`
		Annotations map[string]string `json:"annotations"`
	} `json:"metadata"`
	Spec struct {
		Unschedulable bool `json:"unschedulable"`
	} `json:"spec"`
	Status struct {
		Conditions []jobCondition `json:"conditions"`
		NodeInfo   struct {
			KubeletVersion string `json:"kubeletVersion"`
		} `json:"nodeInfo"`
	} `json:"status"`

	raw json.RawMessage
}

func (n node) name() string              { return n.Metadata.Name }
func (n *node) keep(doc json.RawMessage) { n.raw = doc }

func (n node) ann(key string) string { return n.Metadata.Annotations[key] }

// roles reads the node-role labels. A node can hold several, and on a
// single-node cluster it holds all of them.
func (n node) roles() string {
	var out []string
	for label := range n.Metadata.Labels {
		if role, ok := strings.CutPrefix(label, roleLabelPrefix); ok && role != "" {
			out = append(out, role)
		}
	}
	sort.Strings(out)
	return strings.Join(out, ",")
}

func (n node) readyCondition() (jobCondition, bool) {
	for _, c := range n.Status.Conditions {
		if c.Type == "Ready" {
			return c, true
		}
	}
	return jobCondition{}, false
}

// ready reports the kubelet's verdict. A node whose Ready condition is missing
// entirely still gets a row — the zsh version dropped it silently.
func (n node) ready() string {
	c, ok := n.readyCondition()
	if !ok {
		return "Unknown"
	}
	switch c.Status {
	case "True":
		return "Ready"
	case "False":
		return "NotReady"
	default:
		return "Unknown"
	}
}

// configState folds the machine-config annotations into one answer, judged
// against the pool's rendered target rather than against the node alone.
//
// The state annotation cannot be trusted on its own: until the operator gets
// round to a node it leaves currentConfig, desiredConfig and state as they
// were, so a node that has not been told about a new config reads as "Done"
// while sitting on the old one. Comparing against the pool is the only way to
// tell "finished" from "not started". poolTarget is empty when no pool could
// be determined, and then this falls back to what the node says.
func (n node) configState(poolTarget string) string {
	state := n.ann(mcoState)
	if state == "Degraded" || state == "Working" {
		return state
	}

	cur, desired := n.ann(mcoCurrentConfig), n.ann(mcoDesiredConfig)
	if cur == "" {
		return state
	}
	if desired != "" && cur != desired {
		return "pending" // told to change, has not finished
	}
	if poolTarget != "" && cur != poolTarget {
		return "behind" // not told yet; the pool has moved on without it
	}
	return state
}

// poolFor finds the pool covering a node, following the rule the machine-config
// operator itself uses rather than guessing from the selectors.
//
// A node belongs to exactly one pool. master wins outright. Otherwise a custom
// pool — anything not named worker — beats the worker pool, which is only the
// fallback for nodes no custom pool claims. Selector specificity is no help
// here: in a setup where the worker pool holds nothing and the real pools are
// named after an app or storage role, worker and the custom pool select on one
// label each, and the tie went to whichever sorted first.
//
// Two custom pools matching one node is a state the operator itself treats as
// an error. The most specific wins, then the first by name, so at least the
// answer is stable; `nodes config NODE` names the pool that was picked.
func poolFor(n node, pools []machineConfigPool) (machineConfigPool, bool) {
	var matching []machineConfigPool
	for _, p := range pools {
		if selects(p, n) {
			matching = append(matching, p)
		}
	}

	var worker machineConfigPool
	var custom []machineConfigPool
	for _, p := range matching {
		switch p.Metadata.Name {
		case masterPool:
			return p, true
		case workerPool:
			worker = p
		default:
			custom = append(custom, p)
		}
	}

	if len(custom) > 0 {
		sort.SliceStable(custom, func(i, j int) bool {
			li := len(custom[i].Spec.NodeSelector.MatchLabels)
			lj := len(custom[j].Spec.NodeSelector.MatchLabels)
			if li != lj {
				return li > lj
			}
			return custom[i].Metadata.Name < custom[j].Metadata.Name
		})
		return custom[0], true
	}

	return worker, worker.Metadata.Name != ""
}

// selects reports whether a pool's nodeSelector covers a node. Only matchLabels
// are read; a pool selecting by expression is treated as not matching, which
// `selector()` explains when one tries to drill into it.
func selects(p machineConfigPool, n node) bool {
	labels := p.Spec.NodeSelector.MatchLabels
	if len(labels) == 0 {
		return false
	}
	for k, v := range labels {
		if got, ok := n.Metadata.Labels[k]; !ok || got != v {
			return false
		}
	}
	return true
}

// message prefers the operator's reason when the rollout is unhappy: "failed
// to drain node" beats "kubelet is posting ready status" as an explanation.
func (n node) message(poolTarget string) string {
	if n.configState(poolTarget) == "Degraded" {
		if reason := n.ann(mcoReason); reason != "" {
			return oneLine(reason)
		}
	}
	if c, ok := n.readyCondition(); ok {
		return oneLine(c.Message)
	}
	return ""
}

// readySince is an absolute timestamp rather than an age: this column exists to
// be lined up against job start times and log lines, and "4h59m" cannot be.
// Durations are for spans; points in time are shown as points in time.
func (n node) readySince() string {
	c, ok := n.readyCondition()
	if !ok {
		return ""
	}
	if _, err := time.Parse(time.RFC3339, c.LastTransitionTime); err != nil {
		return ""
	}
	return showTimeOf(c.LastTransitionTime)
}

func (n node) transition() time.Time {
	c, _ := n.readyCondition()
	at, _ := time.Parse(time.RFC3339, c.LastTransitionTime)
	return at
}

func (n node) cordoned() string {
	if n.Spec.Unschedulable {
		return "cordoned"
	}
	return ""
}

var nodeEmphasis = map[string]Emphasis{
	"NotReady": Bad,
	"Unknown":  Bad,
	"Degraded": Bad,
	"cordoned": Warn,
	"Working":  Warn,
	"pending":  Warn,
	"behind":   Warn,
	"Ready":    Good,
	"Done":     Good,
}

// Nodes builds the nodes catalogue. MachineConfigPools live here rather than
// under upgrade: a pool is a group of nodes, and the drill-down from a stalled
// pool to the node holding it up stays inside one group.
func Nodes(c kube.Client) *Registry {
	r := NewRegistry()
	r.Describe("nodes", "All things node related")

	admin := c.As(adminUser)

	nodeParam := []Param{{
		Name:     "node",
		Short:    "node name",
		Required: true,
		Complete: func(ctx context.Context) ([]string, error) { return nodeNames(ctx, c) },
	}}

	r.Add(View{
		Group: "nodes", Name: "status",
		Short:     "List nodes: kubelet, scheduling and machine-config rollout",
		Kind:      Snapshot,
		Available: c.Available,
		Params: []Param{{
			Name:  "selector",
			Short: "label selector, as kubectl -l takes it",
		}},
		Actions: []Action{
			openNodeConditions(),
			openNodeConfig(),
			cordonAction(admin, true),
			cordonAction(admin, false),
		},
		Run: func(ctx context.Context, args Args) (Result, error) {
			return nodeStatus(ctx, c, args.Get("selector"))
		},
	})

	r.Add(View{
		Group: "nodes", Name: "conditions",
		Short:     "Show one node's conditions, as the kubelet wrote them",
		Kind:      Snapshot,
		Hidden:    true,
		Available: c.Available,
		Params:    nodeParam,
		Actions:   []Action{openNodeConfig()},
		Run: func(ctx context.Context, args Args) (Result, error) {
			return nodeConditions(ctx, c, args.Get("node"))
		},
	})

	r.Add(View{
		Group: "nodes", Name: "config",
		Short:     "Show one node's machine-config rollout in full",
		Kind:      Snapshot,
		Hidden:    true,
		Available: c.Available,
		Params:    nodeParam,
		Actions:   []Action{openNodeConditions()},
		Run: func(ctx context.Context, args Args) (Result, error) {
			return nodeConfig(ctx, c, args.Get("node"))
		},
	})

	r.Add(View{
		Group: "nodes", Name: "pools",
		Short:     "List MachineConfigPools and their rollout",
		Kind:      Snapshot,
		Available: c.Available,
		Params: []Param{{
			Name:     "pool",
			Short:    "limit to one pool",
			Complete: func(ctx context.Context) ([]string, error) { return poolNames(ctx, c) },
		}},
		Actions: []Action{
			openPoolNodes(),
			poolPauseAction(admin, true),
			poolPauseAction(admin, false),
		},
		Run: func(ctx context.Context, args Args) (Result, error) {
			return machineConfigPools(ctx, c, args.Get("pool"))
		},
	})

	return r
}

func listNodes(ctx context.Context, c kube.Client, selector, only string) ([]node, error) {
	var extra []string
	if selector != "" {
		extra = []string{"-l", selector}
	}
	return fetch[node](ctx, c, "Node", "nodes", only, extra...)
}

func nodeNames(ctx context.Context, c kube.Client) ([]string, error) {
	nodes, err := listNodes(ctx, c, "", "")
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(nodes))
	for _, n := range nodes {
		out = append(out, n.Metadata.Name)
	}
	return out, nil
}

// nodeRank orders by how much a node is in the way.
func nodeRank(n node, poolTarget string) int {
	switch state := n.configState(poolTarget); {
	case n.ready() != "Ready":
		return 0
	case state != "" && state != "Done":
		return 1
	case n.Spec.Unschedulable:
		return 2
	default:
		return 3
	}
}

func nodeStatus(ctx context.Context, c kube.Client, selector string) (Result, error) {
	nodes, err := listNodes(ctx, c, selector, "")
	if err != nil {
		return nil, err
	}

	// Best effort: without the pools the CONFIG column falls back to what each
	// node says about itself, which is worse but still useful. Losing the whole
	// view because MachineConfigPools cannot be read would not be.
	targets := poolTargets(ctx, c, nodes)

	// Trouble first, and within a class the most recent change on top: when a
	// node falls over, that is the row you came for.
	sort.SliceStable(nodes, func(i, j int) bool {
		ri := nodeRank(nodes[i], targets[nodes[i].Metadata.Name])
		rj := nodeRank(nodes[j], targets[nodes[j].Metadata.Name])
		if ri != rj {
			return ri < rj
		}
		if ti, tj := nodes[i].transition(), nodes[j].transition(); !ti.Equal(tj) {
			return ti.After(tj)
		}
		return nodes[i].Metadata.Name < nodes[j].Metadata.Name
	})

	t := Table{
		Cols:     []string{"NAME", "ROLE", "READY", "SINCE", "SCHED", "CONFIG", "VERSION", "MESSAGE"},
		Emphasis: nodeEmphasis,
	}
	for _, n := range nodes {
		target := targets[n.Metadata.Name]
		t.Add(n.raw,
			n.Metadata.Name,
			n.roles(),
			n.ready(),
			n.readySince(),
			n.cordoned(),
			n.configState(target),
			n.Status.NodeInfo.KubeletVersion,
			n.message(target),
		)
	}
	return t, nil
}

// poolTargets maps each node to the rendered config its pool is aiming at.
func poolTargets(ctx context.Context, c kube.Client, nodes []node) map[string]string {
	pools, err := listPools(ctx, c, "")
	if err != nil {
		return nil
	}

	out := make(map[string]string, len(nodes))
	for _, n := range nodes {
		if p, ok := poolFor(n, pools); ok {
			out[n.Metadata.Name] = p.Status.Configuration.Name
		}
	}
	return out
}

func oneNode(ctx context.Context, c kube.Client, name string) (node, error) {
	if name == "" {
		return node{}, fmt.Errorf("no node given")
	}
	nodes, err := listNodes(ctx, c, "", name)
	if err != nil {
		return node{}, err
	}
	if len(nodes) == 0 {
		return node{}, fmt.Errorf("node %q not found", name)
	}
	return nodes[0], nil
}

// nodeConditions shows the kubelet's own words. DiskPressure and MemoryPressure
// live here, and they are what the single READY column cannot say.
func nodeConditions(ctx context.Context, c kube.Client, name string) (Result, error) {
	n, err := oneNode(ctx, c, name)
	if err != nil {
		return nil, err
	}

	conds := n.Status.Conditions
	sort.SliceStable(conds, func(a, b int) bool {
		return conds[a].LastTransitionTime < conds[b].LastTransitionTime
	})

	// No emphasis: "True" is good on Ready and bad on DiskPressure.
	t := Table{Cols: []string{"WHEN", "TYPE", "STATUS", "REASON", "MESSAGE"}}
	for _, cond := range conds {
		t.Add(nil, showTimeOf(cond.LastTransitionTime), cond.Type, cond.Status, cond.Reason, oneLine(cond.Message))
	}
	return t, nil
}

// nodeConfig shows the rollout in full. The rendered config names are far too
// long for a list but are exactly what one compares against the pool.
func nodeConfig(ctx context.Context, c kube.Client, name string) (Result, error) {
	n, err := oneNode(ctx, c, name)
	if err != nil {
		return nil, err
	}

	// The pool's target is the third value, and the one that decides whether
	// "Done" means finished or merely untouched.
	var pool machineConfigPool
	var target string
	if pools, err := listPools(ctx, c, ""); err == nil {
		if p, ok := poolFor(n, pools); ok {
			pool, target = p, p.Status.Configuration.Name
		}
	}

	t := Table{Cols: []string{"WHAT", "VALUE"}, Emphasis: nodeEmphasis}
	t.Add(nil, "state", n.configState(target))
	t.Add(nil, "pool", pool.Metadata.Name)
	t.Add(nil, "pool target", target)
	t.Add(nil, "node desired", n.ann(mcoDesiredConfig))
	t.Add(nil, "node current", n.ann(mcoCurrentConfig))
	t.Add(nil, "annotation", n.ann(mcoState))
	if reason := n.ann(mcoReason); reason != "" {
		t.Add(nil, "reason", oneLine(reason))
	}
	t.Add(nil, "schedulable", map[bool]string{true: "no", false: "yes"}[n.Spec.Unschedulable])
	return t, nil
}
