package config

import (
	"context"
	"fmt"
	"log"

	// "maps"
	// "os"
	// "strings"
	// "text/tabwriter"

	// vars "github.com/etesami/skycluster-cli/internal"
	utils "github.com/etesami/skycluster-cli/internal/utils"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	// "k8s.io/client-go/kubernetes"
)

func init() {
	// configCmd.AddCommand(configListCmd)
	configCmd.AddCommand(configShowCmd)
}

var configCmd = &cobra.Command{
	Use:   "config commands",
	Short: "Config commands",
	Run: func(cmd *cobra.Command, args []string) {
		cmd.Help()
	},
}

var configShowCmd = &cobra.Command{
	Use:   "show",
	Short: "Show current kubeconfig of the overlay k8s",
	Run: func(cmd *cobra.Command, args []string) {
		showConfigs()
	},
}

// var configListCmd = &cobra.Command{
// 	Use:   "list",
// 	Short: "List avaialble overlay k8s configs",
// 	Run: func(cmd *cobra.Command, args []string) {
// 		listConfigs()
// 	},
// }

func showConfigs() {
	kconfig := viper.GetStringMapString("kubeconfig")
	kubeconfig := kconfig["sky-manager"]

	dynClient, err := utils.GetDynamicClient(kubeconfig)
	if err != nil {
		log.Fatalf("Error getting dynamic client: %v", err)
		return
	}

	// Get the unstructured object
	objList, err := dynClient.Resource(schema.GroupVersionResource{
		Group:    "xrds.skycluster.io",
		Version:  "v1alpha1",
		Resource: "xk8sclusters",
	}).List(context.Background(), metav1.ListOptions{})
	if err != nil {
		log.Fatalf("Error fetching object: %v", err)
	}
	for _, obj := range objList.Items {
		k3sConfig, err := utils.TraverseMapString(obj.Object, "status", "k3s", "kubeconfig")
		if err != nil {
			log.Fatalf("Error fetching kubeconfig: %v", err)
		}
		fmt.Printf("%v\n", k3sConfig)
		// At this time I expect to only have one objects
		break
	}
}

func GetConfigCmd() *cobra.Command {
	return configCmd
}
