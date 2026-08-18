package catalog

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/vshn/aldediggi/internal/kube"
)

// application is the slice of an ArgoCD Application this tool needs. Decoding
// into a struct rather than a map keeps the field names checkable, and the raw
// document is kept alongside so actions can hand it back to kubectl unchanged.
type application struct {
	Metadata struct {
		Name string `json:"name"`
	} `json:"metadata"`
	Spec struct {
		SyncPolicy *struct {
			Automated *struct {
				Prune    bool `json:"prune"`
				SelfHeal bool `json:"selfHeal"`
			} `json:"automated"`
		} `json:"syncPolicy"`
	} `json:"spec"`
	Status struct {
		Sync struct {
			Status   string `json:"status"`
			Revision string `json:"revision"`
		} `json:"sync"`
		Health struct {
			Status  string `json:"status"`
			Message string `json:"message"`
		} `json:"health"`
		Conditions []struct {
			Type    string `json:"type"`
			Message string `json:"message"`
		} `json:"conditions"`
		Resources []resource `json:"resources"`
	} `json:"status"`

	raw json.RawMessage
}

type resource struct {
	Kind      string `json:"kind"`
	Namespace string `json:"namespace"`
	Name      string `json:"name"`
	Status    string `json:"status"`
	Health    struct {
		Status  string `json:"status"`
		Message string `json:"message"`
	} `json:"health"`
}

// automated reports whether auto-sync is on. This is what pause and unpause
// toggle, and the zsh script gave no way to see it.
func (a application) automated() bool {
	return a.Spec.SyncPolicy != nil && a.Spec.SyncPolicy.Automated != nil
}

// message prefers the health message and falls back to the first condition,
// which is where a failed sync reports itself.
func (a application) message() string {
	if a.Status.Health.Message != "" {
		return oneLine(a.Status.Health.Message)
	}
	for _, c := range a.Status.Conditions {
		if c.Message != "" {
			return oneLine(c.Type + ": " + c.Message)
		}
	}
	return ""
}

// Where ArgoCD lives in a Project Syn cluster, and who may change it. Fixed
// rather than configurable: these are properties of the platform, not choices.
const (
	argoNamespace = "syn"
	adminUser     = "system:admin"
)

// Argo builds the argo catalogue. See docs/views.md.
func Argo(c kube.Client) *Registry {
	r := NewRegistry()
	r.Describe("argo", "All things ArgoCD related")

	// Reads run as whoever is logged in; only mutations impersonate.
	client := c.In(argoNamespace)
	admin := client.As(adminUser)
	available := client.Available

	r.Add(View{
		Group: "argo", Name: "apps",
		Short:     "List ArgoCD applications and their sync state",
		Kind:      Snapshot,
		Available: available,
		Params: []Param{{
			Name:     "app",
			Short:    "limit to one application",
			Complete: func(ctx context.Context) ([]string, error) { return appNames(ctx, client) },
		}},
		Actions: []Action{
			openSyncStatus(),
			openLogs(),
			pauseAction(admin),
			unpauseAction(admin),
			refreshRowsAction(admin),
		},
		Run: func(ctx context.Context, args Args) (Result, error) {
			return argoApps(ctx, client, args.Get("app"))
		},
	})

	r.Add(View{
		Group: "argo", Name: "syncstatus",
		Short: "Show the resources of one application and their sync state",
		Kind:  Snapshot,
		// Reached by drilling in from apps rather than picked from a list:
		// without an application it has nothing to show.
		Hidden:    true,
		Available: available,
		Params: []Param{{
			Name:     "app",
			Short:    "application name",
			Required: true,
			Complete: func(ctx context.Context) ([]string, error) { return appNames(ctx, client) },
		}},
		Actions: []Action{openLogs(), refreshAppAction(admin)},
		Run: func(ctx context.Context, args Args) (Result, error) {
			return argoSyncStatus(ctx, client, args.Get("app"))
		},
	})

	r.Add(View{
		Group: "argo", Name: "logs",
		Short:     "Show application-controller log lines about one application",
		Kind:      Snapshot,
		Hidden:    true,
		Available: available,
		Params: []Param{{
			Name:     "app",
			Short:    "application name",
			Required: true,
			Complete: func(ctx context.Context) ([]string, error) { return appNames(ctx, client) },
		}},
		Run: func(ctx context.Context, args Args) (Result, error) {
			return argoLogs(ctx, client, args.Get("app"))
		},
	})

	return r
}

// logTail is how far back to look. The controller is chatty, and anything
// older than this belongs in Loki rather than in a terminal.
const logTail = "5000"

// argoLogs filters the controller log to lines mentioning the application.
// A snapshot rather than a follow: streaming is its own machinery, and after
// looking at a broken app the question is usually what already happened.
func argoLogs(ctx context.Context, c kube.Client, app string) (Result, error) {
	if app == "" {
		return nil, fmt.Errorf("no application given")
	}

	raw, err := c.Output(ctx, "logs", "sts/syn-argocd-application-controller", "--tail", logTail)
	if err != nil {
		return nil, err
	}

	var lines []string
	for _, line := range strings.Split(string(raw), "\n") {
		if strings.Contains(line, app) {
			lines = append(lines, line)
		}
	}
	if len(lines) == 0 {
		return Text{Lines: []string{fmt.Sprintf("no log lines mentioning %q in the last %s", app, logTail)}}, nil
	}
	return Text{Lines: lines}, nil
}

func appNames(ctx context.Context, c kube.Client) ([]string, error) {
	apps, err := listApps(ctx, c, "")
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(apps))
	for _, a := range apps {
		out = append(out, a.Metadata.Name)
	}
	return out, nil
}

// listApps fetches applications, optionally just one, and keeps each raw
// document for the actions.
func listApps(ctx context.Context, c kube.Client, only string) ([]application, error) {
	args := []string{"get", "applications.argoproj.io"}
	if only != "" {
		args = append(args, only)
	}

	raw, err := c.Output(ctx, append(args, "-o", "json")...)
	if err != nil {
		return nil, err
	}

	// A named get returns the object itself, a bare one returns a List.
	var docs []json.RawMessage
	if only != "" {
		docs = []json.RawMessage{raw}
	} else {
		var list kube.List[json.RawMessage]
		if err := json.Unmarshal(raw, &list); err != nil {
			return nil, fmt.Errorf("decoding applications: %w", err)
		}
		docs = list.Items
	}

	apps := make([]application, 0, len(docs))
	for _, doc := range docs {
		var a application
		if err := json.Unmarshal(doc, &a); err != nil {
			return nil, fmt.Errorf("decoding application: %w", err)
		}
		// Anything that is not an Application still decodes cleanly into all
		// zero values, which would surface as an empty table rather than as a
		// problem. The name is the cheapest thing to insist on.
		if a.Metadata.Name == "" {
			return nil, fmt.Errorf("%s: response is not an Application", c.Describe(args...))
		}
		a.raw = doc
		apps = append(apps, a)
	}
	return apps, nil
}

// argoEmphasis is what makes a 200-row list scannable rather than readable.
// Keyed by the exact cell value, so it serves both the app table and the
// resource table.
var argoEmphasis = map[string]Emphasis{
	"OutOfSync":   Bad,
	"Degraded":    Bad,
	"Missing":     Bad,
	"Unknown":     Warn,
	"Progressing": Warn,
	"Suspended":   Warn,
	"PAUSED":      Warn,
	"Synced":      Good,
	"Healthy":     Good,
}

func argoApps(ctx context.Context, c kube.Client, only string) (Result, error) {
	apps, err := listApps(ctx, c, only)
	if err != nil {
		return nil, err
	}

	// Trouble first. An operator opening this view is looking for what broke,
	// not for an alphabetical census.
	sort.SliceStable(apps, func(i, j int) bool {
		return lessByTrouble(
			apps[i].Status.Sync.Status == "Synced", apps[i].Status.Health.Status == "Healthy", apps[i].Metadata.Name,
			apps[j].Status.Sync.Status == "Synced", apps[j].Status.Health.Status == "Healthy", apps[j].Metadata.Name,
		)
	})

	t := Table{Cols: []string{"NAME", "SYNC", "HEALTH", "AUTO", "REVISION", "MESSAGE"}, Emphasis: argoEmphasis}
	for _, a := range apps {
		auto := "PAUSED"
		if a.automated() {
			auto = "on"
		}
		t.Add(a.raw,
			a.Metadata.Name,
			a.Status.Sync.Status,
			a.Status.Health.Status,
			auto,
			shortRevision(a.Status.Sync.Revision),
			a.message(),
		)
	}
	return t, nil
}

func argoSyncStatus(ctx context.Context, c kube.Client, app string) (Result, error) {
	apps, err := listApps(ctx, c, app)
	if err != nil {
		return nil, err
	}
	if len(apps) == 0 {
		return nil, fmt.Errorf("application %q not found", app)
	}

	res := apps[0].Status.Resources
	sort.SliceStable(res, func(i, j int) bool {
		return lessByTrouble(
			res[i].Status == "Synced", healthy(res[i].Health.Status), res[i].Kind+"/"+res[i].Name,
			res[j].Status == "Synced", healthy(res[j].Health.Status), res[j].Kind+"/"+res[j].Name,
		)
	})

	t := Table{Cols: []string{"STATUS", "KIND", "NAMESPACE/NAME", "HEALTH", "MESSAGE"}, Emphasis: argoEmphasis}
	for _, r := range res {
		name := r.Name
		if r.Namespace != "" {
			name = r.Namespace + "/" + r.Name
		}
		t.Add(nil, r.Status, r.Kind, name, r.Health.Status, oneLine(r.Health.Message))
	}
	return t, nil
}

// healthy treats an absent health status as healthy: ArgoCD omits it for
// resources it does not track, and those are not what anyone is hunting for.
func healthy(status string) bool { return status == "" || status == "Healthy" }

// lessByTrouble sorts unsynced before synced, unhealthy before healthy, then
// by name.
func lessByTrouble(aSynced, aHealthy bool, aName string, bSynced, bHealthy bool, bName string) bool {
	if aSynced != bSynced {
		return !aSynced
	}
	if aHealthy != bHealthy {
		return !aHealthy
	}
	return aName < bName
}

func shortRevision(rev string) string {
	if len(rev) > 7 {
		return rev[:7]
	}
	return rev
}

// oneLine keeps a multi-line message from breaking the table layout.
func oneLine(s string) string {
	return strings.Join(strings.Fields(strings.ReplaceAll(s, "\n", " ")), " ")
}
