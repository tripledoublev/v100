package main

import (
	"testing"

	"github.com/tripledoublev/v100/internal/config"
	"github.com/tripledoublev/v100/internal/gateway"
)

func TestGatewayVoiceConfigUsesProfileOverride(t *testing.T) {
	on := true
	off := false

	got := gatewayVoiceConfig(false, gateway.VoiceReplyModeAudioText, gateway.ProfileRuntime{
		OK: true,
		Profile: config.GatewayProfile{
			VoiceReplies:   &on,
			VoiceReplyMode: gateway.VoiceReplyModeAudio,
		},
	})
	if !got.Enabled || got.Mode != gateway.VoiceReplyModeAudio {
		t.Fatalf("profile enabled override = %#v", got)
	}

	got = gatewayVoiceConfig(true, gateway.VoiceReplyModeAudio, gateway.ProfileRuntime{
		OK: true,
		Profile: config.GatewayProfile{
			VoiceReplies: &off,
		},
	})
	if got.Enabled || got.Mode != gateway.VoiceReplyModeAudio {
		t.Fatalf("profile disabled override = %#v", got)
	}

	got = gatewayVoiceConfig(true, gateway.VoiceReplyModeAudioText, gateway.ProfileRuntime{})
	if !got.Enabled || got.Mode != gateway.VoiceReplyModeAudioText {
		t.Fatalf("gateway default = %#v", got)
	}
}
