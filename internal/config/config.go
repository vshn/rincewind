// Package config reads the YAML view definitions and compiles them into
// runnable engine views. Everything that can go wrong in a config file goes
// wrong here, at load time, with the view and field named — never later as a
// nil somewhere in a running pipeline.
package config

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"go.yaml.in/yaml/v3"

	"github.com/vshn/rincewind/internal/engine"
)

// file mirrors the YAML schema. Unknown fields are rejected by the decoder,
// so a typo like "emphsis" fails with a line number instead of being
// silently ignored.
type file struct {
	Views    []view     `yaml:"views"`
	Contexts []ctxEntry `yaml:"contexts"`
}

// ctxEntry is one context: a script, an optional predicate over what it
// printed, and an optional label taken from the same value. Named ctxEntry
// rather than context so nothing in this package reads as the standard
// library's.
type ctxEntry struct {
	ID     string   `yaml:"id"`
	Short  string   `yaml:"short"`
	Source source   `yaml:"source"`
	Active string   `yaml:"active"`
	Label  string   `yaml:"label"`
	Tools  []string `yaml:"tools"`
}

type view struct {
	ID      string    `yaml:"id"`
	Title   string    `yaml:"title"`
	Class   string    `yaml:"class"`
	Refresh string    `yaml:"refresh"`
	Follow  bool      `yaml:"follow"`
	Params  []param   `yaml:"params"`
	Source  source    `yaml:"source"`
	Needs   names     `yaml:"needs"`
	Rows    string    `yaml:"rows"`
	Key     string    `yaml:"key"`
	Columns []column  `yaml:"columns"`
	Sort    []sortBy  `yaml:"sort"`
	YRange  []float64 `yaml:"yrange"`
	Actions []action  `yaml:"actions"`
}

type source struct {
	Shell  []string `yaml:"shell"`
	Script string   `yaml:"script"`
}

type param struct {
	Name     string `yaml:"name"`
	Short    string `yaml:"short"`
	Default  string `yaml:"default"`
	Required bool   `yaml:"required"`
}

type action struct {
	Key   string `yaml:"key"`
	Name  string `yaml:"name"`
	Short string `yaml:"short"`

	Open string            `yaml:"open"`
	Args map[string]string `yaml:"args"`

	Shell   []string          `yaml:"shell"`
	Script  string            `yaml:"script"`
	Env     map[string]string `yaml:"env"`
	Confirm string            `yaml:"confirm"`
	Reload  bool              `yaml:"reload"`
	Scope   string            `yaml:"scope"`
	Params  []param           `yaml:"params"`
}

type column struct {
	Name     string            `yaml:"name"`
	Expr     string            `yaml:"expr"`
	Format   string            `yaml:"format"`
	Emphasis map[string]string `yaml:"emphasis"`
}

type sortBy struct {
	By    string   `yaml:"by"`
	Order string   `yaml:"order"`
	Rank  []string `yaml:"rank"`
}

// names is a field written either as one name or as a list of them, which
// is how `needs:` is read: naming one context is the common case and should
// not have to look like a list.
type names []string

func (n *names) UnmarshalYAML(node *yaml.Node) error {
	var one string
	if err := node.Decode(&one); err == nil {
		*n = names{one}
		return nil
	}
	var many []string
	if err := node.Decode(&many); err != nil {
		return fmt.Errorf("line %d: needs takes a context id or a list of them", node.Line)
	}
	*n = many
	return nil
}

// Hat is what a hat holds: the views it offers, and the contexts that say
// where one is and which of those views apply here. Both are needed
// together, so they travel together.
//
// Not to be confused with hats.Hat, which is a directory in the store; this
// is what reading one produced.
type Hat struct {
	Views    []engine.View
	Contexts []engine.Context
}

// Load reads and compiles a config file, or every *.yaml in a directory.
// A directory keeps one file per group, which is how the views were arranged
// when they were Go source and is still how one thinks about them.
//
// Views and contexts both merge across those files: a hat is one hat however
// it is split up. What may not repeat is an id, of either kind, and a clash
// across two files names both of them — otherwise the reader is left grepping
// for the other half.
func Load(path string) (Hat, error) {
	info, err := os.Stat(path)
	if err != nil {
		return Hat{}, err
	}

	files := []string{path}
	if info.IsDir() {
		if files, err = filepath.Glob(filepath.Join(path, "*.yaml")); err != nil {
			return Hat{}, err
		}
		sort.Strings(files) // menu order must not depend on the filesystem
		if len(files) == 0 {
			return Hat{}, fmt.Errorf("%s: holds no .yaml files", path)
		}
	}

	var hat Hat
	from := map[string]string{}
	for _, name := range files {
		read, err := loadFile(name)
		if err != nil {
			return Hat{}, err
		}
		for _, c := range read.Contexts {
			if prev, taken := from[c.ID]; taken {
				return Hat{}, fmt.Errorf("context %q: declared in %s and in %s", c.ID, prev, name)
			}
			from[c.ID] = name
		}
		hat.Views = append(hat.Views, read.Views...)
		hat.Contexts = append(hat.Contexts, read.Contexts...)
	}

	// One view may open another declared in a different file, and may name a
	// context declared in a third, so linking waits until every file has been
	// read. Its errors are the hat's, not one file's, so it is the hat that is
	// named — the same way an empty directory is.
	if err := link(hat); err != nil {
		return Hat{}, fmt.Errorf("%s: %w", path, err)
	}
	return hat, nil
}

func loadFile(path string) (Hat, error) {
	f, err := os.Open(path)
	if err != nil {
		return Hat{}, err
	}
	defer f.Close() //nolint:errcheck // read-only

	hat, err := compile(f)
	if err != nil {
		return Hat{}, fmt.Errorf("%s: %w", path, err)
	}
	return hat, nil
}

// Parse reads one config document and links it. Split from Load so tests and
// callers with embedded config need no file.
func Parse(r io.Reader) (Hat, error) {
	hat, err := compile(r)
	if err != nil {
		return Hat{}, err
	}
	if err := link(hat); err != nil {
		return Hat{}, err
	}
	return hat, nil
}

// compile turns one document into views and contexts, without resolving what
// its actions open or what its views need — both of those can be declared in
// another file.
func compile(r io.Reader) (Hat, error) {
	dec := yaml.NewDecoder(r)
	dec.KnownFields(true)

	var doc file
	if err := dec.Decode(&doc); err != nil {
		return Hat{}, err
	}

	var hat Hat
	seen := map[string]bool{}
	for _, c := range doc.Contexts {
		def, err := toContextDef(c)
		if err != nil {
			return Hat{}, err
		}
		if seen[def.ID] {
			return Hat{}, fmt.Errorf("context %q: declared twice", def.ID)
		}
		seen[def.ID] = true

		compiled, err := engine.CompileContext(def)
		if err != nil {
			return Hat{}, err
		}
		hat.Contexts = append(hat.Contexts, compiled)
	}

	for _, v := range doc.Views {
		def, err := toDef(v)
		if err != nil {
			return Hat{}, err
		}
		compiled, err := engine.Compile(def)
		if err != nil {
			return Hat{}, err
		}
		hat.Views = append(hat.Views, compiled)
	}
	return hat, nil
}

// link resolves navigating actions, holds every `needs:` to a context that
// exists, and catches an id used twice — which, with several files, is a
// mistake made across them as easily as within one.
func link(hat Hat) error {
	// A hat with no views is nothing to pull anything out of. The rule is
	// the hat's and not the file's, though: one file per group is how these
	// are kept, and a contexts.yaml holding nothing but contexts is a group
	// like any other.
	if len(hat.Views) == 0 {
		return fmt.Errorf("no views defined")
	}

	seen := map[string]bool{}
	for _, v := range hat.Views {
		if seen[v.ID] {
			return fmt.Errorf("view %q: defined twice", v.ID)
		}
		seen[v.ID] = true
	}

	// A view naming a context nobody declared would be shown always, which is
	// the opposite of what was meant — the same reason an `open:` target that
	// is not a view dies here rather than at the keystroke.
	declared := map[string]bool{}
	for _, c := range hat.Contexts {
		declared[c.ID] = true
	}
	for _, v := range hat.Views {
		for _, id := range v.Needs {
			if !declared[id] {
				return fmt.Errorf("view %q: needs %q, which is not a context", v.ID, id)
			}
		}
	}

	return engine.Link(hat.Views)
}

// contextID is what a context may be called. The same shape as a view id
// without the grouping dot: it is typed in `needs:` and printed by `check`,
// so it stays a plain word.
var contextID = regexp.MustCompile(`^[a-z][a-z0-9_-]*$`)

// toContextDef checks everything the schema can express and hands the rest —
// the expressions, and whether the context says anything at all — to
// engine.CompileContext.
func toContextDef(c ctxEntry) (engine.ContextDef, error) {
	if c.ID == "" {
		return engine.ContextDef{}, fmt.Errorf("a context has no id")
	}
	if !contextID.MatchString(c.ID) {
		return engine.ContextDef{}, fmt.Errorf("context %q: an id must be lowercase letters, digits, dashes and underscores", c.ID)
	}
	for _, tool := range c.Tools {
		if strings.TrimSpace(tool) == "" {
			return engine.ContextDef{}, fmt.Errorf("context %q: a tool has no name", c.ID)
		}
	}

	return engine.ContextDef{
		ID:     c.ID,
		Short:  c.Short,
		Shell:  c.Source.Shell,
		Script: c.Source.Script,
		Active: c.Active,
		Label:  c.Label,
		Tools:  c.Tools,
	}, nil
}

// envName is what a parameter may be called. Lowercase only, which keeps
// them clear of PATH, HOME, IFS and every other conventional variable
// without needing a list of forbidden names.
var envName = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)

// reserved are the keys the TUI answers itself. An action claiming one would
// take navigation or quitting away from the user, so config may not.
//
// This set is the contract, and it lives here because config is where a hat
// is held to it. The TUI keeps the other half — the keys it actually binds —
// beside its own switch, and a test there holds the two together.
var reserved = map[string]bool{
	"q": true, "ctrl+c": true, "/": true, "tab": true, "shift+tab": true,
	"esc": true, "up": true, "down": true, "k": true, "j": true,
	"pgup": true, "pgdown": true, "ctrl+f": true, "ctrl+b": true,
	"home": true, "end": true, "g": true, "G": true,
	"r": true, "space": true, " ": true, "a": true, "H": true, "M": true, "Y": true,
	"[": true, "]": true, "x": true, "<": true, ">": true,
}

// ReservedKeys is that set, sorted, for whoever has to agree with it.
func ReservedKeys() []string {
	out := make([]string, 0, len(reserved))
	for key := range reserved {
		out = append(out, key)
	}
	sort.Strings(out)
	return out
}

// toDef checks everything the schema can express and hands the rest —
// expression syntax, sort references — to engine.Compile.
func toDef(v view) (engine.Def, error) {
	if v.ID == "" {
		return engine.Def{}, fmt.Errorf("a view has no id")
	}
	fail := func(format string, args ...any) (engine.Def, error) {
		return engine.Def{}, fmt.Errorf("view %q: %s", v.ID, fmt.Sprintf(format, args...))
	}

	// The id doubles as grouping: everything before the first dot is the
	// sidebar group, the rest the entry's label. A dot at either end would
	// leave one of them empty.
	if strings.HasPrefix(v.ID, ".") || strings.HasSuffix(v.ID, ".") {
		return fail("id must not start or end with a dot")
	}

	// A text view shapes nothing, so the fields that do the shaping are not
	// merely optional there — they are refused, because a `rows:` nothing
	// reads is a misunderstanding worth naming rather than ignoring. A chart
	// shapes series with rows, and refuses the rest the same way.
	switch v.Class {
	case "", "table":
	case "text":
		switch {
		case v.Rows != "":
			return fail("class text takes the output as lines, so rows is not read")
		case len(v.Columns) > 0:
			return fail("class text takes the output as lines, so columns are not read")
		case len(v.Sort) > 0:
			return fail("class text keeps the order the script printed, so sort is not read")
		case v.Key != "":
			return fail("class text has no rows, so key is not read")
		}
	case "chart":
		switch {
		case len(v.Columns) > 0:
			return fail("class chart draws series, so columns are not read")
		case len(v.Sort) > 0:
			return fail("class chart draws series in the order the script produced them, so sort is not read")
		case v.Key != "":
			return fail("class chart has no rows to select, so key is not read")
		}
	default:
		return fail("class %q is not supported; use table, text or chart", v.Class)
	}

	// The value axis is a chart's alone, and half a range is a mistake.
	if len(v.YRange) > 0 {
		switch {
		case v.Class != "chart":
			return fail("yrange belongs to class chart")
		case len(v.YRange) != 2:
			return fail("yrange needs two numbers, low then high")
		case v.YRange[0] >= v.YRange[1]:
			return fail("yrange low %v is not below high %v", v.YRange[0], v.YRange[1])
		}
	}

	// Following is consuming the output as it is written, which only makes
	// sense where the output is lines. A table would have to decide what a
	// second document means for the rows already on screen.
	switch {
	case v.Follow && v.Class != "text":
		return fail("follow needs class: text; a table has no way to grow a row at a time")
	case v.Follow && v.Refresh != "":
		return fail("follow and refresh exclude each other: a stream is already current")
	}

	needed := map[string]bool{}
	for _, id := range v.Needs {
		if strings.TrimSpace(id) == "" {
			return fail("needs: a context has no id")
		}
		// Downstream counts and marks take the list at its word, so a repeat
		// would be shown and tallied twice.
		if needed[id] {
			return fail("needs %q twice", id)
		}
		needed[id] = true
	}

	d := engine.Def{
		ID:     v.ID,
		Title:  v.Title,
		Needs:  v.Needs,
		Text:   v.Class == "text",
		Chart:  v.Class == "chart",
		Follow: v.Follow,
		Shell:  v.Source.Shell,
		Script: v.Source.Script,
		Rows:   v.Rows,
		Key:    v.Key,
	}
	if len(v.YRange) == 2 {
		d.YRange = &[2]float64{v.YRange[0], v.YRange[1]}
	}

	if v.Refresh != "" {
		refresh, err := time.ParseDuration(v.Refresh)
		if err != nil {
			return fail("refresh: %v", err)
		}
		if refresh < time.Second {
			return fail("refresh %s is below 1s", refresh)
		}
		d.Refresh = refresh
	}

	seen := map[string]bool{}
	for _, p := range v.Params {
		if !envName.MatchString(p.Name) {
			return fail("param %q: a name must be lowercase letters, digits and underscores, so it cannot shadow PATH or HOME", p.Name)
		}
		if seen[p.Name] {
			return fail("param %s: declared twice", p.Name)
		}
		seen[p.Name] = true
		d.Params = append(d.Params, engine.Param{
			Name: p.Name, Short: p.Short, Default: p.Default, Required: p.Required,
		})
	}

	names := map[string]bool{}
	for _, c := range v.Columns {
		if c.Name == "" {
			return fail("a column has no name")
		}
		if names[c.Name] {
			return fail("column %s: defined twice", c.Name)
		}
		names[c.Name] = true

		col := engine.ColumnDef{Name: c.Name, Expr: c.Expr, Format: c.Format}
		for value, e := range c.Emphasis {
			emph, err := engine.ParseEmphasis(e)
			if err != nil {
				return fail("column %s: emphasis %q: %v", c.Name, value, err)
			}
			if col.Emph == nil {
				col.Emph = map[string]engine.Emphasis{}
			}
			col.Emph[value] = emph
		}
		d.Columns = append(d.Columns, col)
	}

	for _, s := range v.Sort {
		if len(s.Rank) > 0 && s.Order != "" {
			return fail("sort by %s: rank and order exclude each other", s.By)
		}
		key := engine.SortDef{By: s.By, Rank: s.Rank}
		switch s.Order {
		case "", "asc":
		case "desc":
			key.Desc = true
		default:
			return fail("sort by %s: order %q is not asc or desc", s.By, s.Order)
		}
		d.Sort = append(d.Sort, key)
	}

	keys, actNames := map[string]bool{}, map[string]bool{}
	for _, a := range v.Actions {
		if a.Name == "" {
			return fail("an action has no name")
		}

		// A text or chart view has no rows, so nothing can act on one: no
		// cursor row to navigate from, no selection to work on. What is left
		// is an action that acts on nothing.
		if (d.Text || d.Chart) && (a.Open != "" || a.Scope != engine.ScopeNone) {
			return fail("action %s: a %s view has no rows, so its actions need scope: none", a.Name, v.Class)
		}
		// A followed view has no finished state to return to: leaving its
		// tab pauses the stream and coming back starts it again. Reloading
		// one from a job tab would start a stream behind a background tab,
		// which is the one thing following must not do.
		if d.Follow && a.Reload {
			return fail("action %s: reload cannot re-run a followed view; following resumes by itself", a.Name)
		}
		if actNames[a.Name] {
			return fail("action %s: declared twice", a.Name)
		}
		actNames[a.Name] = true

		switch {
		case a.Key == "":
			return fail("action %s: has no key", a.Name)
		case reserved[a.Key]:
			return fail("action %s: key %q is one the tool answers itself", a.Name, a.Key)
		case keys[a.Key]:
			return fail("action %s: key %q is already taken", a.Name, a.Key)
		}
		keys[a.Key] = true

		// The two kinds are exclusive; mixing their fields is a mistake
		// worth naming rather than quietly ignoring half of it.
		if a.Open != "" {
			switch {
			case len(a.Env) > 0:
				return fail("action %s: env belongs to a script, not to open", a.Name)
			case a.Confirm != "":
				return fail("action %s: confirm belongs to a script, not to open", a.Name)
			case a.Reload:
				return fail("action %s: reload belongs to a script, not to open", a.Name)
			case a.Scope != "":
				return fail("action %s: scope belongs to a script; a navigation always follows the cursor", a.Name)
			case len(a.Params) > 0:
				return fail("action %s: params belong to a script; a navigation takes its arguments from the row", a.Name)
			}
		} else if len(a.Args) > 0 {
			return fail("action %s: args belong to open, not to a script", a.Name)
		}

		var params []engine.Param
		asked := map[string]bool{}
		for _, p := range a.Params {
			switch {
			case !envName.MatchString(p.Name):
				return fail("action %s: param %q: a name must be lowercase letters, digits and underscores, so it cannot shadow PATH or HOME", a.Name, p.Name)
			case asked[p.Name]:
				return fail("action %s: param %s: declared twice", a.Name, p.Name)
			// Both would set the same variable. Rather than declare which
			// wins, forbid it: then the precedence is unobservable and there
			// is one fewer rule to remember.
			case seen[p.Name]:
				return fail("action %s: param %q is also a parameter of this view", a.Name, p.Name)
			case a.Env[p.Name] != "":
				return fail("action %s: param %q is also an env name; both would set the same variable", a.Name, p.Name)
			}
			asked[p.Name] = true
			params = append(params, engine.Param{
				Name: p.Name, Short: p.Short, Default: p.Default, Required: p.Required,
			})
		}

		// A rowless action may still derive values — from its own parameters
		// rather than from a row, which is how a create turns wall clock
		// into RFC 3339. Its env expressions run against a null input, so
		// one that reaches for the row yields empty; that is not silent,
		// because every value is printed in the job tab before anything runs.
		switch a.Scope {
		case "", engine.ScopeRows, engine.ScopeNone:
		default:
			return fail("action %s: scope %q is not rows or none", a.Name, a.Scope)
		}

		for name := range a.Env {
			if !envName.MatchString(name) {
				return fail("action %s: env %q: a name must be lowercase letters, digits and underscores", a.Name, name)
			}
		}

		d.Actions = append(d.Actions, engine.ActionDef{
			Key: a.Key, Name: a.Name, Short: a.Short,
			Open: a.Open, Args: a.Args,
			Shell: a.Shell, Script: a.Script, Env: a.Env,
			Confirm: a.Confirm, Reload: a.Reload, Scope: a.Scope, Params: params,
		})
	}

	return d, nil
}
