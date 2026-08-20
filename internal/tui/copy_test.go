package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/vshn/rincewind/internal/engine"
)

// clipboard runs the command Y produced and returns what it handed the
// terminal. The message type is unexported by bubbletea, so it is read as the
// string it is rather than type-asserted.
func clipboard(t *testing.T, cmd tea.Cmd) string {
	t.Helper()
	if cmd == nil {
		return ""
	}
	return fmt.Sprint(cmd())
}

func copying(t *testing.T) *model {
	t.Helper()
	m := newModel(sampleViews())
	next, _ := m.Update(tea.WindowSizeMsg{Width: 96, Height: 20})
	m = next.(*model)

	m.Update(key("enter"))
	deliver(t, m, nodesTable())
	m.Update(key("tab"))
	return m
}

// The point: what the pane shows, as text one can paste.
func TestCopyTakesTheWholeTable(t *testing.T) {
	m := copying(t)
	_, cmd := m.Update(key("Y"))

	got := clipboard(t, cmd)
	if strings.Contains(got, "\x1b") {
		t.Errorf("an escape sequence went to the clipboard: %q", got)
	}
	lines := strings.Split(strings.TrimRight(got, "\n"), "\n")
	if len(lines) != 5 {
		t.Fatalf("copied %d lines, want a header and four rows:\n%s", len(lines), got)
	}
	if !strings.HasPrefix(lines[0], "NAME") {
		t.Errorf("the header did not come along: %q", lines[0])
	}
	if !strings.Contains(m.notice, "4 rows") {
		t.Errorf("the notice is %q, want it to say how much was copied", m.notice)
	}
}

// The filter is what the pane is showing, so it is what gets copied.
func TestCopyFollowsTheFilter(t *testing.T) {
	m := copying(t)
	typeFilter(m, "NotReady")

	got := clipboard(t, m.copyPane())
	if strings.Contains(got, "worker-01") {
		t.Errorf("a filtered-out row was copied:\n%s", got)
	}
	if !strings.Contains(got, "worker-02") {
		t.Errorf("the matching row is missing:\n%s", got)
	}
	if !strings.Contains(m.notice, "1 row") {
		t.Errorf("the notice is %q", m.notice)
	}
}

// A selection narrows it further — copying is not a mutation, so no
// selection means all of it rather than a refusal.
func TestCopyNarrowsToTheSelection(t *testing.T) {
	m := copying(t)
	m.Update(key("space")) // select the row under the cursor

	got := clipboard(t, m.copyPane())
	lines := strings.Split(strings.TrimRight(got, "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("copied %d lines, want the header and one selected row:\n%s", len(lines), got)
	}
	if !strings.Contains(m.notice, "1 row") {
		t.Errorf("the notice is %q, want the selected count", m.notice)
	}
}

// A log is copied the way it is read: verbatim, indentation kept.
func TestCopyTextIsVerbatim(t *testing.T) {
	m := showing(t)
	got := clipboard(t, m.copyPane())

	if !strings.Contains(got, "    indented continuation line") {
		t.Error("the leading whitespace was lost on the way to the clipboard")
	}
	if !strings.Contains(m.notice, "42 lines") {
		t.Errorf("the notice is %q", m.notice)
	}
}

// An empty pane says so instead of clearing the clipboard.
func TestCopyNothingSaysSo(t *testing.T) {
	m := copying(t)
	typeFilter(m, "no-such-node")

	if cmd := m.copyPane(); cmd != nil {
		t.Errorf("an empty pane still wrote %q to the clipboard", clipboard(t, cmd))
	}
	if m.notice != "nothing to copy" {
		t.Errorf("the notice is %q", m.notice)
	}
}

// OSC 52 cannot be asked whether it worked, so a copy big enough to be
// dropped says how big it was rather than claiming success.
func TestLargeCopyWarnsAboutItsSize(t *testing.T) {
	m := newModel(textViews())
	next, _ := m.Update(tea.WindowSizeMsg{Width: 96, Height: 20})
	m = next.(*model)
	m.Update(key("enter"))

	long := make([]string, 2000)
	for i := range long {
		long[i] = strings.Repeat("x", 80)
	}
	m.Update(resultMsg{seq: m.seq, res: engine.Result{Lines: long}})

	m.copyPane()
	if !strings.Contains(m.notice, "KiB") {
		t.Errorf("the notice is %q, want the size of a copy this large", m.notice)
	}
}

// The commands a job ran are worth copying on their own: that is how what
// this tool just did reaches a ticket or a shell.
func TestCopyTakesAJobsCommands(t *testing.T) {
	m, _ := drilling(t)
	m.Update(key("space")) // an action needs a selection
	press(t, m, "u")       // unpause: no confirmation, runs at once

	if m.cur().job == nil {
		t.Fatal("the action opened no job tab")
	}
	got := clipboard(t, m.copyPane())
	if !strings.Contains(got, "resumed") {
		t.Errorf("the job's output is missing from the clipboard:\n%s", got)
	}
	if strings.Contains(got, "\x1b") {
		t.Errorf("an escape sequence went to the clipboard: %q", got)
	}
}

// The clipboard writer is found on PATH, not decided by the platform.
func TestLocalClipboardIsProbed(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("PATH", dir)
	t.Setenv("SSH_CONNECTION", "")
	t.Setenv("SSH_TTY", "")

	if got := localClipboard(); got != nil {
		t.Errorf("found %v with an empty PATH", got)
	}

	// xclip alone, so the answer cannot come from a hardcoded preference for
	// whatever this machine happens to have.
	fake := filepath.Join(dir, "xclip")
	if err := os.WriteFile(fake, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	got := localClipboard()
	if len(got) == 0 || got[0] != "xclip" {
		t.Fatalf("found %v, want the xclip on PATH", got)
	}
	if len(got) < 2 {
		t.Errorf("xclip was chosen without its selection argument: %v", got)
	}
}

// Over ssh the local clipboard belongs to the wrong machine, so there is
// none: OSC 52 is exactly the case ssh has.
func TestLocalClipboardSkippedOverSSH(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "pbcopy"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)

	t.Setenv("SSH_CONNECTION", "10.0.0.1 22 10.0.0.2 22")
	if got := localClipboard(); got != nil {
		t.Errorf("an ssh session offered %v, which would copy on the far end", got)
	}
}

// The text reaches the program's stdin, which is the whole mechanism.
func TestWriteLocalPipesTheText(t *testing.T) {
	out := filepath.Join(t.TempDir(), "pasted")
	msg := writeLocal([]string{"sh", "-c", "cat > " + out}, "worker-02\tNotReady\n")()

	if err := msg.(copiedMsg).err; err != nil {
		t.Fatalf("the write reported %v", err)
	}
	got, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "worker-02\tNotReady\n" {
		t.Errorf("the program received %q", got)
	}
}

// A copy that failed has to take back the notice that claimed it.
func TestFailedLocalWriteIsSaidOutLoud(t *testing.T) {
	m := copying(t)
	m.clipboard = []string{"sh", "-c", "exit 3"}

	drain(t, m, m.copyPane())
	if !strings.Contains(m.notice, "copy failed") {
		t.Errorf("the notice is %q, want it to withdraw the claim", m.notice)
	}
}

// With a local writer there is no escape-sequence length to warn about.
func TestLocalWriteHasNoSizeWarning(t *testing.T) {
	m := copying(t)
	m.clipboard = []string{"sh", "-c", "cat > /dev/null"}

	long := make([]string, 2000)
	for i := range long {
		long[i] = strings.Repeat("x", 80)
	}
	m.cur().res = &engine.Result{Lines: long}

	m.copyPane()
	if strings.Contains(m.notice, "KiB") {
		t.Errorf("the notice warns about a limit that does not apply: %q", m.notice)
	}
}
