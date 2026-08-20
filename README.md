# aldediggi

Config-driven cluster diagnostic TUI.
Views are defined in a YAML file: each view runs a script, reads a stream of
JSON values from its stdout and shapes them into a table with jq
expressions. Any command that prints JSON can feed a view — kubectl, curl,
your own pipeline.

```yaml
views:
  - id: argo.apps
    source:
      script: |
        kubectl get applications -A -o json
    rows: .items[]
    key: .metadata.name
    columns:
      - name: NAME
        expr: .metadata.name
      - name: SYNC
        expr: .status.sync.status
        emphasis: { Synced: good, OutOfSync: bad }
    sort:
      - by: SYNC
        rank: [OutOfSync, Synced]
    actions:
      - key: enter                 # drill down: opens a view in a new tab
        name: resources
        open: argo.syncstatus
        args: { app: .metadata.name }
```

Actions come in two kinds, and both open a tab. `open:` navigates to another
view, about the row under the cursor. `script:` does work — once per
**selected** row, with values derived by jq reaching the script as
environment variables, never templated into it.

A working action opens its tab showing the exact invocations it is about to
make, and asks underneath them:

```
pause  infra
  name=infra
  sh -c
    kubectl patch machineconfigpool "$name" --type merge -p '…'
  machineconfigpool/infra patched
```

So you confirm with the command in front of you, the output lands beside the
call that produced it, and `esc` takes you back to the list untouched.

## Use

```
aldediggi                        # TUI; config from ~/.config/aldediggi/config.yaml
aldediggi -c examples/config.yaml  # try it without a cluster (demo view included)
aldediggi check                  # validate the config without starting the TUI
```

In the TUI: `⏎` runs the view under the cursor, `⇥` focuses the output,
`/` filters, `space`/`a` select rows, `r` reloads, `q` quits.
In the output pane the view's own action keys apply — the box under the
table says which. Drilling down opens a tab: `]`/`[` switch, `esc` or `x` closes one,
and coming back finds the filter, cursor and scroll exactly as they were.
Both panes scroll: `↑↓`, `⇞`/`⇟` by the screenful, `home`/`end` to the ends,
and `<` `>` sideways through wide tables — the column names stay put.

The mouse works too: the wheel scrolls whichever pane it points at, a click
puts the cursor on a row, and clicking a menu entry runs it. Press `m` to
release the mouse when you want the terminal's own text selection back for
copying, and `m` again to take it back.

## Build

```
make              # lint, test, build
make release      # static macOS and Linux binaries in dist/
```

## Docs

- [docs/architecture.md](docs/architecture.md) — pipeline, stdout contract,
  config schema, error philosophy, decisions
- [examples/config.yaml](examples/config.yaml) — commented example views
