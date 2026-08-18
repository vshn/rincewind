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

// Shapes taken from a real cluster: conditions carry a status, and a hook's
// status is lower case.
const jobsFixture = `{"items":[
 {"metadata":{"name":"done-4-20-32"},
  "spec":{"desiredVersion":{"version":"4.20.32"},"startAfter":"2026-08-17T12:00:00Z","startBefore":"2026-08-17T13:00:00Z"},
  "status":{"conditions":[
    {"lastTransitionTime":"2026-08-17T12:00:00Z","message":"Upgrade started","reason":"Started","status":"True","type":"Started"},
    {"lastTransitionTime":"2026-08-17T12:00:07Z","message":"","reason":"NoManagedPools","status":"False","type":"MachineConfigPoolsPaused"},
    {"lastTransitionTime":"2026-08-17T13:11:45Z","message":"Upgrade succeeded","reason":"Succeeded","status":"True","type":"Succeeded"}],
   "hookTracker":[
    {"hookEvent":"Create","status":"complete","upgradeJobHook":"admin-ack"},
    {"hookEvent":"Start","status":"complete","upgradeJobHook":"maintenance-silence"},
    {"hookEvent":"Finish","status":"complete","upgradeJobHook":"maintenance-silence"}]}},
 {"metadata":{"name":"failed-4-20-31"},
  "spec":{"desiredVersion":{"version":"4.20.31"},"startAfter":"2026-08-10T02:00:00Z","startBefore":"2026-08-10T03:00:00Z"},
  "status":{"conditions":[
    {"lastTransitionTime":"2026-08-10T02:00:00Z","message":"Upgrade started","reason":"Started","status":"True","type":"Started"},
    {"lastTransitionTime":"2026-08-10T03:40:00Z","message":"2 degraded operators","reason":"Failed","status":"False","type":"Succeeded"}],
   "hookTracker":[{"hookEvent":"Start","status":"failed","upgradeJobHook":"cilium"}]}},
 {"metadata":{"name":"pending-4-20-33"},
  "spec":{"desiredVersion":{"version":"4.20.33"},"startAfter":"2026-08-24T12:00:00Z","startBefore":"2026-08-24T13:00:00Z"},
  "status":{}},
 {"metadata":{"name":"expired-4-20-30"},
  "spec":{"desiredVersion":{"version":"4.20.30"},"startAfter":"2026-08-03T02:00:00Z","startBefore":"2026-08-03T03:00:00Z"},
  "status":{"conditions":[]}}
]}`

func fakeJobs(t *testing.T) kube.Client {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "jobs.json"), []byte(jobsFixture), 0o644); err != nil {
		t.Fatal(err)
	}

	script := `D="$(dirname "$0")"
case "$*" in
  *"upgradejob -o json"*) cat "$D/jobs.json" ;;
  *)
    name=$(echo "$*" | sed -n "s/.*upgradejob \([^ ]*\).*/\1/p")
    python3 -c "
import json,sys
d=json.load(open('$D/jobs.json'))
for i in d['items']:
    if i['metadata']['name']=='$name': print(json.dumps(i)); sys.exit(0)
sys.stderr.write('Error from server (NotFound): $name not found\n'); sys.exit(1)" ;;
esac`
	bin := filepath.Join(dir, "kubectl")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\n"+script+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return kube.Client{Bin: bin, Namespace: upgradeNamespace}
}

// now is after the expired job's window and before the pending one's.
var jobsNow = time.Date(2026, 8, 18, 12, 0, 0, 0, time.UTC)

func stateOf(t *testing.T, res Result, name string) (state, message string) {
	t.Helper()
	tbl := res.(Table)
	for _, r := range tbl.Rows {
		if r.Cells[0] == name {
			return r.Cells[2], r.Cells[5]
		}
	}
	t.Fatalf("no row for %q", name)
	return "", ""
}

// A `Succeeded` condition that says "False" is the opposite of success. The
// zsh version only looked for a condition's presence.
func TestJobStateReadsTheConditionStatusNotJustItsType(t *testing.T) {
	res, err := upgradeJobs(context.Background(), fakeJobs(t), jobsNow)
	if err != nil {
		t.Fatal(err)
	}

	if state, msg := stateOf(t, res, "failed-4-20-31"); state != "Failed" {
		t.Errorf("state = %q, want Failed", state)
	} else if !strings.Contains(msg, "degraded operators") {
		t.Errorf("message = %q; the controller's own text is what matters", msg)
	}

	if state, _ := stateOf(t, res, "done-4-20-32"); state != "Succeeded" {
		t.Errorf("state = %q, want Succeeded", state)
	}
}

// The zsh version bound to the Started condition, so a job that had not begun
// produced no output at all — exactly the ones worth seeing before a window.
func TestJobsWithoutConditionsStillAppear(t *testing.T) {
	res, err := upgradeJobs(context.Background(), fakeJobs(t), jobsNow)
	if err != nil {
		t.Fatal(err)
	}

	if got := len(res.(Table).Rows); got != 4 {
		t.Fatalf("got %d rows, want all 4 jobs", got)
	}
	if state, _ := stateOf(t, res, "pending-4-20-33"); state != "Pending" {
		t.Errorf("state = %q, want Pending", state)
	}
}

// A job that never started and whose window has closed will never run, which
// is worth saying rather than leaving it to look merely pending.
func TestJobPastItsStartWindowIsExpired(t *testing.T) {
	res, _ := upgradeJobs(context.Background(), fakeJobs(t), jobsNow)

	state, msg := stateOf(t, res, "expired-4-20-30")
	if state != "Expired" {
		t.Errorf("state = %q, want Expired", state)
	}
	if !strings.Contains(msg, "start window closed") {
		t.Errorf("message = %q", msg)
	}
}

// Upgrade jobs are a calendar. Ordering them by state would move the same job
// around on every reload and make the dates unscannable.
func TestJobsSortByScheduleAlone(t *testing.T) {
	res, _ := upgradeJobs(context.Background(), fakeJobs(t), jobsNow)

	var got []string
	var last time.Time
	for i, r := range res.(Table).Rows {
		got = append(got, r.Cells[0])

		at, err := time.ParseInLocation("2006-01-02 15:04", r.Cells[3], time.Local)
		if err != nil {
			t.Fatalf("row %d has an unreadable date %q", i, r.Cells[3])
		}
		if i > 0 && at.After(last) {
			t.Errorf("row %d (%s) is later than the row above it", i, r.Cells[3])
		}
		last = at
	}

	want := []string{"pending-4-20-33", "done-4-20-32", "failed-4-20-31", "expired-4-20-30"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("order = %v, want %v", got, want)
	}
}

// A hook may appear more than once under different events, so entries are
// counted rather than names.
func TestHookCountCountsEntries(t *testing.T) {
	res, _ := upgradeJobs(context.Background(), fakeJobs(t), jobsNow)

	tbl := res.(Table)
	for _, r := range tbl.Rows {
		switch r.Cells[0] {
		case "done-4-20-32":
			if r.Cells[4] != "3/3" {
				t.Errorf("done hooks = %q, want 3/3", r.Cells[4])
			}
		case "failed-4-20-31":
			if r.Cells[4] != "0/1" {
				t.Errorf("failed hooks = %q, want 0/1", r.Cells[4])
			}
		case "pending-4-20-33":
			if r.Cells[4] != "" {
				t.Errorf("a job with no hooks shows %q", r.Cells[4])
			}
		}
	}
}

// Iterating a null hookTracker is what made jq abort and take the rest of the
// listing with it.
func TestHooksOfAJobWithoutAnyIsEmptyNotAnError(t *testing.T) {
	res, err := upgradeHooks(context.Background(), fakeJobs(t), "pending-4-20-33")
	if err != nil {
		t.Fatalf("a job without hooks errored: %v", err)
	}
	if got := len(res.(Table).Rows); got != 0 {
		t.Errorf("got %d rows", got)
	}
}

func TestHooksKeepBothEventsOfOneHook(t *testing.T) {
	res, err := upgradeHooks(context.Background(), fakeJobs(t), "done-4-20-32")
	if err != nil {
		t.Fatal(err)
	}

	var events []string
	for _, r := range res.(Table).Rows {
		if r.Cells[1] == "maintenance-silence" {
			events = append(events, r.Cells[2])
		}
	}
	if len(events) != 2 {
		t.Errorf("maintenance-silence appears %d times, want 2", len(events))
	}
}

// Where state() has to guess, this view is the correction.
func TestConditionsShowTheControllersOwnWords(t *testing.T) {
	res, err := upgradeConditions(context.Background(), fakeJobs(t), "failed-4-20-31")
	if err != nil {
		t.Fatal(err)
	}

	tbl := res.(Table)
	if len(tbl.Rows) != 2 {
		t.Fatalf("got %d conditions", len(tbl.Rows))
	}
	last := tbl.Rows[1].Cells
	if last[1] != "Succeeded" || last[2] != "False" || last[3] != "Failed" {
		t.Errorf("condition row = %v", last)
	}
}

func TestConditionsRejectAMissingJob(t *testing.T) {
	if _, err := upgradeConditions(context.Background(), fakeJobs(t), "nope"); err == nil {
		t.Fatal("want an error")
	}
}

func TestDrillDownCarriesTheJob(t *testing.T) {
	res, _ := upgradeJobs(context.Background(), fakeJobs(t), jobsNow)
	tbl := res.(Table)
	one := Table{Cols: tbl.Cols, Rows: tbl.Rows[:1]}

	for name, act := range map[string]Action{"conditions": openConditions(), "hooks": openHooks()} {
		out, err := act.Run(context.Background(), one, nil)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if got := out.(Navigate).Args.Get("job"); got != "pending-4-20-33" {
			t.Errorf("%s carries %q", name, got)
		}
	}
}

// From the conditions view the rows are conditions, so the job has to come
// from the view's parameter.
func TestDrillDownFromConditionsUsesTheParameter(t *testing.T) {
	res, _ := upgradeConditions(context.Background(), fakeJobs(t), "done-4-20-32")

	out, err := openHooks().Run(context.Background(), res, Args{"job": "done-4-20-32"})
	if err != nil {
		t.Fatal(err)
	}
	if got := out.(Navigate).Args.Get("job"); got != "done-4-20-32" {
		t.Errorf("carries %q", got)
	}
}

func TestJobsRejectAWrongShape(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "kubectl")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\necho '{\"items\":[{}]}'\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	if _, err := listJobs(context.Background(), kube.Client{Bin: bin}, ""); err == nil {
		t.Fatal("want an error rather than a row of blanks")
	}
}

func TestPausedJobIsReportedAsSuch(t *testing.T) {
	var j upgradeJob
	doc := `{"metadata":{"name":"p"},"status":{"conditions":[
	 {"type":"Started","status":"True","message":"go","lastTransitionTime":"2026-08-17T12:00:00Z"},
	 {"type":"Paused","status":"True","reason":"DelaySet","message":"waiting","lastTransitionTime":"2026-08-17T12:05:00Z"}]}}`
	if err := json.Unmarshal([]byte(doc), &j); err != nil {
		t.Fatal(err)
	}

	if state, msg := j.state(jobsNow); state != "Paused" || msg != "waiting" {
		t.Errorf("state = %q, message = %q", state, msg)
	}
}
