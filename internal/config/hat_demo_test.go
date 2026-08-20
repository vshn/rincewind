package config

import (
	"strings"
	"testing"

	"github.com/vshn/rincewind/internal/engine"
)

// demoHat loads the hat that invents its own data. It needs no cluster and
// no kubectl, so these tests exercise the whole pipeline anywhere.
func demoHat(t *testing.T) []engine.View {
	t.Helper()
	views, err := Load("../../hats/demo")
	if err != nil {
		t.Fatal(err)
	}
	return views
}

func TestHatDemoSortsAndFormats(t *testing.T) {
	res, err := viewByID(t, demoHat(t), "demo.services").Run(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Notes) > 0 {
		t.Fatalf("notes: %v", res.Notes)
	}
	// Two echo calls, three rows: the stdout contract is a stream.
	if len(res.Rows) != 3 {
		t.Fatalf("%d rows, want 3 across both documents", len(res.Rows))
	}

	// Trouble first by rank, then by restarts descending — and numerically,
	// so 17 is not sorted before 2 the way text would be.
	var names, restarts []string
	for i := range res.Rows {
		names = append(names, cellAt(t, res, i, "NAME"))
		restarts = append(restarts, cellAt(t, res, i, "RESTARTS"))
	}
	if got := strings.Join(names, " "); got != "worker api cache" {
		t.Errorf("order %q, want worker api cache", got)
	}
	if got := strings.Join(restarts, " "); got != "17 2 0" {
		t.Errorf("restarts %q, want them ordered numerically", got)
	}

	// The same value read two ways.
	if got := cellAt(t, res, 1, "SINCE"); !strings.HasPrefix(got, "2026-08-18") || strings.Contains(got, "T") {
		t.Errorf("SINCE is %q, want a plain timestamp", got)
	}
	if got := cellAt(t, res, 1, "AGE"); strings.ContainsAny(got, "-:T") {
		t.Errorf("AGE is %q, want a duration", got)
	}

	// Emphasis is resolved while the table is built, not by the renderer.
	if got := res.Rows[0].Cells[1]; got.Emph != engine.Bad {
		t.Errorf("Degraded is %v, want Bad", got.Emph)
	}
}

// A parameter reaches the script as an environment variable, never templated
// into it — and the view it belongs to is not offered in the menu.
func TestHatDemoParameterReachesTheScript(t *testing.T) {
	views := demoHat(t)
	instances := viewByID(t, views, "demo.instances")

	if instances.Listed() {
		t.Error("a view needing a parameter is offered in the menu")
	}

	res, err := instances.Run(t.Context(), engine.Args{"service": "api"})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Rows) != 3 {
		t.Fatalf("%d rows, want 3", len(res.Rows))
	}
	// The crashing one sorts first, and every id carries the parameter.
	if got := cellAt(t, res, 0, "STATE"); got != "CrashLoopBackOff" {
		t.Errorf("first row is %q, want the crashing instance", got)
	}
	for i := range res.Rows {
		if got := cellAt(t, res, i, "ID"); !strings.HasPrefix(got, "api-") {
			t.Errorf("row %d is %q, want the parameter in the id", i, got)
		}
	}
}

// One broken expression must not take down the view: the cell says !err, a
// note says why, and everything else still renders.
func TestHatDemoSurvivesABrokenExpression(t *testing.T) {
	res, err := viewByID(t, demoHat(t), "demo.broken").Run(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Rows) != 1 {
		t.Fatalf("%d rows, want the row to survive", len(res.Rows))
	}
	if got := cellAt(t, res, 0, "NAME"); got != "api" {
		t.Errorf("the healthy cell is %q, want api", got)
	}
	if got := cellAt(t, res, 0, "LABELS"); got != "!err" {
		t.Errorf("the broken cell is %q, want !err", got)
	}
	if len(res.Notes) != 1 || !strings.Contains(res.Notes[0], "LABELS") {
		t.Errorf("notes %v, want one naming the column", res.Notes)
	}
}

// Both kinds of action, and the two scopes, planned but not run.
func TestHatDemoActionsPlanWhatTheyWouldRun(t *testing.T) {
	views := demoHat(t)
	services := viewByID(t, views, "demo.services")

	res, err := services.Run(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}

	byName := map[string]engine.Action{}
	for _, a := range services.Actions {
		byName[a.Name] = a
	}

	// Navigating: the target is resolved at load time, and the argument
	// comes from the row.
	drill := byName["instances"]
	if !drill.Navigates() || drill.Open.ID != "demo.instances" {
		t.Fatalf("the drill-down does not open demo.instances: %+v", drill)
	}
	args, err := drill.Args(res.Rows[0].Src, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := args["service"]; got != "worker" {
		t.Errorf("drilling passes %q, want the cursor row's service", got)
	}

	// Working on rows: one step per row, each naming one object.
	restart := byName["restart"]
	if !restart.NeedsRows() {
		t.Error("restart claims to need no rows")
	}
	steps, err := restart.Plan([]any{res.Rows[0].Src, res.Rows[1].Src}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(steps) != 2 {
		t.Fatalf("%d steps, want one per row", len(steps))
	}
	if got := steps[0].Env["service"]; got != "worker" {
		t.Errorf("first step is about %q, want worker", got)
	}
	if got := strings.Join(steps[0].Command(), "\n"); !strings.Contains(got, "replicas=2") {
		t.Errorf("the command shown %q lost the derived value", got)
	}

	// Working on nothing: one step, its values derived from the collected
	// parameters rather than from a row.
	deploy := byName["deploy"]
	if deploy.NeedsRows() {
		t.Error("deploy demands a selection it will never look at")
	}
	bound, err := deploy.Bind(map[string]string{"service": "api"})
	if err != nil {
		t.Fatal(err)
	}
	if got := bound["image"]; got != "latest" {
		t.Errorf("image is %q, want the default", got)
	}
	steps, err = deploy.Plan(nil, bound)
	if err != nil {
		t.Fatal(err)
	}
	if len(steps) != 1 {
		t.Fatalf("%d steps, want exactly one", len(steps))
	}
	if got := steps[0].Env["reference"]; got != "api:latest" {
		t.Errorf("reference is %q, want it derived from both parameters", got)
	}
	// A rowless step must not inherit the terminal's stdin, or the script
	// would eat the user's keystrokes.
	if steps[0].Stdin == nil {
		t.Error("the rowless step left stdin attached to the terminal")
	}
}

// Actions run: the steps are what actually execute, and say what they did.
func TestHatDemoActionRuns(t *testing.T) {
	views := demoHat(t)
	services := viewByID(t, views, "demo.services")

	res, err := services.Run(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range services.Actions {
		if a.Name != "restart" {
			continue
		}
		steps, err := a.Plan([]any{res.Rows[0].Src}, nil)
		if err != nil {
			t.Fatal(err)
		}
		out, err := steps[0].Run(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		if len(out) != 1 || !strings.Contains(out[0], "restarted worker") {
			t.Fatalf("the step printed %v", out)
		}
		return
	}
	t.Fatal("the demo hat lost its restart action")
}

// A view may open one declared in another file of the same hat: linking
// waits until every file has been read.
func TestHatDemoLinksAcrossFiles(t *testing.T) {
	events := viewByID(t, demoHat(t), "demo.events")
	if len(events.Actions) == 0 {
		t.Fatal("demo.events has no actions")
	}
	if got := events.Actions[0].Open; got == nil || got.ID != "demo.instances" {
		t.Errorf("the action opens %+v, want demo.instances from the other file", got)
	}
}
