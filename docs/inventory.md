# Command inventory

Generated from the registry — do not edit. Regenerate with
`go test ./cmd -run TestInventory -update`; CI fails when this file is stale.

## argo — All things ArgoCD related

### argo apps — List ArgoCD applications and their sync state

Parameters:

- `app` (optional) — limit to one application

Actions:

- `syncstatus` (key `enter`) — Show this application's resources. Acts on the listed rows.
- `logs` (key `l`) — Show controller log lines about this application. Acts on the listed rows.
- `pause` (key `p`) — Pause auto-sync on the listed applications. Acts on the listed rows; asks first; re-runs the view.
- `unpause` (key `P`) — Restore auto-sync on the listed applications. Acts on the listed rows; asks first; re-runs the view.
- `refresh` (key `R`) — Ask ArgoCD to reconcile the listed applications now. Acts on the listed rows; re-runs the view.

### argo syncstatus — Show the resources of one application and their sync state

Reached by drilling in, not listed in the TUI.

Parameters:

- `app` (required) — application name

Actions:

- `logs` (key `l`) — Show controller log lines about this application. Acts on the listed rows.
- `refresh` (key `R`) — Ask ArgoCD to reconcile this application now. Acts on the view's parameter; re-runs the view.

### argo logs — Show application-controller log lines about one application

Reached by drilling in, not listed in the TUI.

Parameters:

- `app` (required) — application name


## upgrade — All things cluster upgrade related

### upgrade jobs — List UpgradeJobs and where each one got to

Parameters:

- `job` (optional) — upgrade job name

Actions:

- `conditions` (key `enter`) — Show this job's conditions. Acts on the listed rows.
- `hooks` (key `h`) — Show this job's hooks. Acts on the listed rows.

### upgrade conditions — Show one UpgradeJob's conditions, as the controller wrote them

Reached by drilling in, not listed in the TUI.

Parameters:

- `job` (required) — upgrade job name

Actions:

- `hooks` (key `h`) — Show this job's hooks. Acts on the listed rows.

### upgrade hooks — Show one UpgradeJob's hooks

Reached by drilling in, not listed in the TUI.

Parameters:

- `job` (required) — upgrade job name

Actions:

- `conditions` (key `enter`) — Show this job's conditions. Acts on the listed rows.

### upgrade suspensions — List upgrade suspensions

Actions:

- `create` (key `n`) — Create an upgrade suspension. Acts on the view's parameter; asks first; re-runs the view.
  Parameters:

- `name` (required) — suspension name
- `reason` (required) — why upgrades are suspended
- `start` (required) — start, e.g. 2026-05-25 09:00
- `end` (required) — end, e.g. 2026-05-25 18:00 or +9h

- `delete` (key `d`) — Delete the listed upgrade suspensions. Acts on the listed rows; asks first; re-runs the view.

### upgrade progress — Show whether the cluster is upgrading, and what it is waiting on


### upgrade logs — Show machine-config-controller logs


## nodes — All things node related

### nodes status — List nodes: kubelet, scheduling and machine-config rollout

Parameters:

- `selector` (optional) — label selector, as kubectl -l takes it

Actions:

- `conditions` (key `enter`) — Show this node's conditions. Acts on the listed rows.
- `config` (key `m`) — Show this node's machine-config rollout. Acts on the listed rows.
- `cordon` (key `c`) — Stop scheduling on the listed nodes. Acts on the listed rows; asks first; re-runs the view.
- `uncordon` (key `C`) — Allow scheduling on the listed nodes. Acts on the listed rows; asks first; re-runs the view.

### nodes conditions — Show one node's conditions, as the kubelet wrote them

Reached by drilling in, not listed in the TUI.

Parameters:

- `node` (required) — node name

Actions:

- `config` (key `m`) — Show this node's machine-config rollout. Acts on the listed rows.

### nodes config — Show one node's machine-config rollout in full

Reached by drilling in, not listed in the TUI.

Parameters:

- `node` (required) — node name

Actions:

- `conditions` (key `enter`) — Show this node's conditions. Acts on the listed rows.

### nodes pools — List MachineConfigPools and their rollout

Parameters:

- `pool` (optional) — limit to one pool

Actions:

- `nodes` (key `enter`) — Show the nodes in this pool. Acts on the listed rows.
- `pause` (key `p`) — Pause the rollout of the listed pools. Acts on the listed rows; asks first; re-runs the view.
- `unpause` (key `P`) — Resume the rollout of the listed pools. Acts on the listed rows; asks first; re-runs the view.
