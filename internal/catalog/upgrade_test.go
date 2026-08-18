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

const suspensionsFixture = `{"items":[
 {"metadata":{"name":"pentecost-2026","labels":{"argocd.argoproj.io/instance":"openshift-upgrade-controller"}},
  "spec":{"reason":"Pentecost 2026","start":"2026-05-25T09:00:00+02:00","end":"2026-05-25T18:00:00+02:00"}},
 {"metadata":{"name":"year-end-2026","labels":{"argocd.argoproj.io/instance":"openshift-upgrade-controller"}},
  "spec":{"reason":"Year-end freeze","start":"2026-12-24T00:00:00+01:00","end":"2027-01-02T00:00:00+01:00"}},
 {"metadata":{"name":"hotfix-db","labels":{"app.kubernetes.io/managed-by":"aldediggi"}},
  "spec":{"reason":"DB migration","start":"2026-08-18T20:00:00+02:00","end":"2026-08-19T02:00:00+02:00"}}
]}`

func fakeSuspensions(t *testing.T) (kube.Client, func() (args, stdin string)) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "suspensions.json"), []byte(suspensionsFixture), 0o644); err != nil {
		t.Fatal(err)
	}

	script := `D="$(dirname "$0")"
echo "$@" > "$D/args"
case "$*" in
  *create*|*delete*) cat > "$D/stdin"; echo ok ;;
  *) cat "$D/suspensions.json" ;;
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
	return kube.Client{Bin: bin, Namespace: upgradeNamespace}, read
}

// Like upgrade jobs, windows are a calendar: latest first, and the dates have
// to be scannable.
func TestSuspensionsSortByScheduleLatestFirst(t *testing.T) {
	c, _ := fakeSuspensions(t)
	// Before hotfix-db opens, so nothing is pinned.
	now := time.Date(2026, 8, 18, 12, 0, 0, 0, time.UTC)

	res, err := upgradeSuspensions(context.Background(), c, now)
	if err != nil {
		t.Fatal(err)
	}

	want := []string{"year-end-2026", "hotfix-db", "pentecost-2026"}
	for i, name := range want {
		if got := cellAt(t, res, i, 0); got != name {
			t.Errorf("row %d is %q, want %q", i, got, name)
		}
	}
}

// The one exception to the calendar order: whether a window is in force right
// now is the question this view exists to answer.
func TestActiveSuspensionIsPinnedToTheTop(t *testing.T) {
	c, _ := fakeSuspensions(t)
	now := time.Date(2026, 8, 18, 22, 0, 0, 0, time.UTC) // inside hotfix-db

	res, err := upgradeSuspensions(context.Background(), c, now)
	if err != nil {
		t.Fatal(err)
	}

	if got := cellAt(t, res, 0, 0); got != "hotfix-db" {
		t.Errorf("top row is %q, want the active suspension", got)
	}
	if got := cellAt(t, res, 0, 1); got != "ACTIVE" {
		t.Errorf("top row state is %q", got)
	}
	// The rest keeps the calendar order.
	if got := cellAt(t, res, 1, 0); got != "year-end-2026" {
		t.Errorf("second row is %q", got)
	}
}

// Whether ArgoCD owns a window decides whether deleting it achieves anything.
func TestSuspensionsShowWhoOwnsThem(t *testing.T) {
	c, _ := fakeSuspensions(t)

	res, _ := upgradeSuspensions(context.Background(), c, time.Date(2026, 8, 18, 22, 0, 0, 0, time.UTC))
	if got := cellAt(t, res, 0, 5); got != "aldediggi" {
		t.Errorf("hotfix-db source = %q", got)
	}
	if got := cellAt(t, res, 1, 5); got != "argocd" {
		t.Errorf("year-end source = %q", got)
	}
}

// Carrying the ArgoCD instance label would hand the window to ArgoCD, which
// prunes it on the next sync because it is not in git.
func TestCreatedSuspensionIsNotClaimedByArgo(t *testing.T) {
	c, read := fakeSuspensions(t)

	args := Args{"name": "pent-27", "reason": "Pentecost", "start": "2027-05-14 09:00", "end": "+9h"}
	if _, err := createSuspensionAction(c).Run(context.Background(), nil, args); err != nil {
		t.Fatal(err)
	}

	cmd, stdin := read()
	if !strings.Contains(cmd, "create -f -") {
		t.Errorf("command was %q", cmd)
	}

	var sent struct {
		APIVersion string `json:"apiVersion"`
		Kind       string `json:"kind"`
		Metadata   struct {
			Name   string            `json:"name"`
			Labels map[string]string `json:"labels"`
		} `json:"metadata"`
		Spec struct {
			Start          string         `json:"start"`
			End            string         `json:"end"`
			Reason         string         `json:"reason"`
			ConfigSelector map[string]any `json:"configSelector"`
			JobSelector    map[string]any `json:"jobSelector"`
		} `json:"spec"`
	}
	if err := json.Unmarshal([]byte(stdin), &sent); err != nil {
		t.Fatal(err)
	}

	if _, claimed := sent.Metadata.Labels[argoLabel]; claimed {
		t.Error("the suspension carries the ArgoCD instance label")
	}
	if sent.Metadata.Labels[managedByLabel] != managedByValue {
		t.Error("the suspension is not marked as ours")
	}
	if sent.APIVersion != suspensionAPIVersion || sent.Kind != suspensionKind {
		t.Errorf("got %s/%s", sent.APIVersion, sent.Kind)
	}
	if sent.Spec.ConfigSelector == nil || sent.Spec.JobSelector == nil {
		t.Error("selectors must be present and empty, meaning everything")
	}
}

// The API wants an offset; the operator types wall clock. This is where the
// two meet, and a relative end counts from the start.
func TestCreatedSuspensionResolvesTimes(t *testing.T) {
	c, read := fakeSuspensions(t)

	args := Args{"name": "w", "reason": "r", "start": "2027-05-14 09:00", "end": "+9h"}
	if _, err := createSuspensionAction(c).Run(context.Background(), nil, args); err != nil {
		t.Fatal(err)
	}

	_, stdin := read()
	var sent struct {
		Spec struct{ Start, End string } `json:"spec"`
	}
	json.Unmarshal([]byte(stdin), &sent)

	start, err := time.Parse(time.RFC3339, sent.Spec.Start)
	if err != nil {
		t.Fatalf("start %q is not RFC 3339: %v", sent.Spec.Start, err)
	}
	end, err := time.Parse(time.RFC3339, sent.Spec.End)
	if err != nil {
		t.Fatalf("end %q is not RFC 3339: %v", sent.Spec.End, err)
	}
	if got := end.Sub(start); got != 9*time.Hour {
		t.Errorf("span is %s, want 9h", got)
	}
}

func TestSuspensionValidation(t *testing.T) {
	base := Args{"name": "ok", "reason": "r", "start": "2027-05-14 09:00", "end": "+9h"}

	for name, change := range map[string]map[string]string{
		"upper-case name":  {"name": "Not-Ok"},
		"underscore":       {"name": "not_ok"},
		"empty name":       {"name": ""},
		"empty reason":     {"reason": "  "},
		"unreadable start": {"start": "next tuesday"},
		"end before start": {"end": "2027-05-13 09:00"},
		"end equals start": {"end": "2027-05-14 09:00"},
	} {
		args := Args{}
		for k, v := range base {
			args[k] = v
		}
		for k, v := range change {
			args[k] = v
		}

		if _, err := suspensionFrom(args); err == nil {
			t.Errorf("%s: want an error", name)
		}
	}

	if _, err := suspensionFrom(base); err != nil {
		t.Errorf("the valid case failed: %v", err)
	}
}

// The confirmation is the last chance to notice that a window was typed for
// the wrong year.
func TestConfirmationShowsWhatWillBeSent(t *testing.T) {
	args := Args{"name": "w", "reason": "Pentecost", "start": "2027-05-14 09:00", "end": "+9h"}
	now := time.Date(2026, 8, 18, 0, 0, 0, 0, time.UTC)

	got := describeSuspension(args, now)
	for _, want := range []string{"2027-05-14T09:00:00", "9h", "Pentecost"} {
		if !strings.Contains(got, want) {
			t.Errorf("confirmation missing %q: %q", want, got)
		}
	}
	if strings.Contains(got, "in the past") {
		t.Error("a future suspension was called past")
	}

	past := Args{"name": "w", "reason": "r", "start": "2020-01-01 09:00", "end": "+2h"}
	if got := describeSuspension(past, now); !strings.Contains(got, "in the past") {
		t.Errorf("no warning for a past suspension: %q", got)
	}
}

func TestConfirmationRefusesRatherThanLying(t *testing.T) {
	bad := Args{"name": "Bad_Name", "reason": "r", "start": "2027-05-14", "end": "+1h"}

	if got := describeSuspension(bad, time.Now()); !strings.Contains(got, "Cannot create") {
		t.Errorf("invalid parameters produced a normal prompt: %q", got)
	}
}

func TestDeleteWarnsAboutGitManagedSuspensions(t *testing.T) {
	c, _ := fakeSuspensions(t)

	all, err := upgradeSuspensions(context.Background(), c, time.Now())
	if err != nil {
		t.Fatal(err)
	}

	got := deleteSuspensionAction(c).Confirm(all, nil)
	if !strings.Contains(got, "ArgoCD will recreate") {
		t.Errorf("no warning about git-managed windows: %q", got)
	}
	if !strings.Contains(got, "year-end-2026") {
		t.Errorf("the warning does not name them: %q", got)
	}
}

func TestDeleteSendsTheShownSuspensions(t *testing.T) {
	c, read := fakeSuspensions(t)

	all, _ := upgradeSuspensions(context.Background(), c, time.Now())
	if _, err := deleteSuspensionAction(c).Run(context.Background(), all, nil); err != nil {
		t.Fatal(err)
	}

	cmd, stdin := read()
	if !strings.Contains(cmd, "delete -f -") {
		t.Errorf("command was %q", cmd)
	}
	var sent struct {
		Kind  string `json:"kind"`
		Items []any  `json:"items"`
	}
	json.Unmarshal([]byte(stdin), &sent)
	if sent.Kind != "List" || len(sent.Items) != 3 {
		t.Errorf("sent %s with %d items", sent.Kind, len(sent.Items))
	}
}

func TestDeleteRefusesAnEmptySet(t *testing.T) {
	c, _ := fakeSuspensions(t)

	if _, err := deleteSuspensionAction(c).Run(context.Background(), Table{}, nil); err == nil {
		t.Fatal("want an error")
	}
}
