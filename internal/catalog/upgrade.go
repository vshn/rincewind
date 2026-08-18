package catalog

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"time"

	"github.com/vshn/aldediggi/internal/kube"
)

// Where the appuio upgrade controller and OpenShift's machine-config operator
// live. Fixed, like the ArgoCD namespace: properties of the platform.
const (
	upgradeNamespace = "appuio-openshift-upgrade-controller"
	mcoNamespace     = "openshift-machine-config-operator"

	suspensionAPIVersion = "managedupgrade.appuio.io/v1beta1"
	suspensionKind       = "UpgradeSuspensionWindow"

	// managedByLabel marks what this tool created, so the list can tell an
	// ad-hoc suspension from one that came out of git.
	managedByLabel = "app.kubernetes.io/managed-by"
	managedByValue = "aldediggi"
	argoLabel      = "argocd.argoproj.io/instance"
)

// suspension is the slice of an UpgradeSuspensionWindow this tool needs. The
// views call these "suspensions" rather than "windows": "maintenance window"
// already means something else in this environment, and the shortened name
// collided with it.
type suspension struct {
	Metadata struct {
		Name   string            `json:"name"`
		Labels map[string]string `json:"labels"`
	} `json:"metadata"`
	Spec struct {
		Start  string `json:"start"`
		End    string `json:"end"`
		Reason string `json:"reason"`
	} `json:"spec"`

	raw json.RawMessage
}

// source says who owns the window, which is what decides whether deleting it
// is safe or whether ArgoCD will simply put it back.
func (w suspension) source() string {
	switch {
	case w.Metadata.Labels[argoLabel] != "":
		return "argocd"
	case w.Metadata.Labels[managedByLabel] == managedByValue:
		return managedByValue
	default:
		return "-"
	}
}

func (w suspension) span() (start, end time.Time) {
	start, _ = time.Parse(time.RFC3339, w.Spec.Start)
	end, _ = time.Parse(time.RFC3339, w.Spec.End)
	return start, end
}

// Upgrade builds the upgrade catalogue. See docs/views.md.
func Upgrade(c kube.Client) *Registry {
	r := NewRegistry()
	r.Describe("upgrade", "All things cluster upgrade related")

	ctrl := c.In(upgradeNamespace)
	admin := ctrl.As(adminUser)

	jobParam := func(required bool) []Param {
		return []Param{{
			Name:     "job",
			Short:    "upgrade job name",
			Required: required,
			Complete: func(ctx context.Context) ([]string, error) { return jobNames(ctx, ctrl) },
		}}
	}

	r.Add(View{
		Group: "upgrade", Name: "jobs",
		Short:     "List UpgradeJobs and where each one got to",
		Kind:      Snapshot,
		Available: ctrl.Available,
		Actions:   []Action{openConditions(), openHooks()},
		Run: func(ctx context.Context, _ Args) (Result, error) {
			return upgradeJobs(ctx, ctrl, time.Now())
		},
	})

	r.Add(View{
		Group: "upgrade", Name: "conditions",
		Short: "Show one UpgradeJob's conditions, as the controller wrote them",
		Kind:  Snapshot,
		// Reached by drilling in from jobs; useless without one.
		Hidden:    true,
		Available: ctrl.Available,
		Params:    jobParam(true),
		Actions:   []Action{openHooks()},
		Run: func(ctx context.Context, args Args) (Result, error) {
			return upgradeConditions(ctx, ctrl, args.Get("job"))
		},
	})

	r.Add(View{
		Group: "upgrade", Name: "hooks",
		Short:     "Show one UpgradeJob's hooks",
		Kind:      Snapshot,
		Hidden:    true,
		Available: ctrl.Available,
		Params:    jobParam(true),
		Actions:   []Action{openConditions()},
		Run: func(ctx context.Context, args Args) (Result, error) {
			return upgradeHooks(ctx, ctrl, args.Get("job"))
		},
	})

	r.Add(View{
		Group: "upgrade", Name: "suspensions",
		Short:     "List upgrade suspensions",
		Kind:      Snapshot,
		Available: ctrl.Available,
		Actions: []Action{
			createSuspensionAction(admin),
			deleteSuspensionAction(admin),
		},
		Run: func(ctx context.Context, _ Args) (Result, error) {
			return upgradeSuspensions(ctx, ctrl, time.Now())
		},
	})

	addMCO(r, c)

	return r
}

var suspensionEmphasis = map[string]Emphasis{
	"ACTIVE":   Bad,
	"upcoming": Warn,
	"past":     Plain,
}

func listSuspensions(ctx context.Context, c kube.Client) ([]suspension, error) {
	raw, err := c.Output(ctx, "get", suspensionKind, "-o", "json")
	if err != nil {
		return nil, err
	}

	var list kube.List[json.RawMessage]
	if err := json.Unmarshal(raw, &list); err != nil {
		return nil, fmt.Errorf("decoding suspension windows: %w", err)
	}

	out := make([]suspension, 0, len(list.Items))
	for _, doc := range list.Items {
		var w suspension
		if err := json.Unmarshal(doc, &w); err != nil {
			return nil, fmt.Errorf("decoding suspension window: %w", err)
		}
		if w.Metadata.Name == "" {
			return nil, fmt.Errorf("%s: response is not an %s", c.Describe("get", suspensionKind), suspensionKind)
		}
		w.raw = doc
		out = append(out, w)
	}
	return out, nil
}

func upgradeSuspensions(ctx context.Context, c kube.Client, now time.Time) (Result, error) {
	suspensions, err := listSuspensions(ctx, c)
	if err != nil {
		return nil, err
	}

	// By schedule, latest first — the same calendar order as upgrade jobs —
	// except that anything active right now is pinned to the top. That one
	// exception is the question this view exists to answer, and there is
	// rarely more than one active at a time.
	sort.SliceStable(suspensions, func(i, j int) bool {
		si, ei := suspensions[i].span()
		sj, ej := suspensions[j].span()

		if ai, aj := suspensionState(si, ei, now) == "ACTIVE", suspensionState(sj, ej, now) == "ACTIVE"; ai != aj {
			return ai
		}
		return si.After(sj)
	})

	t := Table{
		Cols:     []string{"NAME", "STATE", "START", "END", "FOR", "SOURCE", "REASON"},
		Emphasis: suspensionEmphasis,
	}
	for _, w := range suspensions {
		start, end := w.span()
		t.Add(w.raw,
			w.Metadata.Name,
			suspensionState(start, end, now),
			showTime(start),
			showTime(end),
			showDuration(end.Sub(start)),
			w.source(),
			oneLine(w.Spec.Reason),
		)
	}
	return t, nil
}

func suspensionState(start, end, now time.Time) string {
	switch {
	case !now.Before(start) && now.Before(end):
		return "ACTIVE"
	case now.Before(start):
		return "upcoming"
	default:
		return "past"
	}
}
