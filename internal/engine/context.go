package engine

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/itchyny/gojq"
)

// A context is what one is in the middle of: a platform, a cluster, an
// environment. It is one script printing JSON, an optional jq predicate over
// what it printed, and an optional jq label taken from the same value.
//
// The hat asks the question and the world answers it. A hat may never declare
// *that* it is on production — only how to find out — which is what keeps
// "which cluster one is pointed at cannot come from the hat" true even now
// that the hat is the one doing the asking.
//
// There is no text mode here. A probe answers in JSON, because `active` and
// `label` are jq and jq has nothing to say about prose; taking output as
// lines is a view's business, and that is what `class: text` is for.

// ContextDef is a validated, declarative context, the way Def is for a view.
// The config loader builds it from YAML; CompileContext turns it into
// something that can be resolved.
type ContextDef struct {
	ID     string
	Short  string
	Shell  []string // command the script is handed to; defaults to sh -c
	Script string
	Active string   // jq: the script's first value → is this context active?
	Label  string   // jq: the same value → what it contributes to the chrome
	Tools  []string // binaries its views expect; this gates nothing
}

// State is what a round of resolution found out about one context. A bool
// could not say the third thing, and the third thing is the one that matters:
// a probe that could not run has found nothing out, and nothing found out
// must hide nothing.
type State int

const (
	StateUnknown State = iota
	StateActive
	StateInactive
)

func (s State) String() string {
	switch s {
	case StateActive:
		return "active"
	case StateInactive:
		return "inactive"
	default:
		return "unknown"
	}
}

// Context is a compiled, resolvable context.
type Context struct {
	ID    string
	Short string
	// Tools names the binaries the views behind this context expect. It
	// gates nothing: a missing binary is actionable news — install it — and
	// a view that hid itself over one would hide the finding. `check`
	// reports them, and the script's own "command not found" does the rest.
	Tools []string

	shell  []string
	script string
	active *gojq.Code // nil means active whenever the script produced a value
	label  *gojq.Code // nil means it contributes nothing to the chrome
}

// CompileContext checks everything that can be checked without running the
// script and returns the resolvable context. Errors name the context and the
// field, because a config mistake must die at load time.
func CompileContext(d ContextDef) (Context, error) {
	if d.Script == "" {
		return Context{}, fmt.Errorf("context %q: source.script is required", d.ID)
	}
	// One that neither says where it is nor whether it applies has nothing to
	// contribute to either job a context has.
	if d.Active == "" && d.Label == "" {
		return Context{}, fmt.Errorf("context %q: needs active or label; one with neither says nothing", d.ID)
	}

	c := Context{ID: d.ID, Short: d.Short, Tools: d.Tools, shell: d.Shell, script: d.Script}
	if len(c.shell) == 0 {
		c.shell = []string{"sh", "-c"}
	}

	var err error
	if d.Active != "" {
		if c.active, err = compileExprIn("context", d.ID, "active", d.Active); err != nil {
			return Context{}, err
		}
	}
	if d.Label != "" {
		if c.label, err = compileExprIn("context", d.ID, "label", d.Label); err != nil {
			return Context{}, err
		}
	}
	return c, nil
}

// Bearings is where one round of resolution says we are: what became of every
// context, what they contribute to the chrome, and what could not be found
// out.
type Bearings struct {
	State map[string]State
	// Labels is in declaration order, because the chrome drops from the
	// right when it runs short: the hat decides what survives a narrow
	// window.
	Labels []string
	// Notes carries the probes that could not answer. Nothing is hidden on
	// their account, so the TUI stays quiet about them; `check` is where
	// they are reported.
	Notes []string
}

// contextTimeout bounds one context's script. A hanging probe must not hang
// the tool — the chrome is not worth a wedged terminal — and a context is
// expected to be a local read, not a round trip. A var rather than a const,
// so a test need not wait it out.
var contextTimeout = 2 * time.Second

// Resolve runs every context and reports what they found. It never fails:
// a probe that could not answer leaves its context unknown, and unknown hides
// nothing.
func Resolve(ctx context.Context, cs []Context) Bearings {
	b := Bearings{State: make(map[string]State, len(cs))}

	// Identical sources run once per round. Two contexts reading the same
	// kubeconfig — the usual arrangement, since the platform and the cluster
	// name fall out of the same file — must not read it twice.
	seen := map[string]probe{}

	for _, c := range cs {
		key := strings.Join(append(append([]string{}, c.shell...), c.script), "\x00")
		p, done := seen[key]
		if !done {
			p = c.run(ctx)
			seen[key] = p
		}

		state, label, err := c.read(p)
		b.State[c.ID] = state
		if label != "" {
			b.Labels = append(b.Labels, label)
		}
		if err != nil {
			b.Notes = append(b.Notes, fmt.Sprintf("context %q: %v", c.ID, err))
		}
	}
	return b
}

// probe is what one source printed, kept apart from the contexts reading it
// so that several of them can share the one run.
type probe struct {
	value any
	ok    bool
	err   error
}

// run executes the script and takes the first JSON value it printed. A
// context answers once: the question is what one is in the middle of, and
// there is one answer, so a script printing several values is read for its
// first rather than for all of them.
func (c Context) run(ctx context.Context) probe {
	ctx, cancel := context.WithTimeout(ctx, contextTimeout)
	defer cancel()

	out, err := runScript(ctx, c.shell, c.script, nil, nil)
	if err != nil {
		return probe{err: err}
	}
	docs, err := decodeStream(out)
	if err != nil {
		return probe{err: err}
	}
	if len(docs) == 0 {
		return probe{err: fmt.Errorf("printed no JSON value")}
	}
	return probe{value: docs[0], ok: true}
}

// read turns one probe into this context's state and its contribution to the
// chrome. Only an active context contributes a label: one that has resolved
// away is not somewhere we are.
func (c Context) read(p probe) (State, string, error) {
	if !p.ok {
		return StateUnknown, "", p.err
	}

	if c.active != nil {
		v, err := exprValue(c.active, p.value, nil)
		if err != nil {
			return StateUnknown, "", fmt.Errorf("active: %v", err)
		}
		if !truthy(v) {
			return StateInactive, "", nil
		}
	}
	if c.label == nil {
		return StateActive, "", nil
	}

	// A label that cannot be rendered leaves the context active and the
	// chrome short of one word. Failing open is the whole rule: nothing is
	// hidden, so nothing has to be explained.
	v, err := exprValue(c.label, p.value, nil)
	if err != nil {
		return StateActive, "", fmt.Errorf("label: %v", err)
	}
	return StateActive, valueText(v), nil
}

// truthy is jq's own idea of it: null and false are no, and everything else —
// including 0 and the empty string — is yes. A predicate written in jq has to
// mean what it would mean in jq.
func truthy(v any) bool {
	switch t := v.(type) {
	case nil:
		return false
	case bool:
		return t
	}
	return true
}
