package tui

import (
	"fmt"
	"os"
	"os/exec"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/vshn/rincewind/internal/engine"
	"github.com/vshn/rincewind/internal/render"
)

// osc52Warn is where a copy gets big enough to be worth a word about. OSC 52
// hands the text to the terminal as one escape sequence, and terminals cap
// how long one may be — xterm's default is far below this, tmux and the rest
// sit somewhere above. There is no way to ask whether it arrived: the write
// is one-way, and reading the clipboard back is what terminals disable for
// security. So the count is said out loud rather than a success claimed.
const osc52Warn = 64 << 10

// copyPane puts what the output pane is showing on the system clipboard,
// through OSC 52 rather than pbcopy or xclip: the terminal owns the
// clipboard, this tool merely asks — which is also what makes it work over
// ssh, where a local pbcopy would copy on the wrong machine.
//
// A selection narrows it. Copying is not a mutation, so no selection means
// all of it rather than a refusal; the notice says which happened, since
// "copied 3 rows" and "copied 41 rows" is the whole difference.
func (m *model) copyPane() tea.Cmd {
	lines, what := m.copyLines()
	if len(lines) == 0 {
		m.notice = "nothing to copy"
		return nil
	}

	text := strings.Join(lines, "\n") + "\n"
	m.notice = fmt.Sprintf("copied %s", what)

	// OSC 52 goes out either way: it costs nothing, and it is the only thing
	// that reaches the right machine over ssh.
	if m.clipboard == nil {
		if len(text) >= osc52Warn {
			m.notice += fmt.Sprintf(" (%d KiB — some terminals drop this much)", len(text)>>10)
		}
		return tea.SetClipboard(text)
	}
	// With a local writer there is no length limit to warn about, and the
	// exit status says whether it worked — which OSC 52 never can.
	return tea.Batch(tea.SetClipboard(text), writeLocal(m.clipboard, text))
}

// copyLines is what would go on the clipboard, and what to call it. Plain
// text throughout: an escape sequence pasted into a ticket is noise, and the
// styling was never part of what the cells say.
func (m *model) copyLines() (lines []string, what string) {
	t := m.cur()

	// A job's commands are worth copying on their own — that is how the
	// thing this tool just ran gets into a ticket or a shell. Its lines are
	// built styled and padded for the pane, so both come off again: a paste
	// carrying escape codes and trailing spaces is a paste one has to clean.
	if t.job != nil {
		for _, line := range t.job.lines("") {
			lines = append(lines, strings.TrimRight(ansi.Strip(line), " \t"))
		}
		return lines, engine.Plural(len(lines), "line")
	}

	vis := m.visible()
	if vis.IsText() {
		return vis.Lines, engine.Plural(len(vis.Lines), "line")
	}

	// A chart copies as it shows — the braille plot with its legend — drawn
	// again without colour, because escape codes pasted into a ticket are
	// noise the way they are in a job's lines.
	if vis.IsChart() {
		for _, line := range render.Chart(vis, m.contentWidth(), m.vpHeight(), false) {
			lines = append(lines, strings.TrimRight(line, " "))
		}
		return lines, engine.Plural(len(lines), "line")
	}

	rows := vis.Rows
	if len(t.selection) > 0 {
		kept := rows[:0:0]
		for _, r := range rows {
			if t.selection[r.Key] {
				kept = append(kept, r)
			}
		}
		rows = kept
	}
	if len(rows) == 0 {
		return nil, ""
	}

	// The header comes along: a table of values whose columns are unnamed is
	// a table one has to remember the shape of.
	vis.Rows = rows
	return render.Table(vis, render.Style{}), engine.Plural(len(rows), "row")
}

// clipboardTools are the local clipboard writers, in the order they are
// tried. Probed on PATH rather than picked by runtime.GOOS: what matters is
// whether the program is there, and a Mac without pbcopy and a Wayland
// session without wl-copy both exist.
//
// This is the exception to "no shelling out". OSC 52 was meant to be the Go
// replacement for pbcopy, and for ssh it is the only thing that works at all
// — but Terminal.app has never implemented it, and the macOS clipboard is
// NSPasteboard, which needs cgo, which the static build does not have. The
// rule's premise simply fails here.
var clipboardTools = [][]string{
	{"pbcopy"},
	{"wl-copy"},
	{"xclip", "-selection", "clipboard"},
	{"xsel", "--clipboard", "--input"},
}

// localClipboard finds a writer for the clipboard of the machine the user is
// looking at. In an ssh session there is none: xclip on the far end would
// copy into a clipboard nobody can paste from, which is worse than not
// copying, and OSC 52 is exactly the case ssh has.
func localClipboard() []string {
	if os.Getenv("SSH_CONNECTION") != "" || os.Getenv("SSH_TTY") != "" {
		return nil
	}
	for _, argv := range clipboardTools {
		if _, err := exec.LookPath(argv[0]); err == nil {
			return argv
		}
	}
	return nil
}

// copiedMsg reports how the local write went. Nothing is sent when it
// worked: the notice already says what was copied, and a second one saying
// it again would be noise.
type copiedMsg struct{ err error }

// writeLocal pipes the text into the clipboard program. In a tea.Cmd, like
// every other process this tool starts: spawning one in Update would block
// the paint.
func writeLocal(argv []string, text string) tea.Cmd {
	return func() tea.Msg {
		cmd := exec.Command(argv[0], argv[1:]...)
		cmd.Stdin = strings.NewReader(text)
		return copiedMsg{err: cmd.Run()}
	}
}
