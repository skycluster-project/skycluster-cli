package xinstance

import (
	"github.com/spf13/cobra"
)

func init() {
	// xInstanceCmd.AddCommand(flavor.GetFlavorCmd())
	// xInstanceCmd.AddCommand(image.GetImageCmd())
	xInstanceCmd.AddCommand(xInstanceListCmd)
	xInstanceCmd.AddCommand(xInstanceCreateCmd)
	xInstanceCmd.AddCommand(xInstanceDeleteCmd)
}

var xInstanceCmd = &cobra.Command{
	Use:   "xinstance",
	Short: "XInstance commands",
	Run: func(cmd *cobra.Command, args []string) {
		cmd.Help()
	},
}

func GetXInstanceCmd() *cobra.Command {
	return xInstanceCmd
}
