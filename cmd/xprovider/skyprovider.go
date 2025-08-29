package xprovider

import (
	"github.com/spf13/cobra"
)

func init() {
	xProviderCmd.AddCommand(xProviderListCmd)
	xProviderCmd.AddCommand(xProviderDeleteCmd)
}

var xProviderCmd = &cobra.Command{
	Use:   "xprovider commands",
	Short: "XProvider commands",
	Run: func(cmd *cobra.Command, args []string) {
		if len(args) == 0 {
			cmd.Help()
			return
		}
	},
}

func GetXProviderCmd() *cobra.Command {
	return xProviderCmd
}
