package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"

	"github.com/tripledoublev/v100/internal/config"
)

// ---------------------------------------------------------------------------
// atproto_get_follows — list accounts followed by a user
// ---------------------------------------------------------------------------

type atprotoGetFollowsTool struct{ cfg config.ATProtoConfig }

// ATProtoGetFollows returns the atproto_get_follows tool.
func ATProtoGetFollows(cfg *config.Config) Tool { return &atprotoGetFollowsTool{cfg: cfg.ATProto} }

func (t *atprotoGetFollowsTool) Name() string { return "atproto_get_follows" }
func (t *atprotoGetFollowsTool) Description() string {
	return "List accounts followed by a given user (actor). Returns a list of profiles with handle, display name, and a cursor for pagination."
}
func (t *atprotoGetFollowsTool) DangerLevel() DangerLevel { return Safe }
func (t *atprotoGetFollowsTool) Effects() ToolEffects      { return ToolEffects{NeedsNetwork: true} }

func (t *atprotoGetFollowsTool) InputSchema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"required": ["actor"],
		"properties": {
			"actor":  {"type": "string", "description": "Handle or DID of the user to query."},
			"limit":  {"type": "integer", "description": "Number of items to fetch (1–100, default 50)."},
			"cursor": {"type": "string",  "description": "Pagination cursor from a previous call."}
		}
	}`)
}

func (t *atprotoGetFollowsTool) OutputSchema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"ok":      {"type": "boolean"},
			"follows": {"type": "array", "items": {"type": "object"}},
			"cursor":  {"type": "string"}
		}
	}`)
}

func (t *atprotoGetFollowsTool) Exec(_ context.Context, _ ToolCallContext, args json.RawMessage) (ToolResult, error) {
	var in struct {
		Actor  string `json:"actor"`
		Limit  int    `json:"limit"`
		Cursor string `json:"cursor"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return ToolResult{OK: false, Output: "invalid args: " + err.Error()}, nil
	}
	if in.Actor == "" {
		return ToolResult{OK: false, Output: "actor is required"}, nil
	}
	if in.Limit <= 0 {
		in.Limit = 50
	}

	cli := newATProtoClient(t.cfg)
	// getFollows is public, but we might need login if the PDS is restrictive or for higher limits.
	// For now, try authenticated if we have credentials.
	_ = cli.login() 

	params := url.Values{
		"actor": {in.Actor},
		"limit": {fmt.Sprintf("%d", in.Limit)},
	}
	if in.Cursor != "" {
		params.Set("cursor", in.Cursor)
	}
	data, err := cli.xrpcGet("app.bsky.graph.getFollows", params)
	if err != nil {
		return ToolResult{OK: false, Output: err.Error()}, nil
	}

	return ToolResult{OK: true, Output: string(data)}, nil
}

// ---------------------------------------------------------------------------
// atproto_get_followers — list accounts following a user
// ---------------------------------------------------------------------------

type atprotoGetFollowersTool struct{ cfg config.ATProtoConfig }

// ATProtoGetFollowers returns the atproto_get_followers tool.
func ATProtoGetFollowers(cfg *config.Config) Tool { return &atprotoGetFollowersTool{cfg: cfg.ATProto} }

func (t *atprotoGetFollowersTool) Name() string { return "atproto_get_followers" }
func (t *atprotoGetFollowersTool) Description() string {
	return "List accounts following a given user (actor). Returns a list of profiles and a cursor for pagination."
}
func (t *atprotoGetFollowersTool) DangerLevel() DangerLevel { return Safe }
func (t *atprotoGetFollowersTool) Effects() ToolEffects      { return ToolEffects{NeedsNetwork: true} }

func (t *atprotoGetFollowersTool) InputSchema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"required": ["actor"],
		"properties": {
			"actor":  {"type": "string", "description": "Handle or DID of the user to query."},
			"limit":  {"type": "integer", "description": "Number of items to fetch (1–100, default 50)."},
			"cursor": {"type": "string",  "description": "Pagination cursor from a previous call."}
		}
	}`)
}

func (t *atprotoGetFollowersTool) OutputSchema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"ok":        {"type": "boolean"},
			"followers": {"type": "array", "items": {"type": "object"}},
			"cursor":    {"type": "string"}
		}
	}`)
}

func (t *atprotoGetFollowersTool) Exec(_ context.Context, _ ToolCallContext, args json.RawMessage) (ToolResult, error) {
	var in struct {
		Actor  string `json:"actor"`
		Limit  int    `json:"limit"`
		Cursor string `json:"cursor"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return ToolResult{OK: false, Output: "invalid args: " + err.Error()}, nil
	}
	if in.Actor == "" {
		return ToolResult{OK: false, Output: "actor is required"}, nil
	}
	if in.Limit <= 0 {
		in.Limit = 50
	}

	cli := newATProtoClient(t.cfg)
	_ = cli.login()

	params := url.Values{
		"actor": {in.Actor},
		"limit": {fmt.Sprintf("%d", in.Limit)},
	}
	if in.Cursor != "" {
		params.Set("cursor", in.Cursor)
	}
	data, err := cli.xrpcGet("app.bsky.graph.getFollowers", params)
	if err != nil {
		return ToolResult{OK: false, Output: err.Error()}, nil
	}

	return ToolResult{OK: true, Output: string(data)}, nil
}

// ---------------------------------------------------------------------------
// atproto_get_profile — get detailed profile for a user
// ---------------------------------------------------------------------------

type atprotoGetProfileTool struct{ cfg config.ATProtoConfig }

// ATProtoGetProfile returns the atproto_get_profile tool.
func ATProtoGetProfile(cfg *config.Config) Tool { return &atprotoGetProfileTool{cfg: cfg.ATProto} }

func (t *atprotoGetProfileTool) Name() string { return "atproto_get_profile" }
func (t *atprotoGetProfileTool) Description() string {
	return "Get the detailed profile of a Bluesky user (actor) including bio, follower/following counts, and association data."
}
func (t *atprotoGetProfileTool) DangerLevel() DangerLevel { return Safe }
func (t *atprotoGetProfileTool) Effects() ToolEffects      { return ToolEffects{NeedsNetwork: true} }

func (t *atprotoGetProfileTool) InputSchema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"required": ["actor"],
		"properties": {
			"actor": {"type": "string", "description": "Handle or DID of the user to query."}
		}
	}`)
}

func (t *atprotoGetProfileTool) OutputSchema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"ok":      {"type": "boolean"},
			"profile": {"type": "object"}
		}
	}`)
}

func (t *atprotoGetProfileTool) Exec(_ context.Context, _ ToolCallContext, args json.RawMessage) (ToolResult, error) {
	var in struct {
		Actor string `json:"actor"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return ToolResult{OK: false, Output: "invalid args: " + err.Error()}, nil
	}
	if in.Actor == "" {
		return ToolResult{OK: false, Output: "actor is required"}, nil
	}

	cli := newATProtoClient(t.cfg)
	_ = cli.login()

	params := url.Values{"actor": {in.Actor}}
	data, err := cli.xrpcGet("app.bsky.actor.getProfile", params)
	if err != nil {
		return ToolResult{OK: false, Output: err.Error()}, nil
	}

	return ToolResult{OK: true, Output: string(data)}, nil
}
