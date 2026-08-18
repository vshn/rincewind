package cmd

import (
	"flag"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/vshn/aldediggi/internal/catalog"
)

var update = flag.Bool("update", false, "rewrite docs/inventory.md from the registry")

const inventoryPath = "../docs/inventory.md"

// TestInventoryIsCurrent keeps docs/inventory.md in lockstep with the
// registry. The document is the human-readable answer to "what can this tool
// do"; generating it is what stops it from quietly going stale.
func TestInventoryIsCurrent(t *testing.T) {
	got := inventory(registry())

	if *update {
		if err := os.WriteFile(inventoryPath, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}

	want, err := os.ReadFile(inventoryPath)
	if err != nil || string(want) != got {
		t.Fatalf("docs/inventory.md does not match the registry; regenerate with\n\n\tgo test ./cmd -run TestInventory -update\n")
	}
}

func inventory(reg *catalog.Registry) string {
	var b strings.Builder
	b.WriteString("# Command inventory\n\n")
	b.WriteString("Generated from the registry — do not edit.\n")
	b.WriteString("Regenerate with `go test ./cmd -run TestInventory -update`; CI fails when this file is stale.\n")

	for _, group := range reg.Groups() {
		fmt.Fprintf(&b, "\n## %s — %s\n", group, reg.GroupShort(group))
		for _, v := range reg.InGroup(group) {
			fmt.Fprintf(&b, "\n### %s %s — %s\n\n", v.Group, v.Name, v.Short)
			if v.Hidden {
				b.WriteString("Reached by drilling in, not listed in the TUI.\n\n")
			}
			writeParams(&b, "Parameters", v.Params)
			if len(v.Actions) == 0 {
				continue
			}
			b.WriteString("Actions:\n\n")
			for _, a := range v.Actions {
				fmt.Fprintf(&b, "- `%s` (key `%s`) — %s.\n  %s\n", a.Name, a.Key, a.Short, actionTraits(a))
				writeParams(&b, "  Parameters", a.Params)
			}
		}
	}
	return b.String()
}

func writeParams(b *strings.Builder, label string, params []catalog.Param) {
	if len(params) == 0 {
		return
	}
	fmt.Fprintf(b, "%s:\n\n", label)
	for _, p := range params {
		req := "optional"
		if p.Required {
			req = "required"
		}
		if p.Default != "" {
			req += fmt.Sprintf(", default %q", p.Default)
		}
		fmt.Fprintf(b, "- `%s` (%s) — %s\n", p.Name, req, p.Short)
	}
	b.WriteString("\n")
}

func actionTraits(a catalog.Action) string {
	var out []string
	switch a.Scope {
	case catalog.Rows:
		out = append(out, "Acts on the listed rows")
	case catalog.Params:
		out = append(out, "Acts on the view's parameter")
	}
	if a.Confirm != nil {
		out = append(out, "asks first")
	}
	if a.Refresh {
		out = append(out, "re-runs the view")
	}
	return strings.Join(out, "; ") + "."
}
