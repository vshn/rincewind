package catalog

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshn/aldediggi/internal/kube"
)

const appsFixture = `{"items":[
 {"metadata":{"name":"root"},
  "spec":{"syncPolicy":{"automated":{"prune":true,"selfHeal":true},"syncOptions":["CreateNamespace=true"]}},
  "status":{"sync":{"status":"Synced","revision":"a3f9c2172b8e4d1f"},"health":{"status":"Healthy"}}},
 {"metadata":{"name":"cluster-tools"},
  "spec":{"syncPolicy":{"syncOptions":["CreateNamespace=true"]}},
  "status":{"sync":{"status":"OutOfSync"},"health":{"status":"Degraded","message":"deployment has\nunavailable replicas"},
   "resources":[
    {"kind":"ConfigMap","namespace":"tools","name":"settings","status":"Synced"},
    {"kind":"Deployment","namespace":"tools","name":"broker","status":"OutOfSync","health":{"status":"Degraded","message":"0/3"}},
    {"kind":"Service","namespace":"tools","name":"broker","status":"Synced","health":{"status":"Healthy"}}]}},
 {"metadata":{"name":"openshift-logging"},
  "spec":{"syncPolicy":{"automated":{"prune":true}}},
  "status":{"sync":{"status":"Synced"},"health":{"status":"Progressing"}}}
]}`

// fakeKubectl records what it was called with and replays the fixture.
func fakeKubectl(t *testing.T) (kube.Client, func() (args string, stdin string)) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "apps.json"), []byte(appsFixture), 0o644); err != nil {
		t.Fatal(err)
	}

	script := `D="$(dirname "$0")"
echo "$@" > "$D/args"
case "$*" in
  *patch*|*annotate*) cat > "$D/stdin"; echo ok ;;
  *"applications.argoproj.io -o json"*) cat "$D/apps.json" ;;
  *)
    # A named get returns the object itself, the way kubectl does.
    name=$(echo "$*" | sed -n "s/.*applications.argoproj.io \\([^ ]*\\).*/\\1/p")
    python3 -c "
import json,sys
d=json.load(open('$D/apps.json'))
for i in d['items']:
    if i['metadata']['name']=='$name':
        print(json.dumps(i)); sys.exit(0)
sys.stderr.write('Error from server (NotFound): $name not found\\n'); sys.exit(1)" ;;
esac`
	bin := filepath.Join(dir, "kubectl")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\n"+script+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	read := func() (string, string) {
		args, _ := os.ReadFile(filepath.Join(dir, "args"))
		stdin, _ := os.ReadFile(filepath.Join(dir, "stdin"))
		return strings.TrimSpace(string(args)), string(stdin)
	}
	return kube.Client{Bin: bin, Namespace: "syn"}, read
}

func cellAt(t *testing.T, res Result, row, col int) string {
	t.Helper()
	tbl, ok := res.(Table)
	if !ok {
		t.Fatalf("got %T, want Table", res)
	}
	if row >= len(tbl.Rows) {
		t.Fatalf("row %d of %d", row, len(tbl.Rows))
	}
	return tbl.Rows[row].Cells[col]
}

// An operator opening this view is looking for what broke, not for an
// alphabetical census.
func TestAppsSortTroubleFirst(t *testing.T) {
	c, _ := fakeKubectl(t)

	res, err := argoApps(context.Background(), c, "")
	if err != nil {
		t.Fatal(err)
	}

	want := []string{"cluster-tools", "openshift-logging", "root"}
	for i, name := range want {
		if got := cellAt(t, res, i, 0); got != name {
			t.Errorf("row %d is %q, want %q", i, got, name)
		}
	}
}

// AUTO is the column pause and unpause change, and the one the zsh script gave
// no way to see.
func TestAppsShowAutoSyncState(t *testing.T) {
	c, _ := fakeKubectl(t)

	res, _ := argoApps(context.Background(), c, "")
	if got := cellAt(t, res, 0, 3); got != "PAUSED" {
		t.Errorf("cluster-tools AUTO = %q, want PAUSED", got)
	}
	if got := cellAt(t, res, 2, 3); got != "on" {
		t.Errorf("root AUTO = %q, want on", got)
	}
}

func TestAppsTruncateRevisionAndFlattenMessages(t *testing.T) {
	c, _ := fakeKubectl(t)

	res, _ := argoApps(context.Background(), c, "")
	if got := cellAt(t, res, 2, 4); got != "a3f9c21" {
		t.Errorf("revision = %q", got)
	}
	if got := cellAt(t, res, 0, 5); got != "deployment has unavailable replicas" {
		t.Errorf("message = %q; a newline would break the table", got)
	}
}

func TestSyncStatusSortsTroubleFirst(t *testing.T) {
	c, _ := fakeKubectl(t)

	res, err := argoSyncStatus(context.Background(), c, "cluster-tools")
	if err != nil {
		t.Fatal(err)
	}

	// OutOfSync first, then Synced-but-untracked, then Synced and healthy.
	if got := cellAt(t, res, 0, 2); got != "tools/broker" {
		t.Errorf("first row is %q", got)
	}
	if got := cellAt(t, res, 0, 0); got != "OutOfSync" {
		t.Errorf("first row status is %q", got)
	}
}

func TestSyncStatusRejectsAMissingApp(t *testing.T) {
	c, _ := fakeKubectl(t)

	_, err := argoSyncStatus(context.Background(), c, "nope")
	if err == nil {
		t.Fatal("want an error, not an empty table")
	}
	if !strings.Contains(err.Error(), "NotFound") {
		t.Errorf("error lost kubectl's message: %v", err)
	}
}

// A response that is not an Application decodes into all zero values, which
// would otherwise show up as an empty table.
func TestListAppsRejectsAWrongShape(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "kubectl")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\necho '{\"items\":[]}'\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	if _, err := listApps(context.Background(), kube.Client{Bin: bin}, "nope"); err == nil {
		t.Fatal("want an error")
	}
}

// The action must act on exactly the rows shown, which is what `-f -` with a
// List buys us.
func TestPauseSendsTheShownAppsAsAList(t *testing.T) {
	c, read := fakeKubectl(t)

	res, _ := argoApps(context.Background(), c, "")
	if _, err := pauseAction(c).Run(context.Background(), res, nil); err != nil {
		t.Fatal(err)
	}

	args, stdin := read()
	if !strings.Contains(args, "patch -f - --type merge") {
		t.Errorf("args = %q", args)
	}

	var sent struct {
		Kind  string `json:"kind"`
		Items []struct {
			Metadata struct{ Name string } `json:"metadata"`
		} `json:"items"`
	}
	if err := json.Unmarshal([]byte(stdin), &sent); err != nil {
		t.Fatal(err)
	}
	if sent.Kind != "List" || len(sent.Items) != 3 {
		t.Fatalf("sent %s with %d items", sent.Kind, len(sent.Items))
	}
}

// Replacing the whole syncPolicy is what destroyed syncOptions and retry in
// the zsh script; only automated may be touched.
func TestPatchesTouchOnlyAutomated(t *testing.T) {
	for name, patch := range map[string]string{"pause": patchPause, "unpause": patchUnpause} {
		var doc map[string]any
		if err := json.Unmarshal([]byte(patch), &doc); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		policy := doc["spec"].(map[string]any)["syncPolicy"].(map[string]any)
		if len(policy) != 1 {
			t.Errorf("%s patches %d syncPolicy keys, want only automated", name, len(policy))
		}
		if _, ok := policy["automated"]; !ok {
			t.Errorf("%s does not patch automated", name)
		}
	}
}

func TestConfirmNamesTheCountAndSaysWhenRootComesAlong(t *testing.T) {
	c, _ := fakeKubectl(t)
	ctx := context.Background()

	all, _ := argoApps(ctx, c, "")
	if got := pauseAction(c).Confirm(all, nil); !strings.Contains(got, "3 applications") {
		t.Errorf("confirm does not name the count: %q", got)
	} else if strings.Contains(got, "app-of-apps") {
		t.Errorf("warned about root although it is in the set: %q", got)
	}

	// Narrowed to one app, root is patched alongside it and the prompt has to
	// say so. See docs/views.md.
	narrowed := Table{Cols: all.(Table).Cols, Rows: all.(Table).Rows[:1]}
	if got := pauseAction(c).Confirm(narrowed, nil); !strings.Contains(got, "app-of-apps") {
		t.Errorf("prompt does not mention root on a narrowed set: %q", got)
	}
}

func TestActionRefusesAnEmptySet(t *testing.T) {
	c, _ := fakeKubectl(t)

	_, err := pauseAction(c).Run(context.Background(), Table{}, nil)
	if err == nil {
		t.Fatal("want an error rather than a patch against nothing")
	}
}

// Pausing a child alone achieves nothing, so root always comes along. The zsh
// script did this silently; here the confirmation says it.
func TestPauseAlwaysIncludesRoot(t *testing.T) {
	c, read := fakeKubectl(t)
	ctx := context.Background()

	one, err := argoApps(ctx, c, "cluster-tools")
	if err != nil {
		t.Fatal(err)
	}

	act := pauseAction(c)
	if got := act.Confirm(one, nil); !strings.Contains(got, rootApp) {
		t.Errorf("confirmation does not mention root: %q", got)
	}

	if _, err := act.Run(ctx, one, nil); err != nil {
		t.Fatal(err)
	}
	if args, _ := read(); !strings.Contains(args, "applications.argoproj.io "+rootApp) {
		t.Errorf("root was not patched; last call was %q", args)
	}
}

// Resuming the app-of-apps is the last step of a piece of work and stays a
// separate decision.
func TestUnpauseLeavesRootAlone(t *testing.T) {
	c, read := fakeKubectl(t)
	ctx := context.Background()

	one, _ := argoApps(ctx, c, "cluster-tools")

	act := unpauseAction(c)
	if got := act.Confirm(one, nil); !strings.Contains(got, "stays paused") {
		t.Errorf("confirmation does not warn that root stays paused: %q", got)
	}

	if _, err := act.Run(ctx, one, nil); err != nil {
		t.Fatal(err)
	}
	if args, _ := read(); strings.Contains(args, "applications.argoproj.io "+rootApp) {
		t.Error("unpause touched root")
	}
}

func TestDrillDownCarriesTheApplication(t *testing.T) {
	c, _ := fakeKubectl(t)

	one, _ := argoApps(context.Background(), c, "cluster-tools")

	for name, act := range map[string]Action{"syncstatus": openSyncStatus(), "logs": openLogs()} {
		res, err := act.Run(context.Background(), one, nil)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		to, ok := res.(Navigate)
		if !ok {
			t.Fatalf("%s returned %T, want Navigate", name, res)
		}
		if to.Args.Get("app") != "cluster-tools" {
			t.Errorf("%s carries %q", name, to.Args.Get("app"))
		}
	}
}

// Drilling into several applications at once has no meaning; say so instead of
// picking one silently.
func TestDrillDownRefusesAnAmbiguousSelection(t *testing.T) {
	c, _ := fakeKubectl(t)

	all, _ := argoApps(context.Background(), c, "")
	if _, err := openSyncStatus().Run(context.Background(), all, nil); err == nil {
		t.Fatal("want an error for three applications")
	}
}

// From syncstatus the rows are resources, so the application has to come from
// the view's parameters.
func TestLogsFromSyncStatusUsesTheViewParameter(t *testing.T) {
	c, _ := fakeKubectl(t)

	res, err := argoSyncStatus(context.Background(), c, "cluster-tools")
	if err != nil {
		t.Fatal(err)
	}

	out, err := openLogs().Run(context.Background(), res, Args{"app": "cluster-tools"})
	if err != nil {
		t.Fatal(err)
	}
	if got := out.(Navigate).Args.Get("app"); got != "cluster-tools" {
		t.Errorf("carries %q", got)
	}
}

func TestLogsKeepOnlyMatchingLines(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "kubectl")
	script := "#!/bin/sh\nprintf 'a app=one\\nb app=two\\nc app=one\\n'\n"
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}

	res, err := argoLogs(context.Background(), kube.Client{Bin: bin}, "one")
	if err != nil {
		t.Fatal(err)
	}
	if got := res.(Text).Lines; len(got) != 2 {
		t.Errorf("kept %v, want the two matching lines", got)
	}
}

func TestLogsSayWhenNothingMatched(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "kubectl")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\necho 'x app=other'\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	res, err := argoLogs(context.Background(), kube.Client{Bin: bin}, "missing")
	if err != nil {
		t.Fatal(err)
	}
	if lines := res.(Text).Lines; len(lines) != 1 || !strings.Contains(lines[0], "no log lines") {
		t.Errorf("got %v, want an explanation rather than nothing", lines)
	}
}
