# Architecture

## One pipeline per view

Every view describes the same pipeline:

```
script  →  JSON value stream  →  rows expression  →  column projection  →  sort  →  table
(any command)   (stdout)            (jq)                (jq per column)   (declarative)
```

The engine knows nothing about kubectl.
It knows "run this script, expect JSON on stdout" —
every piece of domain knowledge lives in the hat.
A `class: text` view is the same pipeline with the shaping left out:
the script's lines are the answer.

## The stdout contract

A source's stdout is a **stream of JSON values**, not exactly one document.
The `rows` expression runs over each value in the stream
and the results are concatenated —
exactly jq's semantics, where a program runs once per input document.
That makes multi-source views trivial:

```yaml
source:
  script: |
    kubectl get applications -A -o json
    curl -sf https://example.test/api/v1/things
rows: .items[]
```

A single document is just the stream of length one.
Scripts that need real merging, joins or enrichment,
are free to use `jq` themselves —
the no-shelling-out rule below binds this tool's Go code, not the scripts.

## Sources are scripts

`source.script` is the only source form.
It runs through `sh -c` by default —
POSIX sh from `PATH`, never a hardcoded bash:
Alpine has no bash, NixOS has no fixed path.
Who wants bashisms or `pipefail` overrides per view:

```yaml
source:
  shell: [bash, -euo, pipefail, -c]
  script: |
    ...
```

Worth knowing: in POSIX sh, `a | b` reports `b`'s exit status,
so a failing left end goes unnoticed.
Use the bash override with `pipefail` when that matters.

A view's parameters reach the script as **environment variables**,
never templated into its text —
splicing user input into code is an injection hole;
an environment variable is not.

## Expressions are jq

`rows`, `key` and every column `expr` are jq programs,
executed with [gojq](https://github.com/itchyny/gojq) —
pure Go, nothing shells out.
Expressions can be developed against real command output with real jq
and pasted into the hat unchanged.

## Actions

A view produces rows;
an **action** does something with the rows it is showing.
There is one mechanism, with two kinds:

| kind | declared by | operates on | result |
|---|---|---|---|
| navigate | `open:` | the **cursor row**, always | opens the target view in a new tab |
| run | `script:` | the **selection** — refuses when there is none | opens a tab showing the commands, then their output |
| run, `scope: none` | `script:` | **nothing** — a create, or one working from the view's parameters | the same tab, with one step |

Both kinds open a tab.
`esc` closes it either way.

An action may declare `params:` of its own —
values the view cannot supply,
such as the name and dates of something being created.
They are **collected in the tab**, one field at a time,
before anything is planned:
the values are what make the command concrete,
and a command that does not exist yet cannot be shown.
So one tab carries the whole interaction, in order:

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

The form and the question are tab content, which scrolls,
so neither can push the footer off a short window —
and a multi-line confirmation,
the kind that carries the warning that is the point of asking,
has room.
A default is prefilled rather than applied invisibly;
a required field refuses to be empty in place;
`esc` abandons the whole thing and closes the tab.

A `scope: none` action may still declare `env:`,
evaluated against a null input so its expressions work from `$args` —
which is how a create turns wall clock into RFC 3339 without `date`,
whose flags differ between GNU and BSD.
An expression that reaches for the row yields empty there,
and that is not silent:
every value is printed in the tab before anything runs.

Drill-down is therefore not a feature of its own:
it is an action that navigates.
A dedicated `drilldown:` schema would have to be un-built
the moment someone wants to cordon a node.

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

A run action **opens a tab of its own**
showing the invocations it is about to make, one per selected row,
before anything runs —
and the confirmation sits under them,
so the question is asked with the command in plain sight.
`y` runs them and the output appears beside each invocation;
`esc` closes the tab and returns to the view.

That tab is why planning is separate from running in the engine:
`Plan` turns rows into `Step`s without executing any,
and `Step.Run` executes one.
A step is deliberately **not** rendered as a single shell-quoted line,
which would be a fiction — the values never enter the script text.
It shows the environment, the shell, and the script as written:

```
pause  infra
  name=infra
  sh -c
    kubectl patch machineconfigpool "$name" --type merge -p '…'
  machineconfigpool/infra patched
```

One step per row means each call names exactly one object,
so a partial failure is attributable —
"2 of 5 steps failed" closes the tab's listing —
and there is no selector that could match something else.
`reload: true` re-runs the source view in **its own** tab,
so the fresh rows are already there when one escapes back to them.

Two deliberate departures from the previous generation of this tool:

- **A mutation requires an explicit selection.**
  The old rule was "selection narrows, rather than being its own concept" —
  the *filter* was the selector,
  so `/OutOfSync` plus the action key hit all fourteen.
  Nothing is lost by reversing it,
  because `a` selects every row the filter left:
  the bulk case is `/OutOfSync`, `a`, `p`,
  and that one extra keystroke is the whole point.
  A forgotten filter can no longer turn a mutation loose.
- **A navigation always uses the cursor row.**
  The old tool had to refuse when 0 or several rows were in scope
  (`select one pool first (14 shown)`);
  with a row cursor that error disappears.

`confirm` supplies the verb; the **engine appends the count**,
so a confirmation that fails to say how much it touches cannot be written.

Parameters and `env` values reach a script as **environment variables**,
never templated into it.
Two rules follow from that:

- names must match `[a-z][a-z0-9_]*` —
  lowercase-only keeps them clear of `PATH`, `HOME` and `IFS`
  without a list of forbidden names;
- use `if [ -n "$x" ]; then set -- …; fi`, **not** `[ -n "$x" ] && set -- …`:
  the `&&` form leaves exit status 1 when the parameter is empty,
  which the runner would report as a failed script.

There is no `hidden:` flag.
A view with a **required** parameter cannot be started from the menu —
running it bare would show some arbitrary object as if it were the answer —
so it is simply not listed, and an action reaches it.

## Config schema

```yaml
views:
  - id: argo.apps            # unique, required; the part before the first
                             # dot groups the sidebar, the rest is the menu
                             # label — an id without a dot is ungrouped
    title: Argo applications # description, shown in the title bar; default: id
    class: table             # optional; table (default), text or chart
    refresh: 30s             # optional auto-reload; manual r always works
    follow: false            # optional; text only, and excludes refresh
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

- **Sorting compares the raw jq values**, before formatting:
  numbers sort numerically, RFC 3339 timestamps chronologically.
  `rank` lists values from worst to best; anything unlisted sorts last.
- **Emphasis matches the raw value**, before formatting, per column —
  the same value may mean different things in different columns.
- **`key` names a row across reloads.**
  The selection is keyed by it;
  without a key expression the rendered cells stand in,
  and a reload that changes the display can lose the selection.
- **Formats pass unparseable values through** instead of hiding them.

## Text views

`class: text` takes the script's stdout as lines rather than as JSON.
It is what a log is:
no `rows`, no `columns`, no `key`, no `sort` —
those are refused rather than ignored,
because a `rows:` nothing reads is a misunderstanding worth naming.

The lines arrive **verbatim**.
Nothing is collapsed, and no column is padded around them:
in a log the indentation is part of what is being read,
which is exactly why a log cannot be a one-column table.

What still applies is what does not depend on rows.
The filter greps the lines, and says how many of how many matched —
which is most of what one does with a log.
`↑↓`, `⇞⇟` and `home`/`end` scroll rather than moving a row cursor.
What does not apply is everything that needs a row:
there is no selection, no sticky header,
and a view of this class may only carry `scope: none` actions,
since there is no cursor row to navigate from and no selection to work on.

### Following

`follow: true` consumes the script's output **as it is written**
rather than when the script exits.
Only a text view may:
a table is sorted and column-aligned over the whole set,
so it has no way to grow a row at a time.
It rules out `refresh`, for the same reason a live thing is not polled,
and `reload: true` on any of its actions:
following resumes by itself on the way back to the tab,
and a reload from a job tab would start the stream
behind a tab nobody is looking at.

The engine keeps the two paths apart rather than blurring them.
A snapshot view has a `Run` returning a `Result`;
a followed view has a `Stream` returning a channel of `Chunk`s,
and no `Run` at all.
Nothing has to guess which it is holding.
The chunks reach the TUI as messages,
so the reading still happens outside `Update` like every other kind of work.

Three consequences are worth stating,
because each is a decision rather than a detail:

- **Following pauses in the background**, the way a refresh does,
  so there is never more than one script running per tab
  and none at all behind a tab nobody is looking at.
  Lines from the meantime are lost,
  which is why a followed view passes something like `--tail`:
  the source has to bring some of the past back when it starts again.
- **A stream that stops says so.**
  The script ending means the thing being followed went away,
  which is a finding, not a malfunction —
  it is reported under the last line, with `r` to start again.
  Stopping it by hand with `esc` says something else,
  since being told "the stream ended" would explain nothing one did not just do.
- **What it keeps is capped**, at 5000 lines.
  A log followed overnight would otherwise be a leak with a scrollbar.

Everything else about a text view holds while it follows:
the filter greps what has arrived, and the arrows scroll it.
New lines pin the view to the bottom **only when it is already there** —
a reader who has scrolled up is reading,
and must not have the view yanked out from under them.

The two shipped log views follow, and read `--tail 5000` on the way in.
One of them pipes through `grep`, which needs `--line-buffered`:
a pipe makes grep hold its output back in 4k blocks,
and a followed log delivered in blocks is a followed log that looks stuck.

## Chart views

`class: chart` reads the same JSON stream as a table,
but `rows` shapes it into **series** rather than rows:

```yaml
- id: nodes.cpu
  class: chart
  yrange: [0, 100]        # optional; omitted, the axis scales to the data
  source:
    script: |
      curl -sf … /api/v1/query_range …
  rows: .data.result[] | {name: .metric.instance, points: .values}
```

Each value the expression yields must be a series object —
`{name, points: [[t, v], …]}` —
which is exactly what a Prometheus `query_range` response reduces to
in one jq step.
The engine meets the data where it is:
`t` is unix seconds or an RFC 3339 string,
`v` a number **or a string holding one**,
because strings are how Prometheus encodes samples in JSON.
Prometheus itself is never named in Go —
where the numbers come from is hat business,
which is why the class is called `chart` and not `prometheus`.

The series are drawn as a braille time-series plot
(via [ntcharts](https://github.com/NimbleMarkets/ntcharts), the one chart
dependency), with a legend naming each series in its colour.
Three semantics are decisions rather than details:

- **Script order is draw order, later series on top.**
  The engine never reorders series
  (points are sorted by time; nothing else is reshaped),
  and the renderer draws them in that order deliberately —
  `DrawBrailleAll` would iterate a Go map and roll the z-order per repaint.
  A hat that pairs a `max_over_time` band with an `avg_over_time` line
  emits max first, so the spikes lie under the line that tells the trend.
- **Spikes must survive the query, not the renderer.**
  The braille path draws every point,
  but a `query_range` step samples —
  a spike between two steps never reaches the tool at all.
  A hat that wants outliers visible over a week aggregates per bucket
  (`max_over_time(m[30m:5m])` beside `avg_over_time`)
  instead of hoping the renderer can show what the query dropped.
- **`yrange` fixes the value axis.**
  Two charts of a bounded quantity should read against the same scale,
  and a pool of idle nodes hugging a 0–100 axis *is* the answer.
  Left out, the axis scales to the data.

A series name appearing again extends that series,
so a script may deliver one node's week in chunks.
A malformed series becomes a note and is skipped,
and unreadable points are dropped with one note per series —
the same philosophy as a broken column:
a data problem at run time must not take down the view.

What holds for a text view holds here, for the same reason — no rows:
no selection, no cursor, only `scope: none` actions,
and the arrows scroll.
The filter picks **series by name** —
`/worker` is how one node's line is read out of a pool.
`refresh` works; `follow` does not, since a plot is drawn whole.
The chart is rendered to the pane's size and redrawn on resize;
`rincewind view` prints it at a fixed 100×20,
coloured only when stdout is a terminal.

## Copying the pane

`Y` puts what the output pane is showing on the system clipboard.

**OSC 52, and a local writer when there is one.**
OSC 52 — the terminal's own clipboard escape — was meant to be the whole
mechanism, and it is the only thing that reaches the right machine over ssh.
It does not survive contact with Terminal.app, which has never implemented it;
the macOS clipboard is NSPasteboard, which needs cgo, and the build is static.
So the no-shelling-out rule takes an exception here, and only here:
its premise is that each forbidden program has a Go replacement,
and for the clipboard that replacement does not exist.

The writer is **probed on `PATH`** — `pbcopy`, `wl-copy`, `xclip`, `xsel` —
rather than picked by `runtime.GOOS`,
because what matters is whether the program is there;
a Mac without `pbcopy` and a Wayland session without `wl-copy` both exist.
It is skipped when `SSH_CONNECTION` or `SSH_TTY` is set:
an `xclip` on the far end would copy into a clipboard nobody can paste from,
which is worse than not copying,
and ssh is precisely the case OSC 52 covers.

**A selection narrows it, and no selection means all of it.**
Copying is not a mutation,
so the rule that a mutation refuses without a selection does not apply —
but the notice says which happened,
because "copied 3 rows" and "copied 41 rows" is the whole difference.
The filter narrows it too, since the filter is what the pane is showing.

What goes over is **plain text**:
the header with the rows, so a table of values does not arrive without its
column names;
a log verbatim, indentation kept;
and a job's commands and output with the styling and the padding taken off,
which is how what the tool just ran reaches a ticket or a shell.

**A large copy says how large — but only when OSC 52 is alone.**
It hands the text to the terminal as one escape sequence,
terminals cap how long one may be,
and the write is one-way:
reading the clipboard back is what terminals disable for security,
so there is no way to ask whether it arrived.
tmux also needs `set-clipboard on` to pass it through.
A local writer has neither problem —
no length to warn about, and an exit status that says whether it worked,
which is then the only thing reported,
since a notice that claimed the copy has to be taken back if it failed.

## Printing a view

`rincewind view <id>` runs one view and prints what it produces, without the TUI.
A table goes out column-aligned, a text view verbatim,
and a followed view streams until its script ends or it is interrupted.

A **subcommand, not a `--view` flag.**
A flag describes how a command works;
this changes what the command *is* —
not "the TUI, but with something" but "no TUI at all",
which is what `check` and `version` already are.
The id is an argument rather than a flag value,
future switches like an output format hang off `view`
instead of off a root command they mean nothing to,
and cobra supplies the usage line, the argument check
and completion of the view name.

The same `Result` goes to both renderers,
which is what stops the TUI and the CLI from growing separate ideas of a table.
Two things differ, and both follow from the output not being a screen this tool owns:

- **Colour only for a terminal.**
  Asked of the writer, not of the platform:
  piping into a file is the case that matters,
  and it looks the same on every OS.
  Styling still happens after the layout, so the columns line up either way.
- **Notes go to stderr**, and the table to stdout.
  A broken expression is a diagnostic;
  it must not land in the file someone is piping into.

A script that fails takes the command down with it,
carrying its stderr, because a non-zero exit is what a pipeline reads.
There is no deadline: the TUI needs one because an unreachable API server
would leave a spinner turning with no way back,
where out here Ctrl-C is the way back —
and it cancels the context rather than killing the process,
so a followed script is stopped rather than orphaned.

**Parameters are not passed yet, and there is no filter.**
A view that requires a parameter is refused
rather than run on a value it made up,
and the refusal names both the missing parameter and the views that do work.
Narrowing the output is the other half of the same gap:
in the TUI `/` is what narrows a view,
and out here `grep` is not a substitute,
because it takes the header with the rows it drops.
See the decisions table.

## The cluster

The kubectl context is shown **top right**, in the title bar,
because acting on the wrong cluster is the expensive mistake this tool can help make.
The right edge is the same column on every screen,
where a left-aligned label would shift with the length of the view id beside it —
which is what lets it be found without being read.

It is asked for by running `kubectl config current-context`,
which is the one place in this tool's Go code that names a producer.
The exception is deliberate and narrow.
Everywhere else kubectl is hat business,
but *which* cluster one is pointed at cannot come from the hat:
a hat is a set of views, and the answer changes without any of them changing.
Reading it from the same kubeconfig the scripts read
is what makes the display and the scripts agree.

**The context, not reachability.**
`current-context` reads `~/.kube/config` and answers in tens of milliseconds;
asking the API server whether it is up costs a round trip on every view change,
and would blank the display exactly when a cluster is unwell —
which is when knowing which cluster it is matters most.
Whether it answers at all is what running a view already tells you.

**Asked again whenever the view on screen changes.**
Once at startup would go stale
the moment someone switches context in another terminal,
and stale is worse than absent for something read before a mutation.
A two-second TTL collapses a held-down tab key into one run.
The hook sits in one place — the key handler compares the view before and after —
rather than in `open`, `drill`, `activate` and `closeTab`,
which would drift apart.

**The title yields to it, and a bar too narrow for both drops it whole.**
What the view is called matters less than which cluster it is showing;
and half of two similar context names is the same half,
so a clipped one is worse than none.

**No answer means nothing is shown**, and no room is taken.
No kubectl, or no current context, is not a cluster session;
a probe that fails has nothing one could act on,
and a broken cluster is reported by the views themselves.
`example`, which invents its data, is the case to keep in mind:
what is shown is the context the scripts *would* use,
which is the truth a hat cannot override.

## Getting a hat

A hat is the product: sixteen views for one cluster,
written once and useful to everyone who operates it.
`rincewind hats` is how one travels from a repository to the store —
`search`, `install`, `list`, `update`, `remove`.

### A hat is code

This outranks everything else in this section.
A hat is a directory of shell scripts that this tool runs
against your cluster, with your credentials, when you press a key.
`rincewind hats install someone/their-hat` is `curl | sh` with a nicer noun,
and the topic search makes that worse rather than better:
**anyone can add a topic to their repository**,
so the list is discovery, never curation.

So the tool's own rule — *the command is on screen before it runs* —
holds one level up as well:

- `install` names the repository and where it will go,
  says in one line that a hat is code that will run as you,
  and asks. `--yes` is the way past it, for scripts.
- `update` **fetches, shows `git diff --stat`, and asks** before moving anything.
  A silent update of code that runs against a production cluster
  is the worst version of this feature.
- Nothing updates itself. No background fetch, no check on start.
- `install` runs `config.Load` on every hat it found,
  so a hat that does not load fails while you are installing it
  rather than the first time you press a key.
  One broken hat in a repository of five is named and the other four kept;
  only when none loads is the clone removed again.

### git, GitHub, and the two exceptions

**Cloned with git**, not fetched as a tarball.
The clone is what makes `git diff` and `git log` work on an installed hat
afterwards, which is how one answers "what changed and who changed it"
without this tool being involved.
That makes git the second exception to the no-shelling-out rule,
after the clipboard, and it is probed on `PATH` the same way.
Full clones, not `--depth 1`: hats are YAML, the clone is tiny,
and a shallow one breaks `pull --ff-only` after a force-push.

**GitHub only**, which is the same exception the cluster probe makes for
kubectl: a hat cannot say where hats are found,
and the topic search exists nowhere else.
`owner/repo` and a `github.com` URL are the two spellings accepted;
anything else is refused by name rather than half-supported.

**Cloned over ssh, falling back to https.**
A hat repository is as often private as not,
and an ssh key is the credential people already have —
where https would want a token this tool will never ask for,
since a prompt in a command nobody is watching is a hang, not a question.
So `install` names both spellings of the repository
and tries them in order:

```
  from   git@github.com:vshn/rincewind-hats.git
  else   https://github.com/vshn/rincewind-hats
  into   ~/.config/rincewind/vshn-rincewind-hats
```

Three things make that safe rather than merely clever:

- **ssh is offered only when there is ssh to run.**
  Probed on `PATH`, like the clipboard writer and like git itself —
  a machine without it is shown one url, not a plan half of which is fiction.
  The listing and the attempt come from one function,
  so what is printed cannot differ from what runs.
- **The ssh attempt cannot hang.**
  `GIT_TERMINAL_PROMPT=0` silences git, but ssh has prompts of its own —
  an unknown host key, a passphrase — and reads them from the terminal,
  so the attempt adds `BatchMode=yes`.
  A transport that cannot work has to fail rather than wait,
  or the fallback never gets its turn.
  Whatever ssh command the user already configured is *extended* rather than
  replaced, in git's own order — `$GIT_SSH_COMMAND`, then `core.sshCommand` —
  because a custom one is usually the identity that makes ssh work at all.
- **Only the clone chooses.**
  The url that won becomes the clone's origin,
  so `update` and every `git -C` afterwards keep using it
  without this tool remembering anything.

When both fail, both are reported, each with what git said about it:
"it failed" without saying which half failed how
is the report that sends someone looking in the wrong place.

`update` uses `pull --ff-only` and so **refuses on local modifications**.
That is the point rather than a limitation —
a change someone made to a hat is theirs —
and the message points at `git -C <path> stash` for whoever wants to keep it.

### The store is flat

```
~/.config/rincewind/
  vshn-rincewind-hats/       ← the clone, named owner-repo
    albedaran/
```

Flat, because the path is what goes into `RINCEWIND_HAT`
and a person has to be able to read it back out of their own shell history.
A directory holding `.git` is a clone; anything else in there is not,
which lets the store stay an ordinary config directory.

Where the store lives follows the same order the hat does:
`--dir`, then `$RINCEWIND_HATS_DIR`, then `~/.config/rincewind`.
The variable is spelled `_DIR` on purpose:
`RINCEWIND_HATS` would be one letter from `RINCEWIND_HAT`
and mean something entirely different —
one picks the hat to run, the other says where clones land.

### What counts as a hat in a repository

Three layouts, tried in order, because each is what someone would write:

- `hats/<name>/` — a repository that is something else as well,
  the way this one holds its own hats beside the source;
- `<name>/` at the root — a repository that is nothing but hats,
  where a `hats/` level would only repeat the repository's own name;
- `*.yaml` at the root — one repository, one hat.

Dot directories are never hats: `.git` and `.github` both hold YAML.

## The hat

The views are pulled out of a **hat**:
the directory named by `--hat`, else `$RINCEWIND_HAT`.
There is no third step, and that is deliberate.
`~/.config/rincewind/` used to be a hat itself and is now the store,
which it cannot be both:
a directory of hats is not a directory of views.
Guessing one of the installed hats would be worse than asking,
since which cluster one is pointed at
is exactly what this tool should not decide quietly —
so with neither given it refuses, naming both ways to choose
and, when something is installed, the line to paste.
Every `*.yaml` in the hat is read, in name order,
which keeps one file per group —
`argo.yaml`, `upgrade.yaml`, `nodes.yaml`, `alerts.yaml`.
Actions are linked once every file has been read,
so a view may open one declared in another.
Naming a single file works too, for a hat with one thing in it.

One hat is one set of views,
so a cluster that needs its own gets its own —
and since a hat travels, it gets its own repository too:
the albedaran views live in `vshn/rincewind-hats`,
installed with `rincewind hats install`.
`example` stays here, and is the odd one out —
every view in it invents its own data,
so it runs anywhere and is what the tests are pointed at.

`--hat` has no short form.
`-h` is help, everywhere, and a tool that takes it for something else
costs every newcomer the same moment of confusion.
It was the hat once, on the argument that help is asked for once
and the hat on every invocation —
which stopped being true when `$RINCEWIND_HAT` became how one is chosen
for good, leaving only the broken convention.

## Errors: the price of a config engine, paid deliberately

The previous incarnation of this tool kept views as Go builtins because
"typed structs give errors like `application "x" not found`
where a generic expression engine gives a stack of nils".
Reversing that decision means answering it:

1. **Load-time validation.**
   Every expression is compiled at startup,
   sort keys are checked against column names,
   ids for uniqueness, enums for their values;
   unknown YAML fields are rejected with a line number.
   A typo dies before any script runs, naming the view and the field.
   `rincewind check` runs just this.
2. **Runtime failures are per cell, not per view.**
   A column expression that errors renders `!err` in that cell
   and one note below the table;
   the other columns and rows stay usable.
3. **Script failures show the script's stderr**, not a Go stack:
   the script's own message is the useful one.
4. **A failed run replaces nothing.**
   A view that has produced rows keeps them:
   the failure is said under them, in full,
   and a marker on the status line names the time the rows are from.
   Only a view that has never produced anything
   gives the whole pane to the reason.
   One unreachable API server used to take a standing table away
   for a whole `refresh` interval,
   and pressing `esc` mid-reload left an empty view behind without saying so.
   The cost is that an action can act on rows that have gone stale;
   the marker is what makes that a decision rather than a surprise.

## Results are data, not text

A view's `Run` returns a `Table` —
columns, rows with their raw source objects and identity keys, notes.
Rendering to styled lines happens in `internal/render`, in exactly one place,
styling only after the layout is fixed:
escape sequences count towards the width of everything that measures naively
— text/tabwriter, bubbles/table —
which is why cells never contain escape codes
and why the table widget is not `bubbles/table`.

## Selection and tabs

`space` toggles a row, `a` toggles everything the filter left, `esc` clears.
The selection is a set of row **keys**,
so it survives reloads and re-sorting;
keys that vanish from the result are pruned.

A navigating action opens its target in a **new tab**
in the content box's top border.
Tabs are one path, not a heap of windows:
drilling from tab N truncates whatever was opened from it,
and picking a view in the menu collapses back to one.
`]` and `[` move between them, `esc` and `x` close one,
and a click activates one.

Each tab keeps its own fetched table,
filter, selection, row cursor and scroll position,
so switching is instant and coming back lands exactly where one left.
That is the point of tabs:
the old view stack re-ran the parent on the way back and lost all four.
`r` answers staleness.

An auto-`refresh` **pauses** while its tab is in the background —
five tabs on a ten-second refresh
is five concurrent scripts against a cluster nobody is watching —
and catches up on return if the data aged past the interval.
A run in flight when its tab goes to the background finishes into that tab
and only redraws when the tab comes forward.

## Layout

```
cmd/               cobra wiring: root (TUI), check, view, hats, --hat
internal/
  config/          YAML → typed defs; schema validation; hands off to engine
  engine/          Def → compiled View; script runner, JSON stream decode,
                   gojq evaluation, sort, formats; Result/Row/Cell types,
                   and the line stream a followed view is read through
  hats/            the store: GitHub sources, clone scanning, git, search
  render/          Result → styled lines (style-after-layout)
  tui/             bubbletea model: sidebar, table pane, filter, selection
example/           a hat that invents its data, and the config fixture
```

## TUI notes

- Bubbletea **v2**, imported from `charm.land/bubbletea/v2`
  (the `github.com/charmbracelet/…` path fails for v2).
  Same for bubbles and lipgloss.
- `Update` never blocks.
  Every script run is a `tea.Cmd`;
  a `seq` counter discards results of superseded runs,
  and one spinner tick chain is kept alive at a time.
- The run timeout (30s) is the TUI's policy; the engine just honours ctx.
- The screen is framed boxes:
  the title across the top;
  the menu down the left;
  the output and, under it, the view's actions in a box of their own;
  a split footer — version on the menu's width, key legend on the output's.
  The menu is one box spanning the output and the action box beside it.
  A view with no actions gets no empty frame:
  three lines to say so would be three lines of table.
- The frames are drawn by hand rather than with lipgloss borders,
  because the content box carries labels *inside* its top border:
  the tabs on the left and the scroll position on the right,
  where neither costs a row of the table it describes.
- The action box holds the key legend and nothing else.
  A confirmation belongs to the job tab, under the command it is about,
  where the two are read in one glance.
- The action legend sheds its descriptions before it sheds an action:
  knowing that `p` pauses beats knowing what pausing means,
  and a clipped line tells neither.
- Tab layout and tab hit-testing come from one function
  returning both the bar and the spans, as the sidebar's do,
  so what the mouse believes and what the screen shows cannot drift apart.
  Chips are built plain and measured before being styled —
  an escape sequence counted as width would put every hit one column out.
- The table header is held **outside** the viewport
  and painted onto the box's first row,
  shifted by the viewport's own `XOffset`.
  Scrolling a long table must never cost the column names.
  Both panes scroll:
  the viewport follows the row cursor to the edge rather than recentring,
  and the sidebar windows onto the menu the same way.
- **The menu is a ring; the table is not.**
  A single step off either end of the menu comes round to the other,
  because the menu is short and visible whole,
  so the far end is nearer than the way back.
  Neither paging nor the row cursor wraps:
  a page that came round would land in the middle after running off the end,
  which reads as a miss;
  and a table of two hundred rows is not visible whole,
  so losing one's place in it is not a shortcut.
- The key legend is weighted:
  when the footer box is too narrow the lightest keys are dropped
  rather than the line being clipped mid-word.
- The mouse acts on **what it points at**, not on what has the keyboard:
  the wheel over the table scrolls the table even while the menu is focused.
  Hit-testing counts from the same layout constants the renderer draws with,
  so the two cannot drift;
  the tests aim at lines found in the rendered output
  rather than at numbers worked out a second time.
- Capturing the mouse costs the terminal's own text selection,
  which matters for a tool people copy node names out of.
  `MouseMode` is a field of the frame in bubbletea v2,
  so `M` toggling a bool is the whole mechanism —
  released, the terminal handles selection and copying as usual.
- Tests drive `model.Update` with synthetic messages
  and assert on `View().Content` — no terminal, no golden files.
  The fixture views are built in memory;
  one of them sleeps,
  which is how the tests check that the UI keeps painting
  while work is in flight.

## Non-goals

- Managing clusters.
  Anything beyond diagnosis stays with `kubectl`/`oc`.
- Windows support.
- A plugin API.
  The hat plus arbitrary scripts is the extension mechanism.

## Decisions

| Decision | Reason |
|---|---|
| Following is text-only | A table is sorted and column-aligned over the whole set, so it has no way to grow a row at a time |
| A followed view has a `Stream` and no `Run` | Two return shapes behind one field would leave every caller guessing which it holds |
| Following pauses with its tab in the background | One script per tab, none behind a tab nobody is looking at; the `--tail` a real view passes brings the recent past back on return |
| A stream that ends says so, and says it differently from one stopped by hand | The script ending means the thing being followed went away, which is a finding; being told that after pressing esc would explain nothing one did not just do |
| New lines pin to the bottom only from the bottom | A reader who has scrolled up is reading |
| A text view keeps its lines verbatim | Collapsing whitespace or padding a column would destroy the indentation a log is read by; that is why it is a class of its own rather than a one-column table |
| A text view refuses rows, columns, sort and key | Accepting a field nothing reads teaches the wrong thing about the schema |
| The chart class is `chart`, not `prometheus` or `metrics` | The class names the shape of the output, never the producer; where the numbers come from is hat business, like kubectl |
| Chart series stay in script order, drawn later-on-top | The z-order is a statement — max band under avg line — and a map iteration would roll it per repaint |
| Outliers are the query's job, not the renderer's | The renderer draws every point it gets, but a sampling query drops the spike before the tool ever sees it; `max_over_time` per bucket is the hat's answer |
| `yrange` may fix a chart's value axis | Two charts of one bounded quantity should read against the same scale; an idle pool hugging 0–100 is the answer, not a display problem |
| The chart filter picks series by name | /worker reading one node out of a pool is the chart's version of grep |
| ntcharts draws the plot | Braille doubles the resolution and it is built on the same charm.land v2 stack; hand-rolling a line chart is real work the dependency already did |
| Views are config, not Go | The tool's owner wants to add views without recompiling; the error-message cost is paid explicitly (see above) |
| Script is the only source form | One execution path; the hat shows exactly what runs, shell choice included |
| stdout is a JSON stream | Multi-source views without forcing the script to merge |
| jq (gojq) as the only expression language | Users already know it; expressions are developed with real jq and pasted in |
| Parameters as env vars, never templated | Splicing user input into script text is an injection hole |
| Drill-down is an action, not its own feature | The old tool proved one mechanism carries both; a `drilldown:` schema would be un-built the first time someone wants to cordon a node |
| Mutations require an explicit selection | Reverses the old "the filter is the selector"; `a` still selects everything the filter left, so nothing is lost but the accident |
| Navigation follows the cursor row | Removes the old "select one pool first" refusal entirely |
| An action runs once per row | Each call names one object: partial failure is reportable, and no selector can match something else |
| A working action gets a tab, and shows its commands before running | Confirming blind is no confirmation; and the output has room to be read and scrolled instead of clipped |
| The parameter form and the question live in that same tab | The values must be collected before the command exists; putting either in the chrome would need variable-height layout and could not hold a multi-line question |
| An action parameter may not shadow the view's or an `env:` name | Then the precedence is unobservable, and there is one fewer rule to remember |
| Per-row invocations replace the old `kubectl -f -` List document | Each call names one object, so a partial failure is attributable |
| The argo "root rider" is an instruction, not a hidden mutation | The old pause silently patched `root` too; a mutation nobody selected is what the selection rule exists to prevent |
| A hat is a directory, one file per group | Sixteen views in one file is unreadable; it is also how they were arranged as Go source |
| `--hat` has no short form; `-h` is help | The frequency argument that once justified taking `-h` went away when `$RINCEWIND_HAT` became the way to choose a hat: what is left is a universal convention, broken for three saved characters |
| The mouse toggle is `M`, not `m` | Lower-case letters belong to the views, which have far more use for them than one global toggle |
| Planning is separate from running in the engine | The command can only be shown up front if producing it executes nothing |
| A step is shown as env + shell + script, not one quoted line | The values never enter the script text; a quoted one-liner would misrepresent what runs |
| The engine appends the count to a confirmation | A confirmation that fails to say how much it touches cannot be written |
| Config validated against the target view at load | The old `no such view` was a run-time surprise; so was an argument nothing read |
| The five list views lost their optional filter parameters in the port | In the TUI `/` does that job better; the debt is the CLI's, where `view` has no way to narrow output at all |
| A required parameter hides a view from the menu | Running it bare would show an arbitrary object as if it were the answer; no `hidden:` flag needed |
| Sort on raw values, before formatting | Rendered cells order neither numbers nor dates correctly |
| Emphasis per column, on raw values | One value may mean different things in different columns |
| Selection is a set of keys | Indices die on every reload; keys survive reordering |
| `Run` returns data, never formatted strings | A string cannot be filtered, sorted or restyled |
| One table renderer, styling after layout | Escape codes in cells misalign everything that measures naively |
| Cell errors render `!err` + note, view survives | One broken expression must not take down 199 good rows |
| A failed run keeps the last good rows, marked stale | A blip on a refreshing view would otherwise replace the answer with the reason for half a minute; what is on screen is worth more than the certainty that it is current, as long as the screen says which it is |
| No shelling out to jq/column/… from Go | Each has a Go replacement; scripts in a hat may do what they like |
| …except the clipboard | OSC 52 was that replacement and Terminal.app does not implement it; NSPasteboard needs cgo and the build is static, so the premise fails and the rule yields rather than the feature |
| The clipboard writer is probed on PATH | A Mac without pbcopy and a Wayland session without wl-copy both exist; the platform name answers a different question |
| Hats are distributed, not vendored | A hat is the product; sixteen views for one cluster are useful to everyone who operates it, and cloning this repository to get them makes a hat something you have rather than something you can hand over |
| `install` and `update` ask, and `update` shows the diff first | A hat is code that runs as you against your cluster; the rule that a command is on screen before it runs has to hold at install time too |
| The topic search is discovery, not a registry | Anyone can add a topic to their repository, and presenting the result as blessed would be the lie that makes the rest dangerous |
| Cloned with git, not fetched as a tarball | `git diff` and `git log` keep working on an installed hat afterwards, which is how "what changed" is answered without this tool |
| git is the second exception to no-shelling-out | go-git is a large dependency for four commands, and having git there afterwards is the reason a clone beats a tarball |
| GitHub only | The same exception the cluster probe makes for kubectl: a hat cannot say where hats are found, and the topic search exists nowhere else |
| ssh first, https as the fallback | A hat repository is as often private as not, and an ssh key is the credential people already have; https would want a token this tool will not prompt for |
| ssh is offered only when ssh is on PATH | A capability probe, like the clipboard writer — showing a plan half of which cannot run is worse than showing one url |
| The ssh attempt adds `BatchMode=yes` | `GIT_TERMINAL_PROMPT=0` does not reach ssh's own host-key and passphrase prompts; a transport that cannot work has to fail rather than wait, or the fallback never gets its turn |
| A configured ssh command is extended, not replaced | It is usually the identity that makes ssh work at all, so overriding it would defeat the preference it expresses |
| The url that won becomes origin | Update and every later `git -C` keep using it, so nothing has to remember which transport was chosen |
| Both failures are reported together | "It failed" without saying which half failed how sends someone looking in the wrong place |
| The store is flat, `owner-repo` | The path is what goes into `RINCEWIND_HAT`, and it has to be readable back out of a shell history |
| `$RINCEWIND_HATS_DIR`, not `RINCEWIND_HATS` | One letter from `RINCEWIND_HAT` and a different meaning: one picks the hat, the other says where clones land |
| No default hat beyond the variable | No config file, no symlink, no directory that doubles as a hat; the shell already scopes a variable to a project or a terminal |
| The cluster comes from kubectl, not from the hat | A hat is a set of views; which cluster one is pointed at changes without any of them changing, and reading the same kubeconfig the scripts read is what makes the two agree |
| The context, not reachability | Instant and local, where a round trip per view change would blank the display exactly when a cluster is unwell; running a view already proves whether it answers |
| Re-probed when the view on screen changes | Once at startup goes stale the moment someone switches context in another terminal, and stale is worse than absent before a mutation |
| A failed probe shows nothing | There is nothing to act on, and a broken cluster is reported by the views themselves |
| Probe capabilities, not `runtime.GOOS` | Environments lie less than platform names |
