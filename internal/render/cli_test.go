package render

import (
	"bytes"
	"os"
	"strings"
	"testing"

	"github.com/vshn/rincewind/internal/engine"
)

func cliTable() engine.Result {
	return engine.Result{
		Cols: []string{"NAME", "STATE"},
		Rows: []engine.Row{
			{Cells: []engine.Cell{{Text: "worker"}, {Text: "Degraded", Emph: engine.Bad}}},
			{Cells: []engine.Cell{{Text: "api"}, {Text: "Healthy", Emph: engine.Good}}},
		},
	}
}

// Piping into a file must give text a later tool can read.
func TestCLIWithoutColourIsPlain(t *testing.T) {
	var b bytes.Buffer
	if err := cli(&b, cliTable(), false); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(b.String(), "\x1b") {
		t.Errorf("an escape sequence reached a plain writer: %q", b.String())
	}

	want := "NAME    STATE\nworker  Degraded\napi     Healthy\n"
	if b.String() != want {
		t.Errorf("got:\n%q\nwant:\n%q", b.String(), want)
	}
}

// Styled or not, the columns line up: escape codes go on after the layout is
// fixed, which is the whole reason for that rule.
func TestCLIColourKeepsTheColumns(t *testing.T) {
	var plain, fancy bytes.Buffer
	if err := cli(&plain, cliTable(), false); err != nil {
		t.Fatal(err)
	}
	if err := cli(&fancy, cliTable(), true); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(fancy.String(), "\x1b") {
		t.Fatal("the styled path produced no styling")
	}

	if got := stripANSI(fancy.String()); got != plain.String() {
		t.Errorf("styling changed the layout:\n%q\nwant\n%q", got, plain.String())
	}
}

// A log goes out as the script wrote it, indentation and all — the same rule
// the TUI follows, for the same reason.
func TestCLITextIsVerbatim(t *testing.T) {
	var b bytes.Buffer
	res := engine.Result{Lines: []string{"one", "    indented", "three"}}
	if err := cli(&b, res, true); err != nil {
		t.Fatal(err)
	}
	if b.String() != "one\n    indented\nthree\n" {
		t.Errorf("the lines were reshaped: %q", b.String())
	}
}

// A bytes.Buffer is not a terminal, and neither is a file.
func TestColourOnlyForATerminal(t *testing.T) {
	if Colour(&bytes.Buffer{}) {
		t.Error("a buffer was taken for a terminal")
	}
	f, err := os.CreateTemp(t.TempDir(), "out")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	if Colour(f) {
		t.Error("a file was taken for a terminal")
	}
}

// stripANSI removes escape sequences, so the two renderings can be compared
// as text.
func stripANSI(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] != 0x1b {
			b.WriteByte(s[i])
			continue
		}
		for i < len(s) && s[i] != 'm' {
			i++
		}
	}
	return b.String()
}
