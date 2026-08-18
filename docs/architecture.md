# Architecture

## Vocabulary

A **view** produces output (`nodes status`).
An **action** does something from a view (`--action pause`).
Actions belong to a view and never stand alone,
so nothing operates blindly on a selector — you see the set before you touch it.

## Two modes, one definition

- **`aldediggi`** starts the TUI: views on the left,
  output on the right, the view's actions underneath.
- **`aldediggi argo apps`** prints just the output.
- **`aldediggi argo apps --action pause`** runs an action on that same view.

Both modes are generated from one registry; a view is defined once.

## View registry

One registry is the single source of truth — never maintained separately:

```
           ┌─ cobra commands     (aldediggi nodes status)
registry ──┼─ TUI list           (left pane)
           ├─ completion         (aldediggi completion zsh)
           └─ docs/inventory.md  (generated, checked by a test)
```

A view carries: id (`nodes.status`), group, description,
kind, parameters, its actions, and something runnable.
Views are Go builtins;
a declarative config source was considered and **deliberately not built**: the engine came out at roughly the cost of writing every view by hand,
and typed structs give errors like `application "x" not found` where a generic expression engine gives a stack of nils.
`Registry.Merge` keeps the door open — a config source would be one more merge.

## Anatomy of a view

A snapshot view is fetch → shape → `Table`.
The fetch is `catalog.fetch[T]`,
the single read path: `kubectl get … -o json`, the list-or-single asymmetry,
the kind guard and keeping the raw document all live there, so a view states its read as one call.
Shaping — columns, sort order, emphasis — stays ordinary Go next to the view,
because it is domain, not presentation.
Presentation begins where the `Result` is handed to render/tui.

## View kinds

| Kind | Behaviour | Status |
|---|---|---|
| `snapshot` | run once, render result | everything built so far |
| `stream` | long-running, cancellable | pending: following logs |
| `external` | side effects outside the terminal | pending: `argo ui` |

There is no `mutate` kind.
Mutation is a property of an action, not of a view.

## Actions

An action operates on **what the view is showing**: the result,
minus the filter, minus the selection.
In the TUI the selection is the highlighted row once the output pane has focus;
on the command line it is the view's parameter.
Both modes therefore reach the same two cases:

| | TUI | CLI |
|---|---|---|
| one | tab into the table, pick a row | `argo apps root --action pause` |
| several | `/` filter | `argo apps --action pause` |

Its input is the rows the view produced — filtered and sorted — re-serialised.
Not the raw command output and not the rendered table:

- the raw output behind a filtered view is *everything in the cluster*;
  feeding it to an action would act on the cluster, not on the screen
- the rendered table is lossy,
  so a cosmetic column change would silently alter what an action operates on

Serialised as a kubectl `List`,
`kubectl patch -f -` then touches exactly the rows that were on screen — no selector that can match something else.

An action that returns `Navigate` moves to another view instead of reporting.
The TUI pushes the target onto a stack that `esc` unwinds; the CLI runs it and prints.
One Run signature covers both.

Consequences worth stating:

- Confirmations name the count ("Pause 14 applications?") and the cluster.
  The engine adds the cluster, not each action, so it cannot be forgotten.
- The selected row is highlighted without its emphasis colours: a highlight has to survive the whole line,
  and the reset at the end of a coloured cell would cut it short.

## Sorting

Two shapes of view, two orders:

- **Scheduled events** — upgrade jobs, suspensions — sort by time, latest first.
  They are a calendar; ordering by state would move rows on every reload.
  What needs attention is picked out by colour.
  The one exception is a suspension in force right now,
  pinned to the top, because that is the question its view exists to answer.
- **Objects with a health state** — applications, nodes,
  pools — sort by trouble: broken before healthy, then by name.
  The broken ones are what the view is opened for.

## Times

Points in time are shown as timestamps (`2026-08-18 11:48`), spans as durations (`9h`, `2d1h`).
A relative age cannot be lined up against a log line or another view's column,
and correlating those is most of what this tool is for.

Wall-clock input becomes RFC 3339 in exactly one place,
`catalog.ParseTime` / `catalog.FormatTime`; nothing else formats a timestamp for an API.

## Results are data, not text

`Run` never returns a formatted string.
It returns `Table` (columns plus rows;
rows carry their raw source documents), `Text` (plain lines) or `Navigate`.
A `Stream` type joins them with the stream kind.
The CLI and the TUI render the same values,
which is what keeps the two modes from growing separate code paths.

## Layout

```
cmd/          cobra wiring; the generated-inventory test
internal/
  catalog/    domain: View, Action, Registry, Result; the builtin groups
  kube/       kubectl invocation seam (exec + JSON decode)
  render/     Table/Text → styled lines, shared by CLI and TUI
  tui/        bubbletea model, panes, keymap
docs/
```

`internal/kube` exists so a later move to client-go touches one package instead of every action.

## TUI notes

- Bubbletea **v2**,
  imported from `charm.land/bubbletea/v2` (not the old `github.com/charmbracelet/…` path,
  which fails for v2).
  Same for `charm.land/bubbles/v2` and `charm.land/lipgloss/v2`.
- `Update` never blocks.
  Every kubectl call is a `tea.Cmd`.
- Streams (pending) use a goroutine feeding a channel plus the recursive "await next message" pattern — not `tea.Exec`,
  which suspends the TUI.
  Line buffers are bounded.
- Background processes (port-forward,
  pending) are bound to the model lifecycle and killed on quit, not to a shell trap.

## Non-goals

- Managing clusters.
  Anything beyond diagnosis stays with `kubectl`/`oc`.
- Not a k9s replacement: read-mostly,
  every mutation behind a confirmation that names the number of objects it touches.
- Windows support.
- Replacing `kubectl` as the API client (see [platform.md](platform.md)).

## Decisions

| Decision | Reason |
|---|---|
| Registry as single source | Otherwise every command is maintained three times |
| `Result` types, not strings | The only way CLI and TUI share one `Run` |
| argv lists, not shell strings | Parameters come from user input; string interpolation is an injection hole |
| `kubectl -o json` + Go structs, not client-go | CRDs would need generated clients; kubectl keeps kubeconfig, context and auth plugins working |
| Actions hang off views, never standalone | A mutation should act on a set you have looked at |
| Actions take the view's rows, not raw output | The raw output behind a filtered view is everything in the cluster |
| Selection narrows, rather than being its own concept | The filter already did this; `--action` reaches the same cases through the view parameter |
| Navigation is a result type | Keeps one Run signature; the TUI pushes it, the CLI runs it |
| Confirmations state the count | A bulk mutation must name the number of objects before it runs |
| Views are Go, not config | The engine costs about what writing every view costs, and loses the error messages |
| Namespaces and `--as` are constants | Properties of the platform, not choices; a knob nobody turns is ballast |
| Reads do not impersonate | Impersonation needs a verb not everyone has; requiring it for reads locks people out |
| One table renderer for both modes | Styling after layout is the only way escape codes do not shift columns |
| Tables are never truncated | A diagnostic tool that hides the end of a message is worse than one you scroll; the TUI scrolls sideways with `<` and `>` |
