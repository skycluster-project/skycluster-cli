package skyprovider

import (
	"github.com/etesami/skycluster-cli/cmd/k8s/config"
	"github.com/spf13/cobra"
)

func init() {
	skyK8SCmd.AddCommand(config.GetConfigCmd())
	// skyK8SCmd.AddCommand(skyK8SListCmd)
	// skyK8SCmd.AddCommand(skyK8SDeleteCmd)
}

var skyK8SCmd = &cobra.Command{
	Use:   "skyk8s commands",
	Short: "SkyK8S commands",
	Run: func(cmd *cobra.Command, args []string) {
		if len(args) == 0 {
			cmd.Help()
			return
		}
	},
}

func GetSkyK8SCmd() *cobra.Command {
	return skyK8SCmd
}
