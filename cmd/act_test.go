package cmd

import (
	"bytes"
	"context"
	"io"
	"strings"
	"testing"

	"github.com/vshn/rincewind/internal/engine"
)

// runAction drives the real command tree with the two streams kept apart,
// which is the whole point of them: the plan and the question are the
// transcript, and only what the scripts printed is the result.
func runAction(t *testing.T, stdin string, args ...string) (out, errOut string, err error) {
	t.Helper()
	var o, e bytes.Buffer
	err = runCmdBuffers(t, stdin, &o, &e, append(args, "--hat", demoHat)...)
	return o.String(), e.String(), err
}

// The point of the command: the invocation is printed before the question is
// asked, and the question is answerable because the command is above it.
func TestActShowsTheCommandBeforeAsking(t *testing.T) {
	out, errOut, err := runAction(t, "y\n", "act", "demo.services", "restart", "worker")
	if err != nil {
		t.Fatal(err)
	}

	// The plan, the row it is about, the values beside the script, and the
	// hat's own question with the engine's count appended to it.
	for _, want := range []string{"restart  worker", "service   worker", "replicas  2", "sh -c", "Restart worker", "(1 row)", "[y/N]"} {
		if !strings.Contains(errOut, want) {
			t.Errorf("the transcript is missing %q:\n%s", want, errOut)
		}
	}
	if !strings.Contains(out, "restarted worker (2 replicas)") {
		t.Errorf("what the script printed is not on stdout:\n%s", out)
	}
	// The two streams do not bleed into each other.
	if strings.Contains(out, "[y/N]") || strings.Contains(out, "sh -c") {
		t.Errorf("the transcript landed on stdout:\n%s", out)
	}
	if strings.Contains(errOut, "restarted worker (2") {
		t.Errorf("the script's output landed on stderr:\n%s", errOut)
	}
}

// Anything but y is no, and no means nothing ran.
func TestActRunsNothingWithoutAgreement(t *testing.T) {
	out, errOut, err := runAction(t, "n\n", "act", "demo.services", "restart", "worker")
	if err != nil {
		t.Fatalf("declining reported %v, want a clean stop", err)
	}
	if strings.Contains(out, "restarted") {
		t.Errorf("the action ran after being declined:\n%s", out)
	}
	// It was still shown what it would have done.
	if !strings.Contains(errOut, "restart  worker") {
		t.Errorf("the plan was not printed:\n%s", errOut)
	}
}

// Rows are named. With none named the refusal prints the keys it would have
// taken, which is the command line's answer to "select rows first".
func TestActWithoutRowsListsThem(t *testing.T) {
	_, _, err := runAction(t, "", "act", "demo.services", "restart")
	if err == nil {
		t.Fatal("an action that works on rows ran without any")
	}
	for _, want := range []string{"name them", "--all", "api", "worker", "cache"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not mention %q", err, want)
		}
	}
}

// A key that names nothing stops everything, including the rows that were
// found: it is a mistake about the world, not a partial job.
func TestActRefusesAnUnknownRow(t *testing.T) {
	out, _, err := runAction(t, "y\n", "act", "demo.services", "restart", "worker", "wroker")
	if err == nil {
		t.Fatal("an unknown row was accepted")
	}
	if !strings.Contains(err.Error(), `"wroker"`) {
		t.Errorf("error %q does not name the row that is not there", err)
	}
	if strings.Contains(out, "restarted") {
		t.Errorf("rows were acted on despite the refusal:\n%s", out)
	}
}

// --all is the bulk case, and the count in the question is what makes it
// answerable.
func TestActOnAllRows(t *testing.T) {
	out, errOut, err := runAction(t, "y\n", "act", "demo.services", "restart", "--all")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(errOut, "(3 rows)") {
		t.Errorf("the question does not say how much it touches:\n%s", errOut)
	}
	for _, want := range []string{"restarted api", "restarted worker", "restarted cache"} {
		if !strings.Contains(out, want) {
			t.Errorf("stdout is missing %q:\n%s", want, out)
		}
	}
	if !strings.Contains(errOut, "3 steps ok") {
		t.Errorf("the summary is missing:\n%s", errOut)
	}
}

// --yes is the way past the question, never past the plan: what ran is still
// on screen afterwards.
func TestActYesStillPrintsThePlan(t *testing.T) {
	out, errOut, err := runAction(t, "", "act", "demo.services", "restart", "api", "--yes")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(errOut, "sh -c") {
		t.Errorf("--yes skipped the plan as well as the question:\n%s", errOut)
	}
	if strings.Contains(errOut, "[y/N]") {
		t.Errorf("--yes still asked:\n%s", errOut)
	}
	if !strings.Contains(out, "restarted api") {
		t.Errorf("the action did not run:\n%s", out)
	}
}

// A rowless action acts on nothing and takes its values from -p, which is
// the case a script or a cron job has.
func TestActRowlessTakesParameters(t *testing.T) {
	out, errOut, err := runAction(t, "", "act", "demo.services", "deploy",
		"-p", "service=billing", "-p", "image=v2", "--yes")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(errOut, "Deploy billing:v2") {
		t.Errorf("the question does not carry the derived value:\n%s", errOut)
	}
	// A rowless action counts nothing: there are no rows to count.
	if strings.Contains(errOut, "row)") || strings.Contains(errOut, "rows)") {
		t.Errorf("a rowless action counted rows:\n%s", errOut)
	}
	if !strings.Contains(out, "deployed billing:v2") {
		t.Errorf("the action did not run:\n%s", out)
	}
}

// The default is applied when nothing overrides it, exactly as in the form.
func TestActRowlessUsesADefault(t *testing.T) {
	_, errOut, err := runAction(t, "", "act", "demo.services", "deploy", "-p", "service=billing", "--yes")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(errOut, "Deploy billing:latest") {
		t.Errorf("the default did not reach the command:\n%s", errOut)
	}
}

// Nothing is asked of a pipe, so a required parameter without a value is
// refused by name rather than waited for.
func TestActRefusesAMissingRequiredParameter(t *testing.T) {
	_, _, err := runAction(t, "", "act", "demo.services", "deploy", "--yes")
	if err == nil {
		t.Fatal("a required parameter was invented")
	}
	for _, want := range []string{"deploy", "service", "-p name=value"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not mention %q", err, want)
		}
	}
}

// Rows and a rowless action exclude each other: it will never look at them.
func TestActRowlessRefusesRows(t *testing.T) {
	_, _, err := runAction(t, "", "act", "demo.services", "deploy", "api", "-p", "service=x", "--yes")
	if err == nil {
		t.Fatal("a rowless action accepted rows")
	}
	if !strings.Contains(err.Error(), "scope: none") {
		t.Errorf("error %q does not say why", err)
	}
}

// A view's own parameter reaches both the view's script and the action's.
func TestActCarriesTheViewsParameter(t *testing.T) {
	out, errOut, err := runAction(t, "", "act", "demo.instances", "restart", "-p", "service=api", "--yes")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(errOut, "Restart the service api") {
		t.Errorf("the view's parameter did not reach the question:\n%s", errOut)
	}
	if !strings.Contains(out, "restarted api") {
		t.Errorf("the action did not run:\n%s", out)
	}
}

// Navigating is looking, and looking is what `view` does — so the refusal
// hands over the command that does it.
func TestActRefusesANavigatingAction(t *testing.T) {
	_, _, err := runAction(t, "", "act", "demo.services", "instances", "api")
	if err == nil {
		t.Fatal("a navigating action was run")
	}
	for _, want := range []string{"navigates", "rincewind view demo.instances", "-p service="} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not mention %q", err, want)
		}
	}
}

// An action that is not there names the ones that are, and of which kind.
func TestActUnknownActionListsThem(t *testing.T) {
	_, _, err := runAction(t, "", "act", "demo.services", "restrat")
	if err == nil {
		t.Fatal("an unknown action was accepted")
	}
	for _, want := range []string{"restrat", "restart", "works on rows", "deploy", "acts on nothing", "navigates to"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not mention %q", err, want)
		}
	}
}

// tripwire is a stdin that must never be read: the refusals around `-` have
// to come first, or a terminal's stdin would be sat on until EOF before
// anything is said.
type tripwire struct{ t *testing.T }

func (r tripwire) Read([]byte) (int, error) {
	r.t.Error("stdin was read before the refusal")
	return 0, io.EOF
}

func TestActDashRefusesBeforeReading(t *testing.T) {
	var o, e bytes.Buffer
	err := runCmdStreams(t, tripwire{t}, &o, &e,
		"act", "demo.services", "restart", "-", "--hat", demoHat)
	if err == nil {
		t.Fatal("keys from stdin were taken without --yes")
	}
	if !strings.Contains(err.Error(), "--yes") {
		t.Errorf("error %q does not say what would work", err)
	}
}

// Keys off stdin leave nothing to answer with, so that form says so instead
// of refusing at the question and looking like it did nothing.
func TestActKeysFromStdinNeedYes(t *testing.T) {
	_, _, err := runAction(t, "worker\n", "act", "demo.services", "restart", "-")
	if err == nil {
		t.Fatal("keys from stdin were taken without --yes")
	}
	if !strings.Contains(err.Error(), "--yes") {
		t.Errorf("error %q does not say what would work", err)
	}

	out, _, err := runAction(t, "worker\ncache\n", "act", "demo.services", "restart", "-", "--yes")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"restarted worker", "restarted cache"} {
		if !strings.Contains(out, want) {
			t.Errorf("stdout is missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "restarted api") {
		t.Errorf("a row nobody named was acted on:\n%s", out)
	}
}

// One step per row is what makes a partial failure attributable: the broken
// one is named, the rest still run, and the count becomes the exit status.
func TestActReportsAFailingStep(t *testing.T) {
	steps := []engine.Step{
		{Row: "one", Shell: []string{"sh", "-c"}, Script: "echo fine"},
		{Row: "two", Shell: []string{"sh", "-c"}, Script: `echo "no such service" >&2; exit 1`},
		{Row: "three", Shell: []string{"sh", "-c"}, Script: "echo also-fine"},
	}

	var out, errOut bytes.Buffer
	err := work(context.Background(), &out, &errOut, steps)
	if err == nil {
		t.Fatal("a failing step was reported as success")
	}
	if !strings.Contains(err.Error(), "1 of 3 steps failed") {
		t.Errorf("the summary is %q, want it to count the failures", err)
	}
	// The failure names its row, and carries what the script said.
	for _, want := range []string{"two: ", "no such service"} {
		if !strings.Contains(errOut.String(), want) {
			t.Errorf("stderr is missing %q:\n%s", want, errOut.String())
		}
	}
	if !strings.Contains(out.String(), "also-fine") {
		t.Errorf("a step after the failure did not run:\n%s", out.String())
	}
}

// The command means little without the place: in CI `act … --yes` used to say
// nothing at all about which cluster it was about to hit.
func TestActNamesTheContextAboveThePlan(t *testing.T) {
	out, errOut, err := runAction(t, "", "act", "demo.services", "restart", "api", "--yes")
	if err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(errOut, "context: demo-cluster · cloudy") {
		t.Fatalf("the transcript does not say where it acted:\n%s", errOut)
	}
	// Above the plan, so it is read before the invocation it applies to.
	if strings.Index(errOut, "context:") > strings.Index(errOut, "restart  api") {
		t.Errorf("the context is under the plan:\n%s", errOut)
	}
	// An inactive context is not somewhere one is.
	if strings.Contains(errOut, "elsewhere") {
		t.Errorf("an inactive context reached the transcript:\n%s", errOut)
	}
	// Transcript, not result: a redirect catches what the scripts printed.
	if strings.Contains(out, "context:") {
		t.Errorf("the context line landed on stdout:\n%s", out)
	}
}

// A hat with nothing to say about where it is says nothing: an empty label is
// not a heading.
func TestActIsQuietWithoutContexts(t *testing.T) {
	dir := t.TempDir()
	writeHat(t, dir, "plain.yaml", `
views:
  - id: demo.list
    source: { script: "echo '{\"name\":\"a\"}'" }
    rows: .
    key: .name
    columns: [{name: NAME, expr: .name}]
    actions:
      - key: t
        name: touch
        script: echo touched
`)
	var o, e bytes.Buffer
	if err := runCmdBuffers(t, "", &o, &e,
		"act", "demo.list", "touch", "a", "--yes", "--hat", dir); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(e.String(), "context:") {
		t.Errorf("a hat that declares no contexts still got a heading:\n%s", e.String())
	}
}
