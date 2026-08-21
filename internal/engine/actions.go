package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"time"

	"github.com/itchyny/gojq"
)

// compileActions turns a view's declared actions into runnable ones. A
// navigating action's target cannot be resolved here — the other views may
// not exist yet — so Open is left nil and filled in by a second pass; see
// Link.
func compileActions(d Def) ([]Action, error) {
	var out []Action
	for _, a := range d.Actions {
		act := Action{Key: a.Key, Name: a.Name, Short: a.Short, Reload: a.Reload, Params: a.Params}
		where := "action " + a.Name

		switch {
		case a.Open != "" && a.Script != "":
			return nil, fmt.Errorf("view %q: %s: has both open and script; an action either navigates or works", d.ID, where)
		case a.Open == "" && a.Script == "":
			return nil, fmt.Errorf("view %q: %s: needs either open or script", d.ID, where)

		case a.Open != "":
			args, err := compileArgs(d.ID, where, a.Args)
			if err != nil {
				return nil, err
			}
			act.Args, act.openID = args, a.Open
			for name := range a.Args {
				act.argNames = append(act.argNames, name)
			}
			sort.Strings(act.argNames) // stable error messages

		default:
			act.Rowless = a.Scope == ScopeNone
			plan, err := compilePlan(d, a, where, act.Rowless)
			if err != nil {
				return nil, err
			}
			act.Plan = plan

			if a.Confirm != "" {
				code, err := compileExpr(d.ID, where+" confirm", a.Confirm)
				if err != nil {
					return nil, err
				}
				rowless := act.Rowless
				act.Confirm = func(rows []any, cur Args) (string, error) {
					v, err := exprValue(code, rows, cur)
					if err != nil {
						return "", err
					}
					// The count is appended here, in the engine, rather than
					// by whoever asks the question: config cannot write a
					// confirmation that hides how much it touches, and the
					// TUI and the CLI cannot drift into two answers. A
					// rowless action is exactly one step, and that step is
					// shown beside the question.
					if rowless {
						return valueText(v), nil
					}
					return fmt.Sprintf("%s (%s)", valueText(v), Plural(len(rows), "row")), nil
				}
			}
		}
		out = append(out, act)
	}
	return out, nil
}

// exprTimeout bounds one action expression. Args, Plan and Confirm are
// evaluated straight from the TUI's Update, which paints nothing while they
// run, so `while(true; .)` written into a hat would be a frozen tool with no
// way out. A var rather than a const, so a test need not wait it out.
var exprTimeout = 2 * time.Second

// exprValue is firstValue under that deadline. Every action expression goes
// through it; a jq expression is config, and config gets no unbounded turn.
func exprValue(code *gojq.Code, in any, cur Args) (any, error) {
	ctx, cancel := context.WithTimeout(context.Background(), exprTimeout)
	defer cancel()
	return firstValue(ctx, code, in, cur)
}

// compileArgs builds the evaluator for a navigating action's parameters.
func compileArgs(view, where string, exprs map[string]string) (func(any, Args) (Args, error), error) {
	codes := map[string]*gojq.Code{}
	for name, src := range exprs {
		code, err := compileExpr(view, where+" args."+name, src)
		if err != nil {
			return nil, err
		}
		codes[name] = code
	}

	return func(src any, cur Args) (Args, error) {
		out := Args{}
		for name, code := range codes {
			v, err := exprValue(code, src, cur)
			if err != nil {
				// A refusal written into the expression — a pool selecting by
				// expression, say — arrives here. It is the answer, not a
				// malfunction, so it travels as it was written.
				return nil, fmt.Errorf("%s: %v", name, err)
			}
			out[name] = valueText(v)
		}
		return out, nil
	}, nil
}

// compilePlan builds the planner for a working action: one step per row, so
// each invocation names exactly one object. Planning is separate from
// running because the tool shows what it is about to do before it does it.
func compilePlan(d Def, a ActionDef, where string, rowless bool) (func([]any, Args) ([]Step, error), error) {
	shell := a.Shell
	if len(shell) == 0 {
		shell = []string{"sh", "-c"}
	}

	codes := map[string]*gojq.Code{}
	for name, src := range a.Env {
		code, err := compileExpr(d.ID, where+" env."+name, src)
		if err != nil {
			return nil, err
		}
		codes[name] = code
	}

	// evalEnv is one step's environment: the values the view was opened
	// with, then the action's own expressions over the input. Planning must
	// not run anything, so a failing expression is refused here rather than
	// half-executing the set.
	evalEnv := func(input any, cur Args) (Args, error) {
		env := Args{}
		for name, value := range cur {
			env[name] = value
		}
		for name, code := range codes {
			v, err := exprValue(code, input, cur)
			if err != nil {
				return nil, fmt.Errorf("env %s: %v", name, err)
			}
			env[name] = valueText(v)
		}
		return env, nil
	}

	if rowless {
		return func(_ []any, cur Args) ([]Step, error) {
			// No row to evaluate against, so the input is null and the
			// expressions work from $args — which is where a rowless
			// action's values come from anyway.
			env, err := evalEnv(nil, cur)
			if err != nil {
				return nil, err
			}
			// Empty rather than nil: a nil stdin leaves the script attached
			// to the terminal, where it would eat the user's keystrokes.
			return []Step{{Env: env, Shell: shell, Script: a.Script, Stdin: []byte{}}}, nil
		}, nil
	}

	return func(rows []any, cur Args) ([]Step, error) {
		steps := make([]Step, 0, len(rows))
		for _, row := range rows {
			env, err := evalEnv(row, cur)
			if err != nil {
				return nil, err
			}

			// The row itself goes to stdin, so a script needing more than the
			// few values named in env can read the whole object.
			stdin, err := json.Marshal(row)
			if err != nil {
				return nil, err
			}
			steps = append(steps, Step{Env: env, Shell: shell, Script: a.Script, Stdin: stdin})
		}
		return steps, nil
	}, nil
}

// Link resolves navigating actions against the finished set of views. It is
// a second pass because an action may open a view declared further down the
// file, and it is where an unknown target dies — at load time, naming the
// action, rather than as a runtime "no such view".
func Link(views []View) error {
	byID := map[string]*View{}
	for i := range views {
		byID[views[i].ID] = &views[i]
	}

	for i := range views {
		v := &views[i]
		for j := range v.Actions {
			a := &v.Actions[j]
			if a.openID == "" {
				continue
			}
			to, ok := byID[a.openID]
			if !ok {
				return fmt.Errorf("view %q: action %s: opens %q, which is not a view", v.ID, a.Name, a.openID)
			}
			if err := checkArgs(v.ID, *a, to); err != nil {
				return err
			}
			a.Open = to
		}
	}
	return nil
}

// checkArgs holds an action to the parameters its target declares. Getting
// this wrong used to be a run-time surprise — an argument nothing read, or a
// required one silently empty; here it is a load-time error naming both ends.
func checkArgs(from string, a Action, to *View) error {
	declared := map[string]Param{}
	for _, p := range to.Params {
		declared[p.Name] = p
	}

	for _, name := range a.argNames {
		if _, ok := declared[name]; !ok {
			return fmt.Errorf("view %q: action %s: passes %q, which view %q does not declare as a parameter", from, a.Name, name, to.ID)
		}
	}

	given := map[string]bool{}
	for _, name := range a.argNames {
		given[name] = true
	}
	for _, p := range to.Params {
		if p.Required && !given[p.Name] && p.Default == "" {
			return fmt.Errorf("view %q: action %s: view %q requires %q, which this action does not pass", from, a.Name, to.ID, p.Name)
		}
	}
	return nil
}
