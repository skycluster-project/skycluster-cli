package profile

import (
	"github.com/spf13/cobra"
)

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
