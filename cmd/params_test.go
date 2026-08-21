package cmd

import (
	"testing"
)

// -p is name=value; the value side may hold anything, = included, because a
// jq expression or an image reference is exactly what gets passed.
func TestParseParams(t *testing.T) {
	got, err := parseParams([]string{"service=api", "expr=a=b"})
	if err != nil {
		t.Fatal(err)
	}
	if got["service"] != "api" || got["expr"] != "a=b" {
		t.Errorf("parsed %v, want the value kept whole past the first =", got)
	}

	for _, bad := range [][]string{
		{"noequals"},
		{"=value"},
		{"x=1", "x=2"},
		// The first value being empty is still a value: presence decides a
		// duplicate, not what it held.
		{"x=", "x=2"},
	} {
		if _, err := parseParams(bad); err == nil {
			t.Errorf("%v was accepted", bad)
		}
	}
}
