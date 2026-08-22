package config

import (
	"strings"
	"testing"

	"github.com/vshn/rincewind/internal/engine"
)

func viewByID(t *testing.T, views []engine.View, id string) *engine.View {
	t.Helper()
	for i := range views {
		if views[i].ID == id {
			return &views[i]
		}
	}
	t.Fatalf("example config lost the %s view", id)
	return nil
}

func cellAt(t *testing.T, res engine.Result, row int, col string) string {
	t.Helper()
	for i, c := range res.Cols {
		if c == col {
			return res.Rows[row].Cells[i].Text
		}
	}
	t.Fatalf("no column %q", col)
	return ""
}

// demoHat loads the hat that invents its own data. It needs no cluster and
// no kubectl, so these tests exercise the whole pipeline anywhere.
func demoHat(t *testing.T) Hat {
	t.Helper()
	loaded, err := Load("../../example")
	if err != nil {
		t.Fatal(err)
	}
	return loaded
}

// demoViews is the same hat when only its views are wanted.
func demoViews(t *testing.T) []engine.View {
	t.Helper()
	return demoHat(t).Views
}

func TestHatDemoSortsAndFormats(t *testing.T) {
	res, err := viewByID(t, demoViews(t), "demo.services").Run(t.Context(), nil)
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
	views := demoViews(t)
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
	res, err := viewByID(t, demoViews(t), "demo.broken").Run(t.Context(), nil)
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
	views := demoViews(t)
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

	// The question names what is about to be deployed. It reads $args, which
	// holds what was collected — never what env derived from it, since the
	// two are evaluated apart. A confirmation that says "null" is the bug
	// this covers.
	asked, err := deploy.Confirm(nil, bound)
	if err != nil {
		t.Fatal(err)
	}
	if asked != "Deploy api:latest" {
		t.Errorf("the question is %q, want it to name the image", asked)
	}
}

// Actions run: the steps are what actually execute, and say what they did.
func TestHatDemoActionRuns(t *testing.T) {
	views := demoViews(t)
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
	events := viewByID(t, demoViews(t), "demo.events")
	if len(events.Actions) == 0 {
		t.Fatal("demo.events has no actions")
	}
	if got := events.Actions[0].Open; got == nil || got.ID != "demo.instances" {
		t.Errorf("the action opens %+v, want demo.instances from the other file", got)
	}
}

// A text view's output is the answer, verbatim: no jq, and no collapsing of
// whitespace, which is why a log cannot be a one-column table.
func TestHatDemoTextViewKeepsItsLines(t *testing.T) {
	view := viewByID(t, demoViews(t), "demo.log")

	res, err := view.Run(t.Context(), engine.Args{"service": "api"})
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsText() {
		t.Fatal("a text view produced rows")
	}
	if len(res.Rows) != 0 || res.Cols != nil {
		t.Error("a text view produced columns as well")
	}
	if len(res.Lines) != 5 {
		t.Fatalf("%d lines, want 5", len(res.Lines))
	}
	// The parameter reaches the script here too.
	if !strings.Contains(res.Lines[0], "starting api") {
		t.Errorf("first line is %q, want the parameter in it", res.Lines[0])
	}
	// The indented continuation keeps its indentation: that is the whole
	// reason for the class.
	if got := res.Lines[3]; !strings.HasPrefix(got, "    SELECT") {
		t.Errorf("line 4 is %q, want its leading whitespace kept", got)
	}
}

// A chart view's stream becomes series, in the order the script emits them —
// max first, so the avg line is drawn on top of it.
func TestHatDemoChartDrawsSeries(t *testing.T) {
	view := viewByID(t, demoViews(t), "demo.load")

	res, err := view.Run(t.Context(), engine.Args{"service": "web"})
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsChart() {
		t.Fatal("a chart view did not produce series")
	}
	if len(res.Notes) > 0 {
		t.Fatalf("notes: %v", res.Notes)
	}
	// The parameter reaches the script, and the order is the draw order.
	if len(res.Series) != 2 || res.Series[0].Name != "web max" || res.Series[1].Name != "web avg" {
		t.Fatalf("series %+v, want web max then web avg", res.Series)
	}
	if pts := res.Series[0].Points; len(pts) != 96 {
		t.Fatalf("%d points, want a day at 15 minutes", len(pts))
	}
	if res.YRange == nil || res.YRange[0] != 0 || res.YRange[1] != 100 {
		t.Errorf("yrange %v, want the fixed 0..100 axis", res.YRange)
	}
}

// The hat says where it is: three contexts over one invented source, one of
// them the identity case with no predicate at all.
func TestHatDemoContextsResolve(t *testing.T) {
	hat := demoHat(t)
	if len(hat.Contexts) != 3 {
		t.Fatalf("%d contexts, want 3", len(hat.Contexts))
	}

	b := engine.Resolve(t.Context(), hat.Contexts)
	want := map[string]engine.State{
		"cluster":   engine.StateActive,
		"cloudy":    engine.StateActive,
		"elsewhere": engine.StateInactive,
	}
	for id, state := range want {
		if got := b.State[id]; got != state {
			t.Errorf("context %s is %v, want %v", id, got, state)
		}
	}
	if len(b.Notes) > 0 {
		t.Errorf("notes: %v", b.Notes)
	}

	// Declaration order, and only from the contexts one is actually in.
	if got := strings.Join(b.Labels, " · "); got != "demo-cluster · cloudy" {
		t.Errorf("the chrome would read %q, want the cluster then the platform", got)
	}
}

// `needs:` resolves, across files as well as within one.
func TestHatDemoNeedsNamesItsContexts(t *testing.T) {
	views := demoViews(t)

	if got := viewByID(t, views, "demo.events").Needs; strings.Join(got, " ") != "cloudy" {
		t.Errorf("demo.events needs %v, want the context declared in the other file", got)
	}
	if got := viewByID(t, views, "demo.volumes").Needs; strings.Join(got, " ") != "elsewhere" {
		t.Errorf("demo.volumes needs %v, want the inactive context", got)
	}
	// Most views name nothing and are therefore shown everywhere.
	if got := viewByID(t, views, "demo.services").Needs; len(got) > 0 {
		t.Errorf("demo.services needs %v, want nothing", got)
	}
}

// A view its context rules out is still a view: hiding is a menu decision,
// not a deletion, so it loads and runs like any other.
func TestHatDemoHiddenViewStillRuns(t *testing.T) {
	res, err := viewByID(t, demoViews(t), "demo.volumes").Run(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Rows) != 2 {
		t.Fatalf("%d rows, want 2", len(res.Rows))
	}
	if got := cellAt(t, res, 0, "NAME"); got != "pvc-api" {
		t.Errorf("first row is %q, want pvc-api", got)
	}
}
