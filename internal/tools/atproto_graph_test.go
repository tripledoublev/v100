package tools

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/tripledoublev/v100/internal/config"
)

func TestATProtoGraphTools(t *testing.T) {
	cfg := &config.Config{
		ATProto: config.ATProtoConfig{
			Handle: "test.bsky.social",
		},
	}

	t.Run("get_follows_schema", func(t *testing.T) {
		tool := ATProtoGetFollows(cfg)
		if tool.Name() != "atproto_get_follows" {
			t.Errorf("expected name atproto_get_follows, got %s", tool.Name())
		}
		schema := string(tool.InputSchema())
		if !strings.Contains(schema, "actor") {
			t.Error("input schema should contain actor")
		}
	})

	t.Run("get_followers_schema", func(t *testing.T) {
		tool := ATProtoGetFollowers(cfg)
		if tool.Name() != "atproto_get_followers" {
			t.Errorf("expected name atproto_get_followers, got %s", tool.Name())
		}
	})

	t.Run("get_profile_schema", func(t *testing.T) {
		tool := ATProtoGetProfile(cfg)
		if tool.Name() != "atproto_get_profile" {
			t.Errorf("expected name atproto_get_profile, got %s", tool.Name())
		}
	})

	t.Run("community_detect_schema", func(t *testing.T) {
		tool := ATProtoCommunityDetect(cfg)
		if tool.Name() != "atproto_community_detect" {
			t.Errorf("expected name atproto_community_detect, got %s", tool.Name())
		}
		if !strings.Contains(string(tool.InputSchema()), "min_shared") {
			t.Error("input schema should contain min_shared")
		}
	})
}

func TestATProtoCommunityDetectClustersBySharedFollows(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/xrpc/com.atproto.server.createSession", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]string{
			"accessJwt": "jwt",
			"did":       "did:plc:me",
			"handle":    "me.bsky.social",
		})
	})
	mux.HandleFunc("/xrpc/app.bsky.graph.getFollows", func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Query().Get("actor") {
		case "did:plc:me":
			_ = json.NewEncoder(w).Encode(map[string]any{"follows": []map[string]string{
				{"did": "did:plc:a", "handle": "a.bsky.social", "displayName": "Alice"},
				{"did": "did:plc:b", "handle": "b.bsky.social", "displayName": "Bob"},
				{"did": "did:plc:c", "handle": "c.bsky.social", "displayName": "Carol"},
			}})
		case "did:plc:a", "did:plc:b":
			_ = json.NewEncoder(w).Encode(map[string]any{"follows": []map[string]string{
				{"did": "did:plc:x", "handle": "x.bsky.social", "displayName": "Xavier"},
				{"did": "did:plc:y", "handle": "y.bsky.social", "displayName": "Yara"},
			}})
		case "did:plc:c":
			_ = json.NewEncoder(w).Encode(map[string]any{"follows": []map[string]string{
				{"did": "did:plc:z", "handle": "z.bsky.social", "displayName": "Zed"},
			}})
		default:
			t.Fatalf("unexpected actor: %s", r.URL.Query().Get("actor"))
		}
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	cfg := &config.Config{ATProto: config.ATProtoConfig{
		Handle:      "me.bsky.social",
		AppPassword: "pw",
		PDSURL:      srv.URL,
	}}
	tool := ATProtoCommunityDetect(cfg)
	args, _ := json.Marshal(map[string]int{"sample_size": 3, "follows_limit": 3, "min_shared": 2})
	res, err := tool.Exec(t.Context(), ToolCallContext{}, args)
	if err != nil {
		t.Fatal(err)
	}
	if !res.OK {
		t.Fatalf("tool failed: %s", res.Output)
	}
	if !strings.Contains(res.Output, "Clustered 3 follows") {
		t.Fatalf("missing cluster summary: %s", res.Output)
	}
	if !strings.Contains(res.Output, "Alice (@a.bsky.social), Bob (@b.bsky.social)") {
		t.Fatalf("expected Alice and Bob in same community: %s", res.Output)
	}
	if !strings.Contains(res.Output, "Xavier (@x.bsky.social) (2)") {
		t.Fatalf("expected shared follow evidence: %s", res.Output)
	}
}
