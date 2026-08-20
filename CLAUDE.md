# aldediggi

Config-driven cluster diagnostic TUI.
Views are defined in a YAML file: each runs a script,
reads a stream of JSON values from its stdout and shapes them into a table
with jq expressions (gojq).
This replaces the earlier builtin-views design; the reasoning for the
reversal is recorded in docs/architecture.md.

**Status:** config loader, engine pipeline and the table view with
row selection are built. TUI only.
Not built yet: actions, view parameters, CLI output mode,
stream views (follow logs), other view classes.

## Docs

Read [docs/architecture.md](docs/architecture.md) before structural changes:
pipeline, stdout contract, config schema, error philosophy, decisions table.

## Vocabulary

A **view** is one entry in the config file: source script, `rows`/column
jq expressions, sort, emphasis. The **selection** is TUI widget state, a set
of row keys, independent of actions (which do not exist yet). When actions
arrive: their input is the selection if any, else all filtered rows.

## Invariants

- **The engine is producer-agnostic.** It runs a script and decodes JSON;
  kubectl is config-file business. Never special-case a producer in Go.
- **stdout is a stream of JSON values.** `rows` runs per value, results
  concatenate. Do not "fix" this to single-document.
- **Config mistakes die at load time**, naming view and field. Runtime cell
  errors render `!err` plus one note and never take down the view.
  Script failures surface the script's stderr.
- **Sort and emphasis work on raw jq values**, before formatting.
- **Selection is keyed by `key`**, pruned against each new result.
- **`Run` returns a `Table`, never a formatted string.**
- **No escape codes in cells; style through `render.Style`** after layout.
  Everything that measures naively (tabwriter, bubbles/table) counts them
  as width.
- **No blocking work in bubbletea's `Update`.** Every script run is a
  `tea.Cmd`; the `seq` stale-guard stays.
- **No shelling out to `jq`, `column`, `pbcopy`, `nc`, `base64` or `sleep`
  from Go.** User scripts in the config may use whatever they like.
- **Default shell is `sh` from PATH.** Never hardcode `/bin/bash`; per-view
  `source.shell` is the override.
- **Future view parameters become environment variables**, never text
  templated into the script.
- **Probe capabilities, not `runtime.GOOS`.**

## Imports

Charm v2 modules live on vanity paths.
`go get github.com/charmbracelet/bubbletea/v2` **fails**.

```
charm.land/bubbletea/v2   charm.land/bubbles/v2   charm.land/lipgloss/v2
```

## Layout

```
cmd/          cobra wiring: root (TUI), check, --config flag
internal/
  config/     YAML → typed defs, schema validation → engine.Def
  engine/     Compile(Def) → View; script runner, stream decode, gojq,
              sort, formats; Table/Row/Cell
  render/     Table → styled lines
  tui/        bubbletea model: sidebar, table pane, filter, selection
examples/     example config; kept green by internal/config/example_test.go
docs/
```

## Commands

```
go build ./...
go test ./...
go run . -c examples/config.yaml         # TUI against the shipped examples
go run . check -c examples/config.yaml   # validate a config
CGO_ENABLED=0 go build -o aldediggi .    # static, how it ships
make                                     # lint (golangci-lint), test, build
make release                             # macOS + Linux binaries in dist/
```

## Testing

- Engine tests run real `sh` scripts that echo fixture JSON
  (`internal/engine/pipeline_test.go`) — the same path production takes.
- Config tests are table-driven over YAML literals and assert the error
  strings; those messages are a feature, keep them covered.
- TUI tests drive `model.Update` with synthetic key messages, inject
  `resultMsg` directly (bypassing `tea.Cmd`) and assert on `View().Content`;
  fixtures are in-memory views in `internal/tui/fixture_test.go`, one of
  which sleeps so tests can verify painting during work in flight.
  Keep new panes covered — layout bugs are caught here, not by eye.

## Conventions

- Code, comments and docs in English; conversation may be German.
- Comments explain *why*; do not narrate what the code already says.
- Errors from cobra are printed by cobra; do not print them again in
  `Execute`.
