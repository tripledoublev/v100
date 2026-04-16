package tools

import (
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
}
