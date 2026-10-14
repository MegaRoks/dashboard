package github

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestParseRepoURL(t *testing.T) {
	ok := map[string]Repo{
		"https://github.com/MegaRoks/Interview-prep":   {"MegaRoks", "Interview-prep"},
		"github.com/MegaRoks/Interview-prep/":          {"MegaRoks", "Interview-prep"},
		"http://www.github.com/a/b.git":                {"a", "b"},
		"  https://GitHub.com/a/grain-ui?tab=readme  ": {"a", "grain-ui"},
		"https://github.com/a/my.repo#readme":          {"a", "my.repo"},
	}
	for in, want := range ok {
		if got, ok := ParseRepoURL(in); !ok || got != want {
			t.Errorf("ParseRepoURL(%q) = %v, %v; want %v", in, got, ok, want)
		}
	}
	for _, in := range []string{"", "github.com/a", "https://gitlab.com/a/b", "https://github.com/a/b/tree/main", "https://evilgithub.com/a/b"} {
		if got, ok := ParseRepoURL(in); ok {
			t.Errorf("ParseRepoURL(%q) = %v, want no match", in, got)
		}
	}
}

func TestClient(t *testing.T) {
	var gotAuth, gotAccept string
	mux := http.NewServeMux()
	mux.HandleFunc("GET /repos/megaroks/interview-prep", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"name":"Interview-prep","owner":{"login":"MegaRoks"}}`))
	})
	mux.HandleFunc("GET /repos/MegaRoks/Interview-prep/contents/progress.json", func(w http.ResponseWriter, r *http.Request) {
		gotAuth, gotAccept = r.Header.Get("Authorization"), r.Header.Get("Accept")
		w.Write([]byte(`{"title":"x"}`))
	})
	mux.HandleFunc("GET /repos/a/expired/contents/f", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	c := New("secret")
	c.BaseURL = srv.URL
	ctx := context.Background()

	repo, err := c.Repo(ctx, Repo{"megaroks", "interview-prep"})
	if err != nil || repo != (Repo{"MegaRoks", "Interview-prep"}) {
		t.Fatalf("Repo = %v, %v", repo, err)
	}
	body, err := c.File(ctx, repo, "progress.json")
	if err != nil || string(body) != `{"title":"x"}` {
		t.Fatalf("File = %q, %v", body, err)
	}
	if gotAuth != "Bearer secret" || gotAccept != "application/vnd.github.raw+json" {
		t.Fatalf("headers: auth=%q accept=%q", gotAuth, gotAccept)
	}
	if _, err := c.Repo(ctx, Repo{"a", "missing"}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing repo: err = %v", err)
	}
	if _, err := c.File(ctx, Repo{"a", "expired"}, "f"); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("bad token: err = %v", err)
	}
}
