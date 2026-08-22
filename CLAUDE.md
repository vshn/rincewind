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
actions — both kinds — and contexts are built.
Drill-down is an action that navigates.
A working action gets a tab that collects its parameters,
shows the commands, asks, and holds the output.
All 16 of the previous generation's views are ported,
the two log views as followed text views.
`class: chart` draws time series in braille (via ntcharts);
`example/metrics.yaml` is the reference.
Besides the TUI there are two non-interactive commands:
`view` prints one view, `act` runs one of its actions —
same engine, rows named by key instead of selected.

A hat's **contexts** say where one is:
each a script printing JSON, with `active:` and `label:` as jq over it.
The chrome reads their labels, `needs:` hides the views they rule out,
`H` brings those back, and `check` reports the lot.

Not built yet: the hat repo's own `check --strict` pipeline,
which waits on rincewind being public;
batched (`once:`) actions; a filter for `view`;
`loki` and `pods`; a managed port-forward for `argo ui`.

## Docs

Read [docs/architecture.md](docs/architecture.md) before structural changes:
pipeline, stdout contract, error philosophy, decisions table.
[docs/hat.md](docs/hat.md) is the complete schema reference for hat authors;
a schema change updates it in the same commit.

## Vocabulary

A **view** is one entry in a hat's YAML:
source script, `rows` and column jq expressions,
sort, emphasis, parameters, actions.
`class: text` takes the output as lines instead —
a log, verbatim, with no rows and so no selection.
`follow: true` reads those lines as the script writes them;
text only, and it excludes `refresh`.
`class: chart` shapes the stream into series —
`rows` yields `{name, points: [[t, v], …]}` objects —
drawn as a braille time-series plot with a legend.
No rows, so no selection: `/` picks series by name,
actions need `scope: none`, and `yrange:` fixes the value axis.

A **context** is what one is in the middle of:
a platform, a cluster, an environment.
One script printing JSON,
an optional `active:` predicate over the first value it printed,
and an optional `label:` from the same value.
No predicate means active whenever the script produced a value —
the identity case, which is how the cluster's own name is read.
A view names them with `needs:`, a string or a list.

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
- **A failed run replaces nothing.**
  A view that has rows keeps them, with the failure under them
  and `⚠ stale — from <time>` on the status line;
  only a view that has never produced anything
  gives the pane to the error.
- **Sort and emphasis work on raw jq values**, before formatting.
- **A text view's lines are never reshaped.**
  No `oneLine`, no column padding, no truncation:
  the indentation is what a log is read by.
- **A chart's series stay in script order, drawn later-on-top.**
  The z-order is a statement — max band under avg line —
  so the engine sorts points by time and reshapes nothing else.
  Outliers are the query's business (`max_over_time` per bucket);
  the renderer only draws what survived it.
  The class is `chart`, never `prometheus`: producers are hat business.
- **A followed view has `Stream` and no `Run`**, and the reverse for a snapshot.
  One field set, never both, so nothing has to guess which shape it holds.
- **Only one script follows per tab, and none behind a background tab.**
  Leaving pauses it, returning starts it again;
  a followed view therefore passes something like `--tail`,
  because what happened in the meantime is gone.
- **Selection is keyed by `key`**, pruned against each new result.
- **A mutation acts on the selection and refuses without one.**
  The filter is no longer the selector; `a` selects what it left.
  Do not "helpfully" fall back to the filtered set.
  On the command line the rows are named instead —
  a typed key is that same deliberate act — and `act` refuses
  with neither keys nor `--all`.
- **The engine appends the row count to every confirmation**,
  in `compileActions` and nowhere else,
  so config cannot write one that hides how much it touches
  and the TUI and `act` cannot ask different questions about the same rows.
- **`act` shows the plan and asks, always.**
  `--yes` skips the asking, never the showing;
  the hat's warning lives in the confirmation text, so that is said either way.
  The plan and the question go to stderr, the scripts' output to stdout:
  a redirect catches the results, not the transcript.
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
- **A key the schema does not know is the one exception**, and is reported
  rather than refused.
  A hat is read by binaries older than itself,
  and nothing in the file tells a typo from a field written for a newer
  rincewind.
  Only the decoder's "field not found" class is downgraded;
  every other type error still kills the load.
  `check --strict` turns the notes back into errors, for a hat's own CI.
- **A hat may write `rincewind: ">= 0.8"`** when ignoring a field would be
  wrong rather than lossy.
  A lower bound only, and `dev` and `-dirty` builds are not held to it —
  neither says anything comparable, and a dirty tree is where the awaited
  feature lives while it is written.
  An unmeasured run says so; silence would ship the refusal to whoever is
  on a release.
- **The menu wraps at both ends, the table never does.**
  Only single steps, not paging: the menu is short and visible whole,
  a long table is not.
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
- **`H` and `M` are upper case**, like every global toggle:
  `H` shows the views the contexts ruled out, `M` releases the mouse.
  Lower-case keys belong to the views.
- **The clipboard is `Y`: OSC 52 always, plus a local writer when there is
  one.**
  OSC 52 alone was the plan and it does not survive contact with Terminal.app,
  which has never implemented it;
  NSPasteboard needs cgo, and the build is static.
  So the writer is probed on `PATH` — `pbcopy`, `wl-copy`, `xclip`, `xsel` —
  and skipped in an ssh session, where it would copy on the far machine.
  A selection narrows the copy; no selection means all of it.
- **No shelling out to `jq`, `column`, `nc`, `base64` or `sleep` from Go.**
  Each has a Go replacement.
  The clipboard and `internal/hats` are the only exceptions, above,
  and the clipboard's is about the machine the person is sitting at,
  never about what a hat is pointed at.
  Scripts in a hat may use whatever they like.
- **Default shell is `sh` from PATH.**
  Never hardcode `/bin/bash`; per-view `source.shell` is the override.
- **Parameters become environment variables**,
  never text templated into the script.
- **Parameters are printed; the inherited environment is not.**
  Every value reaching a step is shown before it runs,
  so a secret must never be a parameter or an action `env:` value —
  it would land in the job tab, in `Y`, and in `act`'s CI log.
  A script inherits the real environment whole,
  and only its *text* is displayed, so `$TOKEN` on screen is a value nowhere.
- **A hat is code.** `hats install` asks before cloning and `hats update`
  shows `git diff --stat` before applying;
  the topic search is discovery, never a registry.
  Do not add anything that fetches or updates by itself.
- **`--hat`, else `$RINCEWIND_HAT`, else refuse.**
  No short form: `-h` is help, as everywhere else.
  `~/.config/rincewind` is the store, not a hat, and cannot be both.
  Never guess one of the installed hats.
- **git and GitHub are named in Go only in `internal/hats`**,
  which is also the only place the binary reaches a remote system at all.
  Managing hats is the one job that cannot be a hat's own script.
  git is probed on `PATH`; clones are full, never `--depth 1`.
- **A clone is tried over ssh first, then https.**
  ssh is offered only when it is on `PATH`, and the attempt carries
  `BatchMode=yes` — a transport that cannot work must fail, not wait,
  or the fallback never gets its turn.
  `CloneURLs` is the one place that decides the order,
  so what `install` prints is what it runs.
- **The hat asks the question, the world answers it.**
  A hat may never declare *that* it is on prod — only how to find out.
  That is what keeps "which cluster one is pointed at cannot come from
  the hat" true now that the hat is the one asking.
- **No producer is named in Go.** Not kubectl, not anything:
  a hat may run against a cluster, an API or nothing at all,
  and the tool cannot tell.
  A hat that declares no contexts shows nothing top right,
  and that is the correct answer.
  Re-asked when the view on screen changes, behind a TTL —
  one round for every context, no per-context cadence, and no cache key:
  identical sources already run once per round,
  and an expensive probe can cache in its own script.
- **Fail open: active, inactive, unknown.**
  A bool cannot say the third, and the third is the one that matters.
  A view is hidden only when *every* context it names is definitively
  inactive; unknown hides nothing, and nothing is hidden before the first
  round, which falls out of resolution being a `tea.Cmd` like any other.
- **Hiding is a filter, never a deletion.**
  The sidebar's border says how many, `H` brings them back marked,
  and `view`/`act` run a hidden view without complaint —
  asking by name is not being offered it in a menu.
  The menu is rebuilt when what belongs in it changes,
  never filtered at paint time: the cursor cannot land on a line
  that is not there.
- **`tools:` gates nothing.**
  A missing binary is news worth reporting — `check` does — and a view
  that hid itself over one would hide the finding.
- **Probe capabilities, not `runtime.GOOS`.**
- **The tool is agnostic about what a hat talks to.**
  Cluster, cloud API, a local script — the engine runs a script and decodes
  JSON, and no other package may learn more than that.
  Before adding anything that names an external program, ask whether the hat
  could name it instead: `source.script`, `source.shell` and `tools:` are
  where such a name belongs.

## Imports

Charm v2 modules live on vanity paths.
`go get github.com/charmbracelet/bubbletea/v2` **fails**.

```
charm.land/bubbletea/v2   charm.land/bubbles/v2   charm.land/lipgloss/v2
```

## Layout

```
cmd/          cobra wiring: root (TUI), view, act, hats, --hat flag
              hat.go     one load path: unknown keys, version requirement
              check.go   validate, and report the contexts and their tools
              params.go  -p name=value, shared by view and act
internal/
  config/     YAML → typed defs, schema validation → engine.Def
  hats/       the store: GitHub source parsing, clone scanning, git, search
  engine/     Compile(Def) → View, Link resolves navigating actions;
              script runner, stream decode, gojq, sort, formats;
              Table/Row/Cell, Args/Param/Action
              context.go  contexts, their three states, Resolve → Bearings
  render/     Table → styled lines
  tui/        model.go   bubbletea model, tabs, the update loop, mouse
              bearings.go resolving the contexts, the chrome, what is hidden
              keys.go    the binding table and the movement it causes
              layout.go  boxes, hit-testing, scroll positions
              paint.go   View and everything it renders
              action.go  firing actions, confirmation, drill
              frame.go   framed boxes, tab bar, action bar
example/      a hat that invents its own data, needs no cluster; this is
              what the tests run against, and what to read first when
              writing a hat
docs/
```

## Commands

```
go build ./...
go test ./...
go run . --hat example                    # invented data, no cluster needed
go run . check --hat example              # validate a hat, and say where it is
go run . check --hat example --strict     # an unknown key is then an error
go run . view demo.services --hat example # print one view, no TUI
go run . act demo.services restart api --hat example   # run one action
go run . hats search                      # find hats on GitHub
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
- `example` is the config-level fixture: every view invents its own data,
  so `internal/config/hat_demo_test.go` exercises sorts, formats, emphasis,
  parameters, cell errors, both action scopes and cross-file linking
  without a cluster.
  Add to that hat when adding a schema feature.
- There is no test here that runs a real cluster's views against a fake
  `kubectl` on `PATH` any more:
  the albedaran hat moved to `vshn/rincewind-hats`, and that coverage —
  which is what keeps a hat's jq honest — belongs there now.
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
