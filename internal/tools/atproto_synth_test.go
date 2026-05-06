package tools

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/tripledoublev/v100/internal/config"
)

func TestATProtoAnonSynthReturnsSourceURIs(t *testing.T) {
	mux := http.NewServeMux()
	_, cfg := setupATProtoServer(t, mux)
	now := time.Now().UTC().Format(time.RFC3339)
	mux.HandleFunc("/xrpc/app.bsky.feed.getTimeline", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"feed": []map[string]any{
				{
					"post": map[string]any{
						"uri": "at://did:plc:one/app.bsky.feed.post/one",
						"record": map[string]any{
							"text":      "hello @person https://example.com from the feed",
							"createdAt": now,
						},
					},
				},
				{
					"post": map[string]any{
						"uri": "at://did:plc:two/app.bsky.feed.post/two",
						"record": map[string]any{
							"text":      "@other bsky.app/profile/test useful signal",
							"createdAt": now,
						},
					},
				},
			},
		})
	})

	result, err := ATProtoAnonSynth(&config.Config{ATProto: cfg}).Exec(context.Background(), emptyCallCtx(), json.RawMessage(`{"limit":2,"hours":24}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.OK {
		t.Fatalf("expected OK, got: %s", result.Output)
	}
	var out struct {
		Corpus  string   `json:"corpus"`
		Sources []string `json:"sources"`
	}
	if err := json.Unmarshal([]byte(result.Output), &out); err != nil {
		t.Fatalf("invalid output JSON: %v\n%s", err, result.Output)
	}
	if len(out.Sources) != 2 {
		t.Fatalf("sources len = %d, want 2: %#v", len(out.Sources), out.Sources)
	}
	if out.Sources[0] != "at://did:plc:one/app.bsky.feed.post/one" || out.Sources[1] != "at://did:plc:two/app.bsky.feed.post/two" {
		t.Fatalf("sources = %#v", out.Sources)
	}
	if strings.Contains(out.Corpus, "at://") || strings.Contains(out.Corpus, "@person") || strings.Contains(out.Corpus, "https://") {
		t.Fatalf("corpus was not anonymized: %q", out.Corpus)
	}
}
