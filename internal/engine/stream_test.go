package engine

import (
	"context"
	"strings"
	"testing"
	"time"
)

// followed compiles a followed text view and starts it.
func followed(t *testing.T, ctx context.Context, script string, args Args) <-chan Chunk {
	t.Helper()
	v, err := Compile(Def{ID: "t", Text: true, Follow: true, Script: script})
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	if !v.Follows() {
		t.Fatal("a followed view has no Stream")
	}
	if v.Run != nil {
		t.Error("a followed view kept a Run as well, so which one is used is a coin toss")
	}
	ch, err := v.Stream(ctx, args)
	if err != nil {
		t.Fatalf("stream: %v", err)
	}
	return ch
}

// next takes one chunk, failing rather than hanging when none comes.
func next(t *testing.T, ch <-chan Chunk) Chunk {
	t.Helper()
	select {
	case c, ok := <-ch:
		if !ok {
			t.Fatal("the channel closed before the chunk arrived")
		}
		return c
	case <-time.After(10 * time.Second):
		t.Fatal("no chunk within ten seconds")
		return Chunk{}
	}
}

// The whole point: a line is delivered while the script is still running,
// not held back until it exits.
func TestStreamDeliversBeforeTheScriptExits(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// The sleep outlives the assertion, so a chunk arriving proves the line
	// was not waiting for the exit.
	ch := followed(t, ctx, "echo first; sleep 30", nil)

	if c := next(t, ch); len(c.Lines) != 1 || c.Lines[0] != "first" {
		t.Fatalf("first chunk is %#v, want the one line", c)
	}
}

// Every line, in order, then the end.
func TestStreamEndsAfterTheLastLine(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ch := followed(t, ctx, "printf 'a\\nb\\nc\\n'", nil)

	var got []string
	for c := range ch {
		if c.End {
			if c.Err != nil {
				t.Errorf("a script that simply finished reported %v", c.Err)
			}
			break
		}
		got = append(got, c.Lines...)
	}
	if strings.Join(got, ",") != "a,b,c" {
		t.Errorf("read %q, want a,b,c", got)
	}
}

// Cancelling is how following is stopped, and stopping on purpose is not a
// failure to report.
func TestCancellingEndsWithoutAnError(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	ch := followed(t, ctx, "echo first; sleep 30", nil)
	next(t, ch) // the line, so the script is certainly running

	cancel()
	for c := range ch {
		if c.End && c.Err != nil {
			t.Errorf("cancelling reported %v, want no error", c.Err)
		}
	}
}

// A script that fails says why, and stderr is where it says it.
func TestStreamFailureCarriesStderr(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ch := followed(t, ctx, "echo a line; echo 'no such pod' >&2; exit 1", nil)

	var last Chunk
	for c := range ch {
		last = c
	}
	if last.Err == nil {
		t.Fatal("a script that exited 1 ended without an error")
	}
	if !strings.Contains(last.Err.Error(), "no such pod") {
		t.Errorf("the error is %v, want it to carry what stderr said", last.Err)
	}
}

// Parameters reach a followed script the same way they reach a snapshot: as
// environment variables, never templated into the text.
func TestStreamPassesParametersAsEnvironment(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	v, err := Compile(Def{
		ID: "t", Text: true, Follow: true,
		Params: []Param{{Name: "service"}, {Name: "level", Default: "info"}},
		Script: `echo "$service/$level"`,
	})
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	ch, err := v.Stream(ctx, Args{"service": "worker"})
	if err != nil {
		t.Fatalf("stream: %v", err)
	}

	if c := next(t, ch); c.Lines[0] != "worker/info" {
		t.Errorf("the script saw %q, want the parameter and the default", c.Lines[0])
	}
}

// A line past the buffer is a reading failure, not a silent truncation:
// half a JSON log line would read as a line the pod actually wrote.
func TestOverlongLineIsReported(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// awk rather than a shell loop, which would take a while to build a
	// megabyte one character at a time.
	ch := followed(t, ctx, `awk 'BEGIN { s = "x"; while (length(s) < 2000000) s = s s; print s }'`, nil)

	var last Chunk
	for c := range ch {
		last = c
	}
	if last.Err == nil || !strings.Contains(last.Err.Error(), "reading output") {
		t.Errorf("an overlong line ended with %v, want it named as a read failure", last.Err)
	}
}
