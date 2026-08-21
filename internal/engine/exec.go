package engine

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// runScript executes shell + script and returns its stdout. stderr is kept
// apart and becomes the error text on failure: the script's own message is
// the useful one, the exit status is not.
//
// env reaches the script as environment variables and stdin as its input.
// Neither is ever templated into the script text, so a value taken from
// cluster data or from the user cannot become code.
func runScript(ctx context.Context, shell []string, script string, env Args, stdin []byte) ([]byte, error) {
	argv := append(append([]string{}, shell...), script)

	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	// The script is `sh -c …`, and sh forks the members of a pipeline rather
	// than exec-ing them. Killing sh on cancel orphans those children with
	// the pipes still open, and Wait would sit on the pipe copies for as long
	// as the orphans live. WaitDelay bounds that: after the exit or the
	// cancel, the pipes are forced closed — which also hands any orphan
	// still writing an EPIPE instead of a silent forever.
	cmd.WaitDelay = pipeGrace
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr

	if len(env) > 0 {
		cmd.Env = os.Environ()
		for name, value := range env {
			cmd.Env = append(cmd.Env, name+"="+value)
		}
	}
	if stdin != nil {
		cmd.Stdin = bytes.NewReader(stdin)
	}

	err := cmd.Run()
	// The context ending is the caller's decision or deadline, not the
	// script's fault; report it as such so cancel and timeout keep their
	// meaning upstream.
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if err != nil {
		if msg := strings.TrimSpace(stderr.String()); msg != "" {
			return nil, fmt.Errorf("%v: %s", err, msg)
		}
		return nil, fmt.Errorf("script: %w", err)
	}
	return stdout.Bytes(), nil
}
