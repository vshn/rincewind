package hats

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// git is the second exception to "no shelling out", after the clipboard, and
// for the same shape of reason: the Go replacement — go-git — is a large
// dependency to carry for four commands, and what makes a clone worth having
// over a tarball is exactly that git is there afterwards. `git -C <path>`
// keeps working on the store whatever this tool does.
//
// Probed on PATH rather than assumed, like the clipboard writer.
func git(ctx context.Context, dir string, args ...string) (string, error) {
	if _, err := exec.LookPath("git"); err != nil {
		return "", fmt.Errorf("git is not on PATH, and hats are installed by cloning")
	}

	argv := args
	if dir != "" {
		argv = append([]string{"-C", dir}, args...)
	}
	cmd := exec.CommandContext(ctx, "git", argv...)

	// A prompt would hang a command nobody is watching for input. Failing
	// with "authentication failed" is the better answer than a stuck clone.
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")

	var stderr strings.Builder
	cmd.Stderr = &stderr

	out, err := cmd.Output()
	if err != nil {
		if msg := strings.TrimSpace(stderr.String()); msg != "" {
			return "", fmt.Errorf("git %s: %v: %s", strings.Join(args, " "), err, msg)
		}
		return "", fmt.Errorf("git %s: %v", strings.Join(args, " "), err)
	}
	return strings.TrimSpace(string(out)), nil
}

// Clone fetches a repository into the store. Not shallow: hats are YAML and
// the clone is tiny, where a shallow one breaks `pull --ff-only` after a
// force-push and makes `git diff` against history impossible — which is the
// reason git was chosen over a tarball.
func (s Store) Clone(ctx context.Context, src Source) (Clone, error) {
	dir := s.Path(src)
	if _, err := os.Stat(dir); err == nil {
		return Clone{}, fmt.Errorf("%s is already installed at %s\n\nuse `rincewind hats update` to bring it up to date", src.Slug, dir)
	}
	if err := os.MkdirAll(filepath.Dir(dir), 0o755); err != nil {
		return Clone{}, err
	}

	if _, err := git(ctx, "", "clone", "--", src.URL, dir); err != nil {
		// A half-written directory would look installed on the next run.
		_ = os.RemoveAll(dir)
		return Clone{}, err
	}
	return Clone{Slug: src.Slug, Path: dir, Hats: hatsIn(dir, src.Slug)}, nil
}

// Head is the commit a clone is on, short form, for saying what you have.
func Head(ctx context.Context, dir string) string {
	out, err := git(ctx, dir, "rev-parse", "--short", "HEAD")
	if err != nil {
		return "?"
	}
	return out
}

// Incoming fetches and reports what an update would bring, as a diffstat
// against what is checked out. Nothing is moved: this is the "show the
// command before it runs" rule one level up, because the code being updated
// is code that runs against a cluster.
//
// An empty string means there is nothing to update.
func Incoming(ctx context.Context, dir string) (string, error) {
	if _, err := git(ctx, dir, "fetch", "--quiet"); err != nil {
		return "", err
	}
	upstream, err := git(ctx, dir, "rev-parse", "--abbrev-ref", "@{upstream}")
	if err != nil {
		return "", fmt.Errorf("%s has no upstream branch to update from", dir)
	}
	return git(ctx, dir, "diff", "--stat", "HEAD.."+upstream)
}

// Pull moves a clone to its upstream, fast-forward only. Refusing on local
// modifications is the point rather than a limitation: a change someone made
// to a hat is theirs, and clobbering it silently is not an update.
func (s Store) Pull(ctx context.Context, dir string) error {
	if dirty, err := git(ctx, dir, "status", "--porcelain"); err == nil && dirty != "" {
		return fmt.Errorf(
			"%s has local changes, so it is not updated\n\n%s\n\nkeep them with `git -C %s stash`, or discard them with `git -C %s checkout .`",
			dir, dirty, dir, dir)
	}
	_, err := git(ctx, dir, "pull", "--ff-only", "--quiet")
	return err
}

// Remove deletes a clone. Nothing to tidy above it: the store is flat.
func (s Store) Remove(c Clone) error {
	return os.RemoveAll(c.Path)
}
