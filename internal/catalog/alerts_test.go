package catalog

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vshn/aldediggi/internal/kube"
)

const alertsFixture = `[
 {"fingerprint":"f-node","labels":{"alertname":"KubeNodeNotReady","severity":"critical","namespace":"kube-system","node":"worker-03"},
  "annotations":{"summary":"worker-03 not ready"},
  "startsAt":"2026-08-18T09:48:00Z","status":{"state":"active","silencedBy":[],"inhibitedBy":[]}},
 {"fingerprint":"f-td1","labels":{"alertname":"TargetDown","severity":"warning","namespace":"syn"},
  "annotations":{"description":"10% of targets down"},
  "startsAt":"2026-08-17T09:12:00Z","status":{"state":"suppressed","silencedBy":["sil-1abcdefg"],"inhibitedBy":[]}},
 {"fingerprint":"f-td2","labels":{"alertname":"TargetDown","severity":"warning","namespace":"other"},
  "annotations":{"description":"20% of targets down"},
  "startsAt":"2026-08-16T09:12:00Z","status":{"state":"active","silencedBy":[],"inhibitedBy":[]}},
 {"fingerprint":"f-wd","labels":{"alertname":"Watchdog","namespace":"openshift-monitoring"},
  "annotations":{"summary":"Alerting pipeline works"},
  "startsAt":"2026-04-28T09:00:00Z","status":{"state":"active","silencedBy":[],"inhibitedBy":[]}}
]`

const silencesFixture = `[
 {"id":"sil-expired-00","matchers":[{"name":"alertname","value":"Old","isRegex":false,"isEqual":true}],
  "startsAt":"2026-08-01T00:00:00Z","endsAt":"2026-08-02T00:00:00Z","createdBy":"bob","comment":"old",
  "status":{"state":"expired"}},
 {"id":"sil-pending-11","matchers":[{"name":"alertname","value":"Later","isRegex":false,"isEqual":true}],
  "startsAt":"2026-12-24T00:00:00Z","endsAt":"2027-01-02T00:00:00Z","createdBy":"carol","comment":"freeze",
  "status":{"state":"pending"}},
 {"id":"sil-1abcdefg","matchers":[{"name":"alertname","value":"TargetDown","isRegex":false,"isEqual":true},
                                  {"name":"namespace","value":"syn","isRegex":false,"isEqual":true}],
  "startsAt":"2026-08-17T08:00:00Z","endsAt":"2026-08-19T08:00:00Z","createdBy":"alice","comment":"maintenance",
  "status":{"state":"active"}}
]`

func fakeAlertmanager(t *testing.T, alerts string) (*Registry, func() string) {
	t.Helper()
	dir := t.TempDir()
	for name, body := range map[string]string{
		"alerts.json": alerts, "silences.json": silencesFixture,
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	script := `D="$(dirname "$0")"
echo "$@" > "$D/args"
case "$*" in
  *"silence add"*) echo "9c22a7f8-0000-4b5c-9f0a-example" ;;
  *"silence expire"*) : ;;
  *"silence query"*) cat "$D/silences.json" ;;
  *"alert query"*) cat "$D/alerts.json" ;;
esac`
	bin := filepath.Join(dir, "kubectl")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\n"+script+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	read := func() string {
		a, _ := os.ReadFile(filepath.Join(dir, "args"))
		return strings.TrimSpace(string(a))
	}
	return Alerts(kube.Client{Bin: bin}), read
}

func runAlertsView(t *testing.T, r *Registry, id string, args Args) Table {
	t.Helper()
	v, ok := r.Get(id)
	if !ok {
		t.Fatalf("no view %q", id)
	}
	res, err := v.Run(context.Background(), args)
	if err != nil {
		t.Fatal(err)
	}
	table, ok := res.(Table)
	if !ok {
		t.Fatalf("got %T, want Table", res)
	}
	return table
}

func alertsAction(t *testing.T, r *Registry, view, name string) Action {
	t.Helper()
	v, _ := r.Get(view)
	a, ok := v.Lookup(name)
	if !ok {
		t.Fatalf("%s has no action %q", view, name)
	}
	return a
}

func TestFiringLeadsWithThePipelineVerdict(t *testing.T) {
	r, _ := fakeAlertmanager(t, alertsFixture)
	tbl := runAlertsView(t, r, "alerts.firing", nil)

	if got := tbl.Rows[0].Cells; got[0] != "pipeline" || got[2] != "ok" {
		t.Errorf("first row = %v, want the pipeline verdict", got)
	}
}

func TestFiringSaysWhenTheWatchdogIsMissing(t *testing.T) {
	r, _ := fakeAlertmanager(t, `[
 {"fingerprint":"f-1","labels":{"alertname":"TargetDown","severity":"warning"},
  "startsAt":"2026-08-16T09:12:00Z","status":{"state":"active"}}]`)
	tbl := runAlertsView(t, r, "alerts.firing", nil)

	if got := tbl.Rows[0].Cells; got[2] != "broken" {
		t.Errorf("verdict = %v, want broken", got)
	}
}

func TestFiringSortsBySeverityThenRecency(t *testing.T) {
	r, _ := fakeAlertmanager(t, alertsFixture)
	tbl := runAlertsView(t, r, "alerts.firing", nil)

	var names []string
	for _, row := range tbl.Rows[1:] {
		names = append(names, row.Cells[0])
	}
	want := "KubeNodeNotReady TargetDown TargetDown Watchdog"
	if got := strings.Join(names, " "); got != want {
		t.Errorf("order = %q, want %q", got, want)
	}
	if since := tbl.Rows[2].Cells[4]; since != "syn" {
		t.Errorf("second TargetDown row should be the older one; namespace = %q", since)
	}
}

func TestSilencedStateNamesTheReason(t *testing.T) {
	r, _ := fakeAlertmanager(t, alertsFixture)
	tbl := runAlertsView(t, r, "alerts.firing", Args{"alert": "TargetDown"})

	states := map[string]string{}
	for _, row := range tbl.Rows[1:] {
		states[row.Cells[4]] = row.Cells[2]
	}
	if states["syn"] != "silenced" || states["other"] != "firing" {
		t.Errorf("states = %v", states)
	}
}

func TestFiringJudgesThePipelineBeforeFiltering(t *testing.T) {
	r, _ := fakeAlertmanager(t, alertsFixture)
	tbl := runAlertsView(t, r, "alerts.firing", Args{"alert": "TargetDown"})

	if got := tbl.Rows[0].Cells[2]; got != "ok" {
		t.Errorf("verdict = %q; filtering away the Watchdog must not read as broken", got)
	}
	if len(tbl.Rows) != 3 {
		t.Errorf("%d rows, want verdict plus two TargetDown", len(tbl.Rows))
	}
}

func TestAlertReadsGoThroughExecAsAdmin(t *testing.T) {
	r, read := fakeAlertmanager(t, alertsFixture)
	runAlertsView(t, r, "alerts.firing", nil)

	want := "-n openshift-monitoring --as system:admin exec sts/alertmanager-main -- " +
		"amtool --alertmanager.url=http://localhost:9093 alert query -o json"
	if got := read(); got != want {
		t.Errorf("argv = %q, want %q", got, want)
	}
}

func TestSilencesPinTheActiveOne(t *testing.T) {
	r, _ := fakeAlertmanager(t, alertsFixture)
	tbl := runAlertsView(t, r, "alerts.silences", nil)

	first := tbl.Rows[0].Cells
	if first[0] != "sil-1abc" || first[1] != "ACTIVE" {
		t.Errorf("first row = %v, want the active silence pinned", first)
	}
	if first[5] != "alertname=TargetDown namespace=syn" {
		t.Errorf("matchers = %q", first[5])
	}
}

func TestSilenceActionBuildsMatchersFromTheAlert(t *testing.T) {
	r, read := fakeAlertmanager(t, alertsFixture)
	res := runAlertsView(t, r, "alerts.firing", Args{"alert": "KubeNodeNotReady"})

	_, err := alertsAction(t, r, "alerts.firing", "silence").
		Run(context.Background(), res, Args{"for": "2027-01-02 12:00", "comment": "node maintenance"})
	if err != nil {
		t.Fatal(err)
	}

	end, err := time.ParseInLocation("2006-01-02 15:04", "2027-01-02 12:00", time.Local)
	if err != nil {
		t.Fatal(err)
	}
	want := "silence add --author " + localUser() + " --comment node maintenance --end " +
		FormatTime(end) + ` alertname="KubeNodeNotReady" namespace="kube-system"`
	if got := read(); !strings.HasSuffix(got, want) {
		t.Errorf("argv = %q, want suffix %q", got, want)
	}
}

func TestSilenceFallsBackToTheCommonAlertname(t *testing.T) {
	r, read := fakeAlertmanager(t, alertsFixture)
	res := runAlertsView(t, r, "alerts.firing", Args{"alert": "TargetDown"})

	_, err := alertsAction(t, r, "alerts.firing", "silence").
		Run(context.Background(), res, Args{"for": "+2h", "comment": "flaky"})
	if err != nil {
		t.Fatal(err)
	}

	got := read()
	if !strings.HasSuffix(got, ` alertname="TargetDown"`) || strings.Contains(got, "namespace=") {
		t.Errorf("argv = %q, want a matcher on the alertname alone", got)
	}
}

func TestExpireSkipsTheAlreadyExpired(t *testing.T) {
	r, read := fakeAlertmanager(t, alertsFixture)
	res := runAlertsView(t, r, "alerts.silences", nil)

	_, err := alertsAction(t, r, "alerts.silences", "expire").Run(context.Background(), res, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := read(); !strings.HasSuffix(got, "silence expire sil-1abcdefg sil-pending-11") {
		t.Errorf("argv = %q", got)
	}
}

func TestCreatePassesMatchersThroughVerbatim(t *testing.T) {
	r, read := fakeAlertmanager(t, alertsFixture)

	_, err := alertsAction(t, r, "alerts.silences", "create").
		Run(context.Background(), nil, Args{"matchers": `alertname="Foo", namespace=~"syn.*"`, "for": "+2h", "comment": "c"})
	if err != nil {
		t.Fatal(err)
	}
	if got := read(); !strings.HasSuffix(got, `alertname="Foo" namespace=~"syn.*"`) {
		t.Errorf("argv = %q", got)
	}
}

func TestDetailShowsLabelsAndSilencers(t *testing.T) {
	r, _ := fakeAlertmanager(t, alertsFixture)
	tbl := runAlertsView(t, r, "alerts.detail", Args{"fingerprint": "f-td1"})

	got := map[string]string{}
	for _, row := range tbl.Rows {
		got[row.Cells[0]] = row.Cells[1]
	}
	for key, want := range map[string]string{
		"label namespace":        "syn",
		"annotation description": "10% of targets down",
		"silenced by":            "sil-1abcdefg",
		"state":                  "silenced",
	} {
		if got[key] != want {
			t.Errorf("%s = %q, want %q", key, got[key], want)
		}
	}
}
