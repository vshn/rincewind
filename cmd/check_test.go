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

// released pretends this binary is one, for as long as the test runs. The
// version is what decides whether a hat's requirement is enforced at all.
func released(t *testing.T, v string) {
	t.Helper()
	was := version
	version = v
	t.Cleanup(func() { version = was })
}

// A hat too new to run says so in one sentence, rather than failing on
// whatever it happens to use first.
func TestAHatFromTheFutureIsRefusedByName(t *testing.T) {
	dir := t.TempDir()
	writeHat(t, dir, "ahead.yaml", `
rincewind: ">= 99.0"
views:
  - id: demo.list
    source: { script: "echo '{}'" }
    rows: .
    columns: [{name: X, expr: "1"}]
`)
	// The tests run as "dev", which is deliberately not held to a
	// requirement; this is about what a released binary does.
	released(t, "v0.6.0")

	out, err := runCmd(t, "", "check", "--hat", dir)
	if err == nil {
		t.Fatalf("a hat requiring a future version ran:\n%s", out)
	}
	for _, want := range []string{"needs rincewind >= 99.0.0", "this is v0.6.0", "upgrade rincewind"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the error %q does not mention %q", err, want)
		}
	}
}

// One a hat pins and this build satisfies is still worth saying: it tells
// whoever edits the hat what they may write in it.
func TestCheckReportsAMetRequirement(t *testing.T) {
	dir := t.TempDir()
	writeHat(t, dir, "pinned.yaml", `
rincewind: ">= 0.1"
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
	if !strings.Contains(out, "requires rincewind >= 0.1.0") {
		t.Errorf("the report does not say what the hat pins:\n%s", out)
	}
}

// And a released binary that does satisfy the hat simply runs it.
func TestAMetRequirementDoesNotRefuse(t *testing.T) {
	dir := t.TempDir()
	writeHat(t, dir, "pinned.yaml", `
rincewind: ">= 0.6"
views:
  - id: demo.list
    source: { script: "echo '{}'" }
    rows: .
    columns: [{name: X, expr: "1"}]
`)
	released(t, "v0.6.0")

	if _, err := runCmd(t, "", "check", "--hat", dir); err != nil {
		t.Errorf("a met requirement refused: %v", err)
	}
}

// A build with no version cannot be measured, and says so — silence there is
// how one tests a hat locally, ships it, and hands the refusal to whoever is
// running a release.
func TestABuildWithoutAVersionSaysItDidNotCheck(t *testing.T) {
	dir := t.TempDir()
	writeHat(t, dir, "pinned.yaml", `
rincewind: ">= 99.0"
views:
  - id: demo.list
    source: { script: "echo '{}'" }
    rows: .
    columns: [{name: X, expr: "1"}]
`)
	released(t, "dev")

	out, err := runCmd(t, "", "check", "--hat", dir)
	if err != nil {
		t.Fatalf("a build with no version was refused: %v", err)
	}
	if !strings.Contains(out, "not checking") || !strings.Contains(out, "99.0.0") {
		t.Errorf("it passed in silence:\n%s", out)
	}
}

// A build between releases is measured on its tag, which git describe makes a
// real lower bound: v0.6.0-9-g… demonstrably does not hold v0.7.0.
func TestABuildBetweenReleasesIsStillMeasured(t *testing.T) {
	dir := t.TempDir()
	writeHat(t, dir, "pinned.yaml", `
rincewind: ">= 0.8"
views:
  - id: demo.list
    source: { script: "echo '{}'" }
    rows: .
    columns: [{name: X, expr: "1"}]
`)
	released(t, "v0.6.0-9-gb5802a3")

	if _, err := runCmd(t, "", "check", "--hat", dir); err == nil {
		t.Error("a build nine commits past v0.6.0 satisfied >= 0.8")
	}
}

// A working tree matching no commit is not measured, so testing a hat against
// a build one is in the middle of editing never fails on the requirement.
func TestADirtyTreeIsNotMeasured(t *testing.T) {
	dir := t.TempDir()
	writeHat(t, dir, "pinned.yaml", `
rincewind: ">= 99.0"
views:
  - id: demo.list
    source: { script: "echo '{}'" }
    rows: .
    columns: [{name: X, expr: "1"}]
`)
	released(t, "v0.6.0-9-gb5802a3-dirty")

	out, err := runCmd(t, "", "check", "--hat", dir)
	if err != nil {
		t.Fatalf("a build from a working tree was refused: %v", err)
	}
	if !strings.Contains(out, "not checking") {
		t.Errorf("it passed in silence:\n%s", out)
	}
}
