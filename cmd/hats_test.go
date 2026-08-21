package cmd

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/vshn/rincewind/internal/hats"
)

// repoWith builds a real git repository holding the named hats, so the
// command is exercised over the path it takes in production.
func repoWith(t *testing.T, names ...string) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not on PATH")
	}

	dir := t.TempDir()
	for _, name := range names {
		sub := filepath.Join(dir, "hats", name)
		if err := os.MkdirAll(sub, 0o755); err != nil {
			t.Fatal(err)
		}
		body := demoView
		if name == "broken" {
			body = brokenView
		}
		if err := os.WriteFile(filepath.Join(sub, "views.yaml"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// Something that is not a hat, so a repository with no hats still has a
	// commit to make.
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("# hats\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"init", "--initial-branch=main"},
		{"config", "user.email", "t@example.test"},
		{"config", "user.name", "T"},
		{"add", "-A"},
		{"commit", "-m", "first"},
	} {
		if out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	return dir
}

const demoView = `
views:
  - id: demo.list
    source:
      script: echo '{"items":[{"name":"a"}]}'
    rows: .items[]
    columns:
      - name: NAME
        expr: .name
`

// A view whose sort names no column, which config.Load refuses at load time.
const brokenView = `
views:
  - id: demo.broken
    source:
      script: echo '{}'
    rows: .
    columns:
      - name: NAME
        expr: .
    sort:
      - by: NOPE
`

// fromLocal makes the install command clone a repository on disk. Installing
// is GitHub only, so the seam is how the command's own flow — ask, clone,
// check, report — is covered without a network.
func fromLocal(t *testing.T, dir string) {
	t.Helper()
	parseSource = func(string) (hats.Source, error) {
		return hats.Source{URL: dir, Slug: "vshn-" + filepath.Base(dir)}, nil
	}
	t.Cleanup(func() { parseSource = hats.ParseSource })
}

// runCmd drives the real command tree, with the flags reset so one test
// cannot leak its store into the next.
func runCmd(t *testing.T, stdin string, args ...string) (string, error) {
	t.Helper()
	// Cobra keeps a flag's value on the command between runs, so every flag
	// this tree has goes back to its zero value here: one test's --yes is
	// otherwise still set when the next one asks a question.
	hatsDirFlag, yesFlag, hatFlag = "", false, ""
	viewParams, actParams = nil, nil
	actAll, actYes = false, false

	var out bytes.Buffer
	rootCmd.SetOut(&out)
	rootCmd.SetErr(&out)
	rootCmd.SetIn(strings.NewReader(stdin))
	rootCmd.SetArgs(args)
	t.Cleanup(func() {
		rootCmd.SetOut(nil)
		rootCmd.SetErr(nil)
		rootCmd.SetIn(nil)
		rootCmd.SetArgs(nil)
	})

	err := rootCmd.Execute()
	return out.String(), err
}

// The whole loop, over a real clone: install, list, remove.
func TestHatsInstallListRemove(t *testing.T) {
	store := t.TempDir()
	repo := repoWith(t, "albedaran", "demo")
	fromLocal(t, repo)

	out, err := runCmd(t, "", "hats", "-d", store, "-y", "install", repo)
	if err != nil {
		t.Fatalf("install: %v\n%s", err, out)
	}
	for _, want := range []string{"albedaran", "demo", "1 view", "RINCEWIND_HAT="} {
		if !strings.Contains(out, want) {
			t.Errorf("install said nothing about %q:\n%s", want, out)
		}
	}
	// The warning is the reason install asks at all.
	if !strings.Contains(out, "runs as you") {
		t.Errorf("install did not say that a hat is code:\n%s", out)
	}

	out, err = runCmd(t, "", "hats", "-d", store, "list")
	if err != nil {
		t.Fatalf("list: %v\n%s", err, out)
	}
	if !strings.Contains(out, "vshn-"+filepath.Base(repo)) {
		t.Errorf("list does not name the clone:\n%s", out)
	}

	if out, err = runCmd(t, "", "hats", "-d", store, "-y", "remove", filepath.Base(repo)); err != nil {
		t.Fatalf("remove: %v\n%s", err, out)
	}
	out, _ = runCmd(t, "", "hats", "-d", store, "list")
	if !strings.Contains(out, "nothing installed") {
		t.Errorf("the clone survived remove:\n%s", out)
	}
}

// Installing is not a mutation one should be able to make by accident.
func TestInstallStopsWhenTheAnswerIsNo(t *testing.T) {
	store := t.TempDir()

	repo := repoWith(t, "demo")
	fromLocal(t, repo)

	out, err := runCmd(t, "n\n", "hats", "-d", store, "install", repo)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "cloned at") {
		t.Errorf("it cloned anyway:\n%s", out)
	}

	list, _ := runCmd(t, "", "hats", "-d", store, "list")
	if !strings.Contains(list, "nothing installed") {
		t.Errorf("something was installed:\n%s", list)
	}
}

// The check runs at install, not at the first keystroke — and one broken hat
// in a repository of two is not a reason to have neither.
func TestInstallNamesAHatThatDoesNotLoad(t *testing.T) {
	store := t.TempDir()

	repo := repoWith(t, "broken", "demo")
	fromLocal(t, repo)

	out, err := runCmd(t, "", "hats", "-d", store, "-y", "install", repo)
	if err != nil {
		t.Fatalf("one broken hat took the whole install down: %v\n%s", err, out)
	}
	if !strings.Contains(out, "does not load") {
		t.Errorf("the broken hat was not named:\n%s", out)
	}
	if !strings.Contains(out, "NOPE") {
		t.Errorf("the load error was swallowed:\n%s", out)
	}
	if !strings.Contains(out, "demo") {
		t.Errorf("the good hat is missing:\n%s", out)
	}
}

// A repository with nothing in it leaves nothing behind.
func TestInstallRefusesARepositoryWithoutHats(t *testing.T) {
	store := t.TempDir()

	repo := repoWith(t)
	fromLocal(t, repo)

	out, err := runCmd(t, "", "hats", "-d", store, "-y", "install", repo)
	if err == nil {
		t.Fatalf("an empty repository was accepted:\n%s", out)
	}
	if !strings.Contains(err.Error(), "hats/") {
		t.Errorf("the error is %q, want it to name the convention", err)
	}

	list, _ := runCmd(t, "", "hats", "-d", store, "list")
	if !strings.Contains(list, "nothing installed") {
		t.Errorf("the failed install left a clone behind:\n%s", list)
	}
}

// Update shows the diff before it asks, and answering no changes nothing.
func TestUpdateShowsTheDiffAndCanBeDeclined(t *testing.T) {
	store := t.TempDir()
	repo := repoWith(t, "demo")
	fromLocal(t, repo)
	if out, err := runCmd(t, "", "hats", "-d", store, "-y", "install", repo); err != nil {
		t.Fatal(err, out)
	}

	before := hats.Head(t.Context(), filepath.Join(store, "vshn-"+filepath.Base(repo)))

	path := filepath.Join(repo, "hats", "demo", "views.yaml")
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(body, []byte("\n# changed\n")...), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"add", "-A"}, {"commit", "-m", "second"}} {
		if out, err := exec.Command("git", append([]string{"-C", repo}, args...)...).CombinedOutput(); err != nil {
			t.Fatal(err, string(out))
		}
	}

	out, err := runCmd(t, "n\n", "hats", "-d", store, "update")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "views.yaml") {
		t.Errorf("the diff was not shown before the question:\n%s", out)
	}
	after := hats.Head(t.Context(), filepath.Join(store, "vshn-"+filepath.Base(repo)))
	if after != before {
		t.Error("the clone moved although the answer was no")
	}
}

// Two questions in one run, answered from a pipe: the reader that reads the
// first answer must not swallow the second.
func TestUpdateAnswersEveryQuestionFromAPipe(t *testing.T) {
	store := t.TempDir()

	var paths []string
	for range 2 {
		repo := repoWith(t, "demo")
		fromLocal(t, repo)
		if out, err := runCmd(t, "", "hats", "-d", store, "-y", "install", repo); err != nil {
			t.Fatal(err, out)
		}
		commitChange(t, repo)
		paths = append(paths, filepath.Join(store, "vshn-"+filepath.Base(repo)))
	}

	before := make([]string, len(paths))
	for i, p := range paths {
		before[i] = hats.Head(t.Context(), p)
	}

	out, err := runCmd(t, "y\ny\n", "hats", "-d", store, "update")
	if err != nil {
		t.Fatal(err, out)
	}
	for i, p := range paths {
		if hats.Head(t.Context(), p) == before[i] {
			t.Errorf("clone %d did not move although the answer was yes:\n%s", i, out)
		}
	}
}

// commitChange puts a second commit on a repository, so an installed clone
// has something to pull.
func commitChange(t *testing.T, repo string) {
	t.Helper()
	path := filepath.Join(repo, "hats", "demo", "views.yaml")
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(body, []byte("\n# changed\n")...), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"add", "-A"}, {"commit", "-m", "second"}} {
		if out, err := exec.Command("git", append([]string{"-C", repo}, args...)...).CombinedOutput(); err != nil {
			t.Fatal(err, string(out))
		}
	}
}

// An empty store is the answer "nothing", and it says what to do next.
func TestListOfAnEmptyStore(t *testing.T) {
	out, err := runCmd(t, "", "hats", "-d", t.TempDir(), "list")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"nothing installed", "hats search"} {
		if !strings.Contains(out, want) {
			t.Errorf("an empty store does not mention %q:\n%s", want, out)
		}
	}
}

// A pipe that cannot answer has not agreed to anything.
func TestConfirm(t *testing.T) {
	cases := []struct {
		in   string
		yes  bool
		flag bool
	}{
		{in: "y\n", yes: true},
		{in: "Y\n", yes: true},
		{in: "n\n", yes: false},
		{in: "\n", yes: false},
		{in: "", yes: false}, // closed stdin
		{in: "", yes: true, flag: true},
	}
	for _, tc := range cases {
		yesFlag = tc.flag
		cmd := &cobra.Command{}
		cmd.SetOut(&bytes.Buffer{})
		cmd.SetIn(strings.NewReader(tc.in))
		if got := confirm(cmd, "Go?"); got != tc.yes {
			t.Errorf("%q with --yes=%v gave %v", tc.in, tc.flag, got)
		}
	}
	yesFlag = false
}

// The store lives under the home directory, so spelling it out puts the
// interesting half off the edge of a narrow terminal.
func TestShortenPath(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no home directory")
	}
	if got := short(filepath.Join(home, ".config", "rincewind")); got != "~/.config/rincewind" {
		t.Errorf("got %q", got)
	}
	if got := short("/etc/passwd"); got != "/etc/passwd" {
		t.Errorf("a path outside home was rewritten to %q", got)
	}
}

// The whole resolution: the flag, then the environment, then nothing.
func TestHatResolution(t *testing.T) {
	t.Setenv("RINCEWIND_HAT", "/from/env")
	hatFlag = "/from/flag"
	t.Cleanup(func() { hatFlag = "" })

	if got, err := hat(); err != nil || got != "/from/flag" {
		t.Errorf("got %q, %v; want the flag to win", got, err)
	}

	hatFlag = ""
	if got, err := hat(); err != nil || got != "/from/env" {
		t.Errorf("got %q, %v; want the environment", got, err)
	}
}

// There is no fallback: ~/.config/rincewind is the store now, and a
// directory of hats is not a directory of views.
func TestNoHatSaysHowToChooseOne(t *testing.T) {
	t.Setenv("RINCEWIND_HAT", "")
	t.Setenv(hats.EnvDir, t.TempDir())
	hatFlag = ""

	_, err := hat()
	if err == nil {
		t.Fatal("a hat was found where none was chosen")
	}
	for _, want := range []string{"--hat <path>", "RINCEWIND_HAT", "hats install"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the error does not mention %q:\n%s", want, err)
		}
	}
}

// With something installed, the error is the line to paste.
func TestNoHatNamesWhatIsInstalled(t *testing.T) {
	store := t.TempDir()
	repo := repoWith(t, "albedaran")
	fromLocal(t, repo)
	if out, err := runCmd(t, "", "hats", "-d", store, "-y", "install", repo); err != nil {
		t.Fatal(err, out)
	}

	t.Setenv("RINCEWIND_HAT", "")
	t.Setenv(hats.EnvDir, store)
	hatFlag = ""

	_, err := hat()
	if err == nil {
		t.Fatal("a hat was found where none was chosen")
	}
	if !strings.Contains(err.Error(), "albedaran") {
		t.Errorf("the installed hat is not offered:\n%s", err)
	}
}
