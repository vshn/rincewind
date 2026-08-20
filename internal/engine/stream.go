package engine

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// maxLine is how long a single line may be before the reader gives up on it.
// bufio's own default is 64 KiB, which a JSON-logging controller can exceed
// on one line; a megabyte is past anything worth reading in a terminal.
const maxLine = 1 << 20

// Chunk is what a stream delivers: lines as they arrive, and — as the last
// thing before the channel closes — how it ended. Err is nil when the script
// simply exited, which for a followed log means the thing being followed went
// away. That is a finding, not a malfunction, so it travels the same way.
type Chunk struct {
	Lines []string
	Err   error
	End   bool
}

// stream starts the script and delivers its output as it arrives, rather than
// when it exits. The channel closes once the script is done and the last
// chunk has been sent; cancelling ctx kills the script, which is what closes
// it early.
func stream(ctx context.Context, shell []string, script string, env Args) (<-chan Chunk, error) {
	argv := append(append([]string{}, shell...), script)

	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	if len(env) > 0 {
		cmd.Env = os.Environ()
		for name, value := range env {
			cmd.Env = append(cmd.Env, name+"="+value)
		}
	}

	// stderr is collected rather than interleaved: a script that complains
	// once at startup would otherwise have its complaint scroll away among
	// the lines it is producing.
	var stderr strings.Builder
	cmd.Stderr = &stderr

	out, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}

	ch := make(chan Chunk)
	go func() {
		defer close(ch)

		sc := bufio.NewScanner(out)
		sc.Buffer(make([]byte, 0, 64*1024), maxLine)
		for sc.Scan() {
			select {
			case ch <- Chunk{Lines: []string{sc.Text()}}:
			case <-ctx.Done():
				// Nobody is listening any more; the Wait below still runs,
				// so the process is reaped rather than left behind.
				_ = cmd.Wait()
				return
			}
		}

		// A read that failed — a line past maxLine — leaves the script writing
		// into a pipe nothing will drain again, so Wait would never return.
		// Kill it instead; endReason reports the read failure, not the signal.
		if sc.Err() != nil {
			_ = cmd.Process.Kill()
		}

		// Wait after the pipe is drained, never before: it closes the pipe.
		werr := cmd.Wait()

		// The last chunk goes the same way as the others. A reader that has
		// already left — a paused tab, a closed one — must not leave this
		// goroutine parked on a send nobody will ever take.
		select {
		case ch <- Chunk{End: true, Err: endReason(ctx, werr, sc.Err(), stderr.String())}:
		case <-ctx.Done():
		}
	}()

	return ch, nil
}

// endReason says why a stream stopped, or nil when it simply ended. Being
// cancelled is the user closing the tab, which is not worth reporting.
func endReason(ctx context.Context, wait, scan error, stderr string) error {
	if ctx.Err() != nil {
		return nil
	}
	if scan != nil {
		return fmt.Errorf("reading output: %w", scan)
	}
	if wait != nil {
		if msg := strings.TrimSpace(stderr); msg != "" {
			return fmt.Errorf("%v: %s", wait, msg)
		}
		return wait
	}
	return nil
}

// runStream is a followed view: the same script and environment as a snapshot,
// consumed as it is written.
func (cv *compiled) runStream(ctx context.Context, args Args) (<-chan Chunk, error) {
	return stream(ctx, cv.shell, cv.def.Script, cv.env(args))
}
