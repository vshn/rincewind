package engine

import "fmt"

// Plural counts something in words. It lives in this package rather than in
// render because render, tui and cmd all import this one, and because the
// engine counts rows into a confirmation itself — a helper the rule depends
// on cannot sit in a package the rule's home may not import.
func Plural(n int, what string) string {
	if n == 1 {
		return fmt.Sprintf("1 %s", what)
	}
	return fmt.Sprintf("%d %ss", n, what)
}
