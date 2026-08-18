package kube

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fake writes a kubectl stand-in that echoes its arguments and stdin, so tests
// can assert on the command actually issued.
func fake(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "kubectl")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+body+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

// Namespace and impersonation belong to the client, because the zsh script
// applied --as to some commands and not others.
func TestPrefixIsAppliedToEveryCall(t *testing.T) {
	c := Client{Bin: fake(t, `echo "$@"`), Namespace: "syn", Impersonate: "system:admin"}

	out, err := c.Output(context.Background(), "get", "pods")
	if err != nil {
		t.Fatal(err)
	}

	if got := strings.TrimSpace(string(out)); got != "-n syn --as system:admin get pods" {
		t.Errorf("got %q", got)
	}
}

func TestInAppliesNamespaceWithoutMutatingTheReceiver(t *testing.T) {
	base := Client{Bin: "kubectl"}
	scoped := base.In("syn")

	if base.Namespace != "" {
		t.Error("In mutated the receiver")
	}
	if scoped.Namespace != "syn" {
		t.Errorf("scoped namespace is %q", scoped.Namespace)
	}
}

// kubectl's own message is what tells you what went wrong; the exit status is
// not.
func TestErrorCarriesStderr(t *testing.T) {
	c := Client{Bin: fake(t, `echo "Error from server (NotFound): nope" >&2; exit 1`)}

	_, err := c.Output(context.Background(), "get", "app", "nope")
	if err == nil {
		t.Fatal("want an error")
	}
	if !strings.Contains(err.Error(), "NotFound") {
		t.Errorf("error lost kubectl's message: %v", err)
	}
	if !strings.Contains(err.Error(), "get app nope") {
		t.Errorf("error does not name the command: %v", err)
	}
}

func TestInputIsPassedOnStdin(t *testing.T) {
	c := Client{Bin: fake(t, `cat`)}

	out, err := c.Input(context.Background(), []byte("payload"), "apply", "-f", "-")
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != "payload" {
		t.Errorf("got %q", out)
	}
}

// ListOf has to produce something `kubectl … -f -` accepts, which is what lets
// an action operate on exactly the rows shown.
func TestListOfIsAKubectlList(t *testing.T) {
	raw, err := ListOf([]any{
		json.RawMessage(`{"metadata":{"name":"a"}}`),
		json.RawMessage(`{"metadata":{"name":"b"}}`),
	})
	if err != nil {
		t.Fatal(err)
	}

	var got struct {
		APIVersion string `json:"apiVersion"`
		Kind       string `json:"kind"`
		Items      []struct {
			Metadata struct{ Name string } `json:"metadata"`
		} `json:"items"`
	}
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}

	if got.APIVersion != "v1" || got.Kind != "List" {
		t.Errorf("got %s/%s", got.APIVersion, got.Kind)
	}
	if len(got.Items) != 2 || got.Items[0].Metadata.Name != "a" {
		t.Errorf("items = %+v", got.Items)
	}
}

func TestAvailableReportsAMissingBinary(t *testing.T) {
	if err := (Client{Bin: "definitely-not-a-real-binary"}).Available(); err == nil {
		t.Fatal("want an error")
	}
	if err := (Client{Bin: fake(t, "true")}).Available(); err != nil {
		t.Errorf("unexpected: %v", err)
	}
}
