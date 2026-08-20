package tui

import (
	"context"
	"fmt"
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

// longTable is taller and wider than any test window, which is what the
// scrolling tests need.
func longTable() engine.Table {
	t := engine.Table{Cols: []string{"NAME", "STATUS", "MESSAGE"}}
	for i := 1; i <= 40; i++ {
		t.Rows = append(t.Rows, row(
			fmt.Sprintf("worker-%02d", i), "Ready",
			"kubelet is posting ready status and some more text to make this wide"))
	}
	return t
}

// manyViews fills the sidebar past the bottom of its box.
func manyViews() []engine.View {
	var out []engine.View
	for i := 1; i <= 30; i++ {
		name := fmt.Sprintf("view%02d", i)
		out = append(out, engine.View{
			ID: "demo." + name, Name: name, Title: name, Group: "demo",
			Run: func(context.Context, engine.Args) (engine.Table, error) { return longTable(), nil },
		})
	}
	return out
}

// poolRow is a fixture row whose Src carries the object an action derives
// its arguments from, the way a real view's rows do.
func poolRow(name, state string) engine.Row {
	return engine.Row{
		Key:   name,
		Cells: []engine.Cell{{Text: name}, {Text: state}},
		Src:   map[string]any{"name": name, "state": state},
	}
}

func poolTable() engine.Table {
	return engine.Table{
		Cols: []string{"NAME", "STATE"},
		Rows: []engine.Row{poolRow("infra", "Degraded"), poolRow("worker", "Updated")},
	}
}

// planFor records the rows an action was asked to work on and turns each
// into a step that echoes word — enough to see the command on screen and
// the output under it, without needing a cluster.
func planFor(rec *record, word string) func([]any, engine.Args) ([]engine.Step, error) {
	return func(rows []any, cur engine.Args) ([]engine.Step, error) {
		rec.planned = append(rec.planned, rows)
		steps := make([]engine.Step, 0, len(rows))
		for _, r := range rows {
			name, _ := r.(map[string]any)["name"].(string)
			steps = append(steps, engine.Step{
				Env:    engine.Args{"name": name},
				Shell:  []string{"sh", "-c"},
				Script: `echo "` + word + ` $name"`,
			})
		}
		return steps, nil
	}
}

// drillViews is a parent offering both kinds of action and the target they
// reach. The target records what it was handed, which is how the tests check
// that arguments survive the trip.
type record struct {
	// args is what the drill target was opened with, one entry per run.
	args []engine.Args
	// planned is what a working action was asked about. Planning happens
	// when the job tab opens — before the confirmation — so this says
	// nothing about whether anything ran; the job tab's own state does.
	planned [][]any
	// parent counts runs of the view the actions hang off, which is how a
	// reload proves itself.
	parent int
	// planArgs is what a parameterised action was planned with, once its
	// form was filled in.
	planArgs []engine.Args
}

func drillViews(rec *record) []engine.View {
	target := engine.View{
		ID: "demo.nodes", Name: "nodes", Title: "Nodes", Group: "demo",
		Params: []engine.Param{{Name: "selector", Required: true}},
		Run: func(_ context.Context, args engine.Args) (engine.Table, error) {
			rec.args = append(rec.args, args)
			return longTable(), nil
		},
	}

	parent := engine.View{
		ID: "demo.pools", Name: "pools", Title: "Pools", Group: "demo",
		Run: func(context.Context, engine.Args) (engine.Table, error) {
			rec.parent++
			return poolTable(), nil
		},
		Actions: []engine.Action{
			{
				Key: "enter", Name: "nodes", Short: "Show the nodes",
				Open: &target,
				Args: func(src any, _ engine.Args) (engine.Args, error) {
					row := src.(map[string]any)
					if row["state"] == "broken" {
						return nil, fmt.Errorf("pool %v selects nodes by expression", row["name"])
					}
					return engine.Args{"selector": "pool=" + row["name"].(string)}, nil
				},
			},
			{
				Key: "p", Name: "pause", Short: "Pause the rollout",
				Confirm: func(rows []any, _ engine.Args) (string, error) {
					return fmt.Sprintf("Pause %d", len(rows)), nil
				},
				Plan: planFor(rec, "patched"),
			},
			{
				Key: "u", Name: "unpause", Short: "Resume, without asking",
				Reload: true,
				Plan:   planFor(rec, "resumed"),
			},
			// Acts on nothing and needs values first: the create case.
			{
				Key: "n", Name: "create", Short: "Create a pool",
				Rowless: true,
				Params: []engine.Param{
					{Name: "name", Short: "pool name", Required: true},
					{Name: "size", Short: "how many nodes", Default: "3"},
				},
				Confirm: func(_ []any, cur engine.Args) (string, error) {
					return "Create pool " + cur["name"], nil
				},
				Plan: func(_ []any, cur engine.Args) ([]engine.Step, error) {
					rec.planArgs = append(rec.planArgs, cur)
					return []engine.Step{{
						Env:    engine.Args{"name": cur["name"], "size": cur["size"]},
						Shell:  []string{"sh", "-c"},
						Script: `echo "created $name/$size"`,
						Stdin:  []byte{},
					}}, nil
				},
			},
		},
	}
	return []engine.View{parent, target}
}

// sampleViews is the stand-in the layout and interaction tests run against.
// In-memory views rather than parsed config, so the tests need no shell; the
// engine's own tests cover the script path.
func sampleViews() []engine.View {
	return []engine.View{
		{
			ID: "demo.quick", Name: "quick", Title: "One quick row", Group: "demo",
			Run: func(context.Context, engine.Args) (engine.Table, error) {
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
			Run: func(ctx context.Context, _ engine.Args) (engine.Table, error) {
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
