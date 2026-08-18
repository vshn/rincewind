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

// logTailMCO is how far back the controller log is read.
const logTailMCO = "5000"

// machineConfigPool is where an OpenShift upgrade actually stalls.
type machineConfigPool struct {
	Metadata struct {
		Name string `json:"name"`
	} `json:"metadata"`
	Spec struct {
		Paused bool `json:"paused"`
	} `json:"spec"`
	Status struct {
		Configuration struct {
			Name string `json:"name"`
		} `json:"configuration"`
		MachineCount            int            `json:"machineCount"`
		UpdatedMachineCount     int            `json:"updatedMachineCount"`
		ReadyMachineCount       int            `json:"readyMachineCount"`
		DegradedMachineCount    int            `json:"degradedMachineCount"`
		UnavailableMachineCount int            `json:"unavailableMachineCount"`
		Conditions              []jobCondition `json:"conditions"`
	} `json:"status"`

	raw json.RawMessage
}

func (p machineConfigPool) isTrue(kind string) bool {
	for _, c := range p.Status.Conditions {
		if c.Type == kind {
			return c.Status == "True"
		}
	}
	return false
}

func (p machineConfigPool) state() string {
	switch {
	case p.isTrue("Degraded") || p.isTrue("NodeDegraded") || p.isTrue("RenderDegraded"):
		return "Degraded"
	case p.isTrue("Updating"):
		return "Updating"
	case p.isTrue("Updated"):
		return "Updated"
	default:
		return "Unknown"
	}
}

var poolEmphasis = map[string]Emphasis{
	"Degraded": Bad,
	"Unknown":  Bad,
	"Updating": Warn,
	"PAUSED":   Warn,
	"Updated":  Good,
}

// addMCO registers the views that talk to OpenShift rather than to the appuio
// controller. MachineConfigPools and the ClusterVersion are cluster-scoped, so
// they use the unscoped client.
func addMCO(r *Registry, c kube.Client) {
	mco := c.In(mcoNamespace)
	clusterAdmin := c.As(adminUser)

	r.Add(View{
		Group: "upgrade", Name: "progress",
		Short:     "Show whether the cluster is upgrading, and what it is waiting on",
		Kind:      Snapshot,
		Available: c.Available,
		Run: func(ctx context.Context, _ Args) (Result, error) {
			return clusterVersion(ctx, c)
		},
	})

	r.Add(View{
		Group: "upgrade", Name: "pools",
		Short:     "List MachineConfigPools and their rollout",
		Kind:      Snapshot,
		Available: c.Available,
		Actions: []Action{
			poolPauseAction(clusterAdmin, true),
			poolPauseAction(clusterAdmin, false),
		},
		Run: func(ctx context.Context, _ Args) (Result, error) {
			return machineConfigPools(ctx, c)
		},
	})

	r.Add(View{
		Group: "upgrade", Name: "logs",
		Short:     "Show machine-config-controller logs",
		Kind:      Snapshot,
		Available: mco.Available,
		Run: func(ctx context.Context, _ Args) (Result, error) {
			return mcoLogs(ctx, mco)
		},
	})
}

func clusterVersion(ctx context.Context, c kube.Client) (Result, error) {
	var cv struct {
		Spec struct {
			Channel string `json:"channel"`
		} `json:"spec"`
		Status struct {
			Desired struct {
				Version string `json:"version"`
			} `json:"desired"`
			Conditions []jobCondition `json:"conditions"`
		} `json:"status"`
	}
	if err := kube.GetInto(ctx, c, &cv, "get", "clusterversion", "version"); err != nil {
		return nil, err
	}

	conds := cv.Status.Conditions
	sort.SliceStable(conds, func(a, b int) bool {
		return conds[a].LastTransitionTime < conds[b].LastTransitionTime
	})

	// Columns named for what they hold rather than for conditions, because the
	// headline facts share the table: "where are we" is the first question and
	// belongs above the conditions, not appended to them.
	// Only the derived headline row is coloured. The conditions below are not:
	// "True" is good on Available and bad on Failing, so colouring the bare
	// value would be a lie.
	t := Table{
		Cols:     []string{"WHEN", "WHAT", "VALUE", "REASON", "MESSAGE"},
		Emphasis: map[string]Emphasis{"blocked": Bad, "in progress": Warn},
	}

	if v := cv.Status.Desired.Version; v != "" {
		t.Add(nil, "", "version", v, "", "")
	}
	if ch := cv.Spec.Channel; ch != "" {
		t.Add(nil, "", "channel", ch, "", "")
	}

	// The headline answer, before the conditions it is read from. On a settled
	// cluster the conditions say nothing much, and without this row that reads
	// like the view is broken rather than like there is nothing going on.
	state, detail := upgradeProgress(conds)
	t.Add(nil, "", "upgrade", state, "", detail)

	for _, c := range conds {
		t.Add(nil, showTimeOf(c.LastTransitionTime), c.Type, c.Status, c.Reason, oneLine(c.Message))
	}
	return t, nil
}

// upgradeProgress summarises the Progressing and Failing conditions. It copies
// their messages rather than interpreting them; the only judgement is calling
// a cluster that is not progressing "idle".
func upgradeProgress(conds []jobCondition) (state, detail string) {
	byType := map[string]jobCondition{}
	for _, c := range conds {
		byType[c.Type] = c
	}

	state, detail = "idle", ""
	if p := byType["Progressing"]; p.Status == "True" {
		state, detail = "in progress", oneLine(p.Message)
	}

	// Failing carries the actual blocker, which is the next question after
	// "is it moving".
	if f := byType["Failing"]; f.Status == "True" {
		state = "blocked"
		if detail == "" {
			detail = oneLine(f.Message)
		}
	}
	return state, detail
}

func machineConfigPools(ctx context.Context, c kube.Client) (Result, error) {
	var list kube.List[json.RawMessage]
	if err := kube.GetInto(ctx, c, &list, "get", "machineconfigpool"); err != nil {
		return nil, err
	}

	pools := make([]machineConfigPool, 0, len(list.Items))
	for _, doc := range list.Items {
		var p machineConfigPool
		if err := json.Unmarshal(doc, &p); err != nil {
			return nil, fmt.Errorf("decoding machine config pool: %w", err)
		}
		if p.Metadata.Name == "" {
			return nil, fmt.Errorf("response is not a MachineConfigPool")
		}
		p.raw = doc
		pools = append(pools, p)
	}

	// Objects with a health state, so trouble first. See docs/architecture.md.
	sort.SliceStable(pools, func(i, j int) bool {
		return lessByTrouble(
			pools[i].state() == "Updated", pools[i].Status.DegradedMachineCount == 0, pools[i].Metadata.Name,
			pools[j].state() == "Updated", pools[j].Status.DegradedMachineCount == 0, pools[j].Metadata.Name,
		)
	})

	t := Table{
		Cols:     []string{"NAME", "STATE", "PAUSED", "UPDATED", "READY", "DEGRADED", "CONFIG"},
		Emphasis: poolEmphasis,
	}
	for _, p := range pools {
		paused := ""
		if p.Spec.Paused {
			paused = "PAUSED"
		}
		degraded := ""
		if p.Status.DegradedMachineCount > 0 {
			degraded = fmt.Sprint(p.Status.DegradedMachineCount)
		}
		t.Add(p.raw,
			p.Metadata.Name,
			p.state(),
			paused,
			fmt.Sprintf("%d/%d", p.Status.UpdatedMachineCount, p.Status.MachineCount),
			fmt.Sprintf("%d/%d", p.Status.ReadyMachineCount, p.Status.MachineCount),
			degraded,
			p.Status.Configuration.Name,
		)
	}
	return t, nil
}

// mcoLogs reads the controller deployment's log.
func mcoLogs(ctx context.Context, c kube.Client) (Result, error) {
	raw, err := c.Output(ctx, "logs", "deploy/machine-config-controller", "--tail", logTailMCO)
	if err != nil {
		return nil, err
	}

	lines := splitLines(string(raw))
	if len(lines) == 0 {
		return Text{Lines: []string{"no log lines"}}, nil
	}
	return Text{Lines: lines}, nil
}

func showTimeOf(rfc3339 string) string {
	at, err := time.Parse(time.RFC3339, rfc3339)
	if err != nil {
		return rfc3339
	}
	return showTime(at)
}

func joinNames(names []string) string { return strings.Join(names, ", ") }
