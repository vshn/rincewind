# Platform support

Targets: macOS (arm64/amd64) and Linux (amd64/arm64, various distributions).
Everything is pure Go, so `CGO_ENABLED=0` produces a static binary — no glibc/musl question.
The only runtime dependency is `kubectl`.

## Do the work in Go, not in shell utilities

Shell utilities differ between BSD and GNU userlands;
in-process replacements remove the difference instead of abstracting it:

| instead of | varies across | use |
|---|---|---|
| `pbcopy` / `xclip` / `wl-copy` | macOS / X11 / Wayland | `tea.SetClipboard` (OSC 52) |
| `base64 -d` | BSD historically wanted `-D` | `encoding/base64` |
| `nc -z` | BSD / GNU / nmap-ncat / busybox | `net.DialTimeout` |
| `sleep 0.2` | fractions unsupported on busybox | `time.Sleep` |
| `column -t -s` | BSD vs util-linux | `internal/render` |
| `jq` | external, version drift | typed structs |
| `trap … EXIT INT TERM` | signal handling | `context` + `Setpgid` |

Clipboard is worth calling out: OSC 52 goes through the terminal instead of a binary,
so one code path works on macOS, X11, Wayland **and over SSH**.
It requires `set -g set-clipboard on` in tmux, and some terminals disable it.

## Probe capabilities, not the OS

`runtime.GOOS == "linux"` says nothing about whether a browser can be opened — Wayland vs X11 vs headless vs SSH is not an OS distinction.
Resolution order for opening a URL (pending, for `argo ui`):

```
$BROWSER → darwin: open → xdg-open (only if $DISPLAY or $WAYLAND_DISPLAY)
         → otherwise: print the URL and copy it
```

The last step is expected behaviour on a jump host, not a failure.
Note that `open -a "Firefox"` has no xdg-open equivalent;
app selection on Linux goes through `$BROWSER`.

Terminal capability detection (colour profile, background) is handled by bubbletea/lipgloss.
Respect `NO_COLOR`, and fall back to plain text when stdout is not a TTY.

## `aldediggi doctor` (pending)

Prints the detected environment: OS/arch, kubectl path and version,
browser mechanism, OSC 52 status, colour profile.
First question on any bug report from an unfamiliar bastion host.
