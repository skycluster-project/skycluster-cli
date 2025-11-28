package xinstance

import (
	"github.com/spf13/cobra"
)

// debug controls debug output. Tests or a caller can set this to true.
var debug bool

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

// SetDebug sets package-level debug flag after CLI flags are parsed.
func SetDebug(d bool) {
	debug = d
}
