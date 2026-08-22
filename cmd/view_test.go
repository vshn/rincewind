package cmd

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"github.com/vshn/rincewind/internal/config"
	"github.com/vshn/rincewind/internal/engine"
)

const demoHat = "../example"

func demoViews(t *testing.T) []engine.View {
	t.Helper()
	loaded, err := config.Load(demoHat)
	if err != nil {
		t.Fatal(err)
	}
	return loaded.Views
}

// The point of the command: a table on stdout, aligned, no frames.
func TestViewPrintsATable(t *testing.T) {
	v, err := find(demoViews(t), "demo.services", demoHat)
	if err != nil {
		t.Fatal(err)
	}

	var out, errOut bytes.Buffer
	if err := runView(context.Background(), &out, &errOut, v, nil); err != nil {
		t.Fatal(err)
	}

	lines := strings.Split(strings.TrimRight(out.String(), "\n"), "\n")
	if len(lines) != 4 {
		t.Fatalf("got %d lines, want a header and three rows:\n%s", len(lines), out.String())
	}
	if !strings.HasPrefix(lines[0], "NAME") {
		t.Errorf("the first line is not the header: %q", lines[0])
	}
	// The sort is the hat's: Degraded before Healthy.
	if !strings.HasPrefix(lines[1], "worker") {
		t.Errorf("the rows are not in the view's order: %q", lines[1])
	}
}

// A broken expression must not go into the table someone is piping into a
// file; it belongs with the diagnostics.
func TestViewNotesGoToStderr(t *testing.T) {
	v, err := find(demoViews(t), "demo.broken", demoHat)
	if err != nil {
		t.Fatal(err)
	}

	var out, errOut bytes.Buffer
	if err := runView(context.Background(), &out, &errOut, v, nil); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "!err") {
		t.Error("the broken cell is not marked in the table")
	}
	if strings.Contains(out.String(), "note:") {
		t.Errorf("a note landed on stdout: %q", out.String())
	}
	if !strings.Contains(errOut.String(), "column LABELS") {
		t.Errorf("stderr does not name the broken column: %q", errOut.String())
	}
}

// A followed view streams, and stops when the context does — which is what
// Ctrl-C cancels.
func TestViewFollowsUntilInterrupted(t *testing.T) {
	v, err := find(demoViews(t), "demo.tail", demoHat)
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2500*time.Millisecond)
	defer cancel()

	var out, errOut bytes.Buffer
	if err := runView(ctx, &out, &errOut, v, nil); err != nil {
		t.Fatalf("an interrupted stream reported %v, want a clean stop", err)
	}

	lines := strings.Split(strings.TrimRight(out.String(), "\n"), "\n")
	if len(lines) < 2 {
		t.Errorf("the stream produced %d lines in two seconds:\n%s", len(lines), out.String())
	}
	if !strings.Contains(lines[0], "starting api") {
		t.Errorf("the default parameter did not reach the script: %q", lines[0])
	}
}

// Both refusals name what would have worked: a list of ids is the answer to
// either mistake.
func TestViewRefusalsListWhatWorks(t *testing.T) {
	views := demoViews(t)

	_, err := find(views, "demo.nope", demoHat)
	if err == nil {
		t.Fatal("an unknown view was accepted")
	}
	for _, want := range []string{"demo.nope", demoHat, "demo.services"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not mention %q", err, want)
		}
	}

	// The listing marks what a view insists on being given, so it answers
	// "and how would I run that one" as well.
	if !strings.Contains(err.Error(), "demo.instances  (needs service)") {
		t.Errorf("the listing does not say what demo.instances needs: %q", err)
	}
}

// -p is how a view with parameters is run out here. Without it the refusal
// names the parameter rather than only that something is missing.
func TestViewTakesItsParameters(t *testing.T) {
	_, err := runCmd(t, "", "view", "demo.instances", "--hat", demoHat)
	if err == nil {
		t.Fatal("a view needing a parameter ran without one")
	}
	for _, want := range []string{"demo.instances", "service", "-p name=value"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not mention %q", err, want)
		}
	}

	out, err := runCmd(t, "", "view", "demo.instances", "--hat", demoHat, "-p", "service=api")
	if err != nil {
		t.Fatal(err)
	}
	// The value reached the script as $service, which is what the ids are
	// built from.
	if !strings.Contains(out, "api-1") {
		t.Errorf("the parameter did not reach the script:\n%s", out)
	}
}

// A value nothing declares is a typo, and running on the default instead is
// exactly the surprise this catches.
func TestViewRefusesAnUndeclaredParameter(t *testing.T) {
	_, err := runCmd(t, "", "view", "demo.instances", "--hat", demoHat, "-p", "servcie=api")
	if err == nil {
		t.Fatal("a parameter the view does not declare was accepted")
	}
	for _, want := range []string{"servcie", "service"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not mention %q", err, want)
		}
	}
}

// A script that fails takes the command down with it, carrying its stderr:
// a non-zero exit is what a pipeline reads.
func TestViewReportsAFailingScript(t *testing.T) {
	v, err := engine.Compile(engine.Def{
		ID:      "broken.script",
		Script:  `echo "no such pod" >&2; exit 1`,
		Rows:    ".[]",
		Columns: []engine.ColumnDef{{Name: "N", Expr: "."}},
	})
	if err != nil {
		t.Fatal(err)
	}

	var out, errOut bytes.Buffer
	err = runView(context.Background(), &out, &errOut, v, nil)
	if err == nil {
		t.Fatal("a script that exited 1 was reported as success")
	}
	if !strings.Contains(err.Error(), "no such pod") {
		t.Errorf("the error is %v, want it to carry what stderr said", err)
	}
}
