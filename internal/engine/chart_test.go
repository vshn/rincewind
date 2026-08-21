package engine

import (
	"strings"
	"testing"
	"time"
)

// A chart pipeline reads each rows result as a series object. Duplicate
// names extend the earlier series, and points come out in time order no
// matter how they went in.
func TestChartMergesAndOrders(t *testing.T) {
	res := runDef(t, Def{
		ID:    "t",
		Chart: true,
		Script: `echo '{"name":"api","points":[[1755600900,2],[1755600000,1]]}'
echo '{"name":"api","points":[[1755601800,3]]}'`,
		Rows: ".",
	})

	if !res.IsChart() {
		t.Fatal("the result does not read as a chart")
	}
	if len(res.Series) != 1 {
		t.Fatalf("got %d series, want the two api chunks merged into one", len(res.Series))
	}
	pts := res.Series[0].Points
	if len(pts) != 3 || pts[0].V != 1 || pts[1].V != 2 || pts[2].V != 3 {
		t.Fatalf("points %+v are not in time order", pts)
	}
	if want := time.Unix(1755600000, 0); !pts[0].T.Equal(want) {
		t.Errorf("first point at %v, want %v", pts[0].T, want)
	}
}

// A malformed series becomes a note and is skipped; unreadable points are
// dropped with one note per series. The view survives either way, exactly as
// a broken column does not take down a table.
func TestChartNotesBadSeriesAndPoints(t *testing.T) {
	res := runDef(t, Def{
		ID:    "t",
		Chart: true,
		Script: `echo '"not a series object"'
echo '{"name":"api","points":[[1755600000,1],[1755600900,"wat"],"no pair"]}'
echo '{"points":[[1755600000,1]]}'`,
		Rows: ".",
	})

	if len(res.Series) != 1 || res.Series[0].Name != "api" {
		t.Fatalf("got %+v, want only the readable api series", res.Series)
	}
	if len(res.Series[0].Points) != 1 {
		t.Fatalf("points %+v, want only the readable one kept", res.Series[0].Points)
	}
	if len(res.Notes) != 3 {
		t.Fatalf("notes %v, want one per problem: no object, bad points, no name", res.Notes)
	}
	joined := strings.Join(res.Notes, "\n")
	for _, want := range []string{"not a series object", "2 of 3 points", "no name"} {
		if !strings.Contains(joined, want) {
			t.Errorf("notes %q do not mention %q", joined, want)
		}
	}
}

// An empty stream is still a chart — the shape marker is what the TUI and
// the CLI switch on, and it must not depend on the cluster having data.
func TestChartStaysAChartWhenEmpty(t *testing.T) {
	res := runDef(t, Def{ID: "t", Chart: true, Script: "true", Rows: "."})
	if !res.IsChart() || len(res.Series) != 0 {
		t.Fatalf("got %+v, want an empty chart result", res)
	}
}
