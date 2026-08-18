package catalog

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// Demo returns the placeholder registry used to exercise the TUI before any
// real cluster work exists.
//
// Scaffolding: delete this file once the declarative views from
// docs/config.md can be loaded.
func Demo() *Registry {
	r := NewRegistry()
	r.Describe("demo", "Placeholder views for trying out the TUI")

	r.Add(View{
		Group: "demo",
		Name:  "text",
		Short: "Echo a line of text",
		Kind:  Snapshot,
		Run:   demoText,
	})
	r.Add(View{
		Group: "demo",
		Name:  "table",
		Short: "Render a table after a short delay",
		Kind:  Snapshot,
		Run:   demoTable,
	})

	return r
}

// demoText goes through exec even though it could return a constant, because
// the point is to exercise the subprocess path the real views will use.
// argv, never a shell string — see the invariants in CLAUDE.md.
func demoText(ctx context.Context) (Result, error) {
	var out bytes.Buffer
	cmd := exec.CommandContext(ctx, "echo", "123")
	cmd.Stdout = &out
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("echo: %w", err)
	}

	lines := strings.Split(strings.TrimRight(out.String(), "\n"), "\n")
	return Text{Lines: lines}, nil
}

// demoTable sleeps on purpose. Without a slow view there is no way to see
// whether the TUI keeps repainting and accepting keys while work is in
// flight, which is the main thing this skeleton exists to check.
func demoTable(ctx context.Context) (Result, error) {
	select {
	case <-time.After(800 * time.Millisecond):
	case <-ctx.Done():
		return nil, ctx.Err()
	}

	// Widths vary so that column alignment is actually visible.
	return Table{
		Cols: []string{"NAME", "STATUS", "AGE", "MESSAGE"},
		Rows: [][]string{
			{"worker-01", "Ready", "12d", "kubelet is posting ready status"},
			{"worker-02", "NotReady", "3h", "node is reporting a disk pressure condition"},
			{"infra-0", "Ready", "112d", "kubelet is posting ready status"},
			{"master-000000", "Ready", "1y", "kubelet is posting ready status"},
		},
	}, nil
}
