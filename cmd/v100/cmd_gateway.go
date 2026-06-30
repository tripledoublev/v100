package main

import (
	"fmt"

	"github.com/spf13/cobra"
	"github.com/tripledoublev/v100/internal/gateway"
)

func gatewayCmd(cfgPath *string) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "gateway",
		Short: "Run messaging gateways that drive v100 over ACP",
		RunE: func(cmd *cobra.Command, args []string) error {
			return fmt.Errorf("use a subcommand, e.g. `gateway telegram`")
		},
	}

	cmd.AddCommand(gatewayTelegramCmd(cfgPath))
	cmd.AddCommand(gatewaySignalCmd(cfgPath))
	return cmd
}

func gatewayVoiceConfig(enabled bool, mode string, runtime gateway.ProfileRuntime) gateway.VoiceConfig {
	cfg := gateway.VoiceConfig{Enabled: enabled, Mode: mode}
	if runtime.OK {
		if runtime.Profile.VoiceReplies != nil {
			cfg.Enabled = *runtime.Profile.VoiceReplies
		}
		if runtime.Profile.VoiceReplyMode != "" {
			cfg.Mode = runtime.Profile.VoiceReplyMode
		}
	}
	return cfg
}
