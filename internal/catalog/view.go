// Package catalog holds what aldediggi can do: the views it can show and the
// actions those views offer.
//
// The registry is the single source of truth. Cobra commands and the TUI list
// are both generated from it, so a view is described exactly once. See
// docs/architecture.md.
package catalog

import (
	"context"
	"fmt"
	"strings"
)

// Kind tells the engine how to run and render a view. The zsh predecessor
// mixed these silently, which is why one definition could not serve both a
// pipe and an interactive pane.
type Kind int

const (
	// Snapshot runs once and renders whatever it returns. Stream and External
	// are described in docs/architecture.md. There is no mutating kind:
	// mutation is a property of an action.
	Snapshot Kind = iota
)

// Args are the resolved parameters of a view invocation.
type Args map[string]string

func (a Args) Get(name string) string { return a[name] }

// Param is a value a view needs before it can run, such as an app name.
type Param struct {
	Name     string
	Short    string
	Required bool
	Default  string
	// Complete feeds both shell completion and the TUI prompt.
	Complete func(ctx context.Context) ([]string, error)
}

// View produces output.
type View struct {
	Group string
	Name  string
	Short string
	Kind  Kind
	// Hidden keeps the view out of the TUI list. Used for views that only
	// make sense with a parameter, which arrives by drilling in from another
	// view rather than by picking it from a list.
	Hidden  bool
	Params  []Param
	Actions []Action
	// Available reports why the view cannot run here, so the TUI can grey it
	// out with a reason rather than failing on enter.
	Available func() error
	Run       func(ctx context.Context, args Args) (Result, error)
}

// ID is the dotted form used in messages and as the config key.
func (v View) ID() string { return v.Group + "." + v.Name }

// Scope says what an action operates on.
type Scope int

const (
	// Rows acts on everything the view produced. There is no row selection,
	// which is what keeps the `--action` flag and the TUI key binding
	// equivalent.
	Rows Scope = iota
	// Params acts on what the view was asked about rather than on what it
	// returned. Refreshing an application from its syncstatus view is the
	// example: the rows there are the app's resources, not the app.
	Params
)

// Action does something from a view.
type Action struct {
	Name  string
	Key   string
	Short string
	Scope Scope
	// Confirm returns the prompt to show before running, or "" to run
	// unasked. It receives the view's result so the prompt can name the count,
	// which with no row selection is the only brake there is.
	Confirm func(in Result) string
	// Refresh re-runs the view afterwards.
	Refresh bool
	Run     func(ctx context.Context, in Result, args Args) (Result, error)
}

// Bind resolves positional arguments against the view's parameters. Both the
// CLI and the TUI go through this, so a required parameter cannot be missing
// in one mode and silently defaulted in the other.
func (v View) Bind(positional []string) (Args, error) {
	out := Args{}
	for i, p := range v.Params {
		switch {
		case i < len(positional) && positional[i] != "":
			out[p.Name] = positional[i]
		case p.Default != "":
			out[p.Name] = p.Default
		case p.Required:
			return nil, fmt.Errorf("%s requires %s", v.ID(), strings.ToUpper(p.Name))
		}
	}
	return out, nil
}

// Lookup finds one of the view's actions by name.
func (v View) Lookup(name string) (Action, bool) {
	for _, a := range v.Actions {
		if a.Name == name {
			return a, true
		}
	}
	return Action{}, false
}

// ActionNames lists the actions in declaration order, for help and completion.
func (v View) ActionNames() []string {
	out := make([]string, 0, len(v.Actions))
	for _, a := range v.Actions {
		out = append(out, a.Name)
	}
	return out
}

// Prompt renders an action's confirmation together with the cluster it would
// land on. The engine adds the cluster rather than each action remembering to,
// because prod and staging usually sit in the same kubeconfig and the one
// question worth always asking is "on which".
func Prompt(text, cluster string) string {
	if cluster == "" {
		return text
	}
	return text + "\n\ncluster: " + cluster
}

// Registry holds views in insertion order, because that order is what the user
// sees in both the TUI list and the help output.
type Registry struct {
	views  []View
	groups []string
	shorts map[string]string
}

func NewRegistry() *Registry {
	return &Registry{shorts: map[string]string{}}
}

// Describe records a group's help text. Groups appear in the order they are
// first described or used.
func (r *Registry) Describe(group, short string) {
	if _, seen := r.shorts[group]; !seen {
		r.groups = append(r.groups, group)
	}
	r.shorts[group] = short
}

func (r *Registry) Add(v View) {
	if _, seen := r.shorts[v.Group]; !seen {
		r.Describe(v.Group, "")
	}
	r.views = append(r.views, v)
}

func (r *Registry) All() []View { return r.views }

// Get finds a view by its dotted id, which is how Navigate names its target.
func (r *Registry) Get(id string) (View, bool) {
	for _, v := range r.views {
		if v.ID() == id {
			return v, true
		}
	}
	return View{}, false
}

// Listed returns the group's views that belong in the TUI list.
func (r *Registry) Listed(group string) []View {
	var out []View
	for _, v := range r.InGroup(group) {
		if !v.Hidden {
			out = append(out, v)
		}
	}
	return out
}

func (r *Registry) Groups() []string { return r.groups }

func (r *Registry) GroupShort(group string) string { return r.shorts[group] }

// InGroup returns the group's views in registration order.
func (r *Registry) InGroup(group string) []View {
	var out []View
	for _, v := range r.views {
		if v.Group == group {
			out = append(out, v)
		}
	}
	return out
}

// Merge appends another registry's groups and views, so the argo, pods and
// other catalogues can be assembled independently.
func (r *Registry) Merge(other *Registry) {
	for _, g := range other.Groups() {
		r.Describe(g, other.GroupShort(g))
	}
	for _, v := range other.All() {
		r.views = append(r.views, v)
	}
}
