// Package hats manages the store: a directory of git clones, each holding
// one or more hats. It knows how to fetch a repository, find the hats in it
// and say where they are — deciding which hat to *run* stays in cmd, because
// that is a path and this is a store.
package hats

import (
	"fmt"
	"path/filepath"
	"strings"
)

// Source is a repository to install: where to clone it from, and where it
// goes in the store.
type Source struct {
	// URL is handed to git unchanged — including a .git suffix, which some
	// forges want and none mind. A shorthand becomes an https URL; a full
	// one is passed through, so ssh reaches a private repository with
	// whatever credentials git already has.
	URL string
	// Slug is host/owner/repo, which is the path under repos/. Keeping the
	// host in it stops two forges from colliding and makes a clone's origin
	// readable from its path alone.
	Slug string
}

// ParseSource reads what the user typed. Three forms are accepted, and the
// full URL forms matter more than they look: an organisation's real cluster
// hats live in a private repository, which the topic search will never
// return and which ssh reaches for free.
//
//	vshn/rincewind
//	https://github.com/vshn/rincewind[.git]
//	git@github.com:vshn/private-hats[.git]
//	/path/to/a/repo  or  file:///path/to/a/repo
func ParseSource(s string) (Source, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return Source{}, fmt.Errorf("no repository given")
	}

	switch {
	case strings.HasPrefix(s, "http://"), strings.HasPrefix(s, "https://"):
		return fromURL(s)
	case strings.HasPrefix(s, "file://"), strings.HasPrefix(s, "/"):
		return fromLocal(s)
	case strings.Contains(s, "@") && strings.Contains(s, ":"):
		return fromSCP(s)
	default:
		return fromShorthand(s)
	}
}

// fromLocal handles a repository already on disk — how a hat's author
// installs their own before it is anywhere else. Only absolute paths, which
// is what keeps it apart from the owner/repo shorthand: that never starts
// with a slash.
//
// There is no host to build a slug from, so it gets "local". Two local
// repositories with the same basename collide, and the second install says
// so rather than overwriting the first.
func fromLocal(s string) (Source, error) {
	path := strings.TrimPrefix(s, "file://")
	name := strings.TrimSuffix(filepath.Base(strings.TrimRight(path, "/")), ".git")
	if name == "" || name == "." || name == string(filepath.Separator) {
		return Source{}, fmt.Errorf("%q does not name a repository directory", s)
	}
	return Source{URL: s, Slug: "local/" + name}, nil
}

// fromShorthand expands owner/repo. GitHub is assumed here and only here:
// the shorthand exists because that is where the topic search looks, and
// anything else is spelled out as a URL.
func fromShorthand(s string) (Source, error) {
	parts := strings.Split(strings.TrimSuffix(s, "/"), "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return Source{}, fmt.Errorf(
			"%q is not a repository: use owner/repo, an https or ssh URL, or an absolute path", s)
	}
	repo := strings.TrimSuffix(parts[1], ".git")
	return Source{
		URL:  "https://github.com/" + parts[0] + "/" + repo,
		Slug: "github.com/" + parts[0] + "/" + repo,
	}, nil
}

// fromURL handles https://host/owner/repo, with any number of path segments
// so that a self-hosted forge with a subgroup works.
func fromURL(s string) (Source, error) {
	rest := strings.TrimPrefix(strings.TrimPrefix(s, "https://"), "http://")

	// Credentials in the URL belong to git, not to the path on disk.
	if i := strings.Index(rest, "@"); i >= 0 {
		rest = rest[i+1:]
	}
	slug, err := slugOf(rest, s)
	if err != nil {
		return Source{}, err
	}
	return Source{URL: s, Slug: slug}, nil
}

// fromSCP handles git@host:owner/repo, which is not a URL at all — the colon
// separates host from path rather than naming a port.
func fromSCP(s string) (Source, error) {
	at := strings.Index(s, "@")
	colon := strings.Index(s[at:], ":")
	if colon < 0 {
		return Source{}, fmt.Errorf("%q is not an ssh repository: expected git@host:owner/repo", s)
	}
	colon += at

	slug, err := slugOf(s[at+1:colon]+"/"+s[colon+1:], s)
	if err != nil {
		return Source{}, err
	}
	return Source{URL: s, Slug: slug}, nil
}

// slugOf turns host/owner/repo into the path it gets in the store, refusing
// anything that would not name one repository — or that would put the clone
// somewhere other than under the store.
func slugOf(path, whole string) (string, error) {
	path = strings.Trim(path, "/")
	path = strings.TrimSuffix(path, ".git")

	parts := strings.Split(path, "/")
	if len(parts) < 3 {
		return "", fmt.Errorf("%q does not name a repository: expected host/owner/repo", whole)
	}
	for _, p := range parts {
		if p == "" || p == "." || p == ".." {
			return "", fmt.Errorf("%q has an empty or relative path segment", whole)
		}
	}
	return strings.Join(parts, "/"), nil
}
