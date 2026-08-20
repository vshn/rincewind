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

The config lives at `~/.config/aldediggi/config.yaml`, overridable with
`--config`/`-c` or `$ALDEDIGGI_CONFIG`.

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

## Selection

The table view selects rows independently of whether anything acts on them:
`space` toggles, `a` toggles everything the filter left, `esc` clears. The
selection is a set of row **keys**, so it survives reloads and re-sorting;
keys that vanish from the result are pruned. When actions return, the rule
is: an action's input is the selection if there is one, otherwise all
filtered rows — always what the user can see, never the raw stream.

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
| Parameters as env vars, never templated (pending) | Splicing user input into script text is an injection hole |
| Sort on raw values, before formatting | Rendered cells order neither numbers nor dates correctly |
| Emphasis per column, on raw values | One value may mean different things in different columns |
| Selection is a set of keys | Indices die on every reload; keys survive reordering |
| `Run` returns data, never formatted strings | A string cannot be filtered, sorted or restyled |
| One table renderer, styling after layout | Escape codes in cells misalign everything that measures naively |
| Cell errors render `!err` + note, view survives | One broken expression must not take down 199 good rows |
| No shelling out to jq/column/pbcopy/… from Go | Each has a Go replacement; user scripts may do what they like |
| Probe capabilities, not `runtime.GOOS` | Environments lie less than platform names |
| Never hardcode `/bin/bash` | Alpine has no bash; NixOS has no fixed path |
