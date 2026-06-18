package main

import (
	"fmt"

	"github.com/spf13/cobra"
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
	return cmd
}
