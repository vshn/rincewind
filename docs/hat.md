# Writing a hat

A hat is a directory of YAML files.
Every `*.yaml` in it is read, in name order;
views and contexts merge into one hat,
and links — `open:` targets, `needs:` ids — resolve across files.
A single file works too.

[example/](../example/) is the reference hat:
it invents its own data, runs without a cluster,
and between its files it uses every part of this schema.
Read it beside this page.

`rincewind check --hat <dir>` validates a hat.
Every mistake below dies there, naming the view and the field,
before any script runs.

## The contract

A source script prints a **stream of JSON values** on stdout —
not necessarily one document.
`rows` runs once per value (jq semantics) and the results concatenate,
so a script may print from several sources without merging them.

`rows`, `key`, every column `expr`, `active`, `label`,
`confirm` and the `env`/`args` values are jq programs, run with gojq.
Develop them against real output with real jq and paste them in unchanged.

Parameters reach the script as **environment variables**,
never templated into its text.
Names are lowercase (`[a-z][a-z0-9_]*`) so they cannot shadow `PATH` or `HOME`.
The script also inherits the caller's environment whole —
which is where a secret belongs:
parameters are printed in the job tab and in `act`'s transcript,
the inherited environment is not.

## Contexts

Contexts say where one is:
their labels fill the chrome top right,
and views that name them via `needs:` leave the menu where they do not apply.
[example/services.yaml](../example/services.yaml) declares all three kinds —
identity, condition that holds, condition that does not.

```yaml
contexts:
  - id: cloudscale        # required, unique across the hat; [a-z][a-z0-9_-]*
    short: Cloudscale-managed clusters    # optional; shown by check
    source:
      shell: [sh, -c]     # optional; this is the default
      script: kubectl config view --minify -o json   # required; prints JSON
    active: '."current-context" | test("-cloudscale-")'
                          # optional jq predicate; absent means active
                          # whenever the script produced a value
    label: '"cloudscale"' # optional jq; what it adds to the chrome
    tools: [cloudscale-cli]   # optional; reported by check, gates nothing
```

At least one of `active:` or `label:` is required.
A context has three states — active, inactive, unknown (the probe failed) —
and unknown hides nothing:
a view is hidden only when **every** context it names is definitively
inactive, and a hidden view still runs by name (`view`, `act`).
Contexts resolve once per view change, behind a 2s TTL;
identical `source` blocks run once per round.

## Views

```yaml
views:
  - id: argo.apps         # required, unique; the part before the first dot
                          # groups the sidebar, the rest is the menu label;
                          # no leading or trailing dot
    title: Argo applications   # optional; the title bar text, default: id
    class: table          # optional; table (default) | text | chart
    needs: cloudscale     # optional; a context id or a list, no repeats
    refresh: 30s          # optional auto-reload, >= 1s; excludes follow
    follow: false         # optional; class text only, excludes refresh
    params:               # optional; collected as $name, see below
      - name: app         # required; [a-z][a-z0-9_]*
        short: application name  # optional; labels the prompt and -p errors
        default: ""       # optional; prefilled, never applied invisibly
        required: false   # optional; a required param unlists the view —
                          # an action's args or -p reaches it
    source:
      shell: [sh, -c]     # optional; this is the default
      script: |           # required
        kubectl get applications -A -o json
    rows: .items[]        # required for table and chart; refused for text
    key: .metadata.name   # optional row identity; selection survives reloads
                          # by it, and `act` refuses a view without one
    columns:              # table only; at least one, names unique
      - name: SYNC        # required
        expr: .status.sync.status    # required; jq over the row
        format: timestamp # optional; timestamp | age;
                          # unparseable values pass through unformatted
        emphasis: { Synced: good, OutOfSync: bad, Unknown: warn }
                          # optional; exact raw value -> good | warn | bad
    sort:                 # table only; multi-key, stable, on raw jq values
      - by: SYNC          # a column name
        rank: [OutOfSync, Unknown, Synced]   # worst to best; unlisted last
      - by: NAME
        order: asc        # asc (default) | desc; rank xor order
    yrange: [0, 100]      # chart only; low then high; absent fits the data
    actions: []           # see below
```

Sorting and emphasis read the **raw** jq value, before formatting:
numbers sort numerically, RFC 3339 timestamps chronologically,
and the same value may carry different emphasis in different columns.

Per class:

- **table** — the default; everything above applies.
- **text** — the script's lines are the answer, verbatim;
  `rows`, `columns`, `key` and `sort` are refused rather than ignored.
  `/` greps the lines; actions need `scope: none`.
  `follow: true` reads the lines as the script writes them;
  leaving the tab stops the script and returning restarts it,
  so a real source passes something like `--tail`.
  See `demo.log` and `demo.tail` in
  [example/services.yaml](../example/services.yaml).
- **chart** — `rows` yields series objects
  `{name, points: [[t, v], …]}`;
  `t` is unix seconds or RFC 3339, `v` a number or a string holding one.
  Series keep script order, later drawn on top;
  a name appearing again extends its series.
  `columns`, `key` and `sort` are refused; `/` picks series by name;
  actions need `scope: none`.
  See [example/metrics.yaml](../example/metrics.yaml).

## Actions

One mechanism, two kinds, and both open a tab (`esc` closes it):
`open:` navigates to another view and acts on the **cursor row**;
`script:` runs once per **selected** row and refuses without a selection.
Both kinds are used throughout
[example/services.yaml](../example/services.yaml).

```yaml
actions:
  - key: enter            # required, unique in the view, and not one of the
                          # reserved keys the TUI answers itself:
                          # q ctrl+c / tab shift+tab esc arrows j k
                          # pgup pgdown ctrl+f ctrl+b home end g G
                          # r space a H M Y [ ] x < >
    name: resources       # required; `act <view> <name>` calls it by this
    short: Show this app's resources   # optional; the action bar's text
    open: argo.syncstatus # the target view; must exist
    args:                 # jq over the cursor row; every name must be a
      app: .metadata.name # declared param of the target view

  - key: p
    name: pause
    short: Pause the selected pools
    script: kubectl patch machineconfigpool "$name" --type merge -p '…'
    shell: [bash, -euo, pipefail, -c]   # optional; default sh -c
    env:                  # jq per row -> environment variables
      name: .metadata.name
    confirm: '"Pause \(map(.metadata.name) | join(", "))"'
                          # optional jq over the row array; the engine
                          # appends the row count itself
    reload: true          # optional; re-runs the view after the action;
                          # refused on a followed view
    scope: rows           # rows (default) | none
    params: []            # as on a view; collected in the job tab first
```

The two kinds keep their fields apart, and mixing is refused by name:
`open` takes only `args`;
`env`, `confirm`, `reload`, `scope` and `params` belong to `script`.

`scope: none` acts on nothing — the create case.
Its `env` expressions run against null and work from `$args`,
the collected parameters —
see the `deploy` action in
[example/services.yaml](../example/services.yaml).
An action's param may not repeat a view param or an `env` name:
both would set the same variable.

## From the command line

`rincewind view <id> -p name=value` prints a view;
`rincewind act <view> <action> [key…]` runs a script action,
rows named by their `key` (or `--all`, or `-` from stdin).
`needs:` does not gate either — asking by name is deliberate.
Navigating actions are refused there;
the printed hint composes the `view` call instead.
