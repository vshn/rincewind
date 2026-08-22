package config

import (
	"strings"
	"testing"
)

// A key this build does not know is reported and ignored rather than
// refused. A hat travels separately from the binary, so one person adding a
// field to a shared hat must not take the whole tool away from everyone who
// has not upgraded — and nothing in the file tells a typo apart from a hat
// written for a newer rincewind.
func TestAnUnknownKeyIsAWarningRatherThanARefusal(t *testing.T) {
	loaded, err := Parse(strings.NewReader(strings.Replace(valid, "emphasis:", "emphsis:", 1)))
	if err != nil {
		t.Fatalf("an unknown key took the hat down: %v", err)
	}
	// Everything the schema does know still works.
	if len(loaded.Views) != 1 || loaded.Views[0].ID != "demo.list" {
		t.Fatalf("got %+v, want the view to have compiled anyway", loaded.Views)
	}
	if len(loaded.Warnings) != 1 {
		t.Fatalf("warnings are %v, want one", loaded.Warnings)
	}
	for _, want := range []string{"line ", `unknown key "emphsis"`, "a column"} {
		if !strings.Contains(loaded.Warnings[0], want) {
			t.Errorf("the warning %q does not mention %q", loaded.Warnings[0], want)
		}
	}
}

// The message has to say where, in the words the hat is written in — a Go
// type name is no use to whoever has to fix it.
func TestAnUnknownKeyIsNamedWhereItSits(t *testing.T) {
	for _, tc := range []struct {
		name string
		yaml string
		want string
	}{
		{
			name: "in a hat",
			yaml: "kontexte: []\n" + valid,
			want: `line 1: unknown key "kontexte" in a hat`,
		},
		{
			name: "in a view",
			yaml: strings.Replace(valid, "    rows: .items[]", "    zukunft: ja\n    rows: .items[]", 1),
			want: `unknown key "zukunft" in a view`,
		},
		{
			name: "in a column",
			yaml: strings.Replace(valid, "        expr: .name", "        expr: .name\n        glitzer: true", 1),
			want: `unknown key "glitzer" in a column`,
		},
		{
			name: "in a context",
			yaml: "contexts:\n  - id: x\n    label: '\"x\"'\n    funkel: true\n    source: { script: \"echo '{}'\" }\n" + valid,
			want: `unknown key "funkel" in a context`,
		},
		{
			name: "in an action",
			yaml: valid + "    actions:\n      - key: t\n        name: touch\n        script: \"true\"\n        wumms: ja\n",
			want: `unknown key "wumms" in an action`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			loaded, err := Parse(strings.NewReader(tc.yaml))
			if err != nil {
				t.Fatalf("refused: %v", err)
			}
			if len(loaded.Warnings) != 1 || !strings.Contains(loaded.Warnings[0], tc.want) {
				t.Errorf("warnings are %v, want one containing %q", loaded.Warnings, tc.want)
			}
		})
	}
}

// Only the unknown-key class is downgraded. Everything the schema does know
// is still held to it here, which is the whole of "config mistakes die at
// load time" and is not what changed.
func TestARealTypeErrorStillKillsTheLoad(t *testing.T) {
	yaml := strings.Replace(valid, "    rows: .items[]", "    refresh: [nicht, ein, string]\n    rows: .items[]", 1)
	if _, err := Parse(strings.NewReader(yaml)); err == nil {
		t.Fatal("a value of the wrong type loaded")
	} else if !strings.Contains(err.Error(), "cannot unmarshal") {
		t.Errorf("the error %q is not about the type", err)
	}
}

// A hat that is both wrong and ahead is refused for the wrong half alone.
func TestAFatalErrorBesideAnUnknownKeyStillRefuses(t *testing.T) {
	yaml := strings.Replace(valid, "    rows: .items[]", "    zukunft: ja\n    refresh: [a, b]\n    rows: .items[]", 1)
	if _, err := Parse(strings.NewReader(yaml)); err == nil {
		t.Fatal("loaded despite a type error")
	} else if strings.Contains(err.Error(), "zukunft") {
		t.Errorf("the refusal blames the unknown key too: %q", err)
	}
}

// A warning about line 14 is no use without the file it is line 14 of.
func TestWarningsNameTheirFile(t *testing.T) {
	loaded, err := hatDir(t, map[string]string{
		"ahead.yaml": `
kontexte: []
views:
  - id: a.one
    source: { script: "echo '{}'" }
    rows: .
    columns: [{name: X, expr: "1"}]
`,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded.Warnings) != 1 || !strings.Contains(loaded.Warnings[0], "ahead.yaml") {
		t.Errorf("warnings are %v, want the file named", loaded.Warnings)
	}
}

// The point of the whole change, in the shape it will actually take: a hat
// grows a feature this build has never heard of, and the build keeps working.
// `needs:` ignored means the view is shown, which is the same fail-open the
// contexts themselves follow.
func TestAHatFromTheFutureStillRuns(t *testing.T) {
	loaded, err := Parse(strings.NewReader(`
contexts:
  - id: cloudscale
    source: { script: "echo '{}'" }
    label: '"cloudscale"'
    someday: whatever
views:
  - id: demo.list
    needs: cloudscale
    source: { script: "echo '{\"n\":1}'" }
    rows: .
    columns: [{name: N, expr: .n}]
    tomorrow: also this
`))
	if err != nil {
		t.Fatalf("a hat from the future took the tool down: %v", err)
	}
	if len(loaded.Views) != 1 {
		t.Fatalf("%d views, want the one it declared", len(loaded.Views))
	}
	if len(loaded.Warnings) != 2 {
		t.Errorf("warnings are %v, want one per unknown key", loaded.Warnings)
	}
	res, err := loaded.Views[0].Run(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Rows) != 1 {
		t.Errorf("%d rows, want the view to run as usual", len(res.Rows))
	}
}
