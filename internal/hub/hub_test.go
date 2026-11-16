package hub

import (
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"slices"
	"strings"
	"testing"
	"time"
)

type roundTrip func(*http.Request) (*http.Response, error)

func (f roundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func reply(body string) *http.Response {
	return &http.Response{StatusCode: 200, Status: "200 OK", Body: io.NopCloser(strings.NewReader(body))}
}

func TestShortNamesKeepsTheDesignRule(t *testing.T) {
	raw, err := os.ReadFile("testdata/tags-2026-11-16.txt")
	if err != nil {
		t.Fatal(err)
	}
	tags := strings.Fields(string(raw))
	if len(tags) != 226 {
		t.Fatalf("fixture has %d tags, want 226", len(tags))
	}
	want := strings.Fields("cagent claude-code claude-code-docker claude-code-minimal claude-code-minimal-test codex codex-docker copilot copilot-docker cursor-agent cursor-agent-docker devin devin-docker docker-agent docker-agent-docker droid droid-docker factory-ai factory-ai-docker gemini gemini-docker kiro kiro-docker latest opencode opencode-docker shell shell-docker ubuntu-python")
	if got := ShortNames(tags); !slices.Equal(got, want) {
		t.Fatalf("ShortNames = %v\nwant %v", got, want)
	}
}

func TestCatalogFollowsNext(t *testing.T) {
	pages := map[string]string{
		firstPage: `{"count":3,"next":"https://hub.docker.com/v2/repositories/docker/sandbox-templates/tags?page=2&page_size=100","results":[{"name":"a"},{"name":"b"}]}`,
		"https://hub.docker.com/v2/repositories/docker/sandbox-templates/tags?page=2&page_size=100": `{"next":"https://hub.docker.com/v2/repositories/docker/sandbox-templates/tags?page=3&page_size=100","results":[{"name":"c"}]}`,
		"https://hub.docker.com/v2/repositories/docker/sandbox-templates/tags?page=3&page_size=100": `{"next":null,"results":[{"name":"d"}]}`,
	}
	c := NewWith(&http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
		body, ok := pages[r.URL.String()]
		if !ok {
			t.Errorf("unexpected request %s", r.URL)
			return nil, errors.New("unexpected")
		}
		return reply(body), nil
	})})
	got, err := c.Tags(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"a", "b", "c", "d"}; !slices.Equal(got, want) {
		t.Fatalf("Tags = %v, want %v", got, want)
	}
}

func TestCatalogGivesUpAtTheDeadline(t *testing.T) {
	c := client{timeout: 50 * time.Millisecond, http: &http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
		<-r.Context().Done()
		return nil, r.Context().Err()
	})}}
	start := time.Now()
	if _, err := c.Tags(context.Background()); err == nil {
		t.Fatal("want an error from a transport that never answers")
	}
	if d := time.Since(start); d > 2*time.Second {
		t.Fatalf("took %v, deadline is 50ms", d)
	}
}

func TestCatalogRefusesANon200(t *testing.T) {
	c := NewWith(&http.Client{Transport: roundTrip(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 429, Status: "429 Too Many Requests", Body: io.NopCloser(strings.NewReader(""))}, nil
	})})
	if _, err := c.Tags(context.Background()); err == nil || !strings.Contains(err.Error(), "429") {
		t.Fatalf("err = %v, want one naming 429", err)
	}
}

func TestDescribeListsOrPointsAtThePage(t *testing.T) {
	got := Describe(context.Background(), Fake{Names: []string{"codex", "claude-code", "claude-code-docker", "claude-code-nightly"}})
	if want := "official images: claude-code, claude-code-docker, codex"; got != want {
		t.Fatalf("Describe = %q, want %q", got, want)
	}
	got = Describe(context.Background(), Fake{Err: errors.New("offline")})
	if want := "official images: see " + TagsPage; got != want {
		t.Fatalf("Describe = %q, want %q", got, want)
	}
}
