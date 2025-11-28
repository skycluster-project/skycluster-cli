package profile

import (
	"github.com/spf13/cobra"
)

var debug bool

func init() {
	profileCmd.AddCommand(profileListCmd)
	profileCmd.AddCommand(profileCreateCmd)
	profileCmd.AddCommand(profileDeleteCmd)
}

var profileCmd = &cobra.Command{
	Use:   "profile",
	Short: "Profile commands",
	Run: func(cmd *cobra.Command, args []string) {
		if len(args) == 0 {
			cmd.Help()
			return
		}
	},
}

func GetProfileCmd() *cobra.Command {
	return profileCmd
}

// SetDebug sets package-level debug flag after CLI flags are parsed.
func SetDebug(d bool) {
	debug = d
}