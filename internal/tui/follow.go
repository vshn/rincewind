package tui

import (
	"context"

	tea "charm.land/bubbletea/v2"

	"github.com/vshn/rincewind/internal/engine"
)

// maxFollowLines is how much of a followed log is kept. Past it the oldest
// go, because a controller left running overnight would otherwise be a leak
// with a scrollbar. It matches the --tail the snapshot views read.
const maxFollowLines = 5000

// follow starts a followed view. It gets no deadline, unlike every other
// run: the whole point is that it does not finish, and runTimeout would cut
// it off after half a minute.
func (m *model) follow(t *tab) tea.Cmd {
	t.stop()
	m.seq++
	t.seq, t.running, t.err = m.seq, true, nil
	t.ended, t.stopped, t.endErr = false, false, nil

	ctx, cancel := context.WithCancel(context.Background())
	t.cancel = cancel

	ch, err := t.view.Stream(ctx, t.args)
	if err != nil {
		cancel()
		t.running, t.err = false, err
		m.showResult()
		return nil
	}

	// Lines accumulate in the result, so filtering, scrolling and the line
	// count all work on a followed view exactly as on a snapshot.
	res := engine.Result{Lines: []string{}}
	t.res, t.stream = &res, ch

	m.showResult()
	return m.readChunk(t.seq, ch)
}

// readChunk waits for the next lines and re-arms itself, which is how a
// stream is consumed without blocking Update.
//
// It takes everything already queued before returning, so a chatty log
// arrives as a few big repaints rather than thousands of small ones.
func (m *model) readChunk(seq int, ch <-chan engine.Chunk) tea.Cmd {
	return func() tea.Msg {
		c, ok := <-ch
		if !ok {
			return chunkMsg{seq: seq, end: true}
		}
		if c.End {
			return chunkMsg{seq: seq, end: true, err: c.Err}
		}

		lines := c.Lines
		for {
			select {
			case next, ok := <-ch:
				if !ok {
					return chunkMsg{seq: seq, lines: lines, end: true}
				}
				if next.End {
					return chunkMsg{seq: seq, lines: lines, end: true, err: next.Err}
				}
				lines = append(lines, next.Lines...)
			default:
				return chunkMsg{seq: seq, lines: lines}
			}
		}
	}
}

// appendLines adds what arrived, dropping the oldest past the cap.
func (t *tab) appendLines(lines []string) {
	if t.res == nil {
		res := engine.Result{Lines: []string{}}
		t.res = &res
	}
	t.res.Lines = append(t.res.Lines, lines...)
	if n := len(t.res.Lines); n > maxFollowLines {
		t.res.Lines = t.res.Lines[n-maxFollowLines:]
	}
}

// pauseStream stops a followed view when its tab goes to the background, the
// way a refresh pauses. Lines from the meantime are lost; the script's own
// --tail brings the recent past back when it is started again.
func (m *model) pauseStream(t *tab) {
	if t.stream == nil {
		return
	}
	t.stop()
	t.stream, t.running = nil, false
	// The seq moves on so the End chunk the dying script still has in flight
	// finds no tab. Without this a pause would look like the stream ending,
	// and the tab would refuse to start it again on the way back.
	m.seq++
	t.seq = m.seq
}

// stopFollow ends following at the user's request, which is over for good:
// unlike a pause it is not resumed on the way back to the tab.
func (m *model) stopFollow(t *tab) {
	if t.stream == nil {
		return
	}
	m.pauseStream(t)
	t.ended, t.stopped = true, true
}

// atBottom reports whether the viewport is showing the end of the content,
// which is what decides whether new lines scroll into view or wait.
func (m *model) atBottom() bool {
	return m.vp.AtBottom()
}
