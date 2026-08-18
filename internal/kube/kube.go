// Package kube is the single place aldediggi talks to a cluster. Everything
// goes through kubectl rather than client-go, so kubeconfig, contexts and auth
// plugins keep working; see docs/platform.md. Views must not reach for
// exec.Command themselves, because this package is the seam a later move to
// client-go would replace.
package kube

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
)

// Client carries the flags every call shares. The zsh script set --as on some
// commands and not others; here it is one decision.
type Client struct {
	Bin         string
	Namespace   string
	Impersonate string
}

func (c Client) bin() string {
	if c.Bin != "" {
		return c.Bin
	}
	return "kubectl"
}

// In returns a copy scoped to a namespace.
func (c Client) In(namespace string) Client {
	c.Namespace = namespace
	return c
}

// As returns a copy that impersonates a user. Reads should not go through it:
// impersonation needs the impersonate verb, which not every operator has, and
// demanding it to list applications would lock people out of the read-only
// half of the tool.
func (c Client) As(user string) Client {
	c.Impersonate = user
	return c
}

// CurrentContext names the cluster the calls will land on. Shown wherever a
// mistake would be expensive, because prod and staging usually sit in the same
// kubeconfig.
func (c Client) CurrentContext(ctx context.Context) (string, error) {
	out, err := c.Output(ctx, "config", "current-context")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// Available reports whether kubectl can be found, so a view can be greyed out
// with a reason instead of failing when it runs.
func (c Client) Available() error {
	if _, err := exec.LookPath(c.bin()); err != nil {
		return fmt.Errorf("%s not found in PATH", c.bin())
	}
	return nil
}

func (c Client) prefix() []string {
	var out []string
	if c.Namespace != "" {
		out = append(out, "-n", c.Namespace)
	}
	if c.Impersonate != "" {
		out = append(out, "--as", c.Impersonate)
	}
	return out
}

// Output runs kubectl and returns stdout.
func (c Client) Output(ctx context.Context, args ...string) ([]byte, error) {
	return c.run(ctx, nil, args)
}

// Input runs kubectl with stdin attached, for `kubectl … -f -`.
func (c Client) Input(ctx context.Context, stdin []byte, args ...string) ([]byte, error) {
	return c.run(ctx, stdin, args)
}

func (c Client) run(ctx context.Context, stdin []byte, args []string) ([]byte, error) {
	full := append(c.prefix(), args...)

	cmd := exec.CommandContext(ctx, c.bin(), full...)
	if stdin != nil {
		cmd.Stdin = bytes.NewReader(stdin)
	}

	var out, errs bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errs

	if err := cmd.Run(); err != nil {
		// kubectl's own message is the useful one; the exit status is not.
		msg := strings.TrimSpace(errs.String())
		if msg == "" {
			msg = err.Error()
		}
		return nil, fmt.Errorf("%s: %s", c.Describe(args...), msg)
	}

	return out.Bytes(), nil
}

// Describe renders the command as it would be typed, for error messages and
// for showing the user what an action is about to run.
func (c Client) Describe(args ...string) string {
	return strings.Join(append([]string{c.bin()}, append(c.prefix(), args...)...), " ")
}

// GetInto runs kubectl with `-o json` appended and decodes the result.
func GetInto(ctx context.Context, c Client, dst any, args ...string) error {
	raw, err := c.Output(ctx, append(args, "-o", "json")...)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(raw, dst); err != nil {
		return fmt.Errorf("%s: decoding output: %w", c.Describe(args...), err)
	}
	return nil
}

// List is the shape kubectl returns for a collection.
type List[T any] struct {
	Items []T `json:"items"`
}

// ListOf wraps objects back into something `kubectl … -f -` accepts, so an
// action operates on exactly the rows that were on screen rather than on a
// selector that might match something else.
func ListOf(objects []any) ([]byte, error) {
	return json.Marshal(map[string]any{
		"apiVersion": "v1",
		"kind":       "List",
		"items":      objects,
	})
}
