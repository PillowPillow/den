// Package hub lists the official sandbox templates published on Docker Hub.
// It is advisory: every caller degrades to a pointer at TagsPage when the
// listing is unavailable.
package hub

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"slices"
	"strings"
	"time"
)

const TagsPage = "https://hub.docker.com/r/docker/sandbox-templates/tags"

const (
	firstPage = "https://hub.docker.com/v2/repositories/docker/sandbox-templates/tags?page_size=100"
	deadline  = 5 * time.Second
	// maxPages bounds a server that keeps answering a `next`.
	maxPages = 20
)

type Catalog interface {
	Tags(ctx context.Context) ([]string, error)
}

type client struct {
	http    *http.Client
	timeout time.Duration
}

// New is the only code that opens a socket; tests reach the client through NewWith.
func New() Catalog { return NewWith(&http.Client{}) }

func NewWith(c *http.Client) Catalog { return client{http: c, timeout: deadline} }

type page struct {
	Next    string `json:"next"`
	Results []struct {
		Name string `json:"name"`
	} `json:"results"`
}

// Tags follows `next` under one deadline for all pages: a slow Hub must not
// hold `den up` for 5 s per page.
func (c client) Tags(ctx context.Context) ([]string, error) {
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	var names []string
	for url, n := firstPage, 0; url != ""; n++ {
		if n == maxPages {
			return nil, fmt.Errorf("docker hub: more than %d pages of tags", maxPages)
		}
		p, err := c.fetch(ctx, url)
		if err != nil {
			return nil, err
		}
		for _, r := range p.Results {
			names = append(names, r.Name)
		}
		url = p.Next
	}
	return names, nil
}

func (c client) fetch(ctx context.Context, url string) (page, error) {
	var p page
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return p, err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return p, fmt.Errorf("docker hub: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return p, fmt.Errorf("docker hub: %s answered %s", url, resp.Status)
	}
	if err := json.NewDecoder(resp.Body).Decode(&p); err != nil {
		return p, fmt.Errorf("docker hub: %w", err)
	}
	return p, nil
}

var semver = regexp.MustCompile(`[0-9]+\.[0-9]+\.[0-9]+`)

// ShortNames drops nightly builds and version-pinned tags, which are not
// something a user picks by name, and returns the rest sorted.
func ShortNames(tags []string) []string {
	var out []string
	for _, t := range tags {
		if strings.Contains(t, "nightly") || semver.MatchString(t) {
			continue
		}
		out = append(out, t)
	}
	slices.Sort(out)
	return slices.Compact(out)
}

// Describe is one line for an error or a notice; it never fails.
func Describe(ctx context.Context, c Catalog) string {
	tags, err := c.Tags(ctx)
	names := ShortNames(tags)
	if err != nil || len(names) == 0 {
		return "official images: see " + TagsPage
	}
	return "official images: " + strings.Join(names, ", ")
}
