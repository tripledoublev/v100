package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"sort"
	"strings"

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

// ---------------------------------------------------------------------------
// atproto_graph_explorer — "who do my follows follow that I don't?"
// ---------------------------------------------------------------------------

type atprotoGraphExplorerTool struct{ cfg config.ATProtoConfig }

// ATProtoGraphExplorer returns the atproto_graph_explorer tool.
func ATProtoGraphExplorer(cfg *config.Config) Tool { return &atprotoGraphExplorerTool{cfg: cfg.ATProto} }

func (t *atprotoGraphExplorerTool) Name() string { return "atproto_graph_explorer" }
func (t *atprotoGraphExplorerTool) Description() string {
	return "Explore your 2nd-degree follow graph to find new people to follow. Analyzes who your follows are following and suggests the most common ones you don't already follow."
}
func (t *atprotoGraphExplorerTool) DangerLevel() DangerLevel { return Safe }
func (t *atprotoGraphExplorerTool) Effects() ToolEffects      { return ToolEffects{NeedsNetwork: true} }

func (t *atprotoGraphExplorerTool) InputSchema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"sample_size": {"type": "integer", "description": "Number of your follows to sample (default 10, max 25)."},
			"follows_limit": {"type": "integer", "description": "Number of follows to fetch per sampled account (default 20, max 100)."}
		}
	}`)
}

func (t *atprotoGraphExplorerTool) OutputSchema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"ok":     {"type": "boolean"},
			"output": {"type": "string"}
		}
	}`)
}

func (t *atprotoGraphExplorerTool) Exec(_ context.Context, _ ToolCallContext, args json.RawMessage) (ToolResult, error) {
	var in struct {
		SampleSize   int `json:"sample_size"`
		FollowsLimit int `json:"follows_limit"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return ToolResult{OK: false, Output: "invalid args: " + err.Error()}, nil
	}
	if in.SampleSize <= 0 {
		in.SampleSize = 10
	}
	if in.SampleSize > 25 {
		in.SampleSize = 25
	}
	if in.FollowsLimit <= 0 {
		in.FollowsLimit = 20
	}
	if in.FollowsLimit > 100 {
		in.FollowsLimit = 100
	}

	cli := newATProtoClient(t.cfg)
	if err := cli.login(); err != nil {
		return ToolResult{OK: false, Output: err.Error()}, nil
	}

	// 1. Get my own follows to know who NOT to suggest
	myFollowsData, err := cli.xrpcGet("app.bsky.graph.getFollows", url.Values{
		"actor": {cli.session.DID},
		"limit": {"100"},
	})
	if err != nil {
		return ToolResult{OK: false, Output: "failed to get my follows: " + err.Error()}, nil
	}

	var myFollowsResp struct {
		Follows []struct {
			DID    string `json:"did"`
			Handle string `json:"handle"`
		} `json:"follows"`
	}
	if err := json.Unmarshal(myFollowsData, &myFollowsResp); err != nil {
		return ToolResult{OK: false, Output: "failed to parse my follows: " + err.Error()}, nil
	}

	alreadyFollowed := make(map[string]bool)
	alreadyFollowed[cli.session.DID] = true // Don't suggest myself
	for _, f := range myFollowsResp.Follows {
		alreadyFollowed[f.DID] = true
	}

	// 2. Sample N of my follows and see who they follow
	suggestions := make(map[string]int)
	profileInfo := make(map[string]string) // DID -> handle

	count := 0
	for _, f := range myFollowsResp.Follows {
		if count >= in.SampleSize {
			break
		}

		followsData, err := cli.xrpcGet("app.bsky.graph.getFollows", url.Values{
			"actor": {f.DID},
			"limit": {fmt.Sprintf("%d", in.FollowsLimit)},
		})
		if err != nil {
			continue // skip failures for individual accounts
		}

		var fResp struct {
			Follows []struct {
				DID         string `json:"did"`
				Handle      string `json:"handle"`
				DisplayName string `json:"displayName"`
			} `json:"follows"`
		}
		if err := json.Unmarshal(followsData, &fResp); err != nil {
			continue
		}

		for _, candidate := range fResp.Follows {
			if alreadyFollowed[candidate.DID] {
				continue
			}
			suggestions[candidate.DID]++
			if _, ok := profileInfo[candidate.DID]; !ok {
				name := candidate.DisplayName
				if name == "" {
					name = candidate.Handle
				}
				profileInfo[candidate.DID] = fmt.Sprintf("%s (@%s)", name, candidate.Handle)
			}
		}
		count++
	}

	// 3. Sort and present top suggestions
	type suggestion struct {
		DID   string
		Count int
	}
	var sorted []suggestion
	for did, c := range suggestions {
		sorted = append(sorted, suggestion{did, c})
	}
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].Count > sorted[j].Count
	})

	if len(sorted) == 0 {
		return ToolResult{OK: true, Output: "no new suggestions found in this sample."}, nil
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "Found %d suggestions from sampling %d follows:\n\n", len(sorted), count)
	
	limit := 15
	if len(sorted) < limit {
		limit = len(sorted)
	}

	for i := 0; i < limit; i++ {
		s := sorted[i]
		fmt.Fprintf(&sb, "%d. %s — followed by %d of your sampled follows\n", 
			i+1, profileInfo[s.DID], s.Count)
	}

	return ToolResult{OK: true, Output: sb.String()}, nil
}
