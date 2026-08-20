package hats

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// answer is a captured GitHub reply, trimmed to the fields that are read.
const answer = `{
  "total_count": 2,
  "items": [
    {"full_name": "vshn/rincewind-hats", "description": "VSHN Rincewind Hats",
     "stargazers_count": 3, "html_url": "https://github.com/vshn/rincewind-hats"},
    {"full_name": "someone/their-hats", "description": null,
     "stargazers_count": 0, "html_url": "https://github.com/someone/their-hats"}
  ]
}`

// serve stands in for GitHub with a fixed reply, for the paths where only
// the answer matters. The tests that assert on the request build their own
// handler, so they can look at it while it is still in hand.
func serve(t *testing.T, status int, body string, headers map[string]string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		for k, v := range headers {
			w.Header().Set(k, v)
		}
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func search(t *testing.T, srv *httptest.Server, query string) ([]Repo, error) {
	t.Helper()
	return Searcher{BaseURL: srv.URL, Client: srv.Client()}.Search(context.Background(), query)
}

// The topic is the search; anything the user types narrows it.
func TestSearchAsksForTheTopic(t *testing.T) {
	var asked string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		asked = r.URL.Query().Get("q")
		if r.Header.Get("User-Agent") == "" {
			t.Error("no User-Agent, which GitHub refuses")
		}
		if r.Header.Get("Accept") != "application/vnd.github+json" {
			t.Errorf("Accept is %q", r.Header.Get("Accept"))
		}
		_, _ = w.Write([]byte(answer))
	}))
	defer srv.Close()

	if _, err := search(t, srv, ""); err != nil {
		t.Fatal(err)
	}
	if asked != "topic:"+Topic {
		t.Errorf("asked %q", asked)
	}

	if _, err := search(t, srv, "openshift"); err != nil {
		t.Fatal(err)
	}
	if asked != "topic:"+Topic+" openshift" {
		t.Errorf("asked %q, want the topic narrowed by the words", asked)
	}
}

func TestSearchReadsTheAnswer(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(answer))
	}))
	defer srv.Close()

	repos, err := search(t, srv, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(repos) != 2 {
		t.Fatalf("got %d repositories", len(repos))
	}
	want := Repo{
		Name:        "vshn/rincewind-hats",
		Description: "VSHN Rincewind Hats",
		Stars:       3,
		URL:         "https://github.com/vshn/rincewind-hats",
	}
	if repos[0] != want {
		t.Errorf("got %+v, want %+v", repos[0], want)
	}
	// A repository without a description sends null, which must not become
	// the word "null".
	if repos[1].Description != "" {
		t.Errorf("a null description became %q", repos[1].Description)
	}
}

// Ten a minute is little enough that this will happen, so it has to say what
// to do about it.
func TestRateLimitSaysWhatToDo(t *testing.T) {
	srv := serve(t, http.StatusForbidden, `{}`, map[string]string{"X-RateLimit-Remaining": "0"})

	_, err := search(t, srv, "")
	if err == nil {
		t.Fatal("a used-up rate limit was reported as success")
	}
	for _, want := range []string{"rate limit", "GITHUB_TOKEN"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the error is %q, want it to mention %q", err, want)
		}
	}
}

// A token is what raises that limit and reaches private repositories.
func TestTokenIsSentWhenSet(t *testing.T) {
	t.Setenv("GITHUB_TOKEN", "sekrit")

	var auth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth = r.Header.Get("Authorization")
		_, _ = w.Write([]byte(answer))
	}))
	defer srv.Close()

	if _, err := search(t, srv, ""); err != nil {
		t.Fatal(err)
	}
	if auth != "Bearer sekrit" {
		t.Errorf("Authorization is %q", auth)
	}

	t.Setenv("GITHUB_TOKEN", "")
	if _, err := search(t, srv, ""); err != nil {
		t.Fatal(err)
	}
	if auth != "" {
		t.Errorf("an empty token was sent as %q", auth)
	}
}

// Anything else says what GitHub said rather than swallowing it.
func TestOtherFailuresAreReported(t *testing.T) {
	srv := serve(t, http.StatusInternalServerError, `{}`, nil)

	_, err := search(t, srv, "")
	if err == nil || !strings.Contains(err.Error(), "500") {
		t.Errorf("got %v, want the status in the message", err)
	}
}

// A search that matches nothing is an answer, not a failure.
func TestNoMatchesIsNotAnError(t *testing.T) {
	srv := serve(t, http.StatusOK, `{"total_count":0,"items":[]}`, nil)

	repos, err := search(t, srv, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(repos) != 0 {
		t.Errorf("got %+v", repos)
	}
}
