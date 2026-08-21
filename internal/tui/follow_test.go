package tui

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"charm.land/bubbles/v2/spinner"
	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/vshn/rincewind/internal/engine"
)

// streamRec records what the followed view was asked to do: how often it was
// started, and with which contexts — cancelling one is how the tool stops a
// script, so the test has to be able to see it happen.
type streamRec struct {
	starts int
	ctxs   []context.Context
}

// followViews is a table with an action opening a followed view. The chunks
// are pushed by the test rather than by a script, so a stream's timing is
// under the test's control; the engine's own tests cover the script half.
func followViews(rec *streamRec) []engine.View {
	tail := engine.View{
		ID: "demo.tail", Name: "tail", Title: "A followed log", Group: "demo",
		Params: []engine.Param{{Name: "service", Required: true}},
		Stream: func(ctx context.Context, _ engine.Args) (<-chan engine.Chunk, error) {
			rec.starts++
			rec.ctxs = append(rec.ctxs, ctx)
			return make(chan engine.Chunk), nil
		},
	}
	services := engine.View{
		ID: "demo.services", Name: "services", Title: "Services", Group: "demo",
		Run: func(context.Context, engine.Args) (engine.Result, error) {
			return engine.Result{
				Cols: []string{"NAME"},
				Rows: []engine.Row{row("api"), row("worker")},
			}, nil
		},
		Actions: []engine.Action{{
			Key: "enter", Name: "follow", Short: "Follow the log",
			Open: &tail,
			Args: func(any, engine.Args) (engine.Args, error) {
				return engine.Args{"service": "api"}, nil
			},
		}},
	}
	return []engine.View{services, tail}
}

// following opens the table, drills into the followed view and leaves the
// stream running with nothing delivered yet.
func following(t *testing.T) (*model, *streamRec) {
	t.Helper()
	rec := &streamRec{}
	m := newModel(followViews(rec))
	next, _ := m.Update(tea.WindowSizeMsg{Width: 96, Height: 20})
	m = next.(*model)

	m.Update(key("enter"))
	deliver(t, m, engine.Result{
		Cols: []string{"NAME"},
		Rows: []engine.Row{row("api"), row("worker")},
	})
	m.Update(key("tab"))
	m.Update(key("enter")) // the drill-down action

	if m.cur().stream == nil {
		t.Fatal("drilling into a followed view started no stream")
	}
	return m, rec
}

// send presses a key without running what it returned. Every command a
// followed view produces is the next read off its channel, and that read
// blocks until a script writes — which in these tests nothing does.
func send(m *model, k string) {
	m.Update(key(k))
}

// chunk delivers lines to the tab on screen, standing in for the tea.Cmd
// that would have read them off the channel.
func chunk(m *model, lines ...string) {
	m.Update(chunkMsg{seq: m.cur().seq, lines: lines})
}

// "following…" is painted with a spinner beside it, and a spinner only turns
// while a tick chain is alive — which starting a stream has to see to.
func TestFollowStartsTheSpinnerTick(t *testing.T) {
	rec := &streamRec{}
	m := newModel(followViews(rec))
	next, _ := m.Update(tea.WindowSizeMsg{Width: 96, Height: 20})
	m = next.(*model)

	m.Update(key("enter"))
	deliver(t, m, engine.Result{
		Cols: []string{"NAME"},
		Rows: []engine.Row{row("api"), row("worker")},
	})

	// The table's own run started a chain; with nothing running any more the
	// next tick retires it. That is the state a drill-down starts from.
	m.Update(spinner.TickMsg{})
	if m.tickAlive {
		t.Fatal("a tick with nothing running left the chain alive")
	}

	m.Update(key("tab"))
	m.Update(key("enter")) // the drill-down action
	if m.cur().stream == nil {
		t.Fatal("drilling into a followed view started no stream")
	}
	if !m.tickAlive {
		t.Error("following started no tick chain, so the spinner stands still")
	}
}

// The point of following: lines show up as they are produced, not when the
// script exits.
func TestFollowShowsLinesAsTheyArrive(t *testing.T) {
	m, _ := following(t)

	chunk(m, "first line")
	if !strings.Contains(ansi.Strip(m.View().Content), "first line") {
		t.Fatal("the first line never appeared")
	}
	if !m.cur().running {
		t.Error("the tab stopped running when its first lines arrived")
	}

	chunk(m, "second line")
	content := ansi.Strip(m.View().Content)
	if !strings.Contains(content, "first line") || !strings.Contains(content, "second line") {
		t.Errorf("the second chunk replaced the first: %q", content)
	}
}

// A reader who has scrolled up is reading. New lines must not yank the view
// out from under them.
func TestFollowSticksToTheBottomOnlyFromTheBottom(t *testing.T) {
	m, _ := following(t)
	for i := range 60 {
		chunk(m, fmt.Sprintf("line %02d", i))
	}
	if !strings.Contains(ansi.Strip(m.View().Content), "line 59") {
		t.Fatal("following did not stay at the end")
	}

	m.Update(key("home"))
	at := m.vp.YOffset()
	chunk(m, "line 60")

	if m.vp.YOffset() != at {
		t.Errorf("a new line scrolled a reader who had scrolled up: %d, want %d", m.vp.YOffset(), at)
	}
	if strings.Contains(ansi.Strip(m.View().Content), "line 60") {
		t.Error("the new line dragged the viewport to the bottom")
	}

	m.Update(key("end"))
	chunk(m, "line 61")
	if !strings.Contains(ansi.Strip(m.View().Content), "line 61") {
		t.Error("back at the bottom, following did not resume")
	}
}

// A log followed overnight would otherwise be a leak with a scrollbar.
func TestFollowCapsWhatItKeeps(t *testing.T) {
	m, _ := following(t)
	for i := range maxFollowLines + 200 {
		chunk(m, fmt.Sprintf("line %d", i))
	}

	lines := m.cur().res.Lines
	if len(lines) != maxFollowLines {
		t.Errorf("kept %d lines, want the cap of %d", len(lines), maxFollowLines)
	}
	if lines[0] != "line 200" {
		t.Errorf("the oldest kept line is %q, want the 200 before it dropped", lines[0])
	}
}

// A script that stops is a finding — the pod went away — so it is said
// rather than left looking like a quiet log.
func TestFollowSaysWhenTheStreamEnds(t *testing.T) {
	m, _ := following(t)
	chunk(m, "the last thing it said")
	m.Update(chunkMsg{seq: m.cur().seq, end: true})

	content := ansi.Strip(m.View().Content)
	if !strings.Contains(content, "stream ended") {
		t.Errorf("the end went unmentioned: %q", content)
	}
	if !strings.Contains(content, "the last thing it said") {
		t.Error("the lines read before the end are gone")
	}
	if m.cur().running || m.cur().stream != nil {
		t.Error("the tab still counts as following after the stream ended")
	}
}

// A stream that broke must not replace what it managed to read with an error
// pane: those lines are most of what one wants to look at.
func TestFollowFailureKeepsTheLines(t *testing.T) {
	m, _ := following(t)
	chunk(m, "before it broke")
	m.Update(chunkMsg{seq: m.cur().seq, end: true, err: fmt.Errorf("exit status 1")})

	content := ansi.Strip(m.View().Content)
	if !strings.Contains(content, "before it broke") {
		t.Error("the error pane swallowed the lines")
	}
	if !strings.Contains(content, "exit status 1") {
		t.Errorf("the failure went unmentioned: %q", content)
	}
}

// esc stops following. Being told "the stream ended" after asking for it to
// stop would explain nothing one did not just do.
func TestEscStopsFollowing(t *testing.T) {
	m, rec := following(t)
	chunk(m, "a line")
	m.Update(key("esc"))

	if err := rec.ctxs[0].Err(); err == nil {
		t.Error("esc left the script running")
	}
	content := ansi.Strip(m.View().Content)
	if !strings.Contains(content, "stopped") {
		t.Errorf("stopping went unmentioned: %q", content)
	}
	if strings.Contains(content, "stream ended") {
		t.Error("a stream stopped by hand is reported as having ended by itself")
	}
	if m.cur().stream != nil {
		t.Error("esc left the tab following")
	}
}

// Reloading is how one starts again, whether the stream ended or was stopped.
func TestReloadStartsFollowingAgain(t *testing.T) {
	m, rec := following(t)
	m.Update(chunkMsg{seq: m.cur().seq, end: true})

	send(m, "r")

	if rec.starts != 2 {
		t.Fatalf("the view was started %d times, want a second run", rec.starts)
	}
	if !m.cur().running || m.cur().stream == nil {
		t.Error("reloading did not put the tab back to following")
	}
	if strings.Contains(ansi.Strip(m.View().Content), "stream ended") {
		t.Error("the end marker survived the restart")
	}
}

// Only one script follows at a time: leaving the tab stops it, coming back
// starts it again. Lines from the meantime are lost, which is why a real
// followed view passes something like --tail.
func TestFollowPausesInTheBackground(t *testing.T) {
	m, rec := following(t)
	chunk(m, "before leaving")

	send(m, "[") // back to the table
	if err := rec.ctxs[0].Err(); err == nil {
		t.Fatal("the stream kept running with its tab in the background")
	}

	send(m, "]") // and back again
	if rec.starts != 2 {
		t.Errorf("the view was started %d times, want it started again on return", rec.starts)
	}
	if !m.cur().running {
		t.Error("the tab did not resume following")
	}
	if strings.Contains(ansi.Strip(m.View().Content), "stream ended") {
		t.Error("a pause was reported as the stream ending")
	}
}

// The chunk a dying script still has in flight belongs to a run that is over.
// Without this a pause would look like an end, and the tab would refuse to
// start again on the way back.
func TestLateChunkFromAPausedStreamIsIgnored(t *testing.T) {
	m, _ := following(t)
	stale := m.cur().seq
	chunk(m, "kept")

	send(m, "[")
	m.Update(chunkMsg{seq: stale, lines: []string{"too late"}, end: true})
	send(m, "]")

	if m.cur().ended {
		t.Error("a chunk from the paused run ended the tab")
	}
	for _, l := range m.cur().res.Lines {
		if l == "too late" {
			t.Error("a line from the paused run was appended to the new one")
		}
	}
}

// Closing the tab is the other way out, and must not leave a script behind.
func TestClosingTheTabStopsTheScript(t *testing.T) {
	m, rec := following(t)
	m.Update(chunkMsg{seq: m.cur().seq, end: true}) // so esc means "back", not "stop"

	send(m, "esc")

	if len(m.tabs) != 1 {
		t.Fatalf("esc left %d tabs open", len(m.tabs))
	}
	if err := rec.ctxs[0].Err(); err == nil {
		t.Error("closing the tab left the script running")
	}
}

// Picking another view from the menu replaces the first tab rather than
// closing it, so its run has to be cancelled by hand.
func TestOpeningAnotherViewStopsTheScript(t *testing.T) {
	m, rec := following(t)
	chunk(m, "a line")

	// Collapse to one tab first, so the followed view is the one replaced.
	m.tabs[0] = m.tabs[1]
	m.tabs, m.act = m.tabs[:1], 0

	m.focus = paneList
	m.Update(key("enter"))

	if err := rec.ctxs[0].Err(); err == nil {
		t.Error("opening another view left the script running")
	}
}

// While following, the legend must offer what esc actually does — and not
// the meaning it has everywhere else.
func TestFollowHelpOffersStopNotBack(t *testing.T) {
	m, _ := following(t)
	chunk(m, "a line")

	help := ansi.Strip(m.View().Content)
	if !strings.Contains(help, "esc stop") {
		t.Errorf("the legend does not say esc stops the stream: %q", help)
	}
	if strings.Contains(help, "esc back") {
		t.Error("the legend promises esc goes back while a stream is running")
	}
}
