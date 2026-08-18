package catalog

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"time"

	"github.com/vshn/aldediggi/internal/kube"
)

// upgradeJob is the slice of an UpgradeJob this tool needs.
type upgradeJob struct {
	Metadata struct {
		Name   string            `json:"name"`
		Labels map[string]string `json:"labels"`
	} `json:"metadata"`
	Spec struct {
		DesiredVersion struct {
			Version string `json:"version"`
		} `json:"desiredVersion"`
		StartAfter  string `json:"startAfter"`
		StartBefore string `json:"startBefore"`
	} `json:"spec"`
	Status struct {
		Conditions  []jobCondition `json:"conditions"`
		HookTracker []hookEntry    `json:"hookTracker"`
	} `json:"status"`

	raw json.RawMessage
}

type jobCondition struct {
	Type               string `json:"type"`
	Status             string `json:"status"`
	Reason             string `json:"reason"`
	Message            string `json:"message"`
	LastTransitionTime string `json:"lastTransitionTime"`
}

type hookEntry struct {
	HookEvent      string `json:"hookEvent"`
	Status         string `json:"status"`
	UpgradeJobHook string `json:"upgradeJobHook"`
}

// condition finds one by type. A condition that has never transitioned is
// simply absent, which is what makes a pending job look empty.
func (j upgradeJob) name() string              { return j.Metadata.Name }
func (j *upgradeJob) keep(doc json.RawMessage) { j.raw = doc }

func (j upgradeJob) condition(kind string) (jobCondition, bool) {
	for _, c := range j.Status.Conditions {
		if c.Type == kind {
			return c, true
		}
	}
	return jobCondition{}, false
}

func (j upgradeJob) isTrue(kind string) bool {
	c, ok := j.condition(kind)
	return ok && c.Status == "True"
}

// state derives what the job is doing. Deliberately defensive: the conditions
// are checked by type *and* status, because a `Succeeded` condition that says
// "False" is the opposite of success — and the zsh version, which only looked
// for a condition's presence, dropped any job that had none at all.
//
// Where this guesses, `upgrade conditions` shows the raw truth.
func (j upgradeJob) state(now time.Time) (string, string) {
	if c, ok := j.condition("Succeeded"); ok {
		if c.Status == "True" {
			return "Succeeded", c.Message
		}
		// A settled negative verdict. While a job merely runs, the controller
		// leaves this condition absent or unset rather than saying "False"
		// with a reason.
		if c.Reason != "" && c.Reason != "Pending" {
			return "Failed", firstText(c.Message, c.Reason)
		}
	}
	if j.isTrue("Failed") {
		c, _ := j.condition("Failed")
		return "Failed", firstText(c.Message, c.Reason)
	}
	if j.isTrue("Paused") {
		c, _ := j.condition("Paused")
		return "Paused", firstText(c.Message, c.Reason)
	}
	if j.isTrue("Started") {
		return "Running", j.latestMessage()
	}

	// Never started and its window has closed: this job will not run.
	if before, err := time.Parse(time.RFC3339, j.Spec.StartBefore); err == nil && now.After(before) {
		return "Expired", "start window closed at " + showTime(before)
	}
	return "Pending", j.latestMessage()
}

// latestMessage is the newest condition that has something to say, so a row
// carries real text even where the state above had to guess.
func (j upgradeJob) latestMessage() string {
	var newest time.Time
	var msg string
	for _, c := range j.Status.Conditions {
		if c.Message == "" {
			continue
		}
		at, err := time.Parse(time.RFC3339, c.LastTransitionTime)
		if err != nil {
			continue
		}
		if at.After(newest) {
			newest, msg = at, c.Message
		}
	}
	return msg
}

// hooks counts the finished ones. A hook may appear more than once under
// different events, so entries are counted rather than names.
func (j upgradeJob) hooks() string {
	if len(j.Status.HookTracker) == 0 {
		return ""
	}

	done := 0
	for _, h := range j.Status.HookTracker {
		if h.Status == hookComplete {
			done++
		}
	}
	return fmt.Sprintf("%d/%d", done, len(j.Status.HookTracker))
}

func (j upgradeJob) startAfter() time.Time {
	t, _ := time.Parse(time.RFC3339, j.Spec.StartAfter)
	return t
}

const hookComplete = "complete"

var jobEmphasis = map[string]Emphasis{
	"Failed":    Bad,
	"Expired":   Bad,
	"Running":   Warn,
	"Paused":    Warn,
	"Pending":   Plain,
	"Succeeded": Good,
}

// hookEmphasis keys are lower case: the tracker writes "complete", not the
// capitalised vocabulary the ArgoCD resources use.
var hookEmphasis = map[string]Emphasis{
	hookComplete: Good,
	"failed":     Bad,
	"error":      Bad,
	"active":     Warn,
	"pending":    Warn,
}

func listJobs(ctx context.Context, c kube.Client, only string) ([]upgradeJob, error) {
	return fetch[upgradeJob](ctx, c, "UpgradeJob", "upgradejob", only)
}

func upgradeJobs(ctx context.Context, c kube.Client, only string, now time.Time) (Result, error) {
	jobs, err := listJobs(ctx, c, only)
	if err != nil {
		return nil, err
	}

	// Purely by schedule, latest first. Upgrade jobs are a calendar, and a
	// calendar that reorders itself as states change cannot be read: the same
	// job would move on every reload. What needs attention is picked out by
	// colour instead. Latest first because the coming and the just-happened
	// are the interesting end, and history should fall away below.
	sort.SliceStable(jobs, func(a, b int) bool {
		return jobs[a].startAfter().After(jobs[b].startAfter())
	})

	t := Table{
		Cols:     []string{"NAME", "VERSION", "STATE", "START", "HOOKS", "MESSAGE"},
		Emphasis: jobEmphasis,
	}
	for _, j := range jobs {
		state, msg := j.state(now)
		t.Add(j.raw,
			j.Metadata.Name,
			j.Spec.DesiredVersion.Version,
			state,
			showTime(j.startAfter()),
			j.hooks(),
			oneLine(msg),
		)
	}
	return t, nil
}

// upgradeConditions is the drill-down that shows the controller's own words,
// so a wrong guess in state() above is one keypress from being corrected.
func upgradeConditions(ctx context.Context, c kube.Client, job string) (Result, error) {
	jobs, err := listJobs(ctx, c, job)
	if err != nil {
		return nil, err
	}
	if len(jobs) == 0 {
		return nil, fmt.Errorf("upgrade job %q not found", job)
	}

	conds := jobs[0].Status.Conditions
	sort.SliceStable(conds, func(a, b int) bool {
		return conds[a].LastTransitionTime < conds[b].LastTransitionTime
	})

	// No emphasis: "True" is good on PreHealthCheckDone and bad on Paused, so
	// colouring by the bare value would be a lie — and this view exists to
	// show the controller's words rather than an interpretation of them.
	t := Table{Cols: []string{"AT", "TYPE", "STATUS", "REASON", "MESSAGE"}}
	for _, c := range conds {
		at, err := time.Parse(time.RFC3339, c.LastTransitionTime)
		when := c.LastTransitionTime
		if err == nil {
			when = showTime(at)
		}
		t.Add(nil, when, c.Type, c.Status, c.Reason, oneLine(c.Message))
	}
	return t, nil
}

// upgradeHooks replaces the nested text block the zsh version printed. A job
// with no hooks yields an empty table rather than aborting the whole listing,
// which is what iterating a null hookTracker did in jq.
func upgradeHooks(ctx context.Context, c kube.Client, job string) (Result, error) {
	jobs, err := listJobs(ctx, c, job)
	if err != nil {
		return nil, err
	}
	if len(jobs) == 0 {
		return nil, fmt.Errorf("upgrade job %q not found", job)
	}

	hooks := jobs[0].Status.HookTracker
	sort.SliceStable(hooks, func(a, b int) bool {
		if hooks[a].UpgradeJobHook != hooks[b].UpgradeJobHook {
			return hooks[a].UpgradeJobHook < hooks[b].UpgradeJobHook
		}
		return hooks[a].HookEvent < hooks[b].HookEvent
	})

	t := Table{Cols: []string{"STATUS", "HOOK", "EVENT"}, Emphasis: hookEmphasis}
	for _, h := range hooks {
		t.Add(nil, h.Status, h.UpgradeJobHook, h.HookEvent)
	}
	return t, nil
}

func jobNames(ctx context.Context, c kube.Client) ([]string, error) {
	jobs, err := listJobs(ctx, c, "")
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(jobs))
	for _, j := range jobs {
		out = append(out, j.Metadata.Name)
	}
	return out, nil
}
