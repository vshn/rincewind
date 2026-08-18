// Cross-group helpers for squeezing command output into cells and lines.
// Anything used by more than one group lives here rather than in whichever
// group happened to need it first.

package catalog

import "strings"

// oneLine keeps a multi-line message from breaking the table layout.
func oneLine(s string) string {
	return strings.Join(strings.Fields(strings.ReplaceAll(s, "\n", " ")), " ")
}

func splitLines(s string) []string {
	s = strings.TrimRight(s, "\n")
	if s == "" {
		return nil
	}
	return strings.Split(s, "\n")
}

func firstText(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

func joinNames(names []string) string { return strings.Join(names, ", ") }
