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

const demoHat = "../hats/demo"

func demoViews(t *testing.T) []engine.View {
	t.Helper()
	views, err := config.Load(demoHat)
	if err != nil {
		t.Fatal(err)
	}
	return views
}

// The point of the command: a table on stdout, aligned, no frames.
func TestViewPrintsATable(t *testing.T) {
	v, err := pick(demoViews(t), "demo.services", demoHat)
	if err != nil {
		t.Fatal(err)
	}

	var out, errOut bytes.Buffer
	if err := runView(context.Background(), &out, &errOut, v); err != nil {
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
	v, err := pick(demoViews(t), "demo.broken", demoHat)
	if err != nil {
		t.Fatal(err)
	}

	var out, errOut bytes.Buffer
	if err := runView(context.Background(), &out, &errOut, v); err != nil {
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
	v, err := pick(demoViews(t), "demo.tail", demoHat)
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2500*time.Millisecond)
	defer cancel()

	var out, errOut bytes.Buffer
	if err := runView(ctx, &out, &errOut, v); err != nil {
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

	_, err := pick(views, "demo.nope", demoHat)
	if err == nil {
		t.Fatal("an unknown view was accepted")
	}
	for _, want := range []string{"demo.nope", demoHat, "demo.services"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not mention %q", err, want)
		}
	}

	// demo.instances requires a service, and there is no way to pass one.
	_, err = pick(views, "demo.instances", demoHat)
	if err == nil {
		t.Fatal("a view needing an argument was accepted")
	}
	for _, want := range []string{"demo.instances", "service", "demo.services"} {
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
	err = runView(context.Background(), &out, &errOut, v)
	if err == nil {
		t.Fatal("a script that exited 1 was reported as success")
	}
	if !strings.Contains(err.Error(), "no such pod") {
		t.Errorf("the error is %v, want it to carry what stderr said", err)
	}
}
