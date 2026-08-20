# aldediggi

Config-driven cluster diagnostic TUI.
Views are defined in a YAML file: each runs a script,
reads a stream of JSON values from its stdout and shapes them into a table
with jq expressions (gojq).
This replaces the earlier builtin-views design; the reasoning for the
reversal is recorded in docs/architecture.md.

**Status:** config loader, engine pipeline, the table view with row
selection, view parameters and actions — both kinds — are built. Drill-down
is an action that navigates; targets open as tabs. TUI only.
Not built yet: action parameters (prompting for a value), batched actions,
CLI output mode, stream views (follow logs), other view classes.

## Docs

Read [docs/architecture.md](docs/architecture.md) before structural changes:
pipeline, stdout contract, config schema, error philosophy, decisions table.

## Vocabulary

A **view** is one entry in the config file: source script, `rows`/column
jq expressions, sort, emphasis, parameters, actions.

An **action** does something with the rows a view is showing. One mechanism,
two kinds: `open:` navigates to another view (this is drill-down) and acts on
the **cursor row**; `script:` works, once per row, and acts on the
**selection**, refusing when there is none. Drill-down is not a separate
feature — do not add one.

A **tab** holds either a view — with its own result, filter, selection,
cursor and scroll position — or a **job**: a working action's commands and
their output. Both kinds of action open a tab; `esc` closes it.

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
- **A mutation acts on the selection and refuses without one.** The filter is
  no longer the selector; `a` selects what it left. Do not "helpfully" fall
  back to the filtered set.
- **The engine appends the row count to every confirmation**, so config
  cannot write one that hides how much it touches.
- **An action runs once per row**, each call naming one object.
- **The command is on screen before it runs.** `Plan` produces steps without
  executing anything; only `Step.Run` executes. Never collapse the two, and
  never render a step as one shell-quoted line — the values travel beside
  the script, not inside it.
- **Everything a config can get wrong dies in `internal/config` at load
  time**, naming view and action — including an `open` target that does not
  exist and an argument the target does not declare.
- **Each tab owns its state**; `m.cur()` is the tab on screen, `m.selected()`
  the sidebar's cursor. They are not the same view after a drill-down.
- **Messages find their tab by `seq`**, a counter that never reuses a number.
  One rule covers a superseded run, a closed tab and a background tick.
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
  engine/     Compile(Def) → View, Link resolves navigating actions;
              script runner, stream decode, gojq, sort, formats;
              Table/Row/Cell, Args/Param/Action
  render/     Table → styled lines
  tui/        model.go   bubbletea model, tabs, keys, mouse
              action.go  firing actions, confirmation, drill
              frame.go   framed boxes, tab bar, action bar
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
