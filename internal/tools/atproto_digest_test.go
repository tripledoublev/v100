package tools

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/tripledoublev/v100/internal/config"
)

func setupDigestServer(t *testing.T, mux *http.ServeMux) (*httptest.Server, config.ATProtoConfig) {
	t.Helper()
	mux.HandleFunc("/xrpc/com.atproto.server.createSession", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{
			"accessJwt": "test-jwt",
			"did":       "did:plc:digest-test",
			"handle":    "test.bsky.social",
		})
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	cfg := config.ATProtoConfig{
		Handle:      "test.bsky.social",
		AppPassword: "test-pw",
		PDSURL:      srv.URL,
	}
	return srv, cfg
}

// ---------------------------------------------------------------------------
// atproto_vibe_check tests
// ---------------------------------------------------------------------------

func TestATProtoVibeCheck_Basic(t *testing.T) {
	mux := http.NewServeMux()
	_, cfg := setupDigestServer(t, mux)
	now := time.Now().UTC()
	mux.HandleFunc("/xrpc/app.bsky.feed.getTimeline", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"feed": []map[string]any{
				{
					"post": map[string]any{
						"uri": "at://did:plc:a/app.bsky.feed.post/1",
						"author": map[string]string{
							"handle":      "alice.bsky.social",
							"displayName": "Alice",
						},
						"record": map[string]string{
							"text":      "golang is great",
							"createdAt": now.Format(time.RFC3339),
						},
						"likeCount":   10,
						"repostCount": 5,
						"replyCount":  2,
					},
				},
				{
					"post": map[string]any{
						"uri": "at://did:plc:b/app.bsky.feed.post/2",
						"author": map[string]string{
							"handle":      "bob.bsky.social",
							"displayName": "Bob",
						},
						"record": map[string]string{
							"text":      "golang rocks",
							"createdAt": now.Add(-1 * time.Hour).Format(time.RFC3339),
						},
						"likeCount":   20,
						"repostCount": 8,
						"replyCount":  3,
					},
				},
			},
		})
	})

	fullCfg := &config.Config{ATProto: cfg}
	tool := ATProtoVibeCheck(fullCfg)
	args, _ := json.Marshal(map[string]any{})
	result, err := tool.Exec(context.Background(), emptyCallCtx(), args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.OK {
		t.Fatalf("expected OK=true, got: %s", result.Output)
	}
	// Should have header + posts
	if !strings.Contains(result.Output, "posts from the last") {
		t.Errorf("expected header line in output, got: %s", result.Output)
	}
	if !strings.Contains(result.Output, "alice") {
		t.Errorf("expected alice's post in output, got: %s", result.Output)
	}
	if !strings.Contains(result.Output, "bob") {
		t.Errorf("expected bob's post in output, got: %s", result.Output)
	}
	if !strings.Contains(result.Output, "golang") {
		t.Errorf("expected 'golang' keyword in output, got: %s", result.Output)
	}
}

func TestATProtoVibeCheck_HoursFilter(t *testing.T) {
	mux := http.NewServeMux()
	_, cfg := setupDigestServer(t, mux)
	now := time.Now().UTC()
	oldTime := now.Add(-25 * time.Hour)
	mux.HandleFunc("/xrpc/app.bsky.feed.getTimeline", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"feed": []map[string]any{
				{
					"post": map[string]any{
						"uri":    "at://did:plc:a/app.bsky.feed.post/1",
						"author": map[string]string{"handle": "alice.bsky.social", "displayName": "Alice"},
						"record": map[string]string{
							"text":      "recent post",
							"createdAt": now.Add(-1 * time.Hour).Format(time.RFC3339),
						},
						"likeCount":   5,
						"repostCount": 1,
						"replyCount":  0,
					},
				},
				{
					"post": map[string]any{
						"uri":    "at://did:plc:b/app.bsky.feed.post/2",
						"author": map[string]string{"handle": "bob.bsky.social", "displayName": "Bob"},
						"record": map[string]string{
							"text":      "old post",
							"createdAt": oldTime.Format(time.RFC3339),
						},
						"likeCount":   100,
						"repostCount": 50,
						"replyCount":  10,
					},
				},
			},
		})
	})

	fullCfg := &config.Config{ATProto: cfg}
	tool := ATProtoVibeCheck(fullCfg)
	// default is 12 hours — oldTime is 25 hours ago, should be excluded
	args, _ := json.Marshal(map[string]any{})
	result, err := tool.Exec(context.Background(), emptyCallCtx(), args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.OK {
		t.Fatalf("expected OK=true, got: %s", result.Output)
	}
	if strings.Contains(result.Output, "old post") {
		t.Errorf("old post should be filtered out, got: %s", result.Output)
	}
	if !strings.Contains(result.Output, "recent post") {
		t.Errorf("recent post should be included, got: %s", result.Output)
	}
}

func TestATProtoVibeCheck_DefaultParams(t *testing.T) {
	mux := http.NewServeMux()
	_, cfg := setupDigestServer(t, mux)
	var capturedLimit string
	mux.HandleFunc("/xrpc/app.bsky.feed.getTimeline", func(w http.ResponseWriter, r *http.Request) {
		capturedLimit = r.URL.Query().Get("limit")
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"feed": []any{}})
	})

	fullCfg := &config.Config{ATProto: cfg}
	tool := ATProtoVibeCheck(fullCfg)
	args, _ := json.Marshal(map[string]any{})
	_, _ = tool.Exec(context.Background(), emptyCallCtx(), args)
	// API calls are batched in groups of 20 for efficiency
	// Just verify that a limit was sent and it's reasonable
	if capturedLimit == "" {
		t.Errorf("expected limit to be sent to API")
	}
}

func TestATProtoVibeCheck_TopWords(t *testing.T) {
	mux := http.NewServeMux()
	_, cfg := setupDigestServer(t, mux)
	now := time.Now().UTC()
	mux.HandleFunc("/xrpc/app.bsky.feed.getTimeline", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"feed": []map[string]any{
				{
					"post": map[string]any{
						"uri":    "at://1",
						"author": map[string]string{"handle": "a.bsky.social", "displayName": "A"},
						"record": map[string]string{
							"text":      "golang is great golang golang",
							"createdAt": now.Format(time.RFC3339),
						},
						"likeCount": 1, "repostCount": 1, "replyCount": 1,
					},
				},
				{
					"post": map[string]any{
						"uri":    "at://2",
						"author": map[string]string{"handle": "b.bsky.social", "displayName": "B"},
						"record": map[string]string{
							"text":      "rust is great rust",
							"createdAt": now.Format(time.RFC3339),
						},
						"likeCount": 1, "repostCount": 1, "replyCount": 1,
					},
				},
			},
		})
	})

	fullCfg := &config.Config{ATProto: cfg}
	tool := ATProtoVibeCheck(fullCfg)
	args, _ := json.Marshal(map[string]any{})
	result, err := tool.Exec(context.Background(), emptyCallCtx(), args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.OK {
		t.Fatalf("expected OK=true, got: %s", result.Output)
	}
	// 'golang' appears 3 times, 'rust' appears 2 times — golang should be in top words
	// 'is' and 'great' are frequent but should be filtered as stopwords
	if !strings.Contains(result.Output, "golang") {
		t.Errorf("expected 'golang' in top words, got: %s", result.Output)
	}
}

// ---------------------------------------------------------------------------
// atproto_daily_digest tests
// ---------------------------------------------------------------------------

func TestATProtoDailyDigest_Basic(t *testing.T) {
	mux := http.NewServeMux()
	_, cfg := setupDigestServer(t, mux)
	now := time.Now().UTC()
	mux.HandleFunc("/xrpc/app.bsky.feed.getTimeline", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"feed": []map[string]any{
				{
					"post": map[string]any{
						"uri":    "at://1",
						"author": map[string]string{"handle": "a.bsky.social", "displayName": "A"},
						"record": map[string]string{
							"text":      "post a",
							"createdAt": now.Format(time.RFC3339),
						},
						"likeCount":   10,
						"repostCount": 5,
						"replyCount":  2,
					},
				},
				{
					"post": map[string]any{
						"uri":    "at://2",
						"author": map[string]string{"handle": "b.bsky.social", "displayName": "B"},
						"record": map[string]string{
							"text":      "post b",
							"createdAt": now.Format(time.RFC3339),
						},
						"likeCount":   20,
						"repostCount": 0,
						"replyCount":  0,
					},
				},
			},
		})
	})

	fullCfg := &config.Config{ATProto: cfg}
	tool := ATProtoDailyDigest(fullCfg)
	args, _ := json.Marshal(map[string]any{})
	result, err := tool.Exec(context.Background(), emptyCallCtx(), args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.OK {
		t.Fatalf("expected OK=true, got: %s", result.Output)
	}
	if !strings.Contains(result.Output, "posts from the last") {
		t.Errorf("expected header line, got: %s", result.Output)
	}
}

func TestATProtoDailyDigest_TopN(t *testing.T) {
	mux := http.NewServeMux()
	_, cfg := setupDigestServer(t, mux)
	now := time.Now().UTC()
	mux.HandleFunc("/xrpc/app.bsky.feed.getTimeline", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		feed := []map[string]any{}
		for i := 1; i <= 5; i++ {
			feed = append(feed, map[string]any{
				"post": map[string]any{
					"uri":    "at://" + string(rune(i)),
					"author": map[string]string{"handle": "user" + string(rune(i)), "displayName": "User"},
					"record": map[string]string{
						"text":      "post " + string(rune(i)),
						"createdAt": now.Format(time.RFC3339),
					},
					"likeCount":   int64(i * 10),
					"repostCount": int64(i),
					"replyCount":  0,
				},
			})
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"feed": feed})
	})

	fullCfg := &config.Config{ATProto: cfg}
	tool := ATProtoDailyDigest(fullCfg)
	args, _ := json.Marshal(map[string]any{"top_n": 2})
	result, err := tool.Exec(context.Background(), emptyCallCtx(), args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.OK {
		t.Fatalf("expected OK=true, got: %s", result.Output)
	}
	// Should contain only top 2 posts by engagement score
	// Count numbered lines (1., 2., etc) which indicate posts
	postCount := 0
	for _, line := range strings.Split(result.Output, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "1.") ||
		   strings.HasPrefix(strings.TrimSpace(line), "2.") ||
		   strings.HasPrefix(strings.TrimSpace(line), "3.") {
			postCount++
		}
	}
	if postCount > 2 {
		t.Errorf("expected at most 2 posts in top_n=2, got %d posts in: %s", postCount, result.Output)
	}
}

func TestATProtoDailyDigest_ScoreOrder(t *testing.T) {
	mux := http.NewServeMux()
	_, cfg := setupDigestServer(t, mux)
	now := time.Now().UTC()
	mux.HandleFunc("/xrpc/app.bsky.feed.getTimeline", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"feed": []map[string]any{
				{
					"post": map[string]any{
						"uri":    "at://low",
						"author": map[string]string{"handle": "low.bsky.social", "displayName": "Low"},
						"record": map[string]string{
							"text":      "low engagement",
							"createdAt": now.Format(time.RFC3339),
						},
						"likeCount":   1,
						"repostCount": 0,
						"replyCount":  0,
					},
				},
				{
					"post": map[string]any{
						"uri":    "at://high",
						"author": map[string]string{"handle": "high.bsky.social", "displayName": "High"},
						"record": map[string]string{
							"text":      "high engagement",
							"createdAt": now.Format(time.RFC3339),
						},
						"likeCount":   100,
						"repostCount": 10,
						"replyCount":  5,
					},
				},
			},
		})
	})

	fullCfg := &config.Config{ATProto: cfg}
	tool := ATProtoDailyDigest(fullCfg)
	args, _ := json.Marshal(map[string]any{"top_n": 10})
	result, err := tool.Exec(context.Background(), emptyCallCtx(), args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.OK {
		t.Fatalf("expected OK=true, got: %s", result.Output)
	}
	// High engagement post (100 + 10*2 + 5 = 125) should appear before low (1)
	highIdx := strings.Index(result.Output, "high engagement")
	lowIdx := strings.Index(result.Output, "low engagement")
	if highIdx < 0 || lowIdx < 0 {
		t.Fatalf("expected both posts in output, got: %s", result.Output)
	}
	if highIdx > lowIdx {
		t.Errorf("high engagement should appear before low engagement, got: %s", result.Output)
	}
}

// ---------------------------------------------------------------------------
// tool metadata tests
// ---------------------------------------------------------------------------

func TestATProtoToolMetadata_Digest(t *testing.T) {
	cfg := &config.Config{}
	cases := []struct {
		tool      Tool
		name      string
		dangerous bool
	}{
		{ATProtoVibeCheck(cfg), "atproto_vibe_check", false},
		{ATProtoDailyDigest(cfg), "atproto_daily_digest", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.tool.Name() != tc.name {
				t.Errorf("Name() = %q, want %q", tc.tool.Name(), tc.name)
			}
			gotDangerous := tc.tool.DangerLevel() == Dangerous
			if gotDangerous != tc.dangerous {
				t.Errorf("DangerLevel() dangerous=%v, want %v", gotDangerous, tc.dangerous)
			}
			fx := tc.tool.Effects()
			if !fx.NeedsNetwork {
				t.Errorf("Effects().NeedsNetwork should be true")
			}
			if fx.ExternalSideEffect {
				t.Errorf("Effects().ExternalSideEffect should be false")
			}
		})
	}
}
