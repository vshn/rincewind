// How actions read what a view showed. The rows carry their raw documents;
// these helpers decode them back into typed objects, or hand them to kubectl
// as a List on stdin so the set acted on is exactly the set on screen.

package catalog

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/vshn/aldediggi/internal/kube"
)

// rowsIn decodes the raw documents of a table's rows back into T. Rows that
// are not a T are skipped rather than an error, matching the guard in fetch:
// a row without a usable source has nothing to offer an action.
func rowsIn[T any, P interface {
	*T
	fetchable
}](in Result) []T {
	t, ok := in.(Table)
	if !ok {
		return nil
	}

	var out []T
	for _, src := range t.Sources() {
		raw, ok := src.(json.RawMessage)
		if !ok {
			continue
		}
		var v T
		p := P(&v)
		if err := json.Unmarshal(raw, p); err != nil || p.name() == "" {
			continue
		}
		p.keep(raw)
		out = append(out, v)
	}
	return out
}

// namesIn reads just the names back out of the rows the view produced.
func namesIn[T any, P interface {
	*T
	fetchable
}](in Result) []string {
	rows := rowsIn[T, P](in)
	out := make([]string, 0, len(rows))
	for i := range rows {
		out = append(out, P(&rows[i]).name())
	}
	return out
}

// runOnRows hands the objects behind the rows to kubectl on stdin, so the set
// acted on is exactly the set on screen.
func runOnRows(_ context.Context, in Result, run func(list []byte) ([]byte, error)) (Result, error) {
	t, ok := in.(Table)
	if !ok {
		return nil, fmt.Errorf("this action needs a table, got %T", in)
	}

	sources := t.Sources()
	if len(sources) == 0 {
		return nil, fmt.Errorf("nothing to act on")
	}

	list, err := kube.ListOf(sources)
	if err != nil {
		return nil, err
	}
	out, err := run(list)
	if err != nil {
		return nil, err
	}
	return Text{Lines: splitLines(string(out))}, nil
}

// patchRows merge-patches every object the view showed.
func patchRows(ctx context.Context, c kube.Client, in Result, patch string) (Result, error) {
	return runOnRows(ctx, in, func(list []byte) ([]byte, error) {
		return c.Input(ctx, list, "patch", "-f", "-", "--type", "merge", "-p", patch)
	})
}
