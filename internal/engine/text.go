package engine

import "strings"

// oneLine keeps a multi-line value from breaking the table layout.
func oneLine(s string) string {
	return strings.Join(strings.Fields(strings.ReplaceAll(s, "\n", " ")), " ")
}
