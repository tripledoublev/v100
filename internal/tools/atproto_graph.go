package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/tripledoublev/v100/internal/config"
	"golang.org/x/sync/errgroup"
)

type sampledFollow struct {
	DID     string
	Handle  string
	Name    string
	Follows map[string]string
}

type communityCluster struct {
	Members []sampledFollow
	Shared  map[string]int
}

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
func (t *atprotoGetFollowsTool) Effects() ToolEffects     { return ToolEffects{NeedsNetwork: true} }

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
func (t *atprotoGetFollowersTool) Effects() ToolEffects     { return ToolEffects{NeedsNetwork: true} }

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
func (t *atprotoGetProfileTool) Effects() ToolEffects     { return ToolEffects{NeedsNetwork: true} }

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
func ATProtoGraphExplorer(cfg *config.Config) Tool {
	return &atprotoGraphExplorerTool{cfg: cfg.ATProto}
}

func (t *atprotoGraphExplorerTool) Name() string { return "atproto_graph_explorer" }
func (t *atprotoGraphExplorerTool) Description() string {
	return "Explore and map a Bluesky user's social graph. Samples the accounts they follow, fetches who those accounts follow, and surfaces the most-connected people in their 2nd-degree network. Use this when asked to graph someone, explore their network, find follow suggestions, or analyze social connections."
}
func (t *atprotoGraphExplorerTool) DangerLevel() DangerLevel { return Safe }
func (t *atprotoGraphExplorerTool) Effects() ToolEffects     { return ToolEffects{NeedsNetwork: true} }

func (t *atprotoGraphExplorerTool) InputSchema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"actor": {"type": "string", "description": "Handle or DID of the user whose graph to explore. Defaults to the authenticated user."},
			"sample_size": {"type": "integer", "description": "Number of follows to sample (default 10, max 25)."},
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
		Actor        string `json:"actor"`
		SampleSize   int    `json:"sample_size"`
		FollowsLimit int    `json:"follows_limit"`
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

	// 1. Determine seed actor (defaults to authenticated user)
	seedActor := in.Actor
	var seedDID string
	if seedActor == "" {
		seedActor = cli.session.DID
		seedDID = cli.session.DID
	} else if strings.HasPrefix(seedActor, "did:") {
		seedDID = seedActor
	} else {
		profileData, err := cli.xrpcGet("app.bsky.actor.getProfile", url.Values{
			"actor": {seedActor},
		})
		if err != nil {
			return ToolResult{OK: false, Output: "failed to resolve actor: " + err.Error()}, nil
		}
		var profile struct {
			DID string `json:"did"`
		}
		if err := json.Unmarshal(profileData, &profile); err != nil {
			return ToolResult{OK: false, Output: "failed to parse actor profile: " + err.Error()}, nil
		}
		if profile.DID == "" {
			return ToolResult{OK: false, Output: "failed to resolve actor DID"}, nil
		}
		seedDID = profile.DID
	}

	// 2. Get seed actor's follows as the exploration pool
	seedData, err := cli.xrpcGet("app.bsky.graph.getFollows", url.Values{
		"actor": {seedActor},
		"limit": {"100"},
	})
	if err != nil {
		return ToolResult{OK: false, Output: "failed to get my follows: " + err.Error()}, nil
	}

	var seedResp struct {
		Follows []struct {
			DID    string `json:"did"`
			Handle string `json:"handle"`
		} `json:"follows"`
	}
	if err := json.Unmarshal(seedData, &seedResp); err != nil {
		return ToolResult{OK: false, Output: "failed to parse my follows: " + err.Error()}, nil
	}

	// When exploring own graph, filter out already-followed accounts.
	// When exploring another user's graph, show all 2nd-degree connections.
	alreadyFollowed := make(map[string]bool)
	alreadyFollowed[seedDID] = true
	if in.Actor == "" {
		for _, f := range seedResp.Follows {
			alreadyFollowed[f.DID] = true
		}
	}

	// 2. Sample N of my follows and see who they follow
	suggestions := make(map[string]int)
	profileInfo := make(map[string]string) // DID -> handle

	count := 0
	for _, f := range seedResp.Follows {
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
	fmt.Fprintf(&sb, "Found %d suggestions from sampling %d of %s's follows:\n\n", len(sorted), count, seedActor)

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

// ---------------------------------------------------------------------------
// atproto_community_detect — cluster follows into sub-communities
// ---------------------------------------------------------------------------

type atprotoCommunityDetectTool struct{ cfg config.ATProtoConfig }

// ATProtoCommunityDetect returns the atproto_community_detect tool.
func ATProtoCommunityDetect(cfg *config.Config) Tool {
	return &atprotoCommunityDetectTool{cfg: cfg.ATProto}
}

func (t *atprotoCommunityDetectTool) Name() string { return "atproto_community_detect" }
func (t *atprotoCommunityDetectTool) Description() string {
	return "Cluster a Bluesky user's follows into sub-communities by sampling who those follows also follow. Use this to identify social circles, topic neighborhoods, and bridge accounts."
}
func (t *atprotoCommunityDetectTool) DangerLevel() DangerLevel { return Safe }
func (t *atprotoCommunityDetectTool) Effects() ToolEffects     { return ToolEffects{NeedsNetwork: true} }

func (t *atprotoCommunityDetectTool) InputSchema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"actor": {"type": "string", "description": "Handle or DID of the user whose follows should be clustered. Defaults to the authenticated user."},
			"sample_size": {"type": "integer", "description": "Number of follows to sample for clustering (default 20, max 50)."},
			"follows_limit": {"type": "integer", "description": "Number of follows to fetch per sampled account (default 50, max 100)."},
			"min_shared": {"type": "integer", "description": "Minimum shared followed accounts needed to join an existing cluster (default 2)."}
		}
	}`)
}

func (t *atprotoCommunityDetectTool) OutputSchema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"ok":     {"type": "boolean"},
			"output": {"type": "string"}
		}
	}`)
}

func (t *atprotoCommunityDetectTool) Exec(ctx context.Context, _ ToolCallContext, args json.RawMessage) (ToolResult, error) {
	var in struct {
		Actor        string `json:"actor"`
		SampleSize   int    `json:"sample_size"`
		FollowsLimit int    `json:"follows_limit"`
		MinShared    int    `json:"min_shared"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return ToolResult{OK: false, Output: "invalid args: " + err.Error()}, nil
	}
	if in.SampleSize <= 0 {
		in.SampleSize = 20
	}
	if in.SampleSize > 50 {
		in.SampleSize = 50
	}
	if in.FollowsLimit <= 0 {
		in.FollowsLimit = 50
	}
	if in.FollowsLimit > 100 {
		in.FollowsLimit = 100
	}
	if in.MinShared <= 0 {
		in.MinShared = 2
	}

	cli := newATProtoClient(t.cfg)
	if err := cli.login(); err != nil {
		return ToolResult{OK: false, Output: err.Error()}, nil
	}

	actor := strings.TrimSpace(in.Actor)
	if actor == "" {
		actor = cli.session.DID
	}

	seedData, err := cli.xrpcGet("app.bsky.graph.getFollows", url.Values{
		"actor": {actor},
		"limit": {"100"},
	})
	if err != nil {
		return ToolResult{OK: false, Output: "failed to get follows: " + err.Error()}, nil
	}

	var seedResp struct {
		Follows []struct {
			DID         string `json:"did"`
			Handle      string `json:"handle"`
			DisplayName string `json:"displayName"`
		} `json:"follows"`
	}
	if err := json.Unmarshal(seedData, &seedResp); err != nil {
		return ToolResult{OK: false, Output: "failed to parse follows: " + err.Error()}, nil
	}
	if len(seedResp.Follows) == 0 {
		return ToolResult{OK: true, Output: "no follows found to cluster."}, nil
	}

	sampleLimit := minInt(len(seedResp.Follows), in.SampleSize)
	sampledByIndex := make([]sampledFollow, sampleLimit)
	keep := make([]bool, sampleLimit)
	var sampledMu sync.Mutex
	group, groupCtx := errgroup.WithContext(ctx)
	group.SetLimit(5)
	for idx := 0; idx < sampleLimit; idx++ {
		idx := idx
		follow := seedResp.Follows[idx]
		group.Go(func() error {
			if err := groupCtx.Err(); err != nil {
				return err
			}
			data, err := cli.xrpcGet("app.bsky.graph.getFollows", url.Values{
				"actor": {follow.DID},
				"limit": {fmt.Sprintf("%d", in.FollowsLimit)},
			})
			if err != nil {
				return nil
			}
			var resp struct {
				Follows []struct {
					DID         string `json:"did"`
					Handle      string `json:"handle"`
					DisplayName string `json:"displayName"`
				} `json:"follows"`
			}
			if err := json.Unmarshal(data, &resp); err != nil {
				return nil
			}
			outbound := make(map[string]string, len(resp.Follows))
			for _, f := range resp.Follows {
				label := f.DisplayName
				if label == "" {
					label = f.Handle
				}
				outbound[f.DID] = fmt.Sprintf("%s (@%s)", label, f.Handle)
			}
			name := follow.DisplayName
			if name == "" {
				name = follow.Handle
			}
			sampledMu.Lock()
			sampledByIndex[idx] = sampledFollow{
				DID:     follow.DID,
				Handle:  follow.Handle,
				Name:    name,
				Follows: outbound,
			}
			keep[idx] = true
			sampledMu.Unlock()
			return nil
		})
	}
	if err := group.Wait(); err != nil {
		return ToolResult{OK: false, Output: "failed to fetch sampled follows: " + err.Error()}, nil
	}
	sampled := make([]sampledFollow, 0, sampleLimit)
	for idx, item := range sampledByIndex {
		if keep[idx] {
			sampled = append(sampled, item)
		}
	}

	if len(sampled) == 0 {
		return ToolResult{OK: true, Output: "no sampled follows returned enough graph data to cluster."}, nil
	}

	var clusters []communityCluster
	for _, candidate := range sampled {
		bestIdx := -1
		bestShared := 0
		for idx := range clusters {
			shared := countSharedFollows(candidate.Follows, clusters[idx].Shared)
			if shared > bestShared {
				bestShared = shared
				bestIdx = idx
			}
		}
		if bestIdx == -1 || bestShared < in.MinShared {
			clusters = append(clusters, communityCluster{
				Members: []sampledFollow{candidate},
				Shared:  countFollowOccurrences(candidate.Follows),
			})
			continue
		}
		clusters[bestIdx].Members = append(clusters[bestIdx].Members, candidate)
		for did := range candidate.Follows {
			clusters[bestIdx].Shared[did]++
		}
	}

	sort.Slice(clusters, func(i, j int) bool {
		if len(clusters[i].Members) == len(clusters[j].Members) {
			return len(clusters[i].Shared) > len(clusters[j].Shared)
		}
		return len(clusters[i].Members) > len(clusters[j].Members)
	})

	var sb strings.Builder
	fmt.Fprintf(&sb, "Clustered %d follows for %s into %d communities.\n\n", len(sampled), actor, len(clusters))
	for idx, cluster := range clusters {
		fmt.Fprintf(&sb, "Community %d: %d accounts\n", idx+1, len(cluster.Members))
		fmt.Fprintf(&sb, "  members: %s\n", formatClusterMembers(cluster.Members, 8))
		topShared := topSharedFollowLabels(cluster, 5)
		if len(topShared) > 0 {
			fmt.Fprintf(&sb, "  shared follows: %s\n", strings.Join(topShared, ", "))
		}
	}

	return ToolResult{OK: true, Output: strings.TrimRight(sb.String(), "\n")}, nil
}

func countFollowOccurrences(follows map[string]string) map[string]int {
	out := make(map[string]int, len(follows))
	for did := range follows {
		out[did] = 1
	}
	return out
}

func countSharedFollows(follows map[string]string, cluster map[string]int) int {
	shared := 0
	for did := range follows {
		if cluster[did] > 0 {
			shared++
		}
	}
	return shared
}

func formatClusterMembers(members []sampledFollow, limit int) string {
	if len(members) == 0 {
		return ""
	}
	out := make([]string, 0, minInt(len(members), limit))
	for i, member := range members {
		if i >= limit {
			break
		}
		out = append(out, fmt.Sprintf("%s (@%s)", member.Name, member.Handle))
	}
	if len(members) > limit {
		out = append(out, fmt.Sprintf("+%d more", len(members)-limit))
	}
	return strings.Join(out, ", ")
}

func topSharedFollowLabels(cluster communityCluster, limit int) []string {
	type sharedFollow struct {
		DID   string
		Label string
		Count int
	}
	labels := make(map[string]string)
	for _, member := range cluster.Members {
		for did, label := range member.Follows {
			if labels[did] == "" {
				labels[did] = label
			}
		}
	}
	var shared []sharedFollow
	for did, count := range cluster.Shared {
		if count < 2 {
			continue
		}
		shared = append(shared, sharedFollow{DID: did, Label: labels[did], Count: count})
	}
	sort.Slice(shared, func(i, j int) bool {
		if shared[i].Count == shared[j].Count {
			return shared[i].Label < shared[j].Label
		}
		return shared[i].Count > shared[j].Count
	})
	out := make([]string, 0, minInt(len(shared), limit))
	for i, item := range shared {
		if i >= limit {
			break
		}
		out = append(out, fmt.Sprintf("%s (%d)", item.Label, item.Count))
	}
	return out
}

// ---------------------------------------------------------------------------
// atproto_follower_momentum — compare followers against a local snapshot
// ---------------------------------------------------------------------------

type atprotoFollowerMomentumTool struct{ cfg config.ATProtoConfig }

// ATProtoFollowerMomentum returns the atproto_follower_momentum tool.
func ATProtoFollowerMomentum(cfg *config.Config) Tool {
	return &atprotoFollowerMomentumTool{cfg: cfg.ATProto}
}

func (t *atprotoFollowerMomentumTool) Name() string { return "atproto_follower_momentum" }
func (t *atprotoFollowerMomentumTool) Description() string {
	return "Track Bluesky follower momentum by comparing current followers with the previous local snapshot under XDG_STATE_HOME. Reports new followers and the topics suggested by their bios."
}
func (t *atprotoFollowerMomentumTool) DangerLevel() DangerLevel { return Safe }
func (t *atprotoFollowerMomentumTool) Effects() ToolEffects {
	return ToolEffects{NeedsNetwork: true, MutatesRunState: true}
}

func (t *atprotoFollowerMomentumTool) InputSchema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"actor": {"type": "string", "description": "Handle or DID whose followers should be tracked. Defaults to the authenticated user."},
			"limit": {"type": "integer", "description": "Followers to fetch for the snapshot (default 100, max 100)."},
			"dry_run": {"type": "boolean", "description": "When true, do not update the local follower snapshot."}
		}
	}`)
}

func (t *atprotoFollowerMomentumTool) OutputSchema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"ok":     {"type": "boolean"},
			"output": {"type": "string"}
		}
	}`)
}

func (t *atprotoFollowerMomentumTool) Exec(_ context.Context, _ ToolCallContext, args json.RawMessage) (ToolResult, error) {
	var in struct {
		Actor  string `json:"actor"`
		Limit  int    `json:"limit"`
		DryRun bool   `json:"dry_run"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return ToolResult{OK: false, Output: "invalid args: " + err.Error()}, nil
	}
	if in.Limit <= 0 {
		in.Limit = 100
	}
	if in.Limit > 100 {
		in.Limit = 100
	}

	cli := newATProtoClient(t.cfg)
	if err := cli.login(); err != nil {
		return ToolResult{OK: false, Output: err.Error()}, nil
	}
	actor := strings.TrimSpace(in.Actor)
	if actor == "" {
		actor = cli.session.DID
	}

	followers, err := fetchFollowerProfiles(cli, actor, in.Limit)
	if err != nil {
		return ToolResult{OK: false, Output: err.Error()}, nil
	}
	snapshotPath, err := followerMomentumSnapshotPath(actor)
	if err != nil {
		return ToolResult{OK: false, Output: err.Error()}, nil
	}
	previous := readFollowerMomentumSnapshot(snapshotPath)
	current := make(map[string]followerMomentumProfile, len(followers))
	var newFollowers []followerMomentumProfile
	for _, follower := range followers {
		current[follower.DID] = follower
		if _, ok := previous[follower.DID]; !ok {
			newFollowers = append(newFollowers, follower)
		}
	}
	sort.Slice(newFollowers, func(i, j int) bool {
		return newFollowers[i].Handle < newFollowers[j].Handle
	})

	if !in.DryRun {
		if err := writeFollowerMomentumSnapshot(snapshotPath, current); err != nil {
			return ToolResult{OK: false, Output: err.Error()}, nil
		}
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "Follower momentum for %s\n", actor)
	fmt.Fprintf(&sb, "followers fetched: %d · previous snapshot: %d · new: %d\n", len(followers), len(previous), len(newFollowers))
	if len(newFollowers) == 0 {
		fmt.Fprintf(&sb, "No new followers since the last snapshot.")
		return ToolResult{OK: true, Output: sb.String()}, nil
	}
	fmt.Fprintf(&sb, "new follower topics: %s\n\n", strings.Join(topFollowerBioWords(newFollowers, 8), ", "))
	for idx, follower := range newFollowers {
		if idx >= 10 {
			fmt.Fprintf(&sb, "...and %d more\n", len(newFollowers)-idx)
			break
		}
		label := follower.DisplayName
		if label == "" {
			label = follower.Handle
		}
		fmt.Fprintf(&sb, "- %s (@%s)", label, follower.Handle)
		if strings.TrimSpace(follower.Description) != "" {
			fmt.Fprintf(&sb, ": %s", clipAtprotoText(follower.Description, 140))
		}
		fmt.Fprintln(&sb)
	}
	return ToolResult{OK: true, Output: strings.TrimRight(sb.String(), "\n")}, nil
}

type followerMomentumProfile struct {
	DID         string `json:"did"`
	Handle      string `json:"handle"`
	DisplayName string `json:"display_name,omitempty"`
	Description string `json:"description,omitempty"`
}

func fetchFollowerProfiles(cli *atProtoClient, actor string, limit int) ([]followerMomentumProfile, error) {
	data, err := cli.xrpcGet("app.bsky.graph.getFollowers", url.Values{
		"actor": {actor},
		"limit": {fmt.Sprintf("%d", limit)},
	})
	if err != nil {
		return nil, err
	}
	var resp struct {
		Followers []struct {
			DID         string `json:"did"`
			Handle      string `json:"handle"`
			DisplayName string `json:"displayName"`
			Description string `json:"description"`
		} `json:"followers"`
	}
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, err
	}
	out := make([]followerMomentumProfile, 0, len(resp.Followers))
	for _, follower := range resp.Followers {
		out = append(out, followerMomentumProfile{
			DID:         follower.DID,
			Handle:      follower.Handle,
			DisplayName: follower.DisplayName,
			Description: follower.Description,
		})
	}
	return out, nil
}

func followerMomentumSnapshotPath(actor string) (string, error) {
	base := os.Getenv("XDG_STATE_HOME")
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		base = filepath.Join(home, ".local", "state")
	}
	safeActor := strings.NewReplacer("/", "_", ":", "_", "@", "_").Replace(actor)
	return filepath.Join(base, "v100", "atproto_followers_"+safeActor+".json"), nil
}

func readFollowerMomentumSnapshot(path string) map[string]followerMomentumProfile {
	data, err := os.ReadFile(path)
	if err != nil {
		return map[string]followerMomentumProfile{}
	}
	var snapshot struct {
		Followers map[string]followerMomentumProfile `json:"followers"`
	}
	if err := json.Unmarshal(data, &snapshot); err != nil || snapshot.Followers == nil {
		return map[string]followerMomentumProfile{}
	}
	return snapshot.Followers
}

func writeFollowerMomentumSnapshot(path string, followers map[string]followerMomentumProfile) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(struct {
		UpdatedAt time.Time                          `json:"updated_at"`
		Followers map[string]followerMomentumProfile `json:"followers"`
	}{UpdatedAt: time.Now().UTC(), Followers: followers}, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

func topFollowerBioWords(followers []followerMomentumProfile, limit int) []string {
	posts := make([]digestPost, 0, len(followers))
	for _, follower := range followers {
		posts = append(posts, digestPost{Text: follower.Description})
	}
	return topWords(posts, limit)
}

func clipAtprotoText(s string, limit int) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) <= limit {
		return s
	}
	return s[:limit] + "..."
}
