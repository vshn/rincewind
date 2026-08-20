# Architecture

## One pipeline per view

Every view in the config file describes the same pipeline:

```
script  →  JSON value stream  →  rows expression  →  column projection  →  sort  →  table
(any command)   (stdout)            (jq)                (jq per column)   (declarative)
```

The engine knows nothing about kubectl. It knows "run this script, expect
JSON on stdout" — every piece of domain knowledge lives in the config file.

## The stdout contract

A source's stdout is a **stream of JSON values**, not exactly one document.
The `rows` expression runs over each value in the stream and the results are
concatenated — exactly jq's semantics, where a program runs once per input
document. That makes multi-source views trivial:

```yaml
source:
  script: |
    kubectl get applications -A -o json
    curl -sf https://example.test/api/v1/things
rows: .items[]
```

A single document is just the stream of length one. Scripts that need real
merging (joins, enrichment) are free to use `jq` themselves — the
no-shelling-out rule below binds this tool's Go code, not user scripts.

## Sources are scripts

`source.script` is the only source form. It runs through `sh -c` by default —
POSIX sh from `PATH`, never a hardcoded bash: Alpine has no bash, NixOS has
no fixed path. Who wants bashisms or `pipefail` overrides per view:

```yaml
source:
  shell: [bash, -euo, pipefail, -c]
  script: |
    ...
```

Worth knowing: in POSIX sh, `a | b` reports `b`'s exit status, so a failing
left end goes unnoticed. Use the bash override with `pipefail` when that
matters.

When views grow parameters (not built yet), they will be passed as
**environment variables**, never templated into the script text — splicing
user input into code is an injection hole; an environment variable is not.

## Expressions are jq

`rows`, `key` and every column `expr` are jq programs, executed with
[gojq](https://github.com/itchyny/gojq) — pure Go, nothing shells out.
Expressions can be developed against real command output with real jq and
pasted into the config unchanged.

## Actions

A view produces rows; an **action** does something with the rows it is
showing. There is one mechanism, with two kinds:

| kind | declared by | operates on | result |
|---|---|---|---|
| navigate | `open:` | the **cursor row**, always | opens the target view in a new tab |
| run | `script:` | the **selection** — refuses when there is none | opens a tab showing the commands, then their output |
| run, `scope: none` | `script:` | **nothing** — a create, or one working from the view's parameters | the same tab, with one step |

Both kinds open a tab. `esc` closes it either way.

An action may declare `params:` of its own — values the view cannot supply,
such as the name and dates of something being created. They are **collected
in the tab**, one field at a time, before anything is planned: the values are
what make the command concrete, and a command that does not exist yet cannot
be shown. So one tab carries the whole interaction, in order:

```
  ask        name: july-maintenance          answered fields stay on screen
             reason: ▌                       (2 of 4)  why upgrades are suspended

  confirm    name=july-maintenance           the planned step, verbatim
             sh -c
               kubectl create -f - <<EOF …
             Create upgrade suspension july-maintenance   [y/N]

  done       upgradesuspensionwindow/… created
             1 step ok
```

The form and the question are tab content, which scrolls, so neither can push
the footer off a short window — and a multi-line confirmation, the kind that
carries the warning that is the point of asking, has room. A default is
prefilled rather than applied invisibly; a required field refuses to be empty
in place; `esc` abandons the whole thing and closes the tab.

A `scope: none` action may still declare `env:`, evaluated against a null
input so its expressions work from `$args` — which is how a create turns wall
clock into RFC 3339 without `date`, whose flags differ between GNU and BSD.
An expression that reaches for the row yields empty there, and that is not
silent: every value is printed in the tab before anything runs.

Drill-down is therefore not a feature of its own: it is an action that
navigates. A dedicated `drilldown:` schema would have to be un-built the
moment someone wants to cordon a node.

```yaml
actions:
  - key: enter                     # the one obvious action
    name: nodes
    open: nodes.status
    args:
      selector: <jq over the cursor row>

  - key: p
    name: pause
    confirm: '"Pause \(map(.metadata.name) | join(", "))"'   # over the row array
    env: { name: .metadata.name }                            # per row
    script: kubectl patch machineconfigpool "$name" …
    reload: true
```

A run action **opens a tab of its own** showing the invocations it is about
to make, one per selected row, before anything runs — and the confirmation
sits under them, so the question is asked with the command in plain sight.
`y` runs them and the output appears beside each invocation; `esc` closes the
tab and returns to the view.

That tab is why planning is separate from running in the engine: `Plan`
turns rows into `Step`s without executing any, and `Step.Run` executes one.
A step is deliberately **not** rendered as a single shell-quoted line, which
would be a fiction — the values never enter the script text. It shows the
environment, the shell, and the script as written:

```
pause  infra
  name=infra
  sh -c
    kubectl patch machineconfigpool "$name" --type merge -p '…'
  machineconfigpool/infra patched
```

One step per row means each call names exactly one object, so a partial
failure is attributable ("2 of 5 steps failed" closes the tab's listing) and
there is no selector that could match something else. `reload: true` re-runs
the source view in **its own** tab, so the fresh rows are already there when
one escapes back to them.

Two deliberate departures from the previous generation of this tool:

- **A mutation requires an explicit selection.** The old rule was "selection
  narrows, rather than being its own concept" — the *filter* was the
  selector, so `/OutOfSync` plus the action key hit all fourteen. Nothing is
  lost by reversing it, because `a` selects every row the filter left: the
  bulk case is `/OutOfSync`, `a`, `p`, and that one extra keystroke is the
  whole point. A forgotten filter can no longer turn a mutation loose.
- **A navigation always uses the cursor row.** The old tool had to refuse
  when 0 or several rows were in scope (`select one pool first (14 shown)`);
  with a row cursor that error disappears.

`confirm` supplies the verb; the **engine appends the count**, so a
confirmation that fails to say how much it touches cannot be written.

Parameters and `env` values reach a script as **environment variables**,
never templated into it. Two rules follow from that:

- names must match `[a-z][a-z0-9_]*` — lowercase-only keeps them clear of
  `PATH`, `HOME` and `IFS` without a list of forbidden names;
- use `if [ -n "$x" ]; then set -- …; fi`, **not** `[ -n "$x" ] && set -- …`:
  the `&&` form leaves exit status 1 when the parameter is empty, which the
  runner would report as a failed script.

There is no `hidden:` flag. A view with a **required** parameter cannot be
started from the menu — running it bare would show some arbitrary object as
if it were the answer — so it is simply not listed, and an action reaches it.

## Config schema

```yaml
views:
  - id: argo.apps            # unique, required; the part before the first
                             # dot groups the sidebar, the rest is the menu
                             # label — an id without a dot is ungrouped
    title: Argo applications # description, shown in the title bar; default: id
    class: table             # optional; only "table" exists so far
    refresh: 30s             # optional auto-reload; manual r always works
    source:
      shell: [sh, -c]        # optional; this is the default
      script: |              # required
        kubectl get applications -A -o json
    rows: .items[]           # jq: one stdout value → row objects; required
    key: .metadata.name      # jq: row identity; optional but recommended
    columns:                 # required, at least one
      - name: SYNC
        expr: .status.sync.status
        emphasis: { Synced: good, OutOfSync: bad, Unknown: warn }
        format: ""           # "" | timestamp | age
    sort:                    # optional, multi-key, stable
      - by: SYNC
        rank: [OutOfSync, Unknown, Synced]   # rank xor order
      - by: NAME             # order: asc (default) | desc
```

Semantics that are easy to get wrong:

- **Sorting compares the raw jq values**, before formatting: numbers sort
  numerically, RFC 3339 timestamps chronologically. `rank` lists values from
  worst to best; anything unlisted sorts last.
- **Emphasis matches the raw value**, before formatting, per column — the
  same value may mean different things in different columns.
- **`key` names a row across reloads.** The selection is keyed by it; without
  a key expression the rendered cells stand in, and a reload that changes the
  display can lose the selection.
- **Formats pass unparseable values through** instead of hiding them.

The config lives at `~/.config/aldediggi/`, overridable with `--config`/`-c`
or `$ALDEDIGGI_CONFIG`. A **directory** is read as every `*.yaml` in it, in
name order, which keeps one file per group — `argo.yaml`, `upgrade.yaml`,
`nodes.yaml`, `alerts.yaml`. A single file works just as well; pointing at
the directory covers both. Actions are linked once every file has been read,
so a view may open one declared in another.

## Errors: the price of a config engine, paid deliberately

The previous incarnation of this tool kept views as Go builtins because
"typed structs give errors like `application "x" not found` where a generic
expression engine gives a stack of nils". Reversing that decision means
answering it:

1. **Load-time validation.** Every expression is compiled at startup, sort
   keys are checked against column names, ids for uniqueness, enums for
   their values; unknown YAML fields are rejected with a line number. A typo
   dies before any script runs, naming the view and the field.
   `aldediggi check` runs just this.
2. **Runtime failures are per cell, not per view.** A column expression that
   errors renders `!err` in that cell and one note below the table; the
   other columns and rows stay usable.
3. **Script failures show the script's stderr**, not a Go stack: the
   script's own message is the useful one.

## Results are data, not text

A view's `Run` returns a `Table` — columns, rows with their raw source
objects and identity keys, notes. Rendering to styled lines happens in
`internal/render`, in exactly one place, styling only after the layout is
fixed: escape sequences count towards the width of everything that measures
naively (text/tabwriter, bubbles/table), which is why cells never contain
escape codes and why the table widget is not `bubbles/table`.

## Selection and tabs

`space` toggles a row, `a` toggles everything the filter left, `esc` clears.
The selection is a set of row **keys**, so it survives reloads and
re-sorting; keys that vanish from the result are pruned.

A navigating action opens its target in a **new tab** in the content box's
top border. Tabs are one path, not a heap of windows: drilling from tab N
truncates whatever was opened from it, and picking a view in the menu
collapses back to one. `]` and `[` move between them, `esc` and `x` close
one, and a click activates one.

Each tab keeps its own fetched table, filter, selection, row cursor and
scroll position, so switching is instant and coming back lands exactly where
one left. That is the point of tabs: the old view stack re-ran the parent on
the way back and lost all four. `r` answers staleness.

An auto-`refresh` **pauses** while its tab is in the background — five tabs
on a ten-second refresh is five concurrent scripts against a cluster nobody
is watching — and catches up on return if the data aged past the interval. A
run in flight when its tab goes to the background finishes into that tab and
only redraws when the tab comes forward.

## Layout

```
cmd/               cobra wiring: root (TUI), check, --config
internal/
  config/          YAML → typed defs; schema validation; hands off to engine
  engine/          Def → compiled View; script runner, JSON stream decode,
                   gojq evaluation, sort, formats; Table/Row/Cell types
  render/          Table → styled lines (style-after-layout)
  tui/             bubbletea model: sidebar, table pane, filter, selection
examples/          shipped example config, kept green by a test
```

## TUI notes

- Bubbletea **v2**, imported from `charm.land/bubbletea/v2` (the
  `github.com/charmbracelet/…` path fails for v2). Same for bubbles and
  lipgloss.
- `Update` never blocks. Every script run is a `tea.Cmd`; a `seq` counter
  discards results of superseded runs, and one spinner tick chain is kept
  alive at a time.
- The run timeout (30s) is the TUI's policy; the engine just honours ctx.
- The screen is framed boxes: the title across the top; the menu down the
  left; the output and, under it, the view's actions in a box of their own;
  a split footer — version on the menu's width, key legend on the output's.
  The menu is one box spanning the output and the action box beside it. A
  view with no actions gets no empty frame: three lines to say so would be
  three lines of table.
- The frames are drawn by hand rather than with lipgloss borders, because
  the content box carries labels *inside* its top border: the tabs on the
  left and the scroll position on the right, where neither costs a row of
  the table it describes. A pending confirmation takes over the action box,
  so the question sits where the answer is expected and swallows every key.
- The action legend sheds its descriptions before it sheds an action:
  knowing that `p` pauses beats knowing what pausing means, and a clipped
  line tells neither.
- Tab layout and tab hit-testing come from one function returning both the
  bar and the spans, as the sidebar's do, so what the mouse believes and what
  the screen shows cannot drift apart. Chips are built plain and measured
  before being styled — an escape sequence counted as width would put every
  hit one column out.
- The table header is held **outside** the viewport and painted onto the
  box's first row, shifted by the viewport's own `XOffset`. Scrolling a long
  table must never cost the column names. Both panes scroll: the viewport
  follows the row cursor to the edge rather than recentring, and the sidebar
  windows onto the menu the same way.
- The key legend is weighted: when the footer box is too narrow the lightest
  keys are dropped rather than the line being clipped mid-word.
- The mouse acts on **what it points at**, not on what has the keyboard: the
  wheel over the table scrolls the table even while the menu is focused.
  Hit-testing counts from the same layout constants the renderer draws with,
  so the two cannot drift; the tests aim at lines found in the rendered
  output rather than at numbers worked out a second time.
- Capturing the mouse costs the terminal's own text selection, which matters
  for a tool people copy node names out of. `MouseMode` is a field of the
  frame in bubbletea v2, so `m` toggling a bool is the whole mechanism —
  released, the terminal handles selection and copying as usual.
- Tests drive `model.Update` with synthetic messages and assert on
  `View().Content` — no terminal, no golden files. The fixture views are
  built in memory; one of them sleeps, which is how the tests check that
  the UI keeps painting while work is in flight.

## Non-goals

- Managing clusters. Anything beyond diagnosis stays with `kubectl`/`oc`.
- Windows support.
- A plugin API. The config file plus arbitrary scripts is the extension
  mechanism.

## Decisions

| Decision | Reason |
|---|---|
| Views are config, not Go | The tool's owner wants to add views without recompiling; the error-message cost is paid explicitly (see above) |
| Script is the only source form | One execution path; the config shows exactly what runs, shell choice included |
| stdout is a JSON stream | Multi-source views without forcing the script to merge |
| jq (gojq) as the only expression language | Users already know it; expressions are developed with real jq and pasted in |
| Parameters as env vars, never templated | Splicing user input into script text is an injection hole |
| Drill-down is an action, not its own feature | The old tool proved one mechanism carries both; a `drilldown:` schema would be un-built the first time someone wants to cordon a node |
| Mutations require an explicit selection | Reverses the old "the filter is the selector"; `a` still selects everything the filter left, so nothing is lost but the accident |
| Navigation follows the cursor row | Removes the old "select one pool first" refusal entirely |
| An action runs once per row | Each call names one object: partial failure is reportable, and no selector can match something else |
| A working action gets a tab, and shows its commands before running | Confirming blind is no confirmation; and the output has room to be read and scrolled instead of clipped |
| The parameter form lives in that same tab | The values must be collected before the command exists; putting the form in the chrome would need variable-height layout and could not hold a multi-line question |
| An action parameter may not shadow the view's or an `env:` name | Then the precedence is unobservable, and there is one fewer rule to remember |
| Per-row invocations replace the old `kubectl -f -` List document | Each call names one object, so a partial failure is attributable |
| The argo "root rider" is an instruction, not a hidden mutation | The old pause silently patched `root` too; a mutation nobody selected is what the selection rule exists to prevent |
| A config directory, one file per group | Sixteen views in one file is unreadable; it is also how they were arranged as Go source |
| The mouse toggle is `M`, not `m` | Lower-case letters belong to the views, which have far more use for them than one global toggle |
| Planning is separate from running in the engine | The command can only be shown up front if producing it executes nothing |
| A step is shown as env + shell + script, not one quoted line | The values never enter the script text; a quoted one-liner would misrepresent what runs |
| The engine appends the count to a confirmation | A confirmation that fails to say how much it touches cannot be written |
| Config validated against the target view at load | The old `no such view` was a run-time surprise; so was an argument nothing read |
| A required parameter hides a view from the menu | Running it bare would show an arbitrary object as if it were the answer; no `hidden:` flag needed |
| Sort on raw values, before formatting | Rendered cells order neither numbers nor dates correctly |
| Emphasis per column, on raw values | One value may mean different things in different columns |
| Selection is a set of keys | Indices die on every reload; keys survive reordering |
| `Run` returns data, never formatted strings | A string cannot be filtered, sorted or restyled |
| One table renderer, styling after layout | Escape codes in cells misalign everything that measures naively |
| Cell errors render `!err` + note, view survives | One broken expression must not take down 199 good rows |
| No shelling out to jq/column/pbcopy/… from Go | Each has a Go replacement; user scripts may do what they like |
| Probe capabilities, not `runtime.GOOS` | Environments lie less than platform names |
| Never hardcode `/bin/bash` | Alpine has no bash; NixOS has no fixed path |
