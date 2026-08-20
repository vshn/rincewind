package hats

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// A hat with one view, enough for config.Load to accept it later.
const oneView = `
views:
  - id: demo.list
    source:
      script: echo '{"items":[{"name":"a"}]}'
    rows: .items[]
    columns:
      - name: NAME
        expr: .name
`

// upstream builds a real git repository to clone from, so the tests take the
// same path production does — the way the engine tests run real sh.
func upstream(t *testing.T, hats ...string) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not on PATH")
	}

	dir := t.TempDir()
	for _, name := range hats {
		sub := filepath.Join(dir, "hats", name)
		if err := os.MkdirAll(sub, 0o755); err != nil {
			t.Fatal(err)
		}
		write(t, filepath.Join(sub, "views.yaml"), oneView)
	}
	// A directory that is not a hat, to prove only hats/ is scanned.
	if err := os.MkdirAll(filepath.Join(dir, "docs"), 0o755); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(dir, "docs", "notes.yaml"), oneView)

	run(t, dir, "init", "--initial-branch=main")
	run(t, dir, "config", "user.email", "test@example.test")
	run(t, dir, "config", "user.name", "Test")
	commit(t, dir, "first")
	return dir
}

func write(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func run(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

func commit(t *testing.T, dir, msg string) {
	t.Helper()
	run(t, dir, "add", "-A")
	run(t, dir, "commit", "-m", msg)
}

func store(t *testing.T) Store { return Store{Dir: t.TempDir()} }

// The three forms, and the two properties each has to produce.
func TestParseSource(t *testing.T) {
	cases := []struct {
		in, url, slug string
	}{
		{"vshn/rincewind", "https://github.com/vshn/rincewind", "github.com/vshn/rincewind"},
		{"vshn/rincewind.git", "https://github.com/vshn/rincewind", "github.com/vshn/rincewind"},
		{"https://github.com/vshn/rincewind", "https://github.com/vshn/rincewind", "github.com/vshn/rincewind"},
		// The URL goes to git as typed; only the path on disk normalises.
		{"https://github.com/vshn/rincewind.git", "https://github.com/vshn/rincewind.git", "github.com/vshn/rincewind"},
		// A private repository over ssh is the case the shorthand cannot reach.
		{"git@github.com:vshn/private-hats.git", "git@github.com:vshn/private-hats.git", "github.com/vshn/private-hats"},
		// A self-hosted forge with a subgroup keeps every segment.
		{"https://git.vshn.net/team/infra/hats", "https://git.vshn.net/team/infra/hats", "git.vshn.net/team/infra/hats"},
	}
	for _, tc := range cases {
		got, err := ParseSource(tc.in)
		if err != nil {
			t.Errorf("%q: %v", tc.in, err)
			continue
		}
		if got.URL != tc.url {
			t.Errorf("%q: url %q, want %q", tc.in, got.URL, tc.url)
		}
		if got.Slug != tc.slug {
			t.Errorf("%q: slug %q, want %q", tc.in, got.Slug, tc.slug)
		}
	}
}

// A slug becomes a path, so anything that could climb out of the store has
// to die while it is still a string.
func TestParseSourceRefusals(t *testing.T) {
	for _, in := range []string{
		"", "rincewind", "vshn/", "/rincewind",
		"https://github.com/vshn", "https://github.com/../../etc",
	} {
		if got, err := ParseSource(in); err == nil {
			t.Errorf("%q was accepted as %+v", in, got)
		}
	}
}

// The convention, demonstrated: one directory per hat under hats/.
func TestCloneFindsTheHats(t *testing.T) {
	src := Source{URL: upstream(t, "albedaran", "demo"), Slug: "example.test/vshn/hats"}
	s := store(t)

	c, err := s.Clone(context.Background(), src)
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Hats) != 2 {
		t.Fatalf("found %d hats, want albedaran and demo: %+v", len(c.Hats), c.Hats)
	}
	if c.Hats[0].Name != "albedaran" || c.Hats[1].Name != "demo" {
		t.Errorf("hats are %v, want them sorted", c.Hats)
	}
	for _, h := range c.Hats {
		if h.Repo != src.Slug {
			t.Errorf("hat %s says it came from %q", h.Name, h.Repo)
		}
		if _, err := os.Stat(filepath.Join(h.Path, "views.yaml")); err != nil {
			t.Errorf("hat %s does not point at its yaml: %v", h.Name, err)
		}
	}
}

// A repository whose root is the hat, which is the one-hat case.
func TestCloneWithoutHatsDirectory(t *testing.T) {
	dir := t.TempDir()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not on PATH")
	}
	write(t, filepath.Join(dir, "views.yaml"), oneView)
	run(t, dir, "init", "--initial-branch=main")
	run(t, dir, "config", "user.email", "test@example.test")
	run(t, dir, "config", "user.name", "Test")
	commit(t, dir, "first")

	s := store(t)
	c, err := s.Clone(context.Background(), Source{URL: dir, Slug: "example.test/one/hat"})
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Hats) != 1 || c.Hats[0].Path != c.Path {
		t.Fatalf("got %+v, want the repository root as the one hat", c.Hats)
	}
}

// docs/ holds yaml and must not become a hat.
func TestOnlyHatsDirectoryIsScanned(t *testing.T) {
	src := Source{URL: upstream(t, "albedaran"), Slug: "example.test/vshn/hats"}
	c, err := store(t).Clone(context.Background(), src)
	if err != nil {
		t.Fatal(err)
	}
	for _, h := range c.Hats {
		if h.Name == "docs" || h.Name == "notes" {
			t.Errorf("%s was taken for a hat", h.Name)
		}
	}
}

// Installing twice is a mistake worth naming rather than a silent no-op.
func TestCloneRefusesToOverwrite(t *testing.T) {
	src := Source{URL: upstream(t, "demo"), Slug: "example.test/vshn/hats"}
	s := store(t)
	if _, err := s.Clone(context.Background(), src); err != nil {
		t.Fatal(err)
	}

	_, err := s.Clone(context.Background(), src)
	if err == nil {
		t.Fatal("the second install was accepted")
	}
	if !strings.Contains(err.Error(), "update") {
		t.Errorf("the error is %q, want it to point at update", err)
	}
}

// List walks to whatever depth the slug has, and Find takes the shorthand.
func TestListAndFind(t *testing.T) {
	s := store(t)
	for _, slug := range []string{"github.com/vshn/rincewind", "git.vshn.net/team/infra/hats"} {
		src := Source{URL: upstream(t, "demo"), Slug: slug}
		if _, err := s.Clone(context.Background(), src); err != nil {
			t.Fatal(err)
		}
	}

	clones, err := s.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(clones) != 2 {
		t.Fatalf("listed %d clones, want 2: %+v", len(clones), clones)
	}
	if clones[0].Slug != "git.vshn.net/team/infra/hats" {
		t.Errorf("the listing is not sorted: %+v", clones)
	}

	if c, err := s.Find("vshn/rincewind"); err != nil || c.Slug != "github.com/vshn/rincewind" {
		t.Errorf("Find by shorthand gave %+v, %v", c, err)
	}
	if _, err := s.Find("nope"); err == nil {
		t.Error("Find accepted something that is not installed")
	}
}

// An empty store is the answer "nothing", not an error: that is what a first
// run should hear.
func TestListOfAnEmptyStore(t *testing.T) {
	clones, err := Store{Dir: t.TempDir()}.List()
	if err != nil || clones != nil {
		t.Errorf("got %+v, %v; want nothing and no error", clones, err)
	}
}

// Update shows what it would bring before it brings it.
func TestIncomingThenPull(t *testing.T) {
	up := upstream(t, "demo")
	s := store(t)
	c, err := s.Clone(context.Background(), Source{URL: up, Slug: "example.test/vshn/hats"})
	if err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	if diff, err := Incoming(ctx, c.Path); err != nil || diff != "" {
		t.Fatalf("a fresh clone has %q, %v; want nothing incoming", diff, err)
	}

	write(t, filepath.Join(up, "hats", "demo", "views.yaml"), oneView+"\n# a change\n")
	commit(t, up, "second")

	diff, err := Incoming(ctx, c.Path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(diff, "views.yaml") {
		t.Errorf("the diffstat is %q, want it to name the changed file", diff)
	}

	before := Head(ctx, c.Path)
	if err := s.Pull(ctx, c.Path); err != nil {
		t.Fatal(err)
	}
	if Head(ctx, c.Path) == before {
		t.Error("the clone did not move")
	}
}

// A change someone made to a hat is theirs. Refusing is the point.
func TestPullRefusesOverLocalChanges(t *testing.T) {
	up := upstream(t, "demo")
	s := store(t)
	c, err := s.Clone(context.Background(), Source{URL: up, Slug: "example.test/vshn/hats"})
	if err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(c.Path, "hats", "demo", "views.yaml"), oneView+"\n# mine\n")

	err = s.Pull(context.Background(), c.Path)
	if err == nil {
		t.Fatal("the local change was overwritten")
	}
	for _, want := range []string{"local changes", "stash"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the error is %q, want it to mention %q", err, want)
		}
	}
}

// Removing takes the empty owner directory with it.
func TestRemoveCleansUpBehindItself(t *testing.T) {
	s := store(t)
	src := Source{URL: upstream(t, "demo"), Slug: "github.com/vshn/rincewind"}
	c, err := s.Clone(context.Background(), src)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Remove(c); err != nil {
		t.Fatal(err)
	}

	if _, err := os.Stat(c.Path); !os.IsNotExist(err) {
		t.Error("the clone is still there")
	}
	if _, err := os.Stat(filepath.Join(s.Dir, reposDir, "github.com", "vshn")); !os.IsNotExist(err) {
		t.Error("an empty owner directory was left behind")
	}
}

// The flag beats the environment beats the conventional place.
func TestDefaultDir(t *testing.T) {
	t.Setenv(EnvDir, "/from/env")
	if got := DefaultDir("/from/flag"); got != "/from/flag" {
		t.Errorf("got %q, want the flag to win", got)
	}
	if got := DefaultDir(""); got != "/from/env" {
		t.Errorf("got %q, want the environment", got)
	}

	t.Setenv(EnvDir, "")
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no home directory")
	}
	if got, want := DefaultDir(""), filepath.Join(home, ".config", "rincewind"); got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}
