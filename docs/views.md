# View semantics

What the views show and the decisions behind them.
What each view and action takes is in [inventory.md](inventory.md) (generated);
how to build one is in [howto-add-view.md](howto-add-view.md).

| Group | Status |
|---|---|
| `argo` | built; `ui` pending (needs `internal/platform`) |
| `upgrade` | built |
| `nodes` | built |
| `alerts` | built |
| `loki`, `pods` | not built yet |

## argo

```
NAME               SYNC       HEALTH       AUTO    REVISION  MESSAGE
cluster-tools      OutOfSync  Degraded     PAUSED  a3f9c21   deployment has unavailable replicas
openshift-logging  Synced     Progressing  on      a3f9c21   waiting for rollout
root               Synced     Healthy      on      a3f9c21
```

`AUTO` is what pause and unpause toggle, so it has to be visible.

- **Pause takes `root` along, unpause does not.**
  `root` is Project Syn's app-of-apps: unpaused, it reverts a child's change immediately.
  Resuming it is the last step of a piece of work and a separate decision.
  The confirmations say both.
- **Unpause sets the default (`prune`, `selfHeal`),
  not the previous value** — nothing records the old one.
  Restoring it for real needs read-modify-write: save the old block to an annotation first.
  Later, if wanted.
- **`argo logs APP` is a snapshot**, the controller log filtered to one application.
  After looking at a broken app the question is what already happened;
  following waits for the stream kind.
- **No `sync` action** until it is decided whether the tool stays read-mostly.

## upgrade

Two controllers share the name: the appuio upgrade controller and OpenShift's machine-config operator.
Each view says which one it means.

```
NAME                            VERSION  STATE      START             HOOKS  MESSAGE
monday-1400-4-20-33-1787572800  4.20.33  Pending    2026-08-24 12:00
friday-0200-4-20-31-1786320000  4.20.31  Failed     2026-08-10 02:00  1/2    pre-upgrade health check failed
skipped-4-20-30-1785715200      4.20.30  Expired    2026-08-03 02:00         start window closed at 2026-08-03 03:00
```

- **`jobs` derives STATE from the conditions**,
  checking each one's status as well as its type — a `Succeeded` condition saying `"False"` is a failure.
  `Expired` means the job never started and its `startBefore` has passed.
  Where the derivation guesses,
  `conditions` (`⏎`) shows the controller's own account verbatim and cannot be wrong.
- **`suspensions`,
  not "windows"** — "maintenance window" already means something else on this platform.
  A suspension in force right now is pinned to the top; the rest is a calendar, latest first.
- **A created suspension carries `app.kubernetes.io/managed-by: aldediggi` and never the ArgoCD instance label.**
  With that label ArgoCD prunes the resource on the next sync — probably at the moment it is needed.
  `SOURCE` reads these labels and says whether deleting a suspension achieves anything.
- **`create`,
  not `apply`:** a name collision should fail rather than silently overwrite a suspension somebody relies on.
  Its selectors are `{}` — every job, every config; anything finer belongs in git.
- **Times:** wall-clock input is read in the machine's local zone,
  an end may be written `+9h` to count from the start,
  and the confirmation shows the resolved timestamps — a daylight-saving mistake is visible before it is sent.
- **`progress` leads with a verdict** (`idle` / `in progress` / `blocked`) above the raw ClusterVersion conditions.
  Without it a quiet cluster reads as broken rather than as quiet.
- **Condition tables carry no colour.**
  `True` is good on `Available` and bad on `Failing`; colouring the bare value would be a lie.
  Interpretation lives in derived rows and STATE columns, and those are coloured.

## nodes

```
NAME           ROLE                  READY     SINCE             SCHED     CONFIG    VERSION  MESSAGE
worker-03      worker                NotReady  2026-08-18 11:48            Working   v1.31.4  kubelet stopped posting node status
worker-07      worker                Ready     2026-08-18 09:00  cordoned  Degraded  v1.31.4  failed to drain node: blocked by PDB
master-000000  control-plane,master  Ready     2026-04-28 09:00            Done      v1.31.5  kubelet is posting ready status
```

`SINCE` is when Ready last changed — a timestamp, not an age,
so it lines up against job start times and log lines.
`SCHED` shows a forgotten cordon, which blocks a drain indefinitely.
`MESSAGE` prefers the operator's drain reason on a degraded node over the kubelet's pleasantries.

### CONFIG

The node's own `machineconfiguration.openshift.io/state` annotation cannot be trusted: a node never told about a new config still says `Done` while sitting on the old one — the normal state of every node in a paused pool.
Each node is therefore judged against its pool's rendered target (`status.configuration.name`),
which updates even while the pool is paused:

| CONFIG | Means |
|---|---|
| `Done` | `currentConfig` matches the pool's target |
| `pending` | told to change, not finished |
| `behind` | told nothing, but the pool has moved on without it |
| `Working` / `Degraded` | straight from the state annotation |

Node→pool matching follows the operator's own rule,
not selector specificity: `master` wins outright,
any custom pool beats `worker`, which is only the fallback for nodes no custom pool claims.
(In these clusters the worker pool exists but holds nothing;
specificity would tie and hand nodes to whichever pool sorts first.)
If the pools cannot be read,
CONFIG falls back to the node's own account rather than failing the view.

`nodes config NODE` shows the full rendered-config names side by side,
which is where the gap becomes readable;
`nodes pools` drills into `nodes status -l <selector>` on `⏎`.

- **No `drain` action.**
  It blocks for minutes, can hang on a PodDisruptionBudget,
  and during an upgrade the machine-config operator drains anyway.
  A command that appears to hang is worse in a diagnostic tool than a missing one;
  the cordon confirmation says the workload stays.
- **A pool's `matchExpressions` are refused,
  not ignored** — rendering only the `matchLabels` half would list the wrong nodes,
  which is worse than saying no.

## alerts

Every call is `kubectl exec` into the Alertmanager pod, running `amtool` with JSON output.
There is no unprivileged way in — `pod/exec` is a privileged verb — so this is the one group whose reads impersonate;
the reads-do-not-impersonate invariant documents the exception.
The flip side is a non-goal worth stating: while Alertmanager itself is down, this group is blind.

```
ALERT             SEVERITY  STATE     SINCE             NAMESPACE    SUMMARY
pipeline                    ok                                       the Watchdog alert is firing
KubeNodeNotReady  critical  firing    2026-08-18 09:48  kube-system  worker-03 not ready
TargetDown        warning   silenced  2026-08-17 09:12  syn          10% of targets down
```

- **The first row is a pipeline verdict.**
  The `Watchdog` alert must always fire;
  when it is missing, the alerting pipeline itself is broken and an empty list would be a lie.
  The verdict is judged before any filter, so a narrowed view cannot cry wolf.
- **`silenced` and `inhibited` alerts stay in the list.**
  A silenced alert is still firing, and during an upgrade those are exactly the ones worth seeing.
  Alertmanager lumps both under "suppressed"; the STATE column names the reason, because the next step differs.
- **Silencing an alert matches on `alertname` plus `namespace`, not on all labels.**
  All labels would pin the silence to a pod name and die on the next restart.
  When several shown alerts share the alertname, the matcher falls back to the alertname alone,
  which is how the CLI silences an alert firing in more than one namespace.
  The confirmation shows the matchers, so what gets muted is visible before it happens.
- **The author is the local user**, because amtool's default would be whoever runs inside the pod: nobody.
- **`expire` skips already-expired silences** rather than letting one stale row fail the whole call.
- **No silence editing.**
  Changing one means expire and recreate, until an `extend` action earns its keep.
