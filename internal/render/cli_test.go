package render

import (
	"bytes"
	"strings"
	"testing"

	"github.com/vshn/aldediggi/internal/catalog"
)

func TestTextIsWrittenVerbatim(t *testing.T) {
	var buf bytes.Buffer
	if err := CLI(&buf, catalog.Text{Lines: []string{"123", "456"}}); err != nil {
		t.Fatal(err)
	}
	if got := buf.String(); got != "123\n456\n" {
		t.Fatalf("got %q", got)
	}
}

func TestTableColumnsAreAligned(t *testing.T) {
	var buf bytes.Buffer
	tbl := catalog.Table{
		Cols: []string{"NAME", "STATUS"},
		Rows: []catalog.Row{{Cells: []string{"a", "Ready"}}, {Cells: []string{"much-longer-name", "NotReady"}}},
	}
	if err := CLI(&buf, tbl); err != nil {
		t.Fatal(err)
	}

	lines := strings.Split(strings.TrimRight(buf.String(), "\n"), "\n")
	if len(lines) != 3 {
		t.Fatalf("got %d lines, want 3", len(lines))
	}

	col := strings.Index(lines[0], "STATUS")
	for i, line := range lines[1:] {
		if got := strings.Index(line, strings.Fields(line)[1]); got != col {
			t.Errorf("row %d: second column at %d, want %d", i, got, col)
		}
	}
}

// Output that is not a terminal must stay free of escape codes so it can be
// piped into grep and friends.
func TestPipedOutputHasNoEscapeCodes(t *testing.T) {
	var buf bytes.Buffer
	tbl := catalog.Table{Cols: []string{"NAME"}, Rows: []catalog.Row{{Cells: []string{"a"}}}}
	if err := CLI(&buf, tbl); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(buf.String(), "\x1b") {
		t.Errorf("escape codes in piped output: %q", buf.String())
	}
}

func TestUnsupportedResultIsAnError(t *testing.T) {
	if err := CLI(&bytes.Buffer{}, nil); err == nil {
		t.Fatal("want an error for a nil result")
	}
}

// tabwriter counts escape sequences as cell content, so a styled header used
// to come out four columns short of the rows below it.
func TestStyledHeaderStaysAligned(t *testing.T) {
	tbl := catalog.Table{
		Cols: []string{"NAME", "STATUS"},
		Rows: []catalog.Row{{Cells: []string{"master-000000", "Ready"}}, {Cells: []string{"a", "NotReady"}}},
	}

	var buf bytes.Buffer
	if err := cli(&buf, tbl, true); err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(buf.String(), "\x1b[1m") {
		t.Fatal("header is not styled")
	}

	lines := strings.Split(strings.TrimRight(visible(buf.String()), "\n"), "\n")
	if len(lines) != 3 {
		t.Fatalf("got %d lines, want 3", len(lines))
	}

	want := strings.Index(lines[1], "Ready")
	if got := strings.Index(lines[0], "STATUS"); got != want {
		t.Errorf("header column at %d, rows at %d", got, want)
	}
}

// visible strips SGR sequences so that alignment can be measured the way a
// terminal would show it.
func visible(s string) string {
	var b strings.Builder
	for {
		i := strings.Index(s, "\x1b[")
		if i < 0 {
			b.WriteString(s)
			return b.String()
		}
		b.WriteString(s[:i])
		j := strings.IndexByte(s[i:], 'm')
		if j < 0 {
			return b.String()
		}
		s = s[i+j+1:]
	}
}
