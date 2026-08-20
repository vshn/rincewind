# rincewind

Config-driven cluster diagnostic TUI.
Views are pulled out of a **hat**: a directory of YAML files.
Each view runs a script,
reads a stream of JSON values from its stdout
and shapes them into a table with jq expressions (gojq).
This replaces the earlier builtin-views design;
the reasoning for the reversal is recorded in docs/architecture.md.

**Status:** the hat loader, engine pipeline,
the table view with row selection, view and action parameters,
and actions — both kinds — are built.
Drill-down is an action that navigates.
A working action gets a tab that collects its parameters,
shows the commands, asks, and holds the output.
The previous generation's 16 views are ported except the two log views.
TUI only.

Not built yet: the two log views and the cluster-name probe,
both deliberately deferred;
batched (`once:`) actions; CLI output mode; stream views (following logs);
`loki` and `pods`; a managed port-forward for `argo ui`.

## Docs

Read [docs/architecture.md](docs/architecture.md) before structural changes:
pipeline, stdout contract, config schema, error philosophy, decisions table.

## Vocabulary

A **view** is one entry in a hat's YAML:
source script, `rows` and column jq expressions,
sort, emphasis, parameters, actions.

An **action** does something with the rows a view is showing.
One mechanism, two kinds:
`open:` navigates to another view — this is drill-down — and acts on the
**cursor row**;
`script:` works, once per row, and acts on the **selection**,
refusing when there is none.
Drill-down is not a separate feature; do not add one.

An action may have `params:` of its own,
and `scope: none` if it acts on nothing, as a create does.
Parameters are collected **in the job tab**, before planning,
because the values are what make the command concrete.

A **tab** holds either a view — with its own result, filter, selection,
cursor and scroll position —
or a **job**: an action's form, its commands, its question and its output,
in that order.
Both kinds of action open a tab; `esc` closes it.

## Invariants

- **The engine is producer-agnostic.**
  It runs a script and decodes JSON; kubectl is hat business.
  Never special-case a producer in Go.
- **stdout is a stream of JSON values.**
  `rows` runs per value, results concatenate.
  Do not "fix" this to single-document.
- **Config mistakes die at load time**, naming view and field.
  Runtime cell errors render `!err` plus one note
  and never take down the view.
  Script failures surface the script's stderr.
- **Sort and emphasis work on raw jq values**, before formatting.
- **Selection is keyed by `key`**, pruned against each new result.
- **A mutation acts on the selection and refuses without one.**
  The filter is no longer the selector; `a` selects what it left.
  Do not "helpfully" fall back to the filtered set.
- **The engine appends the row count to every confirmation**,
  so config cannot write one that hides how much it touches.
- **An action runs once per row**, each call naming one object.
- **The command is on screen before it runs.**
  `Plan` produces steps without executing anything;
  only `Step.Run` executes.
  Never collapse the two,
  and never render a step as one shell-quoted line —
  the values travel beside the script, not inside it.
- **Collect, then plan, then ask, then run** — in that order,
  all in the one job tab.
  Do not move the form or the question into the chrome.
- **Lower-case keys belong to the views.**
  The reserved set in `internal/config` is the contract;
  grow it only under protest.
- **Everything a config can get wrong dies in `internal/config` at load
  time**, naming view and action —
  including an `open` target that does not exist
  and an argument the target does not declare.
- **Each tab owns its state**;
  `m.cur()` is the tab on screen, `m.selected()` the sidebar's cursor.
  They are not the same view after a drill-down.
- **Messages find their tab by `seq`**, a counter that never reuses a number.
  One rule covers a superseded run, a closed tab and a background tick.
- **`Run` returns a `Table`, never a formatted string.**
- **No escape codes in cells; style through `render.Style`** after layout.
  Everything that measures naively — tabwriter, bubbles/table —
  counts them as width.
- **No blocking work in bubbletea's `Update`.**
  Every script run is a `tea.Cmd`; the `seq` stale-guard stays.
- **No shelling out to `jq`, `column`, `pbcopy`, `nc`, `base64` or `sleep`
  from Go.**
  Scripts in a hat may use whatever they like.
- **Default shell is `sh` from PATH.**
  Never hardcode `/bin/bash`; per-view `source.shell` is the override.
- **Parameters become environment variables**,
  never text templated into the script.
- **Probe capabilities, not `runtime.GOOS`.**

## Imports

Charm v2 modules live on vanity paths.
`go get github.com/charmbracelet/bubbletea/v2` **fails**.

```
charm.land/bubbletea/v2   charm.land/bubbles/v2   charm.land/lipgloss/v2
```

## Layout

```
cmd/          cobra wiring: root (TUI), check, --hat flag
internal/
  config/     YAML → typed defs, schema validation → engine.Def
  engine/     Compile(Def) → View, Link resolves navigating actions;
              script runner, stream decode, gojq, sort, formats;
              Table/Row/Cell, Args/Param/Action
  render/     Table → styled lines
  tui/        model.go   bubbletea model, tabs, keys, mouse
              action.go  firing actions, confirmation, drill
              frame.go   framed boxes, tab bar, action bar
hats/         albedaran/  the views for that cluster — a real hat, not a
                          sample; do not treat it as disposable
              demo/       invents its own data, needs no cluster; this is
                          what the tests run against
docs/
```

## Commands

```
go build ./...
go test ./...
go run . -h hats/albedaran               # the albedaran cluster
go run . -h hats/demo                    # invented data, no cluster needed
go run . check -h hats/demo              # validate a hat
CGO_ENABLED=0 go build -o rincewind .    # static, how it ships
make                                     # lint (golangci-lint), test, build
make release                             # the release matrix, locally (goreleaser)
```

## Testing

- Engine tests run real `sh` scripts that echo fixture JSON
  (`internal/engine/pipeline_test.go`) —
  the same path production takes.
- Config tests are table-driven over YAML literals
  and assert the error strings;
  those messages are a feature, keep them covered.
- `hats/demo` is the config-level fixture: every view invents its own data,
  so `internal/config/hat_demo_test.go` exercises sorts, formats, emphasis,
  parameters, cell errors, both action scopes and cross-file linking
  without a cluster.
  Add to that hat when adding a schema feature.
- The `hats/albedaran` tests run the **shipped** views
  against a fake `kubectl` on `PATH`,
  which is what keeps their jq honest.
- TUI tests drive `model.Update` with synthetic key messages,
  inject `resultMsg` directly, bypassing `tea.Cmd`,
  and assert on `View().Content`.
  Fixtures are in-memory views in `internal/tui/fixture_test.go`,
  one of which sleeps so tests can verify painting during work in flight.
  Keep new panes covered — layout bugs are caught here, not by eye.

## CI

Three checks on every push and pull request — `build`, `test`, `lint` —
each calling the matching `make` target,
except lint, which uses the golangci-lint action for its inline annotations.
`.golangci.yml` is what both it and `make lint` read,
so the two cannot check different things.

A release is cut by merging a pull request labelled `Release`:
that tags, the tag runs goreleaser,
and `.goreleaser.yml` is the single definition of what ships —
darwin and linux, amd64 and arm64.
It carries the same `cmd.version` ldflag the Makefile does;
without it a released binary answers `rincewind version` with `dev`.

## Conventions

- Code, comments and docs in English; conversation may be German.
- Markdown is one sentence per line, broken at a comma where that helps.
  A diff should show the sentence that changed, not a reflowed paragraph.
- Comments explain *why*; do not narrate what the code already says.
- Errors from cobra are printed by cobra;
  do not print them again in `Execute`.
