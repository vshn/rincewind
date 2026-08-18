package catalog

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/vshn/aldediggi/internal/kube"
)

// dnsLabel is what Kubernetes accepts as a resource name. Checked here so a
// typo comes back as a sentence rather than as an API server rejection.
var dnsLabel = regexp.MustCompile(`^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`)

func createSuspensionAction(c kube.Client) Action {
	return Action{
		Name:    "create",
		Key:     "n",
		Short:   "Create an upgrade suspension",
		Scope:   Params,
		Refresh: true,
		Confirm: func(_ Result, args Args) string { return describeSuspension(args, time.Now()) },
		Params: []Param{
			{Name: "name", Short: "suspension name", Required: true},
			{Name: "reason", Short: "why upgrades are suspended", Required: true},
			{Name: "start", Short: "start, e.g. 2026-05-25 09:00", Required: true},
			{Name: "end", Short: "end, e.g. 2026-05-25 18:00 or +9h", Required: true},
		},

		Run: func(ctx context.Context, _ Result, args Args) (Result, error) {
			spec, err := suspensionFrom(args)
			if err != nil {
				return nil, err
			}

			doc, err := json.Marshal(spec.object())
			if err != nil {
				return nil, err
			}

			// create, not apply: a name collision should be noticed, not
			// silently overwrite a window somebody else is relying on.
			out, err := c.Input(ctx, doc, "create", "-f", "-")
			if err != nil {
				return nil, err
			}
			return Text{Lines: splitLines(string(out))}, nil
		},
	}
}

func deleteSuspensionAction(c kube.Client) Action {
	return Action{
		Name:    "delete",
		Key:     "d",
		Short:   "Delete the listed upgrade suspensions",
		Scope:   Rows,
		Refresh: true,
		Confirm: func(in Result, _ Args) string {
			names, fromArgo := suspensionsIn(in)
			msg := fmt.Sprintf("Delete %d upgrade suspension(s): %s?", len(names), strings.Join(names, ", "))
			if len(fromArgo) > 0 {
				msg += fmt.Sprintf("\n\n%s come(s) from git; ArgoCD will recreate them on the next sync.",
					strings.Join(fromArgo, ", "))
			}
			return msg
		},
		Run: func(ctx context.Context, in Result, _ Args) (Result, error) {
			t, ok := in.(Table)
			if !ok {
				return nil, fmt.Errorf("this action needs a table, got %T", in)
			}
			sources := t.Sources()
			if len(sources) == 0 {
				return nil, fmt.Errorf("nothing to delete")
			}

			list, err := kube.ListOf(sources)
			if err != nil {
				return nil, err
			}
			out, err := c.Input(ctx, list, "delete", "-f", "-")
			if err != nil {
				return nil, err
			}
			return Text{Lines: splitLines(string(out))}, nil
		},
	}
}

// newSuspension is a validated request, ready to be sent. Distinct from
// `suspension`, which is what comes back from the cluster.
type newSuspension struct {
	name   string
	reason string
	start  time.Time
	end    time.Time
}

// suspensionFrom validates the parameters and resolves the times. Everything that
// can be wrong is caught here, so the API server never sees nonsense and the
// message names the field.
func suspensionFrom(args Args) (newSuspension, error) {
	w := newSuspension{
		name:   strings.TrimSpace(args.Get("name")),
		reason: strings.TrimSpace(args.Get("reason")),
	}

	if !dnsLabel.MatchString(w.name) {
		return w, fmt.Errorf("name %q must be lower-case letters, digits and dashes", w.name)
	}
	if w.reason == "" {
		return w, fmt.Errorf("reason must not be empty")
	}

	var err error
	if w.start, err = ParseTime(args.Get("start"), time.Time{}); err != nil {
		return w, fmt.Errorf("start: %w", err)
	}
	// Relative ends count from the start, which is the only reading that
	// makes sense for a window.
	if w.end, err = ParseTime(args.Get("end"), w.start); err != nil {
		return w, fmt.Errorf("end: %w", err)
	}
	if !w.end.After(w.start) {
		return w, fmt.Errorf("end %s is not after start %s", showTime(w.end), showTime(w.start))
	}

	return w, nil
}

// object builds the resource. The ArgoCD instance label is deliberately absent:
// carrying it would hand the window to ArgoCD, which then prunes it on the next
// sync because it is not in git.
func (w newSuspension) object() map[string]any {
	return map[string]any{
		"apiVersion": suspensionAPIVersion,
		"kind":       suspensionKind,
		"metadata": map[string]any{
			"name":   w.name,
			"labels": map[string]string{managedByLabel: managedByValue},
		},
		"spec": map[string]any{
			"start":  FormatTime(w.start),
			"end":    FormatTime(w.end),
			"reason": w.reason,
			// Empty selectors mean every job and every config, which is what
			// a maintenance window is for.
			"configSelector": map[string]any{},
			"jobSelector":    map[string]any{},
		},
	}
}

// describeSuspension is the confirmation text: what will actually be sent, offsets
// and duration included, because those are what a typo gets wrong.
func describeSuspension(args Args, now time.Time) string {
	w, err := suspensionFrom(args)
	if err != nil {
		return "Cannot create this suspension: " + err.Error()
	}

	msg := fmt.Sprintf("Create upgrade suspension %q?\n\n  %s  →  %s   (%s)\n  reason: %s",
		w.name, FormatTime(w.start), FormatTime(w.end), showDuration(w.end.Sub(w.start)), w.reason)

	if w.end.Before(now) {
		msg += "\n\nThis suspension is entirely in the past."
	}
	return msg
}

// suspensionsIn reads the names back out of the rows, and separates the ones
// that ArgoCD owns.
func suspensionsIn(in Result) (names, fromArgo []string) {
	for _, w := range rowsIn[suspension](in) {
		names = append(names, w.Metadata.Name)
		if w.source() == "argocd" {
			fromArgo = append(fromArgo, w.Metadata.Name)
		}
	}
	return names, fromArgo
}

// openConditions is the drill-down bound to enter: the controller's own account
// of where a job got to.
func openConditions() Action {
	return Action{
		Name:  "conditions",
		Key:   "enter",
		Short: "Show this job's conditions",
		Scope: Rows,
		Run: func(_ context.Context, in Result, args Args) (Result, error) {
			job, err := oneJob(in, args)
			if err != nil {
				return nil, err
			}
			return Navigate{View: "upgrade.conditions", Args: Args{"job": job}}, nil
		},
	}
}

func openHooks() Action {
	return Action{
		Name:  "hooks",
		Key:   "h",
		Short: "Show this job's hooks",
		Scope: Rows,
		Run: func(_ context.Context, in Result, args Args) (Result, error) {
			job, err := oneJob(in, args)
			if err != nil {
				return nil, err
			}
			return Navigate{View: "upgrade.hooks", Args: Args{"job": job}}, nil
		},
	}
}

// oneJob resolves which job to drill into: the selected row, or — when the
// rows are already a single job's conditions or hooks — the view's parameter.
func oneJob(in Result, args Args) (string, error) {
	if job := args.Get("job"); job != "" {
		return job, nil
	}

	names := namesIn[upgradeJob](in)
	switch len(names) {
	case 1:
		return names[0], nil
	case 0:
		return "", fmt.Errorf("no upgrade job selected")
	default:
		return "", fmt.Errorf("select one job first (%d shown; tab into the list, or filter)", len(names))
	}
}

// poolPauseAction stops or resumes a MachineConfigPool's rollout. Pausing one
// is the usual way to hold a node reboot until a maintenance window, and a
// pool left paused is the usual reason an upgrade never finishes.
func poolPauseAction(c kube.Client, pause bool) Action {
	name, key, verb := "unpause", "P", "Resume"
	patch := `{"spec":{"paused":false}}`
	if pause {
		name, key, verb = "pause", "p", "Pause"
		patch = `{"spec":{"paused":true}}`
	}

	return Action{
		Name:    name,
		Key:     key,
		Short:   verb + " the rollout of the listed pools",
		Scope:   Rows,
		Refresh: true,
		Confirm: func(in Result, _ Args) string {
			names := namesIn[machineConfigPool](in)
			msg := fmt.Sprintf("%s the rollout of %d pool(s): %s?", verb, len(names), joinNames(names))
			if pause {
				msg += "\n\nNodes in a paused pool keep their current config until it is resumed."
			}
			return msg
		},
		Run: func(ctx context.Context, in Result, _ Args) (Result, error) {
			return patchRows(ctx, c, in, patch)
		},
	}
}
