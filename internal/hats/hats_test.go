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

// The two spellings of the same GitHub repository, and the flat directory
// name each has to produce.
func TestParseSource(t *testing.T) {
	cases := []struct {
		in, url, slug string
	}{
		{"vshn/rincewind-hats", "https://github.com/vshn/rincewind-hats", "vshn-rincewind-hats"},
		{"vshn/rincewind-hats.git", "https://github.com/vshn/rincewind-hats", "vshn-rincewind-hats"},
		{"https://github.com/vshn/rincewind-hats", "https://github.com/vshn/rincewind-hats", "vshn-rincewind-hats"},
		{"https://github.com/vshn/rincewind-hats.git", "https://github.com/vshn/rincewind-hats", "vshn-rincewind-hats"},
		{"github.com/vshn/rincewind-hats", "https://github.com/vshn/rincewind-hats", "vshn-rincewind-hats"},
		{"  vshn/rincewind-hats  ", "https://github.com/vshn/rincewind-hats", "vshn-rincewind-hats"},
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

// GitHub only, and a slug becomes a directory name — so anything naming
// somewhere else, or naming nothing, dies while it is still a string.
func TestParseSourceRefusals(t *testing.T) {
	for _, in := range []string{
		"", "rincewind", "vshn/", "/rincewind", "vshn/hats/extra",
		"https://gitlab.com/vshn/hats",
		"git@github.com:vshn/hats.git",
		"/srv/git/local", "file:///srv/git/local",
		"https://github.com/../../etc", "vshn/..",
	} {
		if got, err := ParseSource(in); err == nil {
			t.Errorf("%q was accepted as %+v", in, got)
		}
	}
}

// The convention, demonstrated: one directory per hat under hats/.
func TestCloneFindsTheHats(t *testing.T) {
	src := Source{URL: upstream(t, "albedaran", "demo"), Slug: "vshn-hats"}
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

// A repository that is nothing but hats keeps them at its root, where a
// hats/ level would only repeat the repository's own name — which is how
// vshn/rincewind-hats is laid out.
func TestHatsAtTheRepositoryRoot(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not on PATH")
	}
	dir := t.TempDir()
	for _, name := range []string{"albedaran", "spare"} {
		if err := os.MkdirAll(filepath.Join(dir, name), 0o755); err != nil {
			t.Fatal(err)
		}
		write(t, filepath.Join(dir, name, "views.yaml"), oneView)
	}
	// A dot directory holds yaml too, and is never a hat.
	if err := os.MkdirAll(filepath.Join(dir, ".github"), 0o755); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(dir, ".github", "ci.yaml"), oneView)
	write(t, filepath.Join(dir, "README.md"), "# hats\n")

	run(t, dir, "init", "--initial-branch=main")
	run(t, dir, "config", "user.email", "test@example.test")
	run(t, dir, "config", "user.name", "Test")
	commit(t, dir, "first")

	c, err := store(t).Clone(context.Background(), Source{URL: dir, Slug: "vshn-rincewind-hats"})
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Hats) != 2 {
		t.Fatalf("found %+v, want albedaran and spare", c.Hats)
	}
	for _, h := range c.Hats {
		if strings.HasPrefix(h.Name, ".") {
			t.Errorf("%s was taken for a hat", h.Name)
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
	c, err := s.Clone(context.Background(), Source{URL: dir, Slug: "one-hat"})
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Hats) != 1 || c.Hats[0].Path != c.Path {
		t.Fatalf("got %+v, want the repository root as the one hat", c.Hats)
	}
}

// docs/ holds yaml and must not become a hat.
func TestOnlyHatsDirectoryIsScanned(t *testing.T) {
	src := Source{URL: upstream(t, "albedaran"), Slug: "vshn-hats"}
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
	src := Source{URL: upstream(t, "demo"), Slug: "vshn-hats"}
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

// The store is flat, and Find takes any of the three spellings.
func TestListAndFind(t *testing.T) {
	s := store(t)
	for _, slug := range []string{"vshn-rincewind-hats", "acme-cluster-hats"} {
		if _, err := s.Clone(context.Background(), Source{URL: upstream(t, "demo"), Slug: slug}); err != nil {
			t.Fatal(err)
		}
	}
	// Something in the store that is not a clone; a config directory holds
	// other things and they are simply not repositories.
	if err := os.MkdirAll(filepath.Join(s.Dir, "notes"), 0o755); err != nil {
		t.Fatal(err)
	}

	clones, err := s.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(clones) != 2 {
		t.Fatalf("listed %d clones, want 2: %+v", len(clones), clones)
	}
	if clones[0].Slug != "acme-cluster-hats" {
		t.Errorf("the listing is not sorted: %+v", clones)
	}

	for _, name := range []string{"vshn-rincewind-hats", "vshn/rincewind-hats", "rincewind-hats"} {
		if c, err := s.Find(name); err != nil || c.Slug != "vshn-rincewind-hats" {
			t.Errorf("Find(%q) gave %+v, %v", name, c, err)
		}
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
	c, err := s.Clone(context.Background(), Source{URL: up, Slug: "vshn-hats"})
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
	c, err := s.Clone(context.Background(), Source{URL: up, Slug: "vshn-hats"})
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
	left, err := s.List()
	if err != nil || len(left) != 0 {
		t.Errorf("the store still lists %+v", left)
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
