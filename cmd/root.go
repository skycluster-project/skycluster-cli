package cmd

import (
	"fmt"
	"os"

	ks "github.com/etesami/skycluster-cli/cmd/k8s"
	ovl "github.com/etesami/skycluster-cli/cmd/overlay"
	sp "github.com/etesami/skycluster-cli/cmd/skyprovider"
	sv "github.com/etesami/skycluster-cli/cmd/skyvm"

	homedir "github.com/mitchellh/go-homedir"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

var cfgFile string
var kubeconfig string
var ns string

var rootCmd = &cobra.Command{
	Use:   "[args]",
	Short: "SkyCluster Cli is a tool to interact with SkyCluster API",
	Args:  cobra.ArbitraryArgs,
	Run: func(cmd *cobra.Command, args []string) {
		cmd.Help()
	},
}

func Execute() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
}

func init() {
	cobra.OnInitialize(initConfig)
	rootCmd.PersistentFlags().StringVarP(&cfgFile, "config", "c", "", "config file")
	rootCmd.PersistentFlags().StringVarP(&ns, "namespace", "n", "", "namespace")
	rootCmd.CompletionOptions.DisableDefaultCmd = true
	rootCmd.AddCommand(ovl.GetOverlayCmd())
	rootCmd.AddCommand(sp.GetSkyProviderCmd())
	rootCmd.AddCommand(sv.GetSkyVMCmd())
	rootCmd.AddCommand(ks.GetSkyK8SCmd())
}

func initConfig() {
	// Don't forget to read config either from cfgFile or from home directory!
	if cfgFile != "" {
		// Use config file from the flag.
		viper.SetConfigFile(cfgFile)
	} else {
		// Find home directory.
		home, err := homedir.Dir()
		if err != nil {
			fmt.Println(err)
			os.Exit(1)
		}

		// Search config in home directory with name ".skycluster" (without extension).
		viper.AddConfigPath(home)
		viper.SetConfigName(".skycluster")
		viper.SetConfigType("yaml")
	}

	if err := viper.ReadInConfig(); err != nil {
		fmt.Println("Can't read config:", err)
		os.Exit(1)
	}
}
