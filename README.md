# rincewind

Config-driven cluster diagnostic TUI.
Views are pulled out of a **hat**: a directory of YAML files.
Each view runs a script,
reads a stream of JSON values from its stdout
and shapes them into a table with jq expressions.
Any command that prints JSON can feed a view —
kubectl, curl, your own pipeline.

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

Actions come in two kinds, and both open a tab.
`open:` navigates to another view, about the row under the cursor.
`script:` does work — once per **selected** row,
with values derived by jq reaching the script as environment variables,
never templated into it.

A working action opens its tab,
collects any parameters it needs one field at a time,
then shows the exact invocations it is about to make and asks underneath them:

```
pause  infra
  name=infra
  sh -c
    kubectl patch machineconfigpool "$name" --type merge -p '…'
  machineconfigpool/infra patched
```

So you confirm with the command in front of you,
the output lands beside the call that produced it,
and `esc` takes you back to the list untouched.

## Use

```
rincewind hats search                  # find repositories of hats on GitHub
rincewind hats install vshn/rincewind-hats
export RINCEWIND_HAT=~/.config/rincewind/vshn-rincewind-hats/albedaran

rincewind                      # TUI, on the hat above
rincewind --hat ./example      # try it without a cluster, no install needed
rincewind check                # validate the hat without starting the TUI
rincewind view nodes.status    # print one view and exit, no TUI
```

A hat is found with `--hat`, else `$RINCEWIND_HAT`. There is no third step: with
neither given, rincewind says so and lists what is installed, rather than
guessing which cluster you meant.

`hats install` clones into `~/.config/rincewind/` (or `--dir`, or
`$RINCEWIND_HATS_DIR`), one flat directory per repository. `hats update`
fetches, shows you the diff, and asks before applying it; `hats list` prints
the paths. **A hat is shell scripts that run as you, against your cluster** —
the topic search is discovery, not a registry, so read one before installing
it.

`view` writes a table column-aligned, a log verbatim, and a followed view as
it streams. Colour is used only when the output is a terminal, so piping into
a file gives plain text; cell errors go to stderr, out of the way of it. A
view that requires a parameter is refused for now — there is no way to pass
one yet.

A view whose output is a log rather than a table is `class: text`:
the lines arrive verbatim, `/` greps them, and the arrows scroll.
Add `follow: true` and they arrive as the script writes them,
with `esc` to stop and `r` to start again.
Following pauses while its tab is in the background,
so nothing keeps running behind a tab nobody is looking at.

The kubectl context is shown top right, and asked for again whenever the view
changes — so what you are about to act on is on screen before you act.
Without a kubectl, or without a current context, nothing is shown there.

In the TUI: `⏎` runs the view under the cursor, `⇥` focuses the output,
`/` filters, `space`/`a` select rows, `r` reloads, `q` quits.
In the output pane the view's own action keys apply —
the box under the table says which.
Drilling down opens a tab: `]`/`[` switch, `esc` or `x` closes one,
and coming back finds the filter, cursor and scroll exactly as they were.
Both panes scroll: `↑↓`, `⇞`/`⇟` by the screenful, `home`/`end` to the ends,
and `<` `>` sideways through wide tables — the column names stay put.

`Y` copies what the output pane is showing to the clipboard, as plain text —
the selection if there is one, otherwise everything the filter left. It uses
`pbcopy`, `wl-copy`, `xclip` or `xsel`, whichever is on your `PATH`, and also
sends the terminal's own clipboard escape (OSC 52) so that it works over ssh
as well. Not every terminal implements OSC 52 — Terminal.app does not — which
is why the local program is used when there is one; in tmux, OSC 52 needs
`set-clipboard on`.

The mouse works too: the wheel scrolls whichever pane it points at,
a click puts the cursor on a row, and clicking a menu entry runs it.
Press `M` to release the mouse
when you want the terminal's own text selection back for copying,
and `M` again to take it back.

## Build

```
make              # lint, test, build
make release      # all four release binaries in dist/, without publishing
```

## Docs

- [docs/architecture.md](docs/architecture.md) — pipeline, stdout contract,
  config schema, error philosophy, decisions
- [example/](example/) — a hat that invents its own data:
  no cluster, no kubectl.
  Read it first when writing a hat of your own

## Licence

Apache 2.0,
the same as its sibling [gandalf](https://github.com/appuio/gandalf).
See [LICENSE](LICENSE).
