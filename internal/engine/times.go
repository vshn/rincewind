package engine

import (
	"fmt"
	"strings"
	"time"
)

// Layouts accepted for a point in time, most explicit first. Anything without
// a zone is read in the machine's local zone: the values come from APIs that
// write RFC 3339, but a hand-built script may well emit wall clock.
var timeLayouts = []string{
	time.RFC3339,
	"2006-01-02T15:04:05",
	"2006-01-02 15:04:05",
	"2006-01-02T15:04",
	"2006-01-02 15:04",
	"2006-01-02",
}

func parseWhen(value string) (time.Time, bool) {
	value = strings.TrimSpace(value)
	for _, layout := range timeLayouts {
		if t, err := time.ParseInLocation(layout, value, time.Local); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}

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

// Column formats. A value that does not parse as a time passes through
// unchanged: showing the raw value beats hiding it behind an error.
const (
	FormatNone      = ""
	FormatTimestamp = "timestamp" // 2026-08-18 11:48
	FormatAge       = "age"       // 9h, 2d1h — the span since the value
)

func formatCell(text, format string, now time.Time) string {
	switch format {
	case FormatTimestamp:
		if t, ok := parseWhen(text); ok {
			return showTime(t)
		}
	case FormatAge:
		if t, ok := parseWhen(text); ok {
			return showDuration(now.Sub(t))
		}
	}
	return text
}
