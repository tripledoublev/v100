package tools

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

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

	t.Run("follower_momentum_schema", func(t *testing.T) {
		tool := ATProtoFollowerMomentum(cfg)
		if tool.Name() != "atproto_follower_momentum" {
			t.Errorf("expected name atproto_follower_momentum, got %s", tool.Name())
		}
		if !tool.Effects().MutatesRunState {
			t.Error("follower momentum should declare snapshot state mutation")
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

func TestATProtoFollowerMomentumReportsNewFollowersAndPersistsSnapshot(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	mux := http.NewServeMux()
	mux.HandleFunc("/xrpc/com.atproto.server.createSession", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]string{
			"accessJwt": "jwt",
			"did":       "did:plc:me",
			"handle":    "me.bsky.social",
		})
	})
	mux.HandleFunc("/xrpc/app.bsky.graph.getFollowers", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("actor") != "did:plc:me" {
			t.Fatalf("actor = %q, want did:plc:me", r.URL.Query().Get("actor"))
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"followers": []map[string]string{
			{
				"did":         "did:plc:old",
				"handle":      "old.bsky.social",
				"displayName": "Old",
				"description": "existing systems account",
			},
			{
				"did":         "did:plc:new",
				"handle":      "new.bsky.social",
				"displayName": "New",
				"description": "agent evaluation research and tools",
			},
		}})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	cfg := &config.Config{ATProto: config.ATProtoConfig{
		Handle:      "me.bsky.social",
		AppPassword: "pw",
		PDSURL:      srv.URL,
	}}
	snapshotPath, err := followerMomentumSnapshotPath("did:plc:me")
	if err != nil {
		t.Fatal(err)
	}
	if err := writeFollowerMomentumSnapshot(snapshotPath, map[string]followerMomentumProfile{
		"did:plc:old": {DID: "did:plc:old", Handle: "old.bsky.social"},
	}); err != nil {
		t.Fatal(err)
	}

	tool := ATProtoFollowerMomentum(cfg)
	res, err := tool.Exec(t.Context(), ToolCallContext{}, json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	if !res.OK {
		t.Fatalf("tool failed: %s", res.Output)
	}
	if !strings.Contains(res.Output, "new: 1") || !strings.Contains(res.Output, "New (@new.bsky.social)") {
		t.Fatalf("unexpected output: %s", res.Output)
	}
	data, err := os.ReadFile(snapshotPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "did:plc:new") {
		t.Fatalf("snapshot not updated: %s", string(data))
	}
}

func TestATProtoCommunityDetectFetchesSampledFollowsConcurrently(t *testing.T) {
	var inFlight int32
	var maxInFlight int32
	mux := http.NewServeMux()
	mux.HandleFunc("/xrpc/com.atproto.server.createSession", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]string{
			"accessJwt": "jwt",
			"did":       "did:plc:me",
			"handle":    "me.bsky.social",
		})
	})
	mux.HandleFunc("/xrpc/app.bsky.graph.getFollows", func(w http.ResponseWriter, r *http.Request) {
		actor := r.URL.Query().Get("actor")
		if actor == "did:plc:me" {
			follows := make([]map[string]string, 0, 8)
			for i := 0; i < 8; i++ {
				follows = append(follows, map[string]string{
					"did":         "did:plc:follow" + string(rune('a'+i)),
					"handle":      "follow.bsky.social",
					"displayName": "Follow",
				})
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"follows": follows})
			return
		}
		cur := atomic.AddInt32(&inFlight, 1)
		for {
			max := atomic.LoadInt32(&maxInFlight)
			if cur <= max || atomic.CompareAndSwapInt32(&maxInFlight, max, cur) {
				break
			}
		}
		time.Sleep(30 * time.Millisecond)
		atomic.AddInt32(&inFlight, -1)
		_ = json.NewEncoder(w).Encode(map[string]any{"follows": []map[string]string{
			{
				"did":         "did:plc:shared",
				"handle":      "shared.bsky.social",
				"displayName": "Shared",
			},
		}})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	cfg := &config.Config{ATProto: config.ATProtoConfig{
		Handle:      "me.bsky.social",
		AppPassword: "pw",
		PDSURL:      srv.URL,
	}}
	tool := ATProtoCommunityDetect(cfg)
	res, err := tool.Exec(t.Context(), ToolCallContext{}, json.RawMessage(`{"sample_size":8,"follows_limit":5,"min_shared":1}`))
	if err != nil {
		t.Fatal(err)
	}
	if !res.OK {
		t.Fatalf("tool failed: %s", res.Output)
	}
	if got := atomic.LoadInt32(&maxInFlight); got < 2 || got > 5 {
		t.Fatalf("max concurrent sampled getFollows calls = %d, want between 2 and 5", got)
	}
}
