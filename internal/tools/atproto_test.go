package tools

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tripledoublev/v100/internal/config"
)

// setupATProtoServer returns a test HTTP server that serves minimal ATProto
// endpoint responses. The login endpoint always succeeds.
func setupATProtoServer(t *testing.T, mux *http.ServeMux) (*httptest.Server, config.ATProtoConfig) {
	t.Helper()
	mux.HandleFunc("/xrpc/com.atproto.server.createSession", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{
			"accessJwt": "test-jwt",
			"did":       "did:plc:test123",
			"handle":    "test.bsky.social",
		})
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	cfg := config.ATProtoConfig{
		Handle:      "test.bsky.social",
		AppPassword: "test-app-password",
		PDSURL:      srv.URL,
	}
	return srv, cfg
}

func emptyCallCtx() ToolCallContext { return ToolCallContext{} }

// ---------------------------------------------------------------------------
// tool metadata
// ---------------------------------------------------------------------------

func TestATProtoToolMetadata(t *testing.T) {
	cfg := &config.Config{}
	cases := []struct {
		tool       Tool
		name       string
		dangerous  bool
		needsNet   bool
		sideEffect bool
	}{
		{ATProtoFeed(cfg), "atproto_feed", false, true, false},
		{ATProtoNotifications(cfg), "atproto_notifications", false, true, false},
		{ATProtoPost(cfg), "atproto_post", true, true, true},
		{ATProtoResolve(cfg), "atproto_resolve", false, true, false},
		{ATProtoUploadBlob(cfg), "atproto_upload_blob", true, true, true},
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
			if fx.NeedsNetwork != tc.needsNet {
				t.Errorf("Effects().NeedsNetwork=%v, want %v", fx.NeedsNetwork, tc.needsNet)
			}
			if fx.ExternalSideEffect != tc.sideEffect {
				t.Errorf("Effects().ExternalSideEffect=%v, want %v", fx.ExternalSideEffect, tc.sideEffect)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// client login tests
// ---------------------------------------------------------------------------

func TestATProtoClient_LoginSuccess(t *testing.T) {
	mux := http.NewServeMux()
	_, cfg := setupATProtoServer(t, mux)
	cli := newATProtoClient(cfg)
	if err := cli.login(); err != nil {
		t.Fatalf("expected login success, got: %v", err)
	}
	if cli.session.AccessJwt != "test-jwt" {
		t.Errorf("unexpected jwt: %q", cli.session.AccessJwt)
	}
	if cli.session.DID != "did:plc:test123" {
		t.Errorf("unexpected did: %q", cli.session.DID)
	}
}

func TestATProtoClient_LoginMissingCredentials(t *testing.T) {
	cfg := config.ATProtoConfig{Handle: "test.bsky.social"} // no password
	cli := newATProtoClient(cfg)
	err := cli.login()
	if err == nil {
		t.Fatal("expected error for missing credentials")
	}
	if !strings.Contains(err.Error(), "app_password") {
		t.Errorf("expected error to mention app_password, got: %v", err)
	}
}

func TestATProtoClient_LoginMissingHandle(t *testing.T) {
	cfg := config.ATProtoConfig{AppPassword: "pw"} // no handle
	cli := newATProtoClient(cfg)
	err := cli.login()
	if err == nil {
		t.Fatal("expected error for missing handle")
	}
}

func TestATProtoClient_AppPasswordFromEnv(t *testing.T) {
	t.Setenv("TEST_BSKY_PW", "env-app-password")
	mux := http.NewServeMux()
	var capturedPassword string
	mux.HandleFunc("/xrpc/com.atproto.server.createSession", func(w http.ResponseWriter, r *http.Request) {
		var body map[string]string
		_ = json.NewDecoder(r.Body).Decode(&body)
		capturedPassword = body["password"]
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"accessJwt": "jwt", "did": "did:plc:x", "handle": "x.bsky.social"})
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	cfg := config.ATProtoConfig{Handle: "x.bsky.social", AppPasswordEnv: "TEST_BSKY_PW", PDSURL: srv.URL}
	cli := newATProtoClient(cfg)
	if err := cli.login(); err != nil {
		t.Fatalf("expected login success, got: %v", err)
	}
	if capturedPassword != "env-app-password" {
		t.Errorf("expected env password to be sent, got: %q", capturedPassword)
	}
}

func TestATProtoClient_AppPasswordEnvNotSet(t *testing.T) {
	cfg := config.ATProtoConfig{Handle: "x.bsky.social", AppPasswordEnv: "ATPROTO_PW_DEFINITELY_NOT_SET_XYZ"}
	cli := newATProtoClient(cfg)
	err := cli.login()
	if err == nil {
		t.Fatal("expected error when env var is not set")
	}
	if !strings.Contains(err.Error(), "ATPROTO_PW_DEFINITELY_NOT_SET_XYZ") {
		t.Errorf("expected error to name the missing env var, got: %v", err)
	}
}

func TestATProtoClient_LoginServerError(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/xrpc/com.atproto.server.createSession", func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, `{"error":"AuthenticationRequired"}`, http.StatusUnauthorized)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	cfg := config.ATProtoConfig{Handle: "x.bsky.social", AppPassword: "bad", PDSURL: srv.URL}
	cli := newATProtoClient(cfg)
	err := cli.login()
	if err == nil {
		t.Fatal("expected error from server 401")
	}
}

// ---------------------------------------------------------------------------
// atproto_feed tests
// ---------------------------------------------------------------------------

func TestATProtoFeed_Basic(t *testing.T) {
	mux := http.NewServeMux()
	_, cfg := setupATProtoServer(t, mux)
	mux.HandleFunc("/xrpc/app.bsky.feed.getTimeline", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"feed": []map[string]any{
				{
					"post": map[string]any{
						"uri": "at://did:plc:abc/app.bsky.feed.post/123",
						"author": map[string]string{
							"handle":      "alice.bsky.social",
							"displayName": "Alice",
						},
						"record": map[string]string{
							"text":      "Hello world",
							"createdAt": "2025-01-01T00:00:00Z",
						},
						"replyCount":  1,
						"repostCount": 2,
						"likeCount":   5,
					},
				},
			},
			"cursor": "next-cursor",
		})
	})

	fullCfg := &config.Config{ATProto: cfg}
	tool := ATProtoFeed(fullCfg)
	args, _ := json.Marshal(map[string]any{"limit": 5})
	result, err := tool.Exec(context.Background(), emptyCallCtx(), args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.OK {
		t.Fatalf("expected OK=true, got output: %s", result.Output)
	}
	if !strings.Contains(result.Output, "Hello world") {
		t.Errorf("expected output to contain post text, got: %s", result.Output)
	}
	if !strings.Contains(result.Output, "Alice") {
		t.Errorf("expected output to contain author name, got: %s", result.Output)
	}
	if !strings.Contains(result.Output, "cursor: next-cursor") {
		t.Errorf("expected cursor in output for pagination, got: %s", result.Output)
	}
}

func TestATProtoFeed_NoCursorInOutput(t *testing.T) {
	mux := http.NewServeMux()
	_, cfg := setupATProtoServer(t, mux)
	mux.HandleFunc("/xrpc/app.bsky.feed.getTimeline", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		// No cursor field in response.
		_ = json.NewEncoder(w).Encode(map[string]any{"feed": []any{}})
	})

	fullCfg := &config.Config{ATProto: cfg}
	tool := ATProtoFeed(fullCfg)
	args, _ := json.Marshal(map[string]any{})
	result, err := tool.Exec(context.Background(), emptyCallCtx(), args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Contains(result.Output, "cursor:") {
		t.Errorf("cursor line should be absent when API returns no cursor, got: %s", result.Output)
	}
}

func TestATProtoFeed_CursorForwardedToAPI(t *testing.T) {
	mux := http.NewServeMux()
	_, cfg := setupATProtoServer(t, mux)
	var capturedCursor string
	mux.HandleFunc("/xrpc/app.bsky.feed.getTimeline", func(w http.ResponseWriter, r *http.Request) {
		capturedCursor = r.URL.Query().Get("cursor")
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"feed": []any{}})
	})

	fullCfg := &config.Config{ATProto: cfg}
	tool := ATProtoFeed(fullCfg)
	args, _ := json.Marshal(map[string]any{"cursor": "page2-token"})
	_, _ = tool.Exec(context.Background(), emptyCallCtx(), args)
	if capturedCursor != "page2-token" {
		t.Errorf("expected cursor to be forwarded to API, got %q", capturedCursor)
	}
}

func TestATProtoFeed_DefaultLimit(t *testing.T) {
	mux := http.NewServeMux()
	_, cfg := setupATProtoServer(t, mux)
	var capturedLimit string
	mux.HandleFunc("/xrpc/app.bsky.feed.getTimeline", func(w http.ResponseWriter, r *http.Request) {
		capturedLimit = r.URL.Query().Get("limit")
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"feed": []any{}})
	})

	fullCfg := &config.Config{ATProto: cfg}
	tool := ATProtoFeed(fullCfg)
	args, _ := json.Marshal(map[string]any{}) // no limit
	_, _ = tool.Exec(context.Background(), emptyCallCtx(), args)
	if capturedLimit != "20" {
		t.Errorf("expected default limit=20, got %q", capturedLimit)
	}
}

// ---------------------------------------------------------------------------
// atproto_notifications tests
// ---------------------------------------------------------------------------

func TestATProtoNotifications_UnreadFilter(t *testing.T) {
	mux := http.NewServeMux()
	_, cfg := setupATProtoServer(t, mux)
	mux.HandleFunc("/xrpc/app.bsky.notification.listNotifications", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"notifications": []map[string]any{
				{
					"uri":       "at://did:plc:abc/app.bsky.feed.like/1",
					"reason":    "like",
					"author":    map[string]string{"handle": "bob.bsky.social", "displayName": "Bob"},
					"record":    map[string]string{"text": ""},
					"indexedAt": "2025-01-02T00:00:00Z",
					"isRead":    true,
				},
				{
					"uri":       "at://did:plc:abc/app.bsky.feed.mention/2",
					"reason":    "mention",
					"author":    map[string]string{"handle": "carol.bsky.social", "displayName": "Carol"},
					"record":    map[string]string{"text": "hey @test"},
					"indexedAt": "2025-01-02T01:00:00Z",
					"isRead":    false,
				},
			},
		})
	})

	fullCfg := &config.Config{ATProto: cfg}
	tool := ATProtoNotifications(fullCfg)
	args, _ := json.Marshal(map[string]any{"unread_only": true})
	result, err := tool.Exec(context.Background(), emptyCallCtx(), args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.OK {
		t.Fatalf("expected OK=true, got: %s", result.Output)
	}
	if strings.Contains(result.Output, "Bob") {
		t.Errorf("read notification should be filtered out")
	}
	if !strings.Contains(result.Output, "Carol") {
		t.Errorf("unread notification should appear")
	}
}

func TestATProtoNotifications_AllShown(t *testing.T) {
	mux := http.NewServeMux()
	_, cfg := setupATProtoServer(t, mux)
	mux.HandleFunc("/xrpc/app.bsky.notification.listNotifications", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"notifications": []map[string]any{
				{
					"uri":       "at://1",
					"reason":    "like",
					"author":    map[string]string{"handle": "dave.bsky.social", "displayName": "Dave"},
					"record":    map[string]string{"text": ""},
					"indexedAt": "2025-01-03T00:00:00Z",
					"isRead":    true,
				},
				{
					"uri":       "at://2",
					"reason":    "follow",
					"author":    map[string]string{"handle": "eve.bsky.social", "displayName": "Eve"},
					"record":    map[string]string{"text": ""},
					"indexedAt": "2025-01-03T01:00:00Z",
					"isRead":    false,
				},
			},
		})
	})

	fullCfg := &config.Config{ATProto: cfg}
	tool := ATProtoNotifications(fullCfg)
	// unread_only defaults to false — both notifications should appear.
	args, _ := json.Marshal(map[string]any{})
	result, err := tool.Exec(context.Background(), emptyCallCtx(), args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.OK {
		t.Fatalf("expected OK=true, got: %s", result.Output)
	}
	if !strings.Contains(result.Output, "Dave") {
		t.Errorf("read notification should appear when unread_only=false, got: %s", result.Output)
	}
	if !strings.Contains(result.Output, "Eve") {
		t.Errorf("unread notification should appear, got: %s", result.Output)
	}
}

func TestATProtoNotifications_Empty(t *testing.T) {
	mux := http.NewServeMux()
	_, cfg := setupATProtoServer(t, mux)
	mux.HandleFunc("/xrpc/app.bsky.notification.listNotifications", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"notifications": []any{}})
	})

	fullCfg := &config.Config{ATProto: cfg}
	tool := ATProtoNotifications(fullCfg)
	args, _ := json.Marshal(map[string]any{})
	result, err := tool.Exec(context.Background(), emptyCallCtx(), args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.OK {
		t.Fatalf("expected OK=true for empty result, got: %s", result.Output)
	}
	if result.Output != "no notifications" {
		t.Errorf("expected 'no notifications', got: %q", result.Output)
	}
}

func TestATProtoNotifications_DefaultLimit(t *testing.T) {
	mux := http.NewServeMux()
	_, cfg := setupATProtoServer(t, mux)
	var capturedLimit string
	mux.HandleFunc("/xrpc/app.bsky.notification.listNotifications", func(w http.ResponseWriter, r *http.Request) {
		capturedLimit = r.URL.Query().Get("limit")
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"notifications": []any{}})
	})

	fullCfg := &config.Config{ATProto: cfg}
	tool := ATProtoNotifications(fullCfg)
	args, _ := json.Marshal(map[string]any{})
	_, _ = tool.Exec(context.Background(), emptyCallCtx(), args)
	if capturedLimit != "20" {
		t.Errorf("expected default limit=20, got %q", capturedLimit)
	}
}

// ---------------------------------------------------------------------------
// atproto_post tests
// ---------------------------------------------------------------------------

func TestATProtoPost_PlainPost(t *testing.T) {
	mux := http.NewServeMux()
	_, cfg := setupATProtoServer(t, mux)
	var capturedBody map[string]any
	mux.HandleFunc("/xrpc/com.atproto.repo.createRecord", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&capturedBody)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{
			"uri": "at://did:plc:test123/app.bsky.feed.post/new1",
			"cid": "bafycid1",
		})
	})

	fullCfg := &config.Config{ATProto: cfg}
	tool := ATProtoPost(fullCfg)
	args, _ := json.Marshal(map[string]string{"text": "Hello Bluesky!"})
	result, err := tool.Exec(context.Background(), emptyCallCtx(), args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.OK {
		t.Fatalf("expected OK=true, got: %s", result.Output)
	}
	if capturedBody["collection"] != "app.bsky.feed.post" {
		t.Errorf("wrong collection: %v", capturedBody["collection"])
	}
	if !strings.Contains(result.Output, "at://") {
		t.Errorf("expected URI in output, got: %s", result.Output)
	}
}

func TestATProtoPost_Repost(t *testing.T) {
	mux := http.NewServeMux()
	_, cfg := setupATProtoServer(t, mux)
	var capturedBody map[string]any
	mux.HandleFunc("/xrpc/com.atproto.repo.createRecord", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&capturedBody)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"uri": "at://did:plc:test123/app.bsky.feed.repost/1", "cid": "cid1"})
	})

	fullCfg := &config.Config{ATProto: cfg}
	tool := ATProtoPost(fullCfg)
	args, _ := json.Marshal(map[string]string{
		"text":       "ignored",
		"repost_uri": "at://did:plc:other/app.bsky.feed.post/orig",
		"repost_cid": "bafyorigcid",
	})
	result, err := tool.Exec(context.Background(), emptyCallCtx(), args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.OK {
		t.Fatalf("expected OK=true, got: %s", result.Output)
	}
	if capturedBody["collection"] != "app.bsky.feed.repost" {
		t.Errorf("expected repost collection, got: %v", capturedBody["collection"])
	}
	// Repost output should use the same uri=… cid=… format as plain posts.
	if !strings.Contains(result.Output, "uri=") || !strings.Contains(result.Output, "cid=") {
		t.Errorf("expected uri=/cid= format in repost output, got: %s", result.Output)
	}
}

func TestATProtoPost_ReplyRootDefaultsToParent(t *testing.T) {
	mux := http.NewServeMux()
	_, cfg := setupATProtoServer(t, mux)
	var capturedRecord map[string]any
	mux.HandleFunc("/xrpc/com.atproto.repo.createRecord", func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		if rec, ok := body["record"].(map[string]any); ok {
			capturedRecord = rec
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"uri": "at://did:plc:test123/app.bsky.feed.post/reply1", "cid": "rcid1"})
	})

	fullCfg := &config.Config{ATProto: cfg}
	tool := ATProtoPost(fullCfg)
	// Top-level reply: only reply_to_* set, no root_* — root should default to parent.
	args, _ := json.Marshal(map[string]string{
		"text":         "replying here",
		"reply_to_uri": "at://did:plc:other/app.bsky.feed.post/parent",
		"reply_to_cid": "parentcid",
	})
	result, err := tool.Exec(context.Background(), emptyCallCtx(), args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.OK {
		t.Fatalf("expected OK=true, got: %s", result.Output)
	}
	reply, ok := capturedRecord["reply"].(map[string]any)
	if !ok {
		t.Fatalf("expected reply block in record, got: %v", capturedRecord)
	}
	root, _ := reply["root"].(map[string]any)
	parent, _ := reply["parent"].(map[string]any)
	if root["uri"] != "at://did:plc:other/app.bsky.feed.post/parent" {
		t.Errorf("root uri should default to reply_to_uri, got: %v", root["uri"])
	}
	if parent["uri"] != "at://did:plc:other/app.bsky.feed.post/parent" {
		t.Errorf("parent uri should be reply_to_uri, got: %v", parent["uri"])
	}
}

func TestATProtoPost_ReplyNestedExplicitRoot(t *testing.T) {
	mux := http.NewServeMux()
	_, cfg := setupATProtoServer(t, mux)
	var capturedRecord map[string]any
	mux.HandleFunc("/xrpc/com.atproto.repo.createRecord", func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		if rec, ok := body["record"].(map[string]any); ok {
			capturedRecord = rec
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"uri": "at://did:plc:test123/app.bsky.feed.post/nested", "cid": "ncid"})
	})

	fullCfg := &config.Config{ATProto: cfg}
	tool := ATProtoPost(fullCfg)
	// Nested reply: root_* differs from reply_to_* (thread root ≠ immediate parent).
	args, _ := json.Marshal(map[string]string{
		"text":         "nested reply",
		"reply_to_uri": "at://did:plc:other/app.bsky.feed.post/middle",
		"reply_to_cid": "middlecid",
		"root_uri":     "at://did:plc:other/app.bsky.feed.post/root",
		"root_cid":     "rootcid",
	})
	result, err := tool.Exec(context.Background(), emptyCallCtx(), args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.OK {
		t.Fatalf("expected OK=true, got: %s", result.Output)
	}
	reply, ok := capturedRecord["reply"].(map[string]any)
	if !ok {
		t.Fatalf("expected reply block in record, got: %v", capturedRecord)
	}
	root, _ := reply["root"].(map[string]any)
	parent, _ := reply["parent"].(map[string]any)
	if root["uri"] != "at://did:plc:other/app.bsky.feed.post/root" {
		t.Errorf("root uri should be explicit root_uri, got: %v", root["uri"])
	}
	if parent["uri"] != "at://did:plc:other/app.bsky.feed.post/middle" {
		t.Errorf("parent uri should be reply_to_uri, got: %v", parent["uri"])
	}
}

func TestATProtoPost_QuotePost(t *testing.T) {
	mux := http.NewServeMux()
	_, cfg := setupATProtoServer(t, mux)
	var capturedRecord map[string]any
	mux.HandleFunc("/xrpc/com.atproto.repo.createRecord", func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		if rec, ok := body["record"].(map[string]any); ok {
			capturedRecord = rec
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"uri": "at://did:plc:test123/app.bsky.feed.post/q1", "cid": "qcid"})
	})

	fullCfg := &config.Config{ATProto: cfg}
	tool := ATProtoPost(fullCfg)
	args, _ := json.Marshal(map[string]string{
		"text":      "great take",
		"quote_uri": "at://did:plc:other/app.bsky.feed.post/orig",
		"quote_cid": "origcid",
	})
	result, err := tool.Exec(context.Background(), emptyCallCtx(), args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.OK {
		t.Fatalf("expected OK=true, got: %s", result.Output)
	}
	embed, ok := capturedRecord["embed"].(map[string]any)
	if !ok {
		t.Fatalf("expected embed block in record, got: %v", capturedRecord)
	}
	if embed["$type"] != "app.bsky.embed.record" {
		t.Errorf("expected embed $type=app.bsky.embed.record, got: %v", embed["$type"])
	}
	rec, _ := embed["record"].(map[string]any)
	if rec["uri"] != "at://did:plc:other/app.bsky.feed.post/orig" {
		t.Errorf("expected quoted post uri in embed, got: %v", rec["uri"])
	}
	if rec["cid"] != "origcid" {
		t.Errorf("expected quoted post cid in embed, got: %v", rec["cid"])
	}
}

func TestATProtoPost_ImageOnlyPost(t *testing.T) {
	mux := http.NewServeMux()
	_, cfg := setupATProtoServer(t, mux)
	var capturedRecord map[string]any
	mux.HandleFunc("/xrpc/com.atproto.repo.createRecord", func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		if rec, ok := body["record"].(map[string]any); ok {
			capturedRecord = rec
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"uri": "at://did:plc:test123/app.bsky.feed.post/img", "cid": "imgcid"})
	})

	fullCfg := &config.Config{ATProto: cfg}
	tool := ATProtoPost(fullCfg)
	args, _ := json.Marshal(map[string]any{
		"images": []map[string]any{{
			"cid":  "bafkimg",
			"mime": "image/png",
			"size": 42,
			"alt":  "a diagram",
		}},
	})
	result, err := tool.Exec(context.Background(), emptyCallCtx(), args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.OK {
		t.Fatalf("expected OK=true, got: %s", result.Output)
	}
	if capturedRecord["text"] != "" {
		t.Errorf("expected empty text for image-only post, got: %v", capturedRecord["text"])
	}
	embed, ok := capturedRecord["embed"].(map[string]any)
	if !ok {
		t.Fatalf("expected image embed, got: %v", capturedRecord)
	}
	if embed["$type"] != "app.bsky.embed.images" {
		t.Fatalf("expected image embed type, got: %v", embed["$type"])
	}
	images, ok := embed["images"].([]any)
	if !ok || len(images) != 1 {
		t.Fatalf("expected one image, got: %#v", embed["images"])
	}
	item := images[0].(map[string]any)
	if item["alt"] != "a diagram" {
		t.Errorf("alt text mismatch: %v", item["alt"])
	}
}

func TestATProtoPost_QuoteWithImagesUsesRecordWithMedia(t *testing.T) {
	mux := http.NewServeMux()
	_, cfg := setupATProtoServer(t, mux)
	var capturedRecord map[string]any
	mux.HandleFunc("/xrpc/com.atproto.repo.createRecord", func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		if rec, ok := body["record"].(map[string]any); ok {
			capturedRecord = rec
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"uri": "at://did:plc:test123/app.bsky.feed.post/qimg", "cid": "qimgcid"})
	})

	fullCfg := &config.Config{ATProto: cfg}
	tool := ATProtoPost(fullCfg)
	args, _ := json.Marshal(map[string]any{
		"text":      "quote with image",
		"quote_uri": "at://did:plc:other/app.bsky.feed.post/orig",
		"quote_cid": "origcid",
		"images": []map[string]any{{
			"cid":  "bafkimg",
			"mime": "image/jpeg",
			"size": 99,
		}},
	})
	result, err := tool.Exec(context.Background(), emptyCallCtx(), args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.OK {
		t.Fatalf("expected OK=true, got: %s", result.Output)
	}
	embed, ok := capturedRecord["embed"].(map[string]any)
	if !ok {
		t.Fatalf("expected embed block, got: %v", capturedRecord)
	}
	if embed["$type"] != "app.bsky.embed.recordWithMedia" {
		t.Fatalf("expected recordWithMedia embed, got: %v", embed["$type"])
	}
	record, ok := embed["record"].(map[string]any)
	if !ok || record["$type"] != "app.bsky.embed.record" {
		t.Fatalf("expected quote record embed, got: %#v", embed["record"])
	}
	media, ok := embed["media"].(map[string]any)
	if !ok || media["$type"] != "app.bsky.embed.images" {
		t.Fatalf("expected images media embed, got: %#v", embed["media"])
	}
}

func TestATProtoPost_RepostSubjectPayload(t *testing.T) {
	mux := http.NewServeMux()
	_, cfg := setupATProtoServer(t, mux)
	var capturedRecord map[string]any
	mux.HandleFunc("/xrpc/com.atproto.repo.createRecord", func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		if rec, ok := body["record"].(map[string]any); ok {
			capturedRecord = rec
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"uri": "at://did:plc:test123/app.bsky.feed.repost/2", "cid": "rcid2"})
	})

	fullCfg := &config.Config{ATProto: cfg}
	tool := ATProtoPost(fullCfg)
	args, _ := json.Marshal(map[string]string{
		"repost_uri": "at://did:plc:other/app.bsky.feed.post/tgt",
		"repost_cid": "tgtcid",
	})
	result, err := tool.Exec(context.Background(), emptyCallCtx(), args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.OK {
		t.Fatalf("expected OK=true, got: %s", result.Output)
	}
	subject, ok := capturedRecord["subject"].(map[string]any)
	if !ok {
		t.Fatalf("expected subject in repost record, got: %v", capturedRecord)
	}
	if subject["uri"] != "at://did:plc:other/app.bsky.feed.post/tgt" {
		t.Errorf("repost subject uri mismatch: %v", subject["uri"])
	}
	if subject["cid"] != "tgtcid" {
		t.Errorf("repost subject cid mismatch: %v", subject["cid"])
	}
}

func TestATProtoPost_ServerError(t *testing.T) {
	mux := http.NewServeMux()
	_, cfg := setupATProtoServer(t, mux)
	mux.HandleFunc("/xrpc/com.atproto.repo.createRecord", func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, `{"error":"InvalidRequest","message":"too long"}`, http.StatusBadRequest)
	})

	fullCfg := &config.Config{ATProto: cfg}
	tool := ATProtoPost(fullCfg)
	args, _ := json.Marshal(map[string]string{"text": "will fail"})
	result, err := tool.Exec(context.Background(), emptyCallCtx(), args)
	if err != nil {
		t.Fatalf("unexpected Go error (should be OK=false): %v", err)
	}
	if result.OK {
		t.Errorf("expected OK=false on server error, got output: %s", result.Output)
	}
	if !strings.Contains(result.Output, "400") {
		t.Errorf("expected HTTP status in error output, got: %s", result.Output)
	}
}

func TestATProtoPost_MissingText(t *testing.T) {
	mux := http.NewServeMux()
	_, cfg := setupATProtoServer(t, mux)
	fullCfg := &config.Config{ATProto: cfg}
	tool := ATProtoPost(fullCfg)
	args, _ := json.Marshal(map[string]string{}) // no text, no repost
	result, err := tool.Exec(context.Background(), emptyCallCtx(), args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.OK {
		t.Errorf("expected OK=false for missing text")
	}
}

func TestATProtoUploadBlob_RawBody(t *testing.T) {
	mux := http.NewServeMux()
	_, cfg := setupATProtoServer(t, mux)
	var capturedContentType string
	var capturedContentLength int64
	var capturedBody []byte
	mux.HandleFunc("/xrpc/com.atproto.repo.uploadBlob", func(w http.ResponseWriter, r *http.Request) {
		capturedContentType = r.Header.Get("Content-Type")
		capturedContentLength = r.ContentLength
		capturedBody, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"blob": map[string]any{
				"ref":      map[string]string{"$link": "bafkblob"},
				"mimeType": "image/png",
				"size":     len(capturedBody),
			},
		})
	})

	dir := t.TempDir()
	imagePath := filepath.Join(dir, "image.png")
	data := []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n', 1, 2, 3}
	if err := os.WriteFile(imagePath, data, 0o600); err != nil {
		t.Fatal(err)
	}

	fullCfg := &config.Config{ATProto: cfg}
	tool := ATProtoUploadBlob(fullCfg)
	args, _ := json.Marshal(map[string]string{"image_path": imagePath, "alt_text": "alt"})
	result, err := tool.Exec(context.Background(), emptyCallCtx(), args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.OK {
		t.Fatalf("expected OK=true, got: %s", result.Output)
	}
	if capturedContentType != "image/png" {
		t.Fatalf("Content-Type = %q, want image/png", capturedContentType)
	}
	if capturedContentLength != int64(len(data)) {
		t.Fatalf("ContentLength = %d, want %d", capturedContentLength, len(data))
	}
	if string(capturedBody) != string(data) {
		t.Fatalf("upload body mismatch: got %v want %v", capturedBody, data)
	}
	if strings.Contains(capturedContentType, "multipart/") {
		t.Fatalf("upload should not use multipart content type: %s", capturedContentType)
	}
	if !strings.Contains(result.Output, `"cid":"bafkblob"`) || !strings.Contains(result.Output, `"alt":"alt"`) {
		t.Fatalf("unexpected output: %s", result.Output)
	}
}

// ---------------------------------------------------------------------------
// atproto_resolve tests
// ---------------------------------------------------------------------------

func TestATProtoResolve_Success(t *testing.T) {
	mux := http.NewServeMux()
	_, cfg := setupATProtoServer(t, mux)
	mux.HandleFunc("/xrpc/com.atproto.identity.resolveHandle", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"did": "did:plc:resolved123"})
	})

	fullCfg := &config.Config{ATProto: cfg}
	tool := ATProtoResolve(fullCfg)
	args, _ := json.Marshal(map[string]string{"handle": "someone.bsky.social"})
	result, err := tool.Exec(context.Background(), emptyCallCtx(), args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.OK {
		t.Fatalf("expected OK=true, got: %s", result.Output)
	}
	if !strings.Contains(result.Output, "did:plc:resolved123") {
		t.Errorf("expected DID in output, got: %s", result.Output)
	}
}

func TestATProtoResolve_MissingHandle(t *testing.T) {
	mux := http.NewServeMux()
	_, cfg := setupATProtoServer(t, mux)
	fullCfg := &config.Config{ATProto: cfg}
	tool := ATProtoResolve(fullCfg)
	args, _ := json.Marshal(map[string]string{})
	result, err := tool.Exec(context.Background(), emptyCallCtx(), args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.OK {
		t.Errorf("expected OK=false for empty handle")
	}
}

func TestATProtoResolve_NoCredentialsRequired(t *testing.T) {
	// com.atproto.identity.resolveHandle is public — resolve should succeed
	// even when no handle/app_password are configured for the authenticated user.
	mux := http.NewServeMux()
	mux.HandleFunc("/xrpc/com.atproto.identity.resolveHandle", func(w http.ResponseWriter, r *http.Request) {
		// Assert no Authorization header is sent.
		if r.Header.Get("Authorization") != "" {
			http.Error(w, "unexpected auth header", http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"did": "did:plc:public123"})
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	// Config with no credentials at all.
	fullCfg := &config.Config{ATProto: config.ATProtoConfig{PDSURL: srv.URL}}
	tool := ATProtoResolve(fullCfg)
	args, _ := json.Marshal(map[string]string{"handle": "public.bsky.social"})
	result, err := tool.Exec(context.Background(), emptyCallCtx(), args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.OK {
		t.Fatalf("expected OK=true without credentials, got: %s", result.Output)
	}
	if !strings.Contains(result.Output, "did:plc:public123") {
		t.Errorf("expected DID in output, got: %s", result.Output)
	}
}

func TestPickATProtoAccount_Main(t *testing.T) {
	cfg := &config.Config{
		ATProto:    config.ATProtoConfig{Handle: "main.bsky.social"},
		ATProtoAlt: config.ATProtoConfig{Handle: "alt.bsky.social"},
	}
	got, err := pickATProtoAccount(cfg, "main")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Handle != "main.bsky.social" {
		t.Fatalf("expected main account, got handle=%s", got.Handle)
	}
}

func TestPickATProtoAccount_Alt(t *testing.T) {
	cfg := &config.Config{
		ATProto:    config.ATProtoConfig{Handle: "main.bsky.social"},
		ATProtoAlt: config.ATProtoConfig{Handle: "alt.bsky.social"},
	}
	got, err := pickATProtoAccount(cfg, "alt")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Handle != "alt.bsky.social" {
		t.Fatalf("expected alt account, got handle=%s", got.Handle)
	}
}

func TestPickATProtoAccount_EmptyDefaultsToMain(t *testing.T) {
	cfg := &config.Config{
		ATProto:    config.ATProtoConfig{Handle: "main.bsky.social"},
		ATProtoAlt: config.ATProtoConfig{Handle: "alt.bsky.social"},
	}
	got, err := pickATProtoAccount(cfg, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Handle != "main.bsky.social" {
		t.Fatalf("expected empty account to default to main, got handle=%s", got.Handle)
	}
}

func TestPickATProtoAccount_RejectsUnknown(t *testing.T) {
	cfg := &config.Config{
		ATProto:    config.ATProtoConfig{Handle: "main.bsky.social"},
		ATProtoAlt: config.ATProtoConfig{Handle: "alt.bsky.social"},
	}
	for _, val := range []string{"unknown", "garbage", "altt"} {
		if _, err := pickATProtoAccount(cfg, val); err == nil {
			t.Fatalf("account=%q: expected error", val)
		}
	}
}

func TestATProtoPost_AltAccount(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/xrpc/com.atproto.server.createSession", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Identifier string `json:"identifier"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		_ = json.NewEncoder(w).Encode(map[string]string{
			"accessJwt": "tok", "did": "did:plc:alt", "handle": body.Identifier,
		})
	})
	mux.HandleFunc("/xrpc/com.atproto.repo.createRecord", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]string{"uri": "at://alt/post/1", "cid": "cid1"})
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	cfg := &config.Config{
		ATProto:    config.ATProtoConfig{Handle: "main.bsky.social", AppPassword: "pw-main", PDSURL: srv.URL},
		ATProtoAlt: config.ATProtoConfig{Handle: "alt.art", AppPassword: "pw-alt", PDSURL: srv.URL},
	}
	tool := ATProtoPost(cfg)
	args, _ := json.Marshal(map[string]string{"text": "hello from alt", "account": "alt"})
	result, err := tool.Exec(context.Background(), emptyCallCtx(), args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.OK {
		t.Fatalf("expected OK, got: %s", result.Output)
	}
	if !strings.Contains(result.Output, "at://alt/post/1") {
		t.Errorf("expected alt URI in output, got: %s", result.Output)
	}
}
