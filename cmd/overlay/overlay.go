package overlay

import (
	"fmt"
	"log"
	"strconv"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"tailscale.com/tsnet"
)

func init() {
	overlayCmd.AddCommand(overlayJoinCmd)
}

var overlayCmd = &cobra.Command{
	Use:   "overlay commands",
	Short: "Overlay commands",
	// 	Long: `Overlay commands`,
	// Args: cobra.MinimumNArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Println("The overlay sub command is not yet working: " + strings.Join(args, " "))
	},
}

var overlayJoinCmd = &cobra.Command{
	Use:   "join",
	Short: "Join overlay",
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Println("Joining overlay: " + strings.Join(args, " "))

	},
}

func overlayJoin() {
	overlayCfg := viper.GetStringMapString("overlay")
	fmt.Println("Joining overlay")
	overlayPort, _ := strconv.Atoi(overlayCfg["port"])
	server := &tsnet.Server{
		Hostname:   overlayCfg["hostname"],
		ControlURL: overlayCfg["host"],
		Port:       uint16(overlayPort),
		AuthKey:    overlayCfg["authkey"],
	}

	// Start the Tailscale server
	if err := server.Start(); err != nil {
		log.Fatalf("Failed to start tsnet server: %v", err)
	}
	defer server.Close()

	log.Println("Tailscale node started and connected")

}

func GetOverlayCmd() *cobra.Command {
	return overlayCmd
}
