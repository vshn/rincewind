package catalog

import (
	"context"
	"fmt"
	"os/user"
	"strings"
	"time"

	"github.com/vshn/aldediggi/internal/kube"
)

// openAlertDetail is the drill-down bound to enter: pick an alert, see every
// label and annotation.
func openAlertDetail() Action {
	return Action{
		Name:  "detail",
		Key:   "enter",
		Short: "Show this alert in full",
		Scope: Rows,
		Run: func(_ context.Context, in Result, _ Args) (Result, error) {
			alerts := rowsIn[alert](in)
			if len(alerts) != 1 {
				return nil, fmt.Errorf("select one alert first (%d shown; tab into the list, or filter)", len(alerts))
			}
			return Navigate{View: "alerts.detail", Args: Args{"fingerprint": alerts[0].Fingerprint}}, nil
		},
	}
}

// silenceMatchers derives amtool matchers from the shown alerts: the labels
// worth matching on that they all share. One alert gives alertname plus
// namespace; several with the same name give alertname alone, so the CLI can
// silence an alert that fires in more than one namespace. All labels would be
// too narrow — a silence pinned to a pod name dies on the next restart.
func silenceMatchers(alerts []alert) ([]string, error) {
	if len(alerts) == 0 {
		return nil, fmt.Errorf("no alert selected")
	}

	var out []string
	for _, label := range []string{"alertname", "namespace"} {
		value := alerts[0].Labels[label]
		for _, a := range alerts[1:] {
			if a.Labels[label] != value {
				value = ""
				break
			}
		}
		if value != "" {
			out = append(out, fmt.Sprintf("%s=%q", label, value))
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("the shown alerts share no alertname; narrow the set first")
	}
	return out, nil
}

func silenceAlertAction(c kube.Client) Action {
	params := []Param{
		{Name: "for", Short: "until when: wall clock, RFC 3339, or +2h", Default: "+2h"},
		{Name: "comment", Short: "why this is silenced", Required: true},
	}
	return Action{
		Name:    "silence",
		Key:     "s",
		Short:   "Silence the shown alerts",
		Scope:   Rows,
		Params:  params,
		Refresh: true,
		Confirm: func(in Result, args Args) string {
			matchers, err := silenceMatchers(rowsIn[alert](in))
			if err != nil {
				return "Silence: " + err.Error()
			}
			return fmt.Sprintf("Silence %s until %s?\n\ncomment: %s",
				strings.Join(matchers, " "), describeEnd(args.Get("for")), args.Get("comment"))
		},
		Run: func(ctx context.Context, in Result, args Args) (Result, error) {
			matchers, err := silenceMatchers(rowsIn[alert](in))
			if err != nil {
				return nil, err
			}
			return addSilence(ctx, c, matchers, args)
		},
	}
}

func createSilenceAction(c kube.Client) Action {
	return Action{
		Name:  "create",
		Key:   "n",
		Short: "Create a silence from explicit matchers",
		Scope: Params,
		Params: []Param{
			{Name: "matchers", Short: `comma-separated, amtool syntax: alertname="Foo",namespace=~"syn.*"`, Required: true},
			{Name: "for", Short: "until when: wall clock, RFC 3339, or +2h", Default: "+2h"},
			{Name: "comment", Short: "why this is silenced", Required: true},
		},
		Refresh: true,
		Confirm: func(_ Result, args Args) string {
			return fmt.Sprintf("Silence %s until %s?\n\ncomment: %s",
				strings.Join(splitMatchers(args.Get("matchers")), " "), describeEnd(args.Get("for")), args.Get("comment"))
		},
		Run: func(ctx context.Context, _ Result, args Args) (Result, error) {
			matchers := splitMatchers(args.Get("matchers"))
			if len(matchers) == 0 {
				return nil, fmt.Errorf("no matchers given")
			}
			return addSilence(ctx, c, matchers, args)
		},
	}
}

// addSilence resolves the end and runs `amtool silence add`. Each matcher is
// one argv element and amtool parses it itself, so there is no second matcher
// grammar to keep in sync.
func addSilence(ctx context.Context, c kube.Client, matchers []string, args Args) (Result, error) {
	end, err := ParseTime(args.Get("for"), time.Now())
	if err != nil {
		return nil, err
	}

	cmd := []string{"silence", "add",
		"--author", localUser(),
		"--comment", args.Get("comment"),
		"--end", FormatTime(end),
	}
	out, err := amtool(ctx, c, append(cmd, matchers...)...)
	if err != nil {
		return nil, err
	}
	return Text{Lines: append(splitLines(string(out)), "silenced until "+showTime(end))}, nil
}

func expireSilencesAction(c kube.Client) Action {
	return Action{
		Name:    "expire",
		Key:     "x",
		Short:   "Expire the listed silences",
		Scope:   Rows,
		Refresh: true,
		Confirm: func(in Result, _ Args) string {
			ids := expirableIn(in)
			if len(ids) == 0 {
				return "Expire: nothing shown that could be expired."
			}
			return fmt.Sprintf("Expire %d silence(s)?\n\n%s", len(ids), joinNames(shortIDs(ids)))
		},
		Run: func(ctx context.Context, in Result, _ Args) (Result, error) {
			ids := expirableIn(in)
			if len(ids) == 0 {
				return nil, fmt.Errorf("nothing to expire")
			}
			out, err := amtool(ctx, c, append([]string{"silence", "expire"}, ids...)...)
			if err != nil {
				return nil, err
			}
			lines := splitLines(string(out))
			return Text{Lines: append(lines, fmt.Sprintf("expired %d silence(s)", len(ids)))}, nil
		},
	}
}

// expirableIn reads the silence ids back out of the rows, skipping anything
// already expired: sending those to amtool fails the whole call.
func expirableIn(in Result) []string {
	var out []string
	for _, s := range rowsIn[silence](in) {
		if s.Status.State != "expired" {
			out = append(out, s.ID)
		}
	}
	return out
}

func shortIDs(ids []string) []string {
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		out = append(out, silence{ID: id}.shortID())
	}
	return out
}

// splitMatchers cuts the flag value at commas; the pieces stay verbatim for
// amtool to parse.
func splitMatchers(s string) []string {
	var out []string
	for _, m := range strings.Split(s, ",") {
		if m = strings.TrimSpace(m); m != "" {
			out = append(out, m)
		}
	}
	return out
}

// describeEnd resolves the end for a confirmation. Errors surface when the
// action runs; a prompt is not the place to fail.
func describeEnd(value string) string {
	end, err := ParseTime(value, time.Now())
	if err != nil {
		return fmt.Sprintf("%q (unparseable: %v)", value, err)
	}
	return showTime(end)
}

// localUser names the silence's author. amtool's own default would be the
// user inside the pod, which is nobody.
func localUser() string {
	if u, err := user.Current(); err == nil && u.Username != "" {
		return u.Username
	}
	return "aldediggi"
}
