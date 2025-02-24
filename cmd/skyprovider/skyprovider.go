package skyprovider

import (
	"github.com/spf13/cobra"
)

func init() {
	skyProviderCmd.AddCommand(skyProviderListCmd)
}

var skyProviderCmd = &cobra.Command{
	Use:   "skyprovider commands",
	Short: "SkyProvider commands",
	Run: func(cmd *cobra.Command, args []string) {
		if len(args) == 0 {
			cmd.Help()
			return
		}
	},
}

func GetSkyProviderCmd() *cobra.Command {
	return skyProviderCmd
}
