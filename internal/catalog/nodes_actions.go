package catalog

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/vshn/aldediggi/internal/kube"
)

// cordonAction stops or resumes scheduling on the listed nodes. A forgotten
// cordon blocks a drain indefinitely, which is why the state has its own
// column and why undoing it is one key away.
func cordonAction(c kube.Client, cordon bool) Action {
	name, key, verb := "uncordon", "C", "Allow scheduling on"
	patch := `{"spec":{"unschedulable":null}}`
	if cordon {
		name, key, verb = "cordon", "c", "Stop scheduling on"
		patch = `{"spec":{"unschedulable":true}}`
	}

	return Action{
		Name:    name,
		Key:     key,
		Short:   verb + " the listed nodes",
		Scope:   Rows,
		Refresh: true,
		Confirm: func(in Result, _ Args) string {
			names := nodeNamesIn(in)
			msg := fmt.Sprintf("%s %d node(s): %s?", verb, len(names), joinNames(names))
			if cordon {
				// Draining is deliberately not offered; say what this does and
				// does not do, so nobody expects the workload to move.
				msg += "\n\nRunning pods stay where they are; this only stops new ones landing."
			}
			return msg
		},
		Run: func(ctx context.Context, in Result, _ Args) (Result, error) {
			return patchRows(ctx, c, in, patch)
		},
	}
}

func openNodeConditions() Action {
	return Action{
		Name:  "conditions",
		Key:   "enter",
		Short: "Show this node's conditions",
		Scope: Rows,
		Run: func(_ context.Context, in Result, args Args) (Result, error) {
			name, err := oneNodeName(in, args)
			if err != nil {
				return nil, err
			}
			return Navigate{View: "nodes.conditions", Args: Args{"node": name}}, nil
		},
	}
}

func openNodeConfig() Action {
	return Action{
		Name:  "config",
		Key:   "m",
		Short: "Show this node's machine-config rollout",
		Scope: Rows,
		Run: func(_ context.Context, in Result, args Args) (Result, error) {
			name, err := oneNodeName(in, args)
			if err != nil {
				return nil, err
			}
			return Navigate{View: "nodes.config", Args: Args{"node": name}}, nil
		},
	}
}

// openPoolNodes turns a pool's node selector into the node list it covers.
// This is the chain one actually walks: the pool is stuck, which node is
// holding it up, and why.
func openPoolNodes() Action {
	return Action{
		Name:  "nodes",
		Key:   "enter",
		Short: "Show the nodes in this pool",
		Scope: Rows,
		Run: func(_ context.Context, in Result, _ Args) (Result, error) {
			pool, err := onePool(in)
			if err != nil {
				return nil, err
			}
			selector, err := pool.selector()
			if err != nil {
				return nil, err
			}
			return Navigate{View: "nodes.status", Args: Args{"selector": selector}}, nil
		},
	}
}

// oneNodeName resolves which node to drill into: the selected row, or the
// view's parameter when the rows are already one node's conditions.
func oneNodeName(in Result, args Args) (string, error) {
	if name := args.Get("node"); name != "" {
		return name, nil
	}

	names := nodeNamesIn(in)
	switch len(names) {
	case 1:
		return names[0], nil
	case 0:
		return "", fmt.Errorf("no node selected")
	default:
		return "", fmt.Errorf("select one node first (%d shown; tab into the list, or filter)", len(names))
	}
}

func nodeNamesIn(in Result) []string {
	t, ok := in.(Table)
	if !ok {
		return nil
	}

	var out []string
	for _, src := range t.Sources() {
		raw, ok := src.(json.RawMessage)
		if !ok {
			continue
		}
		var n node
		if err := json.Unmarshal(raw, &n); err == nil && n.Metadata.Name != "" {
			out = append(out, n.Metadata.Name)
		}
	}
	return out
}

func onePool(in Result) (machineConfigPool, error) {
	t, ok := in.(Table)
	if !ok {
		return machineConfigPool{}, fmt.Errorf("this action needs a table, got %T", in)
	}

	var pools []machineConfigPool
	for _, src := range t.Sources() {
		raw, ok := src.(json.RawMessage)
		if !ok {
			continue
		}
		var p machineConfigPool
		if err := json.Unmarshal(raw, &p); err == nil && p.Metadata.Name != "" {
			pools = append(pools, p)
		}
	}

	switch len(pools) {
	case 1:
		return pools[0], nil
	case 0:
		return machineConfigPool{}, fmt.Errorf("no pool selected")
	default:
		return machineConfigPool{}, fmt.Errorf("select one pool first (%d shown)", len(pools))
	}
}

// selector renders the pool's nodeSelector the way `kubectl -l` takes it.
// matchExpressions are refused rather than quietly dropped: silently listing
// the wrong nodes would be worse than saying no.
func (p machineConfigPool) selector() (string, error) {
	if len(p.Spec.NodeSelector.MatchExpressions) > 0 {
		return "", fmt.Errorf("pool %q selects nodes by expression, which this view cannot render; use kubectl", p.Metadata.Name)
	}
	if len(p.Spec.NodeSelector.MatchLabels) == 0 {
		return "", fmt.Errorf("pool %q has no node selector", p.Metadata.Name)
	}

	parts := make([]string, 0, len(p.Spec.NodeSelector.MatchLabels))
	for k, v := range p.Spec.NodeSelector.MatchLabels {
		parts = append(parts, k+"="+v)
	}
	sort.Strings(parts) // stable, so the same pool always yields the same call
	return strings.Join(parts, ","), nil
}
