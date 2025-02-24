package skyvm

import (
	flavor "github.com/etesami/skycluster-cli/cmd/skyvm/flavor"
	image "github.com/etesami/skycluster-cli/cmd/skyvm/image"
	"github.com/spf13/cobra"
)

func init() {
	skyVMCmd.AddCommand(skyVMListCmd)
	skyVMCmd.AddCommand(flavor.GetFlavorCmd())
	skyVMCmd.AddCommand(image.GetImageCmd())
}

var skyVMCmd = &cobra.Command{
	Use:   "skyvm commands",
	Short: "SkyVM commands",
	Run: func(cmd *cobra.Command, args []string) {
		cmd.Help()
	},
}

func GetSkyVMCmd() *cobra.Command {
	return skyVMCmd
}
