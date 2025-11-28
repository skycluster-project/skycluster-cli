package xprovider

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

var debug bool

func init() {
	xProviderCmd.AddCommand(xProviderListCmd)
	xProviderCmd.AddCommand(xProviderCreateCmd)
	xProviderCmd.AddCommand(xProviderDeleteCmd)
	xProviderCmd.AddCommand(xProviderSSHCmd)
}

var xProviderCmd = &cobra.Command{
	Use:   "xprovider",
	Short: "XProvider commands",
	Run: func(cmd *cobra.Command, args []string) {
		if len(args) == 0 {
			cmd.Help()
			return
		}
	},
}

// debugf prints debug messages to stderr when debug is enabled.
func debugf(format string, args ...interface{}) {
	if debug {
		_, _ = fmt.Fprintf(os.Stderr, "DEBUG: "+format+"\n", args...)
	}
}

func GetXProviderCmd() *cobra.Command {
	return xProviderCmd
}

// SetDebug sets package-level debug flag after CLI flags are parsed.
func SetDebug(d bool) {
	debug = d
}
