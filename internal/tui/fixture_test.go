package tui

import (
	"context"
	"time"

	"github.com/vshn/aldediggi/internal/engine"
)

// row builds a fixture row whose key is its first cell, the way a config
// with `key: .name` would produce it.
func row(cells ...string) engine.Row {
	r := engine.Row{Key: cells[0]}
	for _, c := range cells {
		r.Cells = append(r.Cells, engine.Cell{Text: c})
	}
	return r
}

func nodesTable() engine.Table {
	return engine.Table{
		Cols: []string{"NAME", "STATUS", "AGE", "MESSAGE"},
		Rows: []engine.Row{
			row("worker-01", "Ready", "12d", "kubelet is posting ready status"),
			row("worker-02", "NotReady", "3h", "node is reporting a disk pressure condition"),
			row("infra-0", "Ready", "112d", "kubelet is posting ready status"),
			row("master-000000", "Ready", "1y", "kubelet is posting ready status"),
		},
	}
}

// sampleViews is the stand-in the layout and interaction tests run against.
// In-memory views rather than parsed config, so the tests need no shell; the
// engine's own tests cover the script path.
func sampleViews() []engine.View {
	return []engine.View{
		{
			ID: "demo.quick", Name: "quick", Title: "One quick row", Group: "demo",
			Run: func(context.Context) (engine.Table, error) {
				return engine.Table{
					Cols: []string{"N"},
					Rows: []engine.Row{row("123")},
				}, nil
			},
		},
		{
			ID: "demo.nodes", Name: "nodes", Title: "Nodes after a delay", Group: "demo",
			// The delay is what makes it possible to check that the UI keeps
			// painting and accepting keys while work is in flight.
			Run: func(ctx context.Context) (engine.Table, error) {
				select {
				case <-time.After(50 * time.Millisecond):
				case <-ctx.Done():
					return engine.Table{}, ctx.Err()
				}
				return nodesTable(), nil
			},
		},
	}
}
