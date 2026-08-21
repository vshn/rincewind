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
	return gitWith(ctx, dir, nil, args...)
}

// gitWith is git with extra environment, which is how the ssh attempt gets
// its own ssh command without every other git call carrying one.
func gitWith(ctx context.Context, dir string, env []string, args ...string) (string, error) {
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
	cmd.Env = append(cmd.Env, env...)

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

// CloneURLs is the order a source's transports are tried in, and the one
// place that decides it: ssh first when ssh can work at all, https after.
// Exported because `hats install` prints the plan before asking, and a plan
// that came from a second opinion could differ from what then runs.
//
// ssh is left out entirely when there is no ssh to run — a capability probe,
// not a guess from the platform, the same way the clipboard writer is found.
func CloneURLs(ctx context.Context, src Source) []string {
	var urls []string
	if src.SSH != "" {
		if _, ok := sshCommand(ctx); ok {
			urls = append(urls, src.SSH)
		}
	}
	if src.URL != "" {
		urls = append(urls, src.URL)
	}
	return urls
}

// sshCommand is the ssh git should use for a clone, and whether ssh is a
// transport this machine has at all.
//
// BatchMode is the point of building it here. GIT_TERMINAL_PROMPT=0 silences
// git, but not ssh — which has prompts of its own, for an unknown host key
// and for a key's passphrase, and reads them straight from the terminal. An
// install that stops on one of those is the hang that variable exists to
// prevent, and for a fallback it is worse still: a transport that cannot
// work has to fail rather than wait, so the next one gets its turn.
//
// Whatever the user already configured is extended rather than replaced, in
// git's own order of precedence — the environment, then core.sshCommand —
// because a custom ssh command is usually the identity that makes ssh work
// at all, and overriding it would defeat the preference it expresses.
func sshCommand(ctx context.Context) (string, bool) {
	base := os.Getenv("GIT_SSH_COMMAND")
	if base == "" {
		// Unset is an exit status, not a failure worth reporting.
		base, _ = git(ctx, "", "config", "--get", "core.sshCommand")
	}
	if base == "" {
		if _, err := exec.LookPath("ssh"); err != nil {
			return "", false
		}
		base = "ssh"
	}
	return base + " -o BatchMode=yes", true
}

// Clone fetches a repository into the store, over ssh where that works and
// https where it does not. Not shallow: hats are YAML and the clone is tiny,
// where a shallow one breaks `pull --ff-only` after a force-push and makes
// `git diff` against history impossible — which is the reason git was chosen
// over a tarball.
//
// Only the clone chooses. Afterwards the URL that worked is the clone's
// origin, so update and every other git command keep using it without this
// tool having to remember which one won.
func (s Store) Clone(ctx context.Context, src Source) (Clone, error) {
	dir := s.Path(src)
	if _, err := os.Stat(dir); err == nil {
		return Clone{}, fmt.Errorf("%s is already installed at %s\n\nuse `rincewind hats update` to bring it up to date", src.Slug, dir)
	}
	if err := os.MkdirAll(filepath.Dir(dir), 0o755); err != nil {
		return Clone{}, err
	}

	urls := CloneURLs(ctx, src)
	if len(urls) == 0 {
		return Clone{}, fmt.Errorf("%s has no url to clone from", src.Slug)
	}

	var failed []string
	for _, url := range urls {
		var env []string
		if isSSH(url) {
			if cmd, ok := sshCommand(ctx); ok {
				env = []string{"GIT_SSH_COMMAND=" + cmd}
			}
		}

		_, err := gitWith(ctx, "", env, "clone", "--", url, dir)
		if err == nil {
			return Clone{Slug: src.Slug, Path: dir, Hats: hatsIn(dir, src.Slug)}, nil
		}
		// A half-written directory would look installed on the next run —
		// and would stop the next transport from cloning into it.
		_ = os.RemoveAll(dir)
		failed = append(failed, fmt.Sprintf("  %s\n    %v", url, err))

		// Being told to stop is not a transport that did not work.
		if ctx.Err() != nil {
			break
		}
	}

	// One url gets its error as it stands; two get both, because "it failed"
	// without saying which half failed how is the report that sends someone
	// looking in the wrong place.
	if len(failed) == 1 {
		return Clone{}, fmt.Errorf("cloning %s failed:\n\n%s", src.Slug, failed[0])
	}
	return Clone{}, fmt.Errorf("cloning %s failed over ssh, and over https:\n\n%s",
		src.Slug, strings.Join(failed, "\n\n"))
}

// isSSH reports whether a url is one ssh will carry, in either spelling.
// http(s) is decided by the scheme alone: `https://user@host/a:b` carries
// both marks of the scp form without being it.
func isSSH(url string) bool {
	if strings.HasPrefix(url, "http://") || strings.HasPrefix(url, "https://") {
		return false
	}
	return strings.HasPrefix(url, "ssh://") || (strings.Contains(url, "@") && strings.Contains(url, ":"))
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
