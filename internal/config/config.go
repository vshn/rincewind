// Package config reads the YAML view definitions and compiles them into
// runnable engine views. Everything that can go wrong in a config file goes
// wrong here, at load time, with the view and field named — never later as a
// nil somewhere in a running pipeline.
package config

import (
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"
	"time"

	"go.yaml.in/yaml/v3"

	"github.com/vshn/aldediggi/internal/engine"
)

// file mirrors the YAML schema. Unknown fields are rejected by the decoder,
// so a typo like "emphsis" fails with a line number instead of being
// silently ignored.
type file struct {
	Views []view `yaml:"views"`
}

type view struct {
	ID      string   `yaml:"id"`
	Title   string   `yaml:"title"`
	Class   string   `yaml:"class"`
	Refresh string   `yaml:"refresh"`
	Params  []param  `yaml:"params"`
	Source  source   `yaml:"source"`
	Rows    string   `yaml:"rows"`
	Key     string   `yaml:"key"`
	Columns []column `yaml:"columns"`
	Sort    []sortBy `yaml:"sort"`
	Actions []action `yaml:"actions"`
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

// Load reads and compiles a config file.
func Load(path string) ([]engine.View, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close() //nolint:errcheck // read-only

	views, err := Parse(f)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return views, nil
}

// Parse reads a config document. Split from Load so tests and callers with
// embedded config need no file.
func Parse(r io.Reader) ([]engine.View, error) {
	dec := yaml.NewDecoder(r)
	dec.KnownFields(true)

	var doc file
	if err := dec.Decode(&doc); err != nil {
		return nil, err
	}
	if len(doc.Views) == 0 {
		return nil, fmt.Errorf("no views defined")
	}

	seen := map[string]bool{}
	views := make([]engine.View, 0, len(doc.Views))
	for _, v := range doc.Views {
		def, err := toDef(v)
		if err != nil {
			return nil, err
		}
		if seen[v.ID] {
			return nil, fmt.Errorf("view %q: defined twice", v.ID)
		}
		seen[v.ID] = true

		compiled, err := engine.Compile(def)
		if err != nil {
			return nil, err
		}
		views = append(views, compiled)
	}

	// Navigating actions are resolved once every view exists, so an action
	// may open one declared further down the file — and an unknown target
	// dies here rather than as a surprise at run time.
	if err := engine.Link(views); err != nil {
		return nil, err
	}
	return views, nil
}

// envName is what a parameter may be called. Lowercase only, which keeps
// them clear of PATH, HOME, IFS and every other conventional variable
// without needing a list of forbidden names.
var envName = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)

// reserved are the keys the TUI answers itself. An action claiming one would
// take navigation or quitting away from the user, so config may not.
var reserved = map[string]bool{
	"q": true, "ctrl+c": true, "/": true, "tab": true, "shift+tab": true,
	"esc": true, "up": true, "down": true, "k": true, "j": true,
	"pgup": true, "pgdown": true, "ctrl+f": true, "ctrl+b": true,
	"home": true, "end": true, "g": true, "G": true,
	"r": true, "space": true, " ": true, "a": true, "m": true,
	"[": true, "]": true, "x": true, "<": true, ">": true,
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

	switch v.Class {
	case "", "table":
	default:
		return fail("class %q is not supported; only table exists", v.Class)
	}

	d := engine.Def{
		ID:     v.ID,
		Title:  v.Title,
		Shell:  v.Source.Shell,
		Script: v.Source.Script,
		Rows:   v.Rows,
		Key:    v.Key,
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
			}
		} else if len(a.Args) > 0 {
			return fail("action %s: args belong to open, not to a script", a.Name)
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
			Confirm: a.Confirm, Reload: a.Reload,
		})
	}

	return d, nil
}
