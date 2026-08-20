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
```

## Use

```
aldediggi                        # TUI; config from ~/.config/aldediggi/config.yaml
aldediggi -c examples/config.yaml  # try it without a cluster (demo view included)
aldediggi check                  # validate the config without starting the TUI
```

In the TUI: `⏎` runs the view under the cursor, `⇥` focuses the output,
`/` filters, `space`/`a` select rows, `r` reloads, `q` quits.
Both panes scroll: `↑↓`, `⇞`/`⇟` by the screenful, `home`/`end` to the ends,
and `<` `>` sideways through wide tables — the column names stay put.

## Build

```
make              # lint, test, build
make release      # static macOS and Linux binaries in dist/
```

## Docs

- [docs/architecture.md](docs/architecture.md) — pipeline, stdout contract,
  config schema, error philosophy, decisions
- [examples/config.yaml](examples/config.yaml) — commented example views
