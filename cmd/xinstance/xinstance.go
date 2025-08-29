package xinstance

import (
	"github.com/spf13/cobra"
)

func init() {
	xInstanceCmd.AddCommand(xInstnaceListCmd)
	// xInstanceCmd.AddCommand(flavor.GetFlavorCmd())
	// xInstanceCmd.AddCommand(image.GetImageCmd())
	// xInstanceCmd.AddCommand(xInstanceDeleteCmd)
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
