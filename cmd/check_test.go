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

// aheadHat is a hat that uses a key this build has never heard of, the way a
// hat written for a newer rincewind would.
const aheadHat = `
kontexte: []
views:
  - id: demo.list
    zukunft: ja
    source: { script: "echo '{\"n\":1}'" }
    rows: .
    columns: [{name: N, expr: .n}]
`

// A hat from the future loads, and says what it could not make sense of.
func TestCheckReportsUnknownKeysWithoutFailing(t *testing.T) {
	dir := t.TempDir()
	writeHat(t, dir, "ahead.yaml", aheadHat)

	out, err := runCmd(t, "", "check", "--hat", dir)
	if err != nil {
		t.Fatalf("an unknown key failed check: %v", err)
	}
	for _, want := range []string{
		`unknown key "kontexte" in a hat`,
		`unknown key "zukunft" in a view`,
		"ignored 2 unknown keys",
		"a hat written for a newer rincewind",
		"1 view ok",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the report does not mention %q:\n%s", want, out)
		}
	}
}

// Strict is for the hat's own pipeline, where an unknown key is a typo:
// the author is by definition running the version they wrote it for.
func TestCheckStrictFailsOnAnUnknownKey(t *testing.T) {
	dir := t.TempDir()
	writeHat(t, dir, "ahead.yaml", aheadHat)

	if _, err := runCmd(t, "", "check", "--hat", dir, "--strict"); err == nil {
		t.Fatal("--strict passed a hat with unknown keys")
	} else if !strings.Contains(err.Error(), "2 unknown keys") {
		t.Errorf("the error %q does not count them", err)
	}
}

func TestCheckStrictPassesACleanHat(t *testing.T) {
	if _, err := runCmd(t, "", "check", "--hat", demoHat, "--strict"); err != nil {
		t.Errorf("--strict failed the example hat: %v", err)
	}
}
