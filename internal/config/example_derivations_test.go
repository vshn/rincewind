package config

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/vshn/rincewind/internal/engine"
)

// fakeKubectl puts a kubectl on the PATH that answers `get <resource>` and
// `exec` from files, so the shipped config can be run without a cluster.
func fakeKubectl(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	script := `#!/bin/sh
for a in "$@"; do
  case "$a" in
    upgradejob)   cat "` + dir + `/jobs.json"; exit 0 ;;
    alert)        cat "` + dir + `/alerts.json"; exit 0 ;;
  esac
done
echo '{"items":[]}'
`
	if err := os.WriteFile(filepath.Join(dir, "kubectl"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return dir
}

func cellAt(t *testing.T, res engine.Table, row int, col string) string {
	t.Helper()
	for i, c := range res.Cols {
		if c == col {
			return res.Rows[row].Cells[i].Text
		}
	}
	t.Fatalf("no column %q", col)
	return ""
}

// The six-way STATE fold is the derivation most easily got wrong: the
// controller leaves several conditions True at once, and "Expired" is the
// case no condition states at all.
func TestExampleUpgradeJobsStateFold(t *testing.T) {
	past := time.Now().Add(-2 * time.Hour).UTC().Format(time.RFC3339)
	future := time.Now().Add(2 * time.Hour).UTC().Format(time.RFC3339)

	jobs := fmt.Sprintf(`{"items":[
	  {"metadata":{"name":"a-done"},"spec":{"startAfter":%q},
	   "status":{"conditions":[{"type":"Succeeded","status":"True","message":"all good","lastTransitionTime":%q}]}},
	  {"metadata":{"name":"b-refused"},"spec":{"startAfter":%q},
	   "status":{"conditions":[{"type":"Succeeded","status":"False","reason":"UpgradeFailed","message":"pre-check failed","lastTransitionTime":%q}]}},
	  {"metadata":{"name":"c-paused"},"spec":{"startAfter":%q},
	   "status":{"conditions":[{"type":"Started","status":"True","lastTransitionTime":%q},
	                           {"type":"Paused","status":"True","lastTransitionTime":%q}]}},
	  {"metadata":{"name":"d-running"},"spec":{"startAfter":%q},
	   "status":{"conditions":[{"type":"Started","status":"True","message":"draining","lastTransitionTime":%q}],
	             "hookTracker":[{"status":"complete"},{"status":"active"}]}},
	  {"metadata":{"name":"e-expired"},"spec":{"startAfter":%q,"startBefore":%q},"status":{}},
	  {"metadata":{"name":"f-waiting"},"spec":{"startAfter":%q,"startBefore":%q},"status":{}}
	]}`, past, past, past, past, past, past, past, past, past, past, past, past, future)

	fakeKubectl(t, map[string]string{"jobs.json": jobs})

	views, err := Load("../../examples")
	if err != nil {
		t.Fatal(err)
	}
	res, err := viewByID(t, views, "upgrade.jobs").Run(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Notes) > 0 {
		t.Fatalf("notes: %v", res.Notes)
	}

	want := map[string]string{
		"a-done": "Succeeded", "b-refused": "Failed", "c-paused": "Paused",
		"d-running": "Running", "e-expired": "Expired", "f-waiting": "Pending",
	}
	for i := range res.Rows {
		name := cellAt(t, res, i, "NAME")
		if got := cellAt(t, res, i, "STATE"); got != want[name] {
			t.Errorf("%s is %q, want %q", name, got, want[name])
		}
	}
	// The hook tracker counts only what finished.
	for i := range res.Rows {
		if cellAt(t, res, i, "NAME") == "d-running" {
			if got := cellAt(t, res, i, "HOOKS"); got != "1/2" {
				t.Errorf("HOOKS %q, want 1/2", got)
			}
		}
	}
}

// Two derivations here: the synthetic pipeline verdict, and splitting
// Alertmanager's one word "suppressed" into who suppressed it.
func TestExampleAlertsFiringVerdictAndSuppression(t *testing.T) {
	now := time.Now().UTC().Format(time.RFC3339)
	alerts := fmt.Sprintf(`[
	  {"fingerprint":"f1","labels":{"alertname":"Watchdog"},"annotations":{},"startsAt":%q,
	   "status":{"state":"active","silencedBy":[],"inhibitedBy":[]}},
	  {"fingerprint":"f2","labels":{"alertname":"DiskFull","severity":"critical","namespace":"syn"},
	   "annotations":{"summary":"disk is full"},"startsAt":%q,
	   "status":{"state":"active","silencedBy":[],"inhibitedBy":[]}},
	  {"fingerprint":"f3","labels":{"alertname":"Noisy","severity":"warning"},"annotations":{},"startsAt":%q,
	   "status":{"state":"suppressed","silencedBy":["s1"],"inhibitedBy":[]}},
	  {"fingerprint":"f4","labels":{"alertname":"Downstream","severity":"warning"},"annotations":{},"startsAt":%q,
	   "status":{"state":"suppressed","silencedBy":[],"inhibitedBy":["f2"]}}
	]`, now, now, now, now)

	fakeKubectl(t, map[string]string{"alerts.json": alerts})

	views, err := Load("../../examples")
	if err != nil {
		t.Fatal(err)
	}
	res, err := viewByID(t, views, "alerts.firing").Run(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}

	// The verdict sorts first: its empty severity ranks above critical.
	if got := cellAt(t, res, 0, "ALERT"); got != "pipeline" {
		t.Fatalf("first row is %q, want the pipeline verdict", got)
	}
	if got := cellAt(t, res, 0, "STATE"); got != "ok" {
		t.Errorf("pipeline is %q, want ok while Watchdog fires", got)
	}

	state := map[string]string{}
	for i := range res.Rows {
		state[cellAt(t, res, i, "ALERT")] = cellAt(t, res, i, "STATE")
	}
	for alert, want := range map[string]string{
		"Watchdog": "firing", "DiskFull": "firing",
		"Noisy": "silenced", "Downstream": "inhibited",
	} {
		if state[alert] != want {
			t.Errorf("%s is %q, want %q", alert, state[alert], want)
		}
	}
	// Critical before warning: severity is what one triages by.
	if got := cellAt(t, res, 1, "ALERT"); got != "DiskFull" {
		t.Errorf("second row is %q, want the critical one", got)
	}
}

// Without Watchdog the pipeline row says so — an empty alert list is
// otherwise indistinguishable from a healthy quiet cluster.
func TestExampleAlertsPipelineNoticesAMissingWatchdog(t *testing.T) {
	fakeKubectl(t, map[string]string{"alerts.json": `[]`})

	views, err := Load("../../examples")
	if err != nil {
		t.Fatal(err)
	}
	res, err := viewByID(t, views, "alerts.firing").Run(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := cellAt(t, res, 0, "STATE"); got != "broken" {
		t.Errorf("pipeline is %q with no Watchdog, want broken", got)
	}
}
