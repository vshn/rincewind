// Package config reads the YAML view definitions and compiles them into
// runnable engine views. Everything that can go wrong in a config file goes
// wrong here, at load time, with the view and field named — never later as a
// nil somewhere in a running pipeline.
package config

import (
	"fmt"
	"io"
	"os"
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
	Source  source   `yaml:"source"`
	Rows    string   `yaml:"rows"`
	Key     string   `yaml:"key"`
	Columns []column `yaml:"columns"`
	Sort    []sortBy `yaml:"sort"`
}

type source struct {
	Shell  []string `yaml:"shell"`
	Script string   `yaml:"script"`
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
	return views, nil
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

	return d, nil
}
