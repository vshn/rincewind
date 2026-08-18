// Package catalog holds what aldediggi can do: the views it can show and,
// later, the actions those views offer.
//
// The registry is the single source of truth. Cobra commands and the TUI list
// are both generated from it, so a view is described exactly once. See
// docs/architecture.md.
package catalog

import "context"

// Kind tells the engine how to run and render a view. The zsh predecessor
// mixed these silently, which is why one definition could not serve both a
// pipe and an interactive pane.
type Kind int

const (
	// Snapshot runs once and renders whatever it returns. Stream and External
	// are described in docs/architecture.md and land with the declarative
	// views. There is no mutating kind: mutation is a property of an action.
	Snapshot Kind = iota
)

// View produces output. Actions hang off a view and always operate on
// everything it produced; they are not modelled yet.
type View struct {
	Group string
	Name  string
	Short string
	Kind  Kind
	Run   func(ctx context.Context) (Result, error)
}

// ID is the dotted form used in messages and, later, as the config key.
func (v View) ID() string { return v.Group + "." + v.Name }

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
