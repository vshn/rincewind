// Package hats manages the store: a flat directory of git clones, each
// holding one or more hats. It knows how to fetch a repository, find the hats
// in it and say where they are — deciding which hat to *run* stays in cmd,
// because that is a path and this is a store.
package hats

import (
	"fmt"
	"strings"
)

// Source is a repository to install: where to clone it from, and what its
// directory in the store is called.
type Source struct {
	URL string
	// Slug is owner-repo, one flat directory name. Flat rather than nested:
	// the path is what goes into RINCEWIND_HAT, and a person has to be able
	// to read it back out of their own shell history.
	Slug string
}

// ParseSource reads what the user typed. GitHub only — the shorthand and the
// URL one copies out of the browser are the same repository, spelled two
// ways:
//
//	vshn/rincewind-hats
//	https://github.com/vshn/rincewind-hats[.git]
//
// Naming GitHub in Go is the same exception the cluster probe makes for
// kubectl: a hat cannot say where hats are found, and the topic search only
// exists there.
func ParseSource(s string) (Source, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return Source{}, fmt.Errorf("no repository given")
	}

	rest := s
	for _, prefix := range []string{"https://github.com/", "http://github.com/", "github.com/"} {
		if after, found := strings.CutPrefix(rest, prefix); found {
			rest = after
			break
		}
	}
	// Anything still carrying a scheme or credentials names somewhere else.
	if strings.Contains(rest, "://") || strings.Contains(rest, "@") {
		return Source{}, notGitHub(s)
	}

	owner, repo, ok := strings.Cut(strings.Trim(rest, "/"), "/")
	repo = strings.TrimSuffix(repo, ".git")
	if !ok || owner == "" || repo == "" || strings.Contains(repo, "/") {
		return Source{}, notGitHub(s)
	}
	// A segment that is not a name would put the clone somewhere else.
	for _, p := range []string{owner, repo} {
		if p == "." || p == ".." || strings.ContainsAny(p, `\:`) {
			return Source{}, fmt.Errorf("%q has a path segment that is not a name", s)
		}
	}

	return Source{
		URL:  "https://github.com/" + owner + "/" + repo,
		Slug: owner + "-" + repo,
	}, nil
}

func notGitHub(s string) error {
	return fmt.Errorf("%q is not a GitHub repository: use owner/repo, or a github.com URL", s)
}
