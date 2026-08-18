package catalog

import (
	"context"
	"fmt"
	"strings"

	"github.com/vshn/aldediggi/internal/kube"
)

// rootApp is Project Syn's app-of-apps. Pausing a child without pausing root
// achieves nothing, because root syncs the change straight back. The zsh
// script encoded this by silently patching root alongside every target; here
// the rule is stated in the confirmation instead of hidden in the command.
const rootApp = "root"

// Patches touch only spec.syncPolicy.automated. The zsh script replaced the
// whole syncPolicy, destroying syncOptions and retry with no way back. A merge
// patch with null also makes pause idempotent, where a JSON-patch remove would
// fail on an app that is already paused.
const (
	patchPause   = `{"spec":{"syncPolicy":{"automated":null}}}`
	patchUnpause = `{"spec":{"syncPolicy":{"automated":{"prune":true,"selfHeal":true}}}}`
)

// openSyncStatus is the drill-down bound to enter: pick an application, see
// what its resources are doing.
func openSyncStatus() Action {
	return Action{
		Name:  "syncstatus",
		Key:   "enter",
		Short: "Show this application's resources",
		Scope: Rows,
		Run: func(_ context.Context, in Result, _ Args) (Result, error) {
			app, err := oneApp(in)
			if err != nil {
				return nil, err
			}
			return Navigate{View: "argo.syncstatus", Args: Args{"app": app}}, nil
		},
	}
}

func openLogs() Action {
	return Action{
		Name:  "logs",
		Key:   "l",
		Short: "Show controller log lines about this application",
		Scope: Rows,
		Run: func(_ context.Context, in Result, args Args) (Result, error) {
			// From syncstatus the rows are resources, so the application is
			// the one the view was asked about.
			app := args.Get("app")
			if app == "" {
				var err error
				if app, err = oneApp(in); err != nil {
					return nil, err
				}
			}
			return Navigate{View: "argo.logs", Args: Args{"app": app}}, nil
		},
	}
}

// oneApp insists on a single application. Drilling into several at once has
// no meaning, and the message says how to narrow it down.
func oneApp(in Result) (string, error) {
	names := namesIn[application](in)
	switch len(names) {
	case 1:
		return names[0], nil
	case 0:
		return "", fmt.Errorf("no application selected")
	default:
		return "", fmt.Errorf("select one application first (%d shown; tab into the list, or filter)", len(names))
	}
}

func pauseAction(c kube.Client) Action {
	return Action{
		Name:    "pause",
		Key:     "p",
		Scope:   Rows,
		Short:   "Pause auto-sync on the listed applications",
		Refresh: true,
		Confirm: func(in Result, _ Args) string {
			names := namesIn[application](in)
			if hasRoot(names) {
				return fmt.Sprintf("Pause auto-sync on %s?", describeApps(names))
			}
			return fmt.Sprintf("Pause auto-sync on %s, and on %q?\n\nThe app-of-apps has to be paused too, or it reverts the change.",
				describeApps(names), rootApp)
		},
		// root rides along: pausing a child alone achieves nothing.
		Run: patchApps(c, patchPause, rootApp),
	}
}

func unpauseAction(c kube.Client) Action {
	return Action{
		Name:    "unpause",
		Key:     "P",
		Scope:   Rows,
		Short:   "Restore auto-sync on the listed applications",
		Refresh: true,
		Confirm: func(in Result, _ Args) string {
			// Says "set", not "restore": the previous value is not recorded
			// anywhere, so this writes the default. See docs/views.md.
			msg := fmt.Sprintf("Set auto-sync (prune, selfHeal) on %s?", describeApps(namesIn[application](in)))
			if !hasRoot(namesIn[application](in)) {
				msg += fmt.Sprintf("\n\n%q stays paused; unpause it separately when you are done.", rootApp)
			}
			return msg
		},
		// Deliberately not adding root: resuming the app-of-apps is the last
		// step of a piece of work, and belongs to a separate decision.
		Run: patchApps(c, patchUnpause),
	}
}

const refreshAnnotation = "argocd.argoproj.io/refresh=normal"

// refreshRowsAction reconciles every application the view listed.
func refreshRowsAction(c kube.Client) Action {
	return Action{
		Name:    "refresh",
		Key:     "R",
		Short:   "Ask ArgoCD to reconcile the listed applications now",
		Scope:   Rows,
		Refresh: true,
		Run: func(ctx context.Context, in Result, _ Args) (Result, error) {
			return runOnRows(ctx, in, func(list []byte) ([]byte, error) {
				return c.Input(ctx, list, "annotate", "-f", "-", refreshAnnotation, "--overwrite")
			})
		},
	}
}

// refreshAppAction reconciles the application the view was asked about. On
// syncstatus the rows are that app's resources, so acting on them would be
// both wrong and impossible — they carry no objects of their own.
func refreshAppAction(c kube.Client) Action {
	return Action{
		Name:    "refresh",
		Key:     "R",
		Short:   "Ask ArgoCD to reconcile this application now",
		Scope:   Params,
		Refresh: true,
		Run: func(ctx context.Context, _ Result, args Args) (Result, error) {
			app := args.Get("app")
			if app == "" {
				return nil, fmt.Errorf("no application to refresh")
			}
			out, err := c.Output(ctx, "annotate", "applications.argoproj.io", app,
				refreshAnnotation, "--overwrite")
			if err != nil {
				return nil, err
			}
			return Text{Lines: splitLines(string(out))}, nil
		},
	}
}

// patchApps builds an action that merge-patches every application the view
// showed. `kubectl patch -f -` takes a List on stdin, so the set acted on is
// exactly the set on screen — no selector that might match something else.
func patchApps(c kube.Client, patch string, also ...string) func(context.Context, Result, Args) (Result, error) {
	return func(ctx context.Context, in Result, _ Args) (Result, error) {
		out, err := patchRows(ctx, c, in, patch)
		if err != nil {
			return nil, err
		}

		// Named applications that always come along, patched by name because
		// kubectl takes either -f or a name, not both.
		lines := out.(Text).Lines
		for _, name := range also {
			if hasRoot(namesIn[application](in)) && name == rootApp {
				continue // already in the set
			}
			extra, err := c.Output(ctx, "patch", "applications.argoproj.io", name, "--type", "merge", "-p", patch)
			if err != nil {
				return nil, err
			}
			lines = append(lines, splitLines(string(extra))...)
		}
		return Text{Lines: lines}, nil
	}
}

func hasRoot(names []string) bool {
	for _, n := range names {
		if n == rootApp {
			return true
		}
	}
	return false
}

// describeApps names the applications when there are few enough to read, and
// falls back to a count. With no row selection the count is the only brake, so
// it is always present.
func describeApps(names []string) string {
	switch {
	case len(names) == 0:
		return "no applications"
	case len(names) == 1:
		return fmt.Sprintf("1 application (%s)", names[0])
	case len(names) <= 5:
		return fmt.Sprintf("%d applications (%s)", len(names), strings.Join(names, ", "))
	default:
		return fmt.Sprintf("%d applications (%s, …)", len(names), strings.Join(names[:3], ", "))
	}
}
