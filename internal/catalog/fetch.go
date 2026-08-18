package catalog

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/vshn/aldediggi/internal/kube"
)

// fetchable is what every resource struct exposes to fetch, via its pointer:
// the name for the kind guard, and a place to keep the raw document so actions
// can hand it back to kubectl unchanged.
type fetchable interface {
	name() string
	keep(raw json.RawMessage)
}

// fetch is the one read path: `kubectl get RESOURCE [ONLY] [EXTRA...] -o json`,
// decoded into T. Naming one object returns the object itself where a bare get
// returns a List; fetch hides that asymmetry from every view.
//
// The name guard exists because anything that is not the expected kind still
// decodes cleanly into all zero values, which would surface as an empty table
// rather than as a problem. The name is the cheapest field to insist on.
func fetch[T any, P interface {
	*T
	fetchable
}](ctx context.Context, c kube.Client, kind, resource, only string, extra ...string) ([]T, error) {
	args := []string{"get", resource}
	if only != "" {
		args = append(args, only)
	}
	args = append(args, extra...)

	raw, err := c.Output(ctx, append(args, "-o", "json")...)
	if err != nil {
		return nil, err
	}

	var docs []json.RawMessage
	if only != "" {
		docs = []json.RawMessage{raw}
	} else {
		var list kube.List[json.RawMessage]
		if err := json.Unmarshal(raw, &list); err != nil {
			return nil, fmt.Errorf("decoding %s list: %w", kind, err)
		}
		docs = list.Items
	}

	out := make([]T, 0, len(docs))
	for _, doc := range docs {
		var t T
		p := P(&t)
		if err := json.Unmarshal(doc, p); err != nil {
			return nil, fmt.Errorf("decoding %s: %w", kind, err)
		}
		if p.name() == "" {
			return nil, fmt.Errorf("%s: response is not %q", c.Describe(args...), kind)
		}
		p.keep(doc)
		out = append(out, t)
	}
	return out, nil
}
