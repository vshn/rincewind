# aldediggi

TUI + CLI cluster diagnostic helper for OpenShift/Kubernetes.

**Status: `argo`, `upgrade` and `nodes` are built.**
Real views with real actions in both the CLI (`--action`,
`--yes`, action parameters as flags) and the TUI (key bindings,
confirmation, `/` filter, `<>` scroll, drill-down with a view stack).
Views are Go builtins in `internal/catalog`;
see docs/views.md for what they show and docs/inventory.md (generated) for the full surface.

**Views stay builtins.**
The declarative config loader is deferred until someone actually wants a view the tool lacks — see the reasoning in docs/architecture.md.
Nothing reads a config file; namespaces and the impersonated user are constants,
because they are properties of the platform rather than choices.

Not built yet: `loki` and `pods`; `argo ui`, which needs an `internal/platform` for browser probing,
the port-forward lifecycle and OSC 52; and following logs, which needs the stream kind.

The TUI tests run against `sampleRegistry()` in `internal/tui/fixture_test.go`,
not against views the binary ships.
One of its views sleeps,
which is how the tests check that the UI keeps painting while work is in flight.

## Docs

Read before making structural changes:

- [docs/architecture.md](docs/architecture.md) — registry,
  view/action model, `Result` types, decisions
- [docs/howto-add-view.md](docs/howto-add-view.md) — how to add a view, action or group
- [docs/views.md](docs/views.md) — what each view shows and the decisions behind it
- [docs/inventory.md](docs/inventory.md) — generated command inventory (see Conventions)
- [docs/platform.md](docs/platform.md) — macOS/Linux handling

## Vocabulary

A **view** produces output (`pods failed`).
An **action** does something from a view (`--action delete`, or a key in the TUI).
An action operates on what the view is showing: the result, minus the filter, minus the selection.
An action that returns `Navigate` drills into another view instead of reporting.

## Invariants

Violating these is what the design exists to prevent:

- **The registry is the single source.**
  Cobra commands, the TUI list and shell completion are generated from it.
  Never define a view in two places.
- **An action's input is the rows the view produced, never the raw command output.**
  The raw output behind a filtered view is everything in the cluster.
- **`Run` returns a `Result`, never a formatted string.**
  A string cannot be sorted, filtered or re-rendered into a table widget.
- **argv lists, never shell strings, for anything taking a parameter.**
  Parameters can come from user input.
- **Reads do not impersonate; only mutations do.**
  `--as` needs the impersonate verb,
  and demanding it to list things would lock people out of the read-only half of the tool.
- **An action receives what the user can see,** filter and selection included.
  `m.target()` in the TUI, never `m.res`.
- **List reads go through `catalog.fetch[T]`,** never a hand-rolled decode loop.
  It owns the list-or-single asymmetry, the kind guard and keeping the raw document for actions.
- **One place turns wall-clock input into RFC 3339:** `catalog.ParseTime` and `catalog.FormatTime`.
  Never format a timestamp for an API by hand.
- **Style tables through `render.Style`, never by putting escape codes in cells.**
  Everything that measures naively — `text/tabwriter`,
  `bubbles/table` — counts them as width and misaligns the row.
- **No shelling out to `jq`, `column`, `pbcopy`, `nc`, `base64` or `sleep`.**
  Each has a Go replacement; see the table in docs/platform.md.
- **No blocking work in bubbletea's `Update`.**
  Every kubectl call is a `tea.Cmd`.
- **Probe capabilities, not `runtime.GOOS`,** for anything environment-dependent.
- **Never hardcode `/bin/bash`.**
  Alpine has no bash; NixOS has no fixed path.

## Imports

Charm v2 modules live on vanity paths.
`go get github.com/charmbracelet/bubbletea/v2` **fails**.

```
charm.land/bubbletea/v2   charm.land/bubbles/v2   charm.land/lipgloss/v2
```

## Layout

```
cmd/          cobra wiring
internal/     catalog, kube, render, tui   (platform pending for argo ui)
docs/
```

`internal/catalog` holds the domain types — `View`, `Registry`, `Result` — and will hold `Action`.
The package is named for the catalog of what the tool can do,
so neither type has to stutter against its package name.

New Kubernetes access goes through `internal/kube`,
not `exec.Command` at the call site — that package is the seam for a possible client-go migration.

Views are tested against a fake kubectl written into `t.TempDir()`;
see `internal/catalog/argo_test.go`.
That covers the argv issued and the stdin sent, which is where the interesting mistakes are.

## Commands

```
go build ./...
go test ./...
go run .                                 # TUI
go run . argo apps                       # same view, plain output
go run . argo apps root --action pause   # act on one application
go run . argo apps root --action logs    # drill in from the command line
CGO_ENABLED=0 go build -o aldediggi .    # static, how it ships
make                                     # lint (golangci-lint), test, build
make release                             # macOS + Linux binaries in dist/
```

The TUI is tested by driving `model.Update` with synthetic messages and asserting on `View().Content` — see `internal/tui/model_test.go`.
Layout bugs (rows wrapping,
the help line falling off) are caught that way rather than by eye, so keep new panes covered.

## Conventions

- A group file reads top-down in one fixed order: resource types,
  then the registry builder (the declarative list of its views), then fetch wrappers, then shaping.
  Actions live in `<group>_actions.go`.
- A helper used by more than one group moves to the named cross-cutting file — `fetch.go` (reads),
  `rows.go` (what actions receive), `text.go`,
  `times.go` — instead of staying with whichever group needed it first.
- `docs/inventory.md` is generated from the registry; never edit it by hand.
  Regenerate with `go test ./cmd -run TestInventory -update` — the test fails in CI while it is stale.
- Code, comments and docs in English; conversation may be German.
- Comments explain *why*, matching the existing style in `cmd/root.go`.
  Do not narrate what the code already says.
- Errors from cobra are printed by cobra; do not print them again in `Execute`.
