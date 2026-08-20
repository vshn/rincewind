package hats

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// EnvDir names the store's location. Deliberately not RINCEWIND_HATS, which
// is one letter from RINCEWIND_HAT and means something entirely different:
// that one picks the hat to run, this one says where clones land.
const EnvDir = "RINCEWIND_HATS_DIR"

// reposDir is the one subdirectory of the store. Everything git owns lives
// under it, so the store can grow another kind of thing later without the
// two becoming hard to tell apart.
const reposDir = "repos"

// Store is a directory of clones.
type Store struct{ Dir string }

// DefaultDir is where the store lives when nothing says otherwise: the flag,
// then the environment, then the conventional place — the same order the hat
// itself is resolved by, so there is one habit to learn rather than two.
func DefaultDir(flag string) string {
	if flag != "" {
		return flag
	}
	if env := os.Getenv(EnvDir); env != "" {
		return env
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "."
	}
	return filepath.Join(home, ".config", "rincewind")
}

// Path is where a repository's clone goes.
func (s Store) Path(src Source) string {
	return filepath.Join(s.Dir, reposDir, filepath.FromSlash(src.Slug))
}

// Hat is one hat found in the store: what to call it, and the path that goes
// into RINCEWIND_HAT. Nothing resolves a name to a path at run time, so the
// path is the useful half and the name is only a label.
type Hat struct {
	Name string
	Path string
	Repo string // the clone it came from, as host/owner/repo
}

// Clone is one installed repository.
type Clone struct {
	Slug string
	Path string
	Hats []Hat
}

// List reports what is installed, in a stable order. A store that does not
// exist yet is not an error: it is the answer "nothing", which is what a
// first run should hear.
func (s Store) List() ([]Clone, error) {
	root := filepath.Join(s.Dir, reposDir)
	if _, err := os.Stat(root); os.IsNotExist(err) {
		return nil, nil
	}

	var out []Clone
	// A clone is any directory holding .git, at whatever depth: the slug has
	// three segments for GitHub and more for a forge with subgroups, so the
	// marker is what says "stop here", not the depth.
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil || !d.IsDir() || path == root {
			return err
		}
		if _, statErr := os.Stat(filepath.Join(path, ".git")); statErr != nil {
			return nil
		}
		slug := filepath.ToSlash(strings.TrimPrefix(path, root+string(filepath.Separator)))
		out = append(out, Clone{Slug: slug, Path: path, Hats: hatsIn(path, slug)})
		return filepath.SkipDir
	})
	if err != nil {
		return nil, err
	}

	sort.Slice(out, func(i, j int) bool { return out[i].Slug < out[j].Slug })
	return out, nil
}

// Find locates one installed clone by slug, or by the owner/repo shorthand
// that installed it.
func (s Store) Find(name string) (Clone, error) {
	clones, err := s.List()
	if err != nil {
		return Clone{}, err
	}
	var hits []Clone
	for _, c := range clones {
		if c.Slug == name || strings.HasSuffix(c.Slug, "/"+name) {
			hits = append(hits, c)
		}
	}
	switch len(hits) {
	case 1:
		return hits[0], nil
	case 0:
		return Clone{}, fmt.Errorf("%q is not installed\n\ninstalled:\n%s", name, listing(clones))
	default:
		return Clone{}, fmt.Errorf("%q matches several:\n%s\n\nname one in full", name, listing(hits))
	}
}

func listing(clones []Clone) string {
	if len(clones) == 0 {
		return "  (nothing)"
	}
	var out []string
	for _, c := range clones {
		out = append(out, "  "+c.Slug)
	}
	return strings.Join(out, "\n")
}

// hatsIn finds the hats in a clone. The convention is what this repository
// already does — one directory per hat under hats/ — with a repository whose
// root is itself a hat as the single-hat case. Nothing else is scanned, so
// docs/ and .github/ cannot become hats by accident.
func hatsIn(clone, slug string) []Hat {
	var out []Hat

	if dirs, err := os.ReadDir(filepath.Join(clone, "hats")); err == nil {
		for _, d := range dirs {
			if !d.IsDir() {
				continue
			}
			path := filepath.Join(clone, "hats", d.Name())
			if hasYAML(path) {
				out = append(out, Hat{Name: d.Name(), Path: path, Repo: slug})
			}
		}
	}
	if len(out) == 0 && hasYAML(clone) {
		out = append(out, Hat{Name: filepath.Base(clone), Path: clone, Repo: slug})
	}

	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func hasYAML(dir string) bool {
	files, err := filepath.Glob(filepath.Join(dir, "*.yaml"))
	return err == nil && len(files) > 0
}
