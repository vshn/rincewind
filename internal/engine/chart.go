package engine

import (
	"context"
	"fmt"
	"math/big"
	"sort"
	"strconv"
	"time"
)

// runChart is the pipeline of a chart view: script → JSON value stream →
// rows expression → series. Each value the expression yields must be a
// series object — {name, points: [[t, v], …]} — which is the shape a
// Prometheus query_range result reduces to in one jq step, string-encoded
// values included.
func (cv *compiled) runChart(ctx context.Context, args Args) (Result, error) {
	out, err := runScript(ctx, cv.shell, cv.def.Script, cv.env(args), nil)
	if err != nil {
		return Result{}, err
	}

	docs, err := decodeStream(out)
	if err != nil {
		return Result{}, err
	}

	// Non-nil even when empty: that is what marks a result as a chart.
	res := Result{Series: []Series{}, YRange: cv.def.YRange}

	// Series arrive in the order the script produced them, which is also the
	// drawing order — the hat decides what lies on top. A name seen again
	// extends its series, so a script may deliver one node's week in chunks.
	index := map[string]int{}
	for _, doc := range docs {
		iter := cv.rows.RunWithContext(ctx, doc, argsValue(args))
		for {
			v, ok := iter.Next()
			if !ok {
				break
			}
			if err, isErr := v.(error); isErr {
				return Result{}, fmt.Errorf("rows: %v", err)
			}
			s, note := toSeries(v)
			if note != "" {
				res.Notes = append(res.Notes, note)
			}
			if s == nil {
				continue
			}
			if i, seen := index[s.Name]; seen {
				res.Series[i].Points = append(res.Series[i].Points, s.Points...)
			} else {
				index[s.Name] = len(res.Series)
				res.Series = append(res.Series, *s)
			}
		}
	}

	// Points in time order, whatever order they arrived in: a line drawn
	// against the clock cannot mean anything else.
	for i := range res.Series {
		pts := res.Series[i].Points
		sort.SliceStable(pts, func(a, b int) bool { return pts[a].T.Before(pts[b].T) })
	}
	return res, nil
}

// toSeries reads one rows result as a series object. A malformed one becomes
// a note and is skipped — a data problem at run time must not take down the
// view — while unreadable points are dropped with one note per series, the
// way a broken column notes once rather than per row.
func toSeries(v any) (*Series, string) {
	obj, ok := v.(map[string]any)
	if !ok {
		return nil, fmt.Sprintf("rows produced %s, not a series object {name, points}", shortValue(v))
	}
	name, ok := obj["name"].(string)
	if !ok || name == "" {
		return nil, "a series has no name"
	}
	raw, ok := obj["points"].([]any)
	if !ok {
		return nil, fmt.Sprintf("series %s: points is not an array of [time, value] pairs", name)
	}

	s := &Series{Name: name, Points: make([]Point, 0, len(raw))}
	bad := 0
	firstErr := ""
	for _, rp := range raw {
		p, err := toPoint(rp)
		if err != nil {
			bad++
			if firstErr == "" {
				firstErr = err.Error()
			}
			continue
		}
		s.Points = append(s.Points, p)
	}
	if bad > 0 {
		return s, fmt.Sprintf("series %s: %d of %d points unreadable: %s", name, bad, len(raw), firstErr)
	}
	return s, ""
}

// toPoint reads one [t, v] pair. The time is unix seconds or an RFC 3339
// string; the value a number — or a string holding one, because that is how
// Prometheus encodes samples in JSON.
func toPoint(v any) (Point, error) {
	pair, ok := v.([]any)
	if !ok || len(pair) != 2 {
		return Point{}, fmt.Errorf("%s is not a [time, value] pair", shortValue(v))
	}
	t, err := toPointTime(pair[0])
	if err != nil {
		return Point{}, err
	}
	f, err := toPointValue(pair[1])
	if err != nil {
		return Point{}, err
	}
	return Point{T: t, V: f}, nil
}

func toPointTime(v any) (time.Time, error) {
	switch t := v.(type) {
	case int, float64, *big.Int:
		sec := toFloat(t)
		return time.Unix(int64(sec), int64((sec-float64(int64(sec)))*1e9)), nil
	case string:
		if when, ok := parseWhen(t); ok {
			return when, nil
		}
	}
	return time.Time{}, fmt.Errorf("%s is not unix seconds or an RFC 3339 time", shortValue(v))
}

func toPointValue(v any) (float64, error) {
	switch t := v.(type) {
	case int, float64, *big.Int:
		return toFloat(t), nil
	case string:
		if f, err := strconv.ParseFloat(t, 64); err == nil {
			return f, nil
		}
	}
	return 0, fmt.Errorf("%s is not a number", shortValue(v))
}

// shortValue names a value in an error without flooding the note with it.
func shortValue(v any) string {
	s := valueText(v)
	if len(s) > 40 {
		s = s[:37] + "…"
	}
	if s == "" {
		return "an empty value"
	}
	return s
}
