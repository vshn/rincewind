# aldediggi

TUI + CLI cluster diagnostic helper for OpenShift/Kubernetes.
One definition per view serves both modes:
`aldediggi` starts the TUI, a subcommand prints the same view, `--action` runs its actions.
The only runtime dependency is `kubectl`.

## Use

```
aldediggi                                # TUI: views left, output right, actions underneath
aldediggi argo apps                      # the same view, plain output
aldediggi argo apps root --action pause  # act on one application
aldediggi upgrade suspensions --action create \
  --name hotfix --reason "DB migration" --start "2026-08-18 20:00" --end +6h
```

Reads run as whoever is logged in.
Mutations impersonate and ask first; `--yes` skips the prompt.

## Build

```
make                           # lint, test, build
make release                   # static macOS and Linux binaries in dist/
make inventory                 # regenerate docs/inventory.md from the registry
```

## Docs

- [docs/inventory.md](docs/inventory.md) — every view and action (generated)
- [docs/views.md](docs/views.md) — what the views show and the decisions behind them
- [docs/howto-add-view.md](docs/howto-add-view.md) — add a view, action or group
- [docs/architecture.md](docs/architecture.md) — registry, view/action model, `Result` types, decisions
- [docs/platform.md](docs/platform.md) — macOS/Linux handling
