package config

import (
	"testing"
)

// The shipped example config must stay loadable, and its cluster-free demo
// view must actually run — it is the first thing anyone tries.
func TestExampleConfigWorks(t *testing.T) {
	views, err := Load("../../examples/config.yaml")
	if err != nil {
		t.Fatal(err)
	}

	for _, v := range views {
		if v.ID != "demo.services" {
			continue
		}
		res, err := v.Run(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		if len(res.Rows) != 3 {
			t.Fatalf("demo view produced %d rows, want 3", len(res.Rows))
		}
		// Trouble first: the sort must put the degraded service on top.
		if got := res.Rows[0].Cells[0].Text; got != "worker" {
			t.Fatalf("first row is %q, want worker", got)
		}
		if len(res.Notes) > 0 {
			t.Fatalf("demo view has notes: %v", res.Notes)
		}
		return
	}
	t.Fatal("example config lost the demo.services view")
}
