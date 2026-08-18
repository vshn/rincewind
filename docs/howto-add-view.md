# Howto: add a view

Running example: `nodes pools` — small, but uses every concept: a parameter,
actions, a confirmation and a drill-down.
Its code: the pool type and shaping in `internal/catalog/upgrademco.go`,
registration in `nodes.go`, actions in `nodes_actions.go`.

A view is defined once, in the registry.
The CLI subcommand, the TUI entry,
shell completion and `docs/inventory.md` are all generated from it.

## 1. A type for the resource

Decode only the fields the view shows; `name`/`keep` wire the type into the generic helpers:

```go
type machineConfigPool struct {
	Metadata struct {
		Name string `json:"name"`
	} `json:"metadata"`
	// … only what the view needs …
	raw json.RawMessage
}

func (p machineConfigPool) name() string              { return p.Metadata.Name }
func (p *machineConfigPool) keep(doc json.RawMessage) { p.raw = doc }
```

## 2. One line to fetch

```go
func listPools(ctx context.Context, c kube.Client, only string) ([]machineConfigPool, error) {
	return fetch[machineConfigPool](ctx, c, "MachineConfigPool", "machineconfigpool", only)
}
```

`fetch` (fetch.go) runs `kubectl get … -o json`,
handles list-vs-single and refuses responses of the wrong kind.
Every list read goes through it.
Reads never impersonate.

## 3. Shape a Result

Sort, then fill a `Table` — never return a formatted string.
Sorting and time rules: [architecture.md](architecture.md).
Colour comes from the `Emphasis` map, keyed by cell value; never put escape codes in cells.

```go
t := Table{Cols: []string{"NAME", "STATE", /* … */}, Emphasis: poolEmphasis}
for _, p := range pools {
	t.Add(p.raw, p.Metadata.Name, p.state() /* … */)
}
```

The first argument to `Add` is the row's source document — it is what lets actions act on exactly the rows shown.

## 4. Register

In the group's builder (`Nodes()` in nodes.go):

```go
r.Add(View{
	Group: "nodes", Name: "pools",
	Short: "List MachineConfigPools and their rollout",
	Kind:  Snapshot,
	Available: c.Available,
	Params: []Param{{Name: "pool", Short: "limit to one pool", Complete: /* … */}},
	Actions: []Action{openPoolNodes(), poolPauseAction(admin, true), poolPauseAction(admin, false)},
	Run: func(ctx context.Context, args Args) (Result, error) {
		return machineConfigPools(ctx, c, args.Get("pool"))
	},
})
```

`Hidden: true` keeps a view out of the TUI list when it only makes sense with a parameter and is reached by drilling in.
A new group is a new builder function plus one `reg.Merge` in `cmd/root.go`.

## 5. Actions

```go
func poolPauseAction(c kube.Client, pause bool) Action {
	return Action{
		Name: "pause", Key: "p", Scope: Rows, Refresh: true,
		Short:   "Pause the listed pools",
		Confirm: func(in Result, _ Args) string { /* names the objects and the count */ },
		Run: func(ctx context.Context, in Result, _ Args) (Result, error) {
			return patchRows(ctx, c, in, patchPoolPause)
		},
	}
}
```

- `Scope: Rows` acts on what the view showed;
  read it back with `rowsIn` / `namesIn` / `runOnRows` (rows.go).
  `Scope: Params` acts on the view's parameter instead.
- Mutations run as `c.As(adminUser)` and return a `Confirm`;
  `Refresh: true` re-runs the view afterwards.
- Return `Navigate{View: "nodes.status",
  Args: …}` to drill into another view instead of reporting (see `openPoolNodes`).
- Action `Params` become CLI flags and TUI prompts;
  two actions of one view must not claim the same name.

## 6. Test against a fake kubectl

Write a script named `kubectl` into `t.TempDir()` that records its argv and stdin and prints a fixture.
Assert on the argv issued and the stdin sent — that is where the interesting mistakes are.
Example: `TestPoolPauseSendsTheShownPools` in upgrademco_test.go.

## 7. Regenerate the inventory

```
go test ./cmd -run TestInventory -update
```

The test fails while `docs/inventory.md` is stale.
