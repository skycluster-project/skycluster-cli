package xkube

import (
	"github.com/spf13/cobra"
)

func init() {
	xKubeCmd.AddCommand(xKubeListCmd)
	xKubeCmd.AddCommand(configShowCmd)
}

var xKubeCmd = &cobra.Command{
	Use:   "xkube",
	Short: "XKube commands",
	Run: func(cmd *cobra.Command, args []string) {
		if len(args) == 0 {
			cmd.Help()
			return
		}
	},
}

func GetXKubeCmd() *cobra.Command {
	return xKubeCmd
}
