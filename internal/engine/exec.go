package engine

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"
)

// runScript executes shell + script and returns its stdout. stderr is kept
// apart and becomes the error text on failure: the script's own message is
// the useful one, the exit status is not.
func runScript(ctx context.Context, shell []string, script string) ([]byte, error) {
	argv := append(append([]string{}, shell...), script)

	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr

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
