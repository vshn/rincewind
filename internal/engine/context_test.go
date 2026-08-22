package engine

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// resolve compiles the definitions and runs a round against a real shell,
// the way the pipeline tests do: the scripts echo fixture JSON, so this is
// the path production takes.
func resolve(t *testing.T, defs ...ContextDef) Bearings {
	t.Helper()
	var cs []Context
	for _, d := range defs {
		c, err := CompileContext(d)
		if err != nil {
			t.Fatalf("compile %s: %v", d.ID, err)
		}
		cs = append(cs, c)
	}
	return Resolve(context.Background(), cs)
}

func TestContextActiveWhenThePredicateHolds(t *testing.T) {
	b := resolve(t, ContextDef{
		ID:     "cloudscale",
		Script: `echo '{"current-context":"c-appuio-lab-cloudscale-rma-0"}'`,
		Active: `."current-context" | test("-cloudscale-")`,
		Label:  `"cloudscale"`,
	})

	if got := b.State["cloudscale"]; got != StateActive {
		t.Errorf("state is %v, want active", got)
	}
	if got := strings.Join(b.Labels, " "); got != "cloudscale" {
		t.Errorf("labels are %q, want the label of the active context", got)
	}
	if len(b.Notes) > 0 {
		t.Errorf("notes: %v", b.Notes)
	}
}

// The hiding case, and the only state that hides anything.
func TestContextInactiveWhenThePredicateDoesNot(t *testing.T) {
	b := resolve(t, ContextDef{
		ID:     "cloudscale",
		Script: `echo '{"current-context":"c-appuio-lab-exoscale-ch-gva-2-0"}'`,
		Active: `."current-context" | test("-cloudscale-")`,
		Label:  `"cloudscale"`,
	})

	if got := b.State["cloudscale"]; got != StateInactive {
		t.Errorf("state is %v, want inactive", got)
	}
	// A context one is not in says nothing about where one is.
	if len(b.Labels) > 0 {
		t.Errorf("an inactive context contributed %q to the chrome", b.Labels)
	}
}

// Fail open: a probe that could not run has found nothing out, and nothing
// found out must hide nothing.
func TestContextUnknownWhenTheScriptFails(t *testing.T) {
	b := resolve(t, ContextDef{
		ID:     "kubernetes",
		Script: `echo "no configuration has been provided" >&2; exit 1`,
		Label:  `."current-context"`,
	})

	if got := b.State["kubernetes"]; got != StateUnknown {
		t.Errorf("state is %v, want unknown", got)
	}
	if len(b.Notes) != 1 || !strings.Contains(b.Notes[0], "no configuration") {
		t.Errorf("notes are %v, want the script's own complaint", b.Notes)
	}
}

// A script that ran but printed nothing to test has answered nothing either.
func TestContextUnknownWithoutAValue(t *testing.T) {
	b := resolve(t, ContextDef{ID: "quiet", Script: `true`, Label: `.`})

	if got := b.State["quiet"]; got != StateUnknown {
		t.Errorf("state is %v, want unknown", got)
	}
	if len(b.Notes) != 1 || !strings.Contains(b.Notes[0], "no JSON value") {
		t.Errorf("notes are %v, want one saying nothing was printed", b.Notes)
	}
}

// An expression that blows up on the data is the same kind of not-knowing.
func TestContextUnknownWhenThePredicateFails(t *testing.T) {
	b := resolve(t, ContextDef{
		ID:     "broken",
		Script: `echo '{"current-context":null}'`,
		Active: `."current-context" | test("x")`,
		Label:  `"broken"`,
	})

	if got := b.State["broken"]; got != StateUnknown {
		t.Errorf("state is %v, want unknown", got)
	}
	if len(b.Notes) != 1 || !strings.Contains(b.Notes[0], "active") {
		t.Errorf("notes are %v, want the field named", b.Notes)
	}
}

// No predicate at all: the identity case, active because the world answered.
func TestContextWithoutAPredicateIsActiveOnAnAnswer(t *testing.T) {
	b := resolve(t, ContextDef{
		ID:     "kubernetes",
		Script: `echo '{"current-context":"demo-cluster"}'`,
		Label:  `."current-context"`,
	})

	if got := b.State["kubernetes"]; got != StateActive {
		t.Errorf("state is %v, want active", got)
	}
	if got := strings.Join(b.Labels, " "); got != "demo-cluster" {
		t.Errorf("labels are %q, want what the label expression rendered", got)
	}
}

// A label that fails leaves the context active and the chrome one word short:
// nothing is hidden, so nothing is explained on screen.
func TestContextStaysActiveWhenTheLabelFails(t *testing.T) {
	b := resolve(t, ContextDef{
		ID:     "kubernetes",
		Script: `echo '{"current-context":"demo"}'`,
		Label:  `.missing | test("x")`,
	})

	if got := b.State["kubernetes"]; got != StateActive {
		t.Errorf("state is %v, want active", got)
	}
	if len(b.Labels) > 0 {
		t.Errorf("labels are %q, want none", b.Labels)
	}
	if len(b.Notes) != 1 || !strings.Contains(b.Notes[0], "label") {
		t.Errorf("notes are %v, want the field named", b.Notes)
	}
}

// The chrome drops from the right when it runs short, so the order the hat
// declared them in is what decides which label survives a narrow window.
func TestLabelsKeepDeclarationOrder(t *testing.T) {
	b := resolve(t,
		ContextDef{ID: "first", Script: `echo '{"n":"one"}'`, Label: `.n`},
		ContextDef{ID: "second", Script: `echo '{"n":"two"}'`, Label: `.n`},
		ContextDef{ID: "third", Script: `echo '{"n":"three"}'`, Label: `.n`},
	)

	if got := strings.Join(b.Labels, " "); got != "one two three" {
		t.Errorf("labels are %q, want them in declaration order", got)
	}
}

// Two contexts reading the same kubeconfig must not read it twice.
func TestIdenticalSourcesRunOncePerRound(t *testing.T) {
	counter := filepath.Join(t.TempDir(), "runs")
	script := `echo x >> ` + counter + `; echo '{"current-context":"demo-cloudscale-0"}'`

	b := resolve(t,
		ContextDef{ID: "kubernetes", Script: script, Label: `."current-context"`},
		ContextDef{ID: "cloudscale", Script: script,
			Active: `."current-context" | test("cloudscale")`, Label: `"cloudscale"`},
		// A third, differing only in its shell, is a different source.
		ContextDef{ID: "other", Shell: []string{"sh", "-e", "-c"}, Script: script, Label: `"other"`},
	)

	if len(b.Labels) != 3 {
		t.Fatalf("labels are %q, want one from each context", b.Labels)
	}
	out, err := os.ReadFile(counter)
	if err != nil {
		t.Fatal(err)
	}
	if got := len(strings.Fields(string(out))); got != 2 {
		t.Errorf("the script ran %d times, want once per distinct source", got)
	}
}

func TestContextRefusals(t *testing.T) {
	for _, tc := range []struct {
		name string
		def  ContextDef
		want string
	}{
		{"no script", ContextDef{ID: "x", Label: `"x"`}, `context "x": source.script is required`},
		{"says nothing", ContextDef{ID: "x", Script: `echo '{}'`}, `context "x": needs active or label`},
		{"broken predicate", ContextDef{ID: "x", Script: `echo '{}'`, Active: `.[`}, `context "x": active:`},
		{"broken label", ContextDef{ID: "x", Script: `echo '{}'`, Label: `.[`}, `context "x": label:`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := CompileContext(tc.def)
			if err == nil {
				t.Fatalf("compiled, want %q", tc.want)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error is %q, want it to contain %q", err, tc.want)
			}
		})
	}
}
