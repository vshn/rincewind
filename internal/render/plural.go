package render

import "fmt"

// Plural counts something in words. It lives here, in the package both the
// TUI and the commands render through, because two copies of it drifted
// apart once already.
func Plural(n int, what string) string {
	if n == 1 {
		return fmt.Sprintf("1 %s", what)
	}
	return fmt.Sprintf("%d %ss", n, what)
}
