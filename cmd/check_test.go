package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeHat puts one file into a directory a --hat can point at.
func writeHat(t *testing.T, dir, name, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

// The point of the report: where the hat thinks it is, what it expects to
// find, and what it is holding back — over a hat that invents all three.
func TestCheckReportsTheContexts(t *testing.T) {
	out, err := runCmd(t, "", "check", "--hat", demoHat)
	if err != nil {
		t.Fatal(err)
	}

	for _, want := range []string{
		"8 views ok",
		// Both states, said in the word the engine uses.
		"cluster",
		"active",
		"elsewhere",
		"inactive",
		// The label an active context contributes to the chrome.
		"demo-cluster",
		// The tools, probed on PATH rather than assumed.
		"sh ✓",
		"dinsdale ✗ not on PATH",
		// What each context gates, so the report answers "what would I lose".
		"1 view",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the report does not mention %q:\n%s", want, out)
		}
	}

	// An inactive context contributes no label, so the line carries none.
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, "elsewhere") && strings.Contains(line, "demo-cluster") {
			t.Errorf("an inactive context claimed a label: %q", line)
		}
	}
}

// Neither an inactive context nor a missing binary is a config error: one may
// well be checking a hat for a platform one is not on. Only a hat that does
// not load fails.
func TestCheckSucceedsOnAPlatformItIsNotOn(t *testing.T) {
	if _, err := runCmd(t, "", "check", "--hat", demoHat); err != nil {
		t.Errorf("check failed over an inactive context and a missing tool: %v", err)
	}
}

func TestCheckFailsOnAHatThatDoesNotLoad(t *testing.T) {
	dir := t.TempDir()
	writeHat(t, dir, "broken.yaml", `
views:
  - id: demo.list
    source: { script: "echo '{}'" }
    rows: .
    columns: [{name: X, expr: "1"}]
    sort: [{by: NOPE}]
`)
	out, err := runCmd(t, "", "check", "--hat", dir)
	if err == nil {
		t.Fatalf("a hat that does not load passed check:\n%s", out)
	}
	if !strings.Contains(err.Error(), "NOPE") {
		t.Errorf("the error %q does not name the mistake", err)
	}
}

// A hat that declares no contexts says nothing about them, rather than a
// heading over an empty list.
func TestCheckIsQuietWithoutContexts(t *testing.T) {
	dir := t.TempDir()
	writeHat(t, dir, "plain.yaml", `
views:
  - id: demo.list
    source: { script: "echo '{}'" }
    rows: .
    columns: [{name: X, expr: "1"}]
`)
	out, err := runCmd(t, "", "check", "--hat", dir)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(out); !strings.HasSuffix(got, "1 view ok") {
		t.Errorf("the report says more than there is to say:\n%s", out)
	}
}

// A probe that could not answer hides nothing, so the TUI stays quiet about
// it; this is where it is reported instead.
func TestCheckReportsAProbeThatCouldNotAnswer(t *testing.T) {
	dir := t.TempDir()
	writeHat(t, dir, "broken-probe.yaml", `
contexts:
  - id: nowhere
    source: { script: 'echo "no configuration has been provided" >&2; exit 1' }
    label: .whatever
views:
  - id: demo.list
    needs: nowhere
    source: { script: "echo '{}'" }
    rows: .
    columns: [{name: X, expr: "1"}]
`)
	out, err := runCmd(t, "", "check", "--hat", dir)
	if err != nil {
		t.Errorf("a probe that failed made check fail: %v", err)
	}
	if !strings.Contains(out, "unknown") {
		t.Errorf("the state is not reported as unknown:\n%s", out)
	}
	if !strings.Contains(out, "no configuration has been provided") {
		t.Errorf("the probe's own complaint is missing:\n%s", out)
	}
}
