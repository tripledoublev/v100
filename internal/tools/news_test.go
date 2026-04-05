package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/tripledoublev/v100/internal/core/executor"
)

func TestNewsFetchExecParsesRSSAndFiltersFreshItems(t *testing.T) {
	now := time.Now().UTC()
	feedURL := "https://fixture.example/feed.xml"
	session := &routingFakeDockerSession{
		routes: map[string]executor.Result{
			feedURL: {
				ExitCode: 0,
				Stdout: fmt.Sprintf("200\napplication/rss+xml\n\n__V100_CURL_BODY__\n<?xml version=\"1.0\" encoding=\"UTF-8\"?>\n"+
					`<rss version="2.0">
  <channel>
    <title>Fixture News</title>
    <item>
      <title>Montreal transit agency expands overnight service</title>
      <link>https://fixture.example/story-1</link>
      <description>STM will add more overnight buses starting this weekend.</description>
      <pubDate>%s</pubDate>
      <category>Transit</category>
    </item>
    <item>
      <title>Old headline that should fall out of freshness window</title>
      <link>https://fixture.example/story-2</link>
      <description>Stale item.</description>
      <pubDate>%s</pubDate>
      <category>Archive</category>
    </item>
  </channel>
</rss>`,
					now.Add(-2*time.Hour).Format(time.RFC1123Z),
					now.Add(-72*time.Hour).Format(time.RFC1123Z),
				),
			},
		},
	}

	args, err := json.Marshal(map[string]any{
		"sources":     []string{feedURL},
		"fresh_hours": 24,
		"max_items":   5,
	})
	if err != nil {
		t.Fatal(err)
	}

	res, err := NewsFetch().Exec(context.Background(), ToolCallContext{Session: session}, args)
	if err != nil {
		t.Fatal(err)
	}
	if !res.OK {
		t.Fatalf("news_fetch failed: %s", res.Output)
	}

	var out newsFetchOutput
	if err := json.Unmarshal([]byte(res.Output), &out); err != nil {
		t.Fatal(err)
	}
	if len(out.Items) != 1 {
		t.Fatalf("expected 1 fresh item, got %d: %+v", len(out.Items), out.Items)
	}
	if got := out.Items[0].Headline; got != "Montreal transit agency expands overnight service" {
		t.Fatalf("headline = %q", got)
	}
	if out.Items[0].FetchMethod != "rss" {
		t.Fatalf("fetch_method = %q, want rss", out.Items[0].FetchMethod)
	}
	if out.Items[0].PublishedAt == "" {
		t.Fatalf("expected published_at to be normalized, got empty")
	}
}

func TestNewsFetchExecExtractsJSONLDHeadlines(t *testing.T) {
	now := time.Now().UTC()
	pageURL := "https://fixture.example/frontpage"
	session := &routingFakeDockerSession{
		routes: map[string]executor.Result{
			pageURL: {
				ExitCode: 0,
				Stdout: fmt.Sprintf("200\ntext/html; charset=utf-8\n\n__V100_CURL_BODY__\n"+`<!doctype html>
<html>
  <head>
    <title>Example News | Latest Headlines</title>
    <script type="application/ld+json">
      {
        "@context": "https://schema.org",
        "@graph": [
          {
            "@type": "NewsArticle",
            "headline": "Quebec budget talks intensify ahead of deadline",
            "url": "https://fixture.example/story-1",
            "datePublished": "%s",
            "description": "Negotiations intensified overnight at the National Assembly."
          },
          {
            "@type": "NewsArticle",
            "headline": "Montreal prepares major spring street closures",
            "url": "https://fixture.example/story-2",
            "datePublished": "%s",
            "description": "Drivers are being warned about a new round of closures."
          }
        ]
      }
    </script>
  </head>
  <body>
    <h1>Example News</h1>
  </body>
</html>`,
					now.Add(-2*time.Hour).Format(time.RFC3339),
					now.Add(-90*time.Minute).Format(time.RFC3339),
				),
			},
		},
	}

	args, err := json.Marshal(map[string]any{
		"sources":   []string{pageURL},
		"max_items": 5,
	})
	if err != nil {
		t.Fatal(err)
	}

	res, err := NewsFetch().Exec(context.Background(), ToolCallContext{Session: session}, args)
	if err != nil {
		t.Fatal(err)
	}
	if !res.OK {
		t.Fatalf("news_fetch failed: %s", res.Output)
	}

	var out newsFetchOutput
	if err := json.Unmarshal([]byte(res.Output), &out); err != nil {
		t.Fatal(err)
	}
	if len(out.Items) != 2 {
		t.Fatalf("expected 2 items, got %d: %+v", len(out.Items), out.Items)
	}
	if out.Items[0].FetchMethod != "html_jsonld" {
		t.Fatalf("fetch_method = %q, want html_jsonld", out.Items[0].FetchMethod)
	}
	for _, item := range out.Items {
		if strings.Contains(strings.ToLower(item.Headline), "latest headlines") {
			t.Fatalf("unexpected generic page title in items: %+v", out.Items)
		}
	}
}

func TestNewsFetchExecReturnsPartialFailureForThinHTML(t *testing.T) {
	goodURL := "https://fixture.example/good"
	thinURL := "https://fixture.example/thin"
	session := &routingFakeDockerSession{
		routes: map[string]executor.Result{
			goodURL: {
				ExitCode: 0,
				Stdout: "200\ntext/html; charset=utf-8\n\n__V100_CURL_BODY__\n" + `<!doctype html>
<html>
  <head><title>Montreal Local News</title></head>
  <body>
    <section class="story-card">
      <h2><a href="/story-1">City Hall approves new bike corridor through downtown</a></h2>
      <p>The project will add protected lanes on two major streets this summer.</p>
    </section>
    <section class="story-card">
      <h2><a href="/story-2">STM warns of weekend metro disruptions on Green line</a></h2>
      <p>Maintenance work will close several stations overnight.</p>
    </section>
  </body>
</html>`,
			},
			thinURL: {
				ExitCode: 0,
				Stdout:   "200\ntext/html; charset=utf-8\n\n__V100_CURL_BODY__\n<!doctype html><html><head><title>Montreal News | Weather & Traffic - Latest Sports | Breaking News</title></head><body><nav>Montreal</nav></body></html>",
			},
		},
	}

	args, err := json.Marshal(map[string]any{
		"sources":   []string{goodURL, thinURL},
		"max_items": 5,
	})
	if err != nil {
		t.Fatal(err)
	}

	res, err := NewsFetch().Exec(context.Background(), ToolCallContext{Session: session}, args)
	if err != nil {
		t.Fatal(err)
	}
	if !res.OK {
		t.Fatalf("news_fetch should succeed with partial results, got: %s", res.Output)
	}

	var out newsFetchOutput
	if err := json.Unmarshal([]byte(res.Output), &out); err != nil {
		t.Fatal(err)
	}
	if len(out.Items) < 2 {
		t.Fatalf("expected extracted HTML headlines, got %+v", out.Items)
	}
	if len(out.Failures) != 1 {
		t.Fatalf("expected 1 partial failure, got %+v", out.Failures)
	}
	if !strings.Contains(out.Failures[0].Error, "no usable headlines") {
		t.Fatalf("failure error = %q", out.Failures[0].Error)
	}
}

func TestResolveNewsSourceRequestsMontrealFrench(t *testing.T) {
	reqs := resolveNewsSourceRequests(newsFetchArgs{
		Region:   "montreal",
		Language: "fr",
		Topic:    "general",
		MaxItems: 5,
	})
	if len(reqs) == 0 {
		t.Fatal("expected Montreal French defaults")
	}
	names := make([]string, 0, len(reqs))
	for _, req := range reqs {
		names = append(names, req.Key)
		if req.Language != "fr" {
			t.Fatalf("unexpected non-French source in Montreal French defaults: %+v", req)
		}
	}
	for _, want := range []string{"lapresse_montreal", "radio_canada_montreal", "journaldemontreal"} {
		if !containsString(names, want) {
			t.Fatalf("expected source %q in %v", want, names)
		}
	}
}

func TestResolveNewsSourceRequestsWorldTopics(t *testing.T) {
	tests := []struct {
		name    string
		topic   string
		wants   []string
		rejects []string
	}{
		{
			name:    "tech includes ars default",
			topic:   "tech",
			wants:   []string{"bbc_technology", "ars_technica"},
			rejects: []string{"guardian_world"},
		},
		{
			name:    "science uses science feed",
			topic:   "science",
			wants:   []string{"ars_technica_science"},
			rejects: []string{"bbc_world", "guardian_world"},
		},
		{
			name:    "culture uses culture feed",
			topic:   "culture",
			wants:   []string{"ars_technica_culture"},
			rejects: []string{"bbc_world", "guardian_world"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			reqs := resolveNewsSourceRequests(newsFetchArgs{
				Region:   "world",
				Language: "en",
				Topic:    tc.topic,
				MaxItems: 5,
			})
			got := make([]string, 0, len(reqs))
			for _, req := range reqs {
				got = append(got, req.Key)
			}
			for _, want := range tc.wants {
				if !containsString(got, want) {
					t.Fatalf("expected source %q in %v", want, got)
				}
			}
			for _, reject := range tc.rejects {
				if containsString(got, reject) {
					t.Fatalf("did not expect source %q in %v", reject, got)
				}
			}
		})
	}
}

func TestFilterItemsByTopicSupportsScienceAndCulture(t *testing.T) {
	items := []newsItem{
		{Headline: "NASA science team studies climate signals from deep space", Section: "science"},
		{Headline: "Gaming studios prepare a major culture showcase this fall", Section: "culture"},
		{Headline: "Parliament opens a new budget session", Section: "politics"},
	}

	science := filterItemsByTopic(items, "science")
	if len(science) != 1 || !strings.Contains(strings.ToLower(science[0].Headline), "nasa") {
		t.Fatalf("science filter returned %+v", science)
	}

	culture := filterItemsByTopic(items, "culture")
	if len(culture) != 1 || !strings.Contains(strings.ToLower(culture[0].Headline), "gaming") {
		t.Fatalf("culture filter returned %+v", culture)
	}
}

func containsString(items []string, want string) bool {
	for _, item := range items {
		if item == want {
			return true
		}
	}
	return false
}

type routingFakeDockerSession struct {
	routes map[string]executor.Result
}

func (s *routingFakeDockerSession) ID() string                  { return "news-test" }
func (s *routingFakeDockerSession) Type() string                { return "docker" }
func (s *routingFakeDockerSession) Start(context.Context) error { return nil }
func (s *routingFakeDockerSession) Close() error                { return nil }
func (s *routingFakeDockerSession) Workspace() string           { return "/tmp/workspace" }
func (s *routingFakeDockerSession) Run(_ context.Context, req executor.RunRequest) (executor.Result, error) {
	url := envValue(req.Env, "V100_URL")
	if res, ok := s.routes[url]; ok {
		return res, nil
	}
	return executor.Result{ExitCode: 1, Stderr: "unexpected url: " + url}, nil
}
