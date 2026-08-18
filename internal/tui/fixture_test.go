package tui

import (
	"context"
	"time"

	"github.com/vshn/aldediggi/internal/catalog"
)

// sampleRegistry is the stand-in the layout and navigation tests run against.
// It lives here rather than in the catalogue so the binary ships no views that
// exist only for tests.
func sampleRegistry() *catalog.Registry {
	r := catalog.NewRegistry()
	r.Describe("demo", "Views for exercising the layout")

	r.Add(catalog.View{
		Group: "demo", Name: "text",
		Short: "Echo a line of text",
		Run: func(context.Context, catalog.Args) (catalog.Result, error) {
			return catalog.Text{Lines: []string{"123"}}, nil
		},
	})

	r.Add(catalog.View{
		Group: "demo", Name: "table",
		Short: "Render a table after a short delay",
		// The delay is what makes it possible to check that the UI keeps
		// painting and accepting keys while work is in flight.
		Run: func(ctx context.Context, _ catalog.Args) (catalog.Result, error) {
			select {
			case <-time.After(50 * time.Millisecond):
			case <-ctx.Done():
				return nil, ctx.Err()
			}

			t := catalog.Table{Cols: []string{"NAME", "STATUS", "AGE", "MESSAGE"}}
			t.Add(nil, "worker-01", "Ready", "12d", "kubelet is posting ready status")
			t.Add(nil, "worker-02", "NotReady", "3h", "node is reporting a disk pressure condition")
			t.Add(nil, "infra-0", "Ready", "112d", "kubelet is posting ready status")
			t.Add(nil, "master-000000", "Ready", "1y", "kubelet is posting ready status")
			return t, nil
		},
	})

	return r
}
