package catalog

import (
	"fmt"
	"strings"
	"time"
)

// Layouts accepted for a point in time, most explicit first. Anything without
// a zone is read in the machine's local zone: an operator scheduling a
// maintenance window thinks in wall clock, and typing an offset by hand is how
// you end up an hour off across a daylight-saving boundary.
var timeLayouts = []string{
	time.RFC3339,
	"2006-01-02T15:04:05",
	"2006-01-02 15:04:05",
	"2006-01-02T15:04",
	"2006-01-02 15:04",
	"2006-01-02",
}

// ParseTime reads a point in time. A value starting with "+" is a duration
// from base, so an end can be given as "+9h" rather than restated in full.
func ParseTime(value string, base time.Time) (time.Time, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return time.Time{}, fmt.Errorf("empty time")
	}

	if strings.HasPrefix(value, "+") {
		d, err := time.ParseDuration(value[1:])
		if err != nil {
			return time.Time{}, fmt.Errorf("%q is not a duration: %w", value, err)
		}
		if base.IsZero() {
			return time.Time{}, fmt.Errorf("%q is relative, but there is nothing to count from", value)
		}
		return base.Add(d), nil
	}

	for _, layout := range timeLayouts {
		if t, err := time.ParseInLocation(layout, value, time.Local); err == nil {
			return t, nil
		}
	}

	return time.Time{}, fmt.Errorf("%q is not a time; use 2006-01-02 15:04, a full RFC 3339 timestamp, or +2h", value)
}

// FormatTime renders a time the way the API expects it, offset included.
func FormatTime(t time.Time) string { return t.Format(time.RFC3339) }

// showTime renders a time for a table: no zone, because every row carries the
// same one and the column is narrow.
func showTime(t time.Time) string { return t.Format("2006-01-02 15:04") }

// showDuration renders a span the way one would say it out loud.
func showDuration(d time.Duration) string {
	d = d.Round(time.Minute)
	if d < 0 {
		d = -d
	}

	days := int(d.Hours()) / 24
	hours := int(d.Hours()) % 24
	mins := int(d.Minutes()) % 60

	switch {
	case days > 0 && hours > 0:
		return fmt.Sprintf("%dd%dh", days, hours)
	case days > 0:
		return fmt.Sprintf("%dd", days)
	case hours > 0 && mins > 0:
		return fmt.Sprintf("%dh%dm", hours, mins)
	case hours > 0:
		return fmt.Sprintf("%dh", hours)
	default:
		return fmt.Sprintf("%dm", mins)
	}
}

func showTimeOf(rfc3339 string) string {
	at, err := time.Parse(time.RFC3339, rfc3339)
	if err != nil {
		return rfc3339
	}
	return showTime(at)
}
