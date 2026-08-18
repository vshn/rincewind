package catalog

import (
	"testing"
	"time"
)

func TestParseTimeAcceptsWallClockInTheLocalZone(t *testing.T) {
	got, err := ParseTime("2026-05-25 09:00", time.Time{})
	if err != nil {
		t.Fatal(err)
	}

	want := time.Date(2026, 5, 25, 9, 0, 0, 0, time.Local)
	if !got.Equal(want) {
		t.Errorf("got %s, want %s", got, want)
	}
}

// A value that already carries an offset must survive untouched, whatever the
// machine's zone is.
func TestParseTimeKeepsAnExplicitOffset(t *testing.T) {
	got, err := ParseTime("2026-05-25T09:00:00+05:00", time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if got.UTC() != time.Date(2026, 5, 25, 4, 0, 0, 0, time.UTC) {
		t.Errorf("got %s", got.UTC())
	}
}

func TestParseTimeHandlesRelativeEnds(t *testing.T) {
	base := time.Date(2026, 5, 25, 9, 0, 0, 0, time.Local)

	got, err := ParseTime("+9h", base)
	if err != nil {
		t.Fatal(err)
	}
	if want := base.Add(9 * time.Hour); !got.Equal(want) {
		t.Errorf("got %s, want %s", got, want)
	}
}

func TestParseTimeRejectsRelativeWithoutABase(t *testing.T) {
	if _, err := ParseTime("+9h", time.Time{}); err == nil {
		t.Fatal("want an error")
	}
}

func TestParseTimeExplainsItself(t *testing.T) {
	_, err := ParseTime("next tuesday", time.Time{})
	if err == nil {
		t.Fatal("want an error")
	}
	if got := err.Error(); !contains(got, "RFC 3339") || !contains(got, "+2h") {
		t.Errorf("error does not say what is accepted: %q", got)
	}
}

func contains(s, sub string) bool { return len(s) >= len(sub) && (s == sub || indexOf(s, sub) >= 0) }

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

// The API wants an offset, and this is the only place it is produced.
func TestFormatTimeCarriesTheOffset(t *testing.T) {
	zone := time.FixedZone("CEST", 2*60*60)
	got := FormatTime(time.Date(2026, 5, 25, 9, 0, 0, 0, zone))

	if got != "2026-05-25T09:00:00+02:00" {
		t.Errorf("got %q", got)
	}
}

func TestShowDuration(t *testing.T) {
	for in, want := range map[time.Duration]string{
		9 * time.Hour:    "9h",
		90 * time.Minute: "1h30m",
		45 * time.Minute: "45m",
		49 * time.Hour:   "2d1h",
		48 * time.Hour:   "2d",
	} {
		if got := showDuration(in); got != want {
			t.Errorf("%s -> %q, want %q", in, got, want)
		}
	}
}
