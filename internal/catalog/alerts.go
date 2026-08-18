package catalog

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/vshn/aldediggi/internal/kube"
)

// Where Alertmanager lives on this platform. There is no HTTP path that keeps
// kubeconfig auth working, so amtool runs inside the pod via exec — which
// makes this the one group whose reads impersonate: pod/exec is a privileged
// verb either way. See docs/views.md.
const (
	monitoringNamespace = "openshift-monitoring"
	alertmanagerTarget  = "sts/alertmanager-main"
	alertmanagerURL     = "http://localhost:9093"

	// watchdogAlert must always fire; its absence means the alerting
	// pipeline itself is broken and an empty list would be a lie.
	watchdogAlert = "Watchdog"
)

// alert is Alertmanager's gettable alert, the slice of it this tool needs.
type alert struct {
	Fingerprint string            `json:"fingerprint"`
	Labels      map[string]string `json:"labels"`
	Annotations map[string]string `json:"annotations"`
	StartsAt    string            `json:"startsAt"`
	Status      struct {
		State       string   `json:"state"`
		SilencedBy  []string `json:"silencedBy"`
		InhibitedBy []string `json:"inhibitedBy"`
	} `json:"status"`

	raw json.RawMessage
}

func (a alert) name() string              { return a.Fingerprint }
func (a *alert) keep(doc json.RawMessage) { a.raw = doc }

func (a alert) alertname() string { return a.Labels["alertname"] }
func (a alert) namespace() string { return a.Labels["namespace"] }

func (a alert) severity() string {
	if s := a.Labels["severity"]; s != "" {
		return s
	}
	return "none"
}

// state splits Alertmanager's "suppressed" into the two reasons, because
// "silenced by whom" and "inhibited by what" lead to different next steps.
func (a alert) state() string {
	switch a.Status.State {
	case "active":
		return "firing"
	case "suppressed":
		if len(a.Status.SilencedBy) > 0 {
			return "silenced"
		}
		if len(a.Status.InhibitedBy) > 0 {
			return "inhibited"
		}
		return "suppressed"
	default:
		return a.Status.State
	}
}

// summary prefers the short annotation and falls back to the long one.
func (a alert) summary() string {
	return oneLine(firstText(a.Annotations["summary"], a.Annotations["message"], a.Annotations["description"]))
}

// severityRank orders by how loudly an alert should be shouting.
func severityRank(a alert) int {
	switch a.severity() {
	case "critical":
		return 0
	case "warning":
		return 1
	case "none":
		return 3
	default:
		return 2
	}
}

// silence is Alertmanager's gettable silence.
type silence struct {
	ID       string `json:"id"`
	Matchers []struct {
		Name    string `json:"name"`
		Value   string `json:"value"`
		IsRegex bool   `json:"isRegex"`
		IsEqual *bool  `json:"isEqual"` // absent in older payloads, meaning true
	} `json:"matchers"`
	StartsAt  string `json:"startsAt"`
	EndsAt    string `json:"endsAt"`
	CreatedBy string `json:"createdBy"`
	Comment   string `json:"comment"`
	Status    struct {
		State string `json:"state"`
	} `json:"status"`

	raw json.RawMessage
}

func (s silence) name() string              { return s.ID }
func (s *silence) keep(doc json.RawMessage) { s.raw = doc }

func (s silence) matchers() string {
	var out []string
	for _, m := range s.Matchers {
		op := "="
		switch {
		case m.IsRegex && (m.IsEqual == nil || *m.IsEqual):
			op = "=~"
		case m.IsRegex:
			op = "!~"
		case m.IsEqual != nil && !*m.IsEqual:
			op = "!="
		}
		out = append(out, m.Name+op+m.Value)
	}
	return strings.Join(out, " ")
}

// shortID is what fits in a list; actions read the full id from the source.
func (s silence) shortID() string {
	if len(s.ID) > 8 {
		return s.ID[:8]
	}
	return s.ID
}

var alertEmphasis = map[string]Emphasis{
	"critical":  Bad,
	"broken":    Bad,
	"warning":   Warn,
	"silenced":  Warn,
	"inhibited": Warn,
	"ACTIVE":    Warn, // a silence in force is masking something
	"ok":        Good,
}

// Alerts builds the alerts catalogue. See docs/views.md.
func Alerts(c kube.Client) *Registry {
	r := NewRegistry()
	r.Describe("alerts", "All things Alertmanager related")

	// The whole group impersonates, reads included: everything goes through
	// pod/exec, and there is no unprivileged way to reach Alertmanager.
	admin := c.In(monitoringNamespace).As(adminUser)

	alertParam := Param{
		Name:     "alert",
		Short:    "limit to one alertname",
		Complete: func(ctx context.Context) ([]string, error) { return alertNames(ctx, admin) },
	}

	r.Add(View{
		Group: "alerts", Name: "firing",
		Short:     "List firing alerts, silenced and inhibited ones included",
		Kind:      Snapshot,
		Available: c.Available,
		Params:    []Param{alertParam},
		Actions: []Action{
			openAlertDetail(),
			silenceAlertAction(admin),
		},
		Run: func(ctx context.Context, args Args) (Result, error) {
			return alertsFiring(ctx, admin, args.Get("alert"))
		},
	})

	r.Add(View{
		Group: "alerts", Name: "silences",
		Short:     "List silences: what is muted, by whom, until when",
		Kind:      Snapshot,
		Available: c.Available,
		Params: []Param{{
			Name:  "silence",
			Short: "limit to one silence id (a prefix is enough)",
		}},
		Actions: []Action{
			expireSilencesAction(admin),
			createSilenceAction(admin),
		},
		Run: func(ctx context.Context, args Args) (Result, error) {
			return alertSilences(ctx, admin, args.Get("silence"))
		},
	})

	r.Add(View{
		Group: "alerts", Name: "detail",
		Short:     "Show one alert in full: every label and annotation",
		Kind:      Snapshot,
		Hidden:    true,
		Available: c.Available,
		Params: []Param{{
			Name:     "fingerprint",
			Short:    "alert fingerprint, as the firing view carries it",
			Required: true,
		}},
		Actions: []Action{silenceAlertAction(admin)},
		Run: func(ctx context.Context, args Args) (Result, error) {
			return alertDetail(ctx, admin, args.Get("fingerprint"))
		},
	})

	return r
}

// amtool runs the alertmanager CLI inside its own pod.
func amtool(ctx context.Context, c kube.Client, args ...string) ([]byte, error) {
	base := []string{"exec", alertmanagerTarget, "--", "amtool", "--alertmanager.url=" + alertmanagerURL}
	return c.Output(ctx, append(base, args...)...)
}

// amtoolList decodes amtool's JSON output, which is a bare array rather than
// a kubectl List.
func amtoolList[T any, P interface {
	*T
	fetchable
}](ctx context.Context, c kube.Client, kind string, args ...string) ([]T, error) {
	raw, err := amtool(ctx, c, args...)
	if err != nil {
		return nil, err
	}
	var docs []json.RawMessage
	if err := json.Unmarshal(raw, &docs); err != nil {
		return nil, fmt.Errorf("decoding %s list: %w", kind, err)
	}
	return decodeDocs[T, P](docs, kind, "amtool "+strings.Join(args, " "))
}

func listAlerts(ctx context.Context, c kube.Client) ([]alert, error) {
	return amtoolList[alert](ctx, c, "alert", "alert", "query", "-o", "json")
}

func listSilences(ctx context.Context, c kube.Client) ([]silence, error) {
	return amtoolList[silence](ctx, c, "silence", "silence", "query", "-o", "json")
}

func alertNames(ctx context.Context, c kube.Client) ([]string, error) {
	alerts, err := listAlerts(ctx, c)
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	var out []string
	for _, a := range alerts {
		if name := a.alertname(); name != "" && !seen[name] {
			seen[name] = true
			out = append(out, name)
		}
	}
	sort.Strings(out)
	return out, nil
}

func alertsFiring(ctx context.Context, c kube.Client, only string) (Result, error) {
	alerts, err := listAlerts(ctx, c)
	if err != nil {
		return nil, err
	}

	// The pipeline verdict is judged before filtering: a narrowed view must
	// not report the Watchdog missing just because it was filtered away.
	pipelineOK := false
	for _, a := range alerts {
		if a.alertname() == watchdogAlert {
			pipelineOK = true
			break
		}
	}

	if only != "" {
		kept := alerts[:0]
		for _, a := range alerts {
			if a.alertname() == only {
				kept = append(kept, a)
			}
		}
		alerts = kept
	}

	// Loudest first, then the most recent: severity is why the pager went
	// off, recency is what changed.
	sort.SliceStable(alerts, func(i, j int) bool {
		if ri, rj := severityRank(alerts[i]), severityRank(alerts[j]); ri != rj {
			return ri < rj
		}
		if alerts[i].StartsAt != alerts[j].StartsAt {
			return alerts[i].StartsAt > alerts[j].StartsAt
		}
		return alerts[i].alertname() < alerts[j].alertname()
	})

	t := Table{
		Cols:     []string{"ALERT", "SEVERITY", "STATE", "SINCE", "NAMESPACE", "SUMMARY"},
		Emphasis: alertEmphasis,
	}
	if pipelineOK {
		t.Add(nil, "pipeline", "", "ok", "", "", "the "+watchdogAlert+" alert is firing")
	} else {
		t.Add(nil, "pipeline", "", "broken", "", "", "the "+watchdogAlert+" alert is missing — alerting may be dead")
	}
	for _, a := range alerts {
		t.Add(a.raw,
			a.alertname(),
			a.severity(),
			a.state(),
			showTimeOf(a.StartsAt),
			a.namespace(),
			a.summary(),
		)
	}
	return t, nil
}

func alertSilences(ctx context.Context, c kube.Client, only string) (Result, error) {
	silences, err := listSilences(ctx, c)
	if err != nil {
		return nil, err
	}

	if only != "" {
		kept := silences[:0]
		for _, s := range silences {
			if strings.HasPrefix(s.ID, only) {
				kept = append(kept, s)
			}
		}
		silences = kept
	}

	// The calendar order of the suspensions view: whatever is muting alerts
	// right now is pinned on top, the rest sorts by start, latest first.
	// StartsAt compares as a string; amtool emits one consistent format.
	sort.SliceStable(silences, func(i, j int) bool {
		if ai, aj := silences[i].Status.State == "active", silences[j].Status.State == "active"; ai != aj {
			return ai
		}
		return silences[i].StartsAt > silences[j].StartsAt
	})

	t := Table{
		Cols:     []string{"ID", "STATE", "START", "END", "CREATOR", "MATCHERS", "COMMENT"},
		Emphasis: alertEmphasis,
	}
	for _, s := range silences {
		state := s.Status.State
		if state == "active" {
			state = "ACTIVE"
		}
		t.Add(s.raw,
			s.shortID(),
			state,
			showTimeOf(s.StartsAt),
			showTimeOf(s.EndsAt),
			s.CreatedBy,
			s.matchers(),
			oneLine(s.Comment),
		)
	}
	return t, nil
}

// alertDetail shows every label and annotation. The list view compresses an
// alert to one line; the labels are what a silence matcher is built from, and
// the description rarely fits a column.
func alertDetail(ctx context.Context, c kube.Client, fingerprint string) (Result, error) {
	alerts, err := listAlerts(ctx, c)
	if err != nil {
		return nil, err
	}

	for _, a := range alerts {
		if a.Fingerprint != fingerprint {
			continue
		}
		t := Table{Cols: []string{"WHAT", "VALUE"}, Emphasis: alertEmphasis}
		t.Add(a.raw, "alert", a.alertname())
		t.Add(nil, "state", a.state())
		t.Add(nil, "since", showTimeOf(a.StartsAt))
		for _, k := range sortedKeys(a.Labels) {
			t.Add(nil, "label "+k, a.Labels[k])
		}
		for _, k := range sortedKeys(a.Annotations) {
			t.Add(nil, "annotation "+k, oneLine(a.Annotations[k]))
		}
		for _, id := range a.Status.SilencedBy {
			t.Add(nil, "silenced by", id)
		}
		return t, nil
	}
	return nil, fmt.Errorf("no alert with fingerprint %q", fingerprint)
}

func sortedKeys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
