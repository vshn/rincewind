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

// A hat may name the least version it is written for, when a field's absence
// would be dangerous rather than harmless — which the warning path above
// cannot know about and only the author can say.
func TestAHatMayRequireAVersion(t *testing.T) {
	for _, tc := range []struct {
		written string
		want    string
	}{
		{`">= 0.8"`, "0.8.0"},
		{`">=0.8.1"`, "0.8.1"},
		{`"0.9.3"`, "0.9.3"},
		{`">= v1.0"`, "1.0.0"},
	} {
		t.Run(tc.written, func(t *testing.T) {
			loaded, err := Parse(strings.NewReader("rincewind: " + tc.written + "\n" + valid))
			if err != nil {
				t.Fatal(err)
			}
			// Normalised, so what is reported reads the same however it was
			// written.
			if loaded.Requires != tc.want {
				t.Errorf("requires %q, want %q", loaded.Requires, tc.want)
			}
		})
	}
}

func TestARequirementThatIsNotOneIsRefused(t *testing.T) {
	for _, written := range []string{`"neulich"`, `"> 0.8"`, `"< 2.0"`, `"0.8.x"`, `"~> 0.8"`} {
		t.Run(written, func(t *testing.T) {
			_, err := Parse(strings.NewReader("rincewind: " + written + "\n" + valid))
			if err == nil {
				t.Fatal("loaded")
			}
			if !strings.Contains(err.Error(), "is not a version requirement") {
				t.Errorf("the error %q does not say what is wrong", err)
			}
		})
	}
}

func TestUnmet(t *testing.T) {
	for _, tc := range []struct {
		name     string
		requires string
		version  string
		unmet    bool
		checked  bool
	}{
		{"met exactly", "0.8.0", "v0.8.0", false, true},
		{"met by a later release", "0.8.0", "v0.9.1", false, true},
		{"met by a later major", "0.8.0", "v1.0.0", false, true},
		{"unmet", "0.8.0", "v0.6.0", true, true},
		{"unmet by a patch", "0.6.1", "v0.6.0", true, true},
		{"nothing required", "", "v0.1.0", false, true},

		// git describe names the nearest ancestor tag, so the tag in the
		// stamp is a real lower bound on what the build holds — and a real
		// upper one on what it does not. Both directions are measured.
		{"a build past a tag that meets it", "0.8.0", "v0.8.0-3-gabc123", false, true},
		{"a build past a tag that does not", "0.8.0", "v0.6.0-9-gb5802a3", true, true},

		// The two stamps that say nothing comparable: no version at all, and
		// a working tree matching no commit — where the feature the hat is
		// waiting for lives while someone is writing it.
		{"a plain go run", "0.8.0", "dev", false, false},
		{"a dirty working tree", "0.8.0", "v0.6.0-9-gb5802a3-dirty", false, false},
		{"a dirty tree on a tag", "0.8.0", "v0.6.0-dirty", false, false},
		{"a plain go run with nothing to check", "", "dev", false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			why, checked := Hat{Requires: tc.requires}.Unmet(tc.version)
			if checked != tc.checked {
				t.Errorf("checked is %v, want %v", checked, tc.checked)
			}
			if tc.unmet && why == "" {
				t.Errorf("%s satisfies >= %s, want it refused", tc.version, tc.requires)
			}
			if !tc.unmet && why != "" {
				t.Errorf("%s does not satisfy >= %s: %s", tc.version, tc.requires, why)
			}
			if tc.unmet && !strings.Contains(why, tc.version) {
				t.Errorf("the reason %q does not name the running version", why)
			}
		})
	}
}

// The hat requires the most any of its files requires: refusing a second
// declaration would be refusing two files for agreeing.
func TestTheHighestRequirementWins(t *testing.T) {
	body := `
views:
  - id: %s
    source: { script: "echo '{}'" }
    rows: .
    columns: [{name: X, expr: "1"}]
`
	loaded, err := hatDir(t, map[string]string{
		"a.yaml": "rincewind: \">= 0.8\"\n" + strings.Replace(body, "%s", "a.one", 1),
		"b.yaml": "rincewind: \">= 0.9.2\"\n" + strings.Replace(body, "%s", "b.two", 1),
		"c.yaml": strings.Replace(body, "%s", "c.three", 1),
	})
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Requires != "0.9.2" {
		t.Errorf("requires %q, want the highest of the three", loaded.Requires)
	}
}
