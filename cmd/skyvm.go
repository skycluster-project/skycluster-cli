package cmd

import (
	"context"
	"fmt"
	"log"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/tools/clientcmd"
)

func init() {
	skyVMCmd.AddCommand(skyVMListCmd)
}

var skyVMCmd = &cobra.Command{
	Use:   "skyvm commands",
	Short: "SkyVM commands",
	// 	Long: `Overlay commands`,
	// Args: cobra.MinimumNArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Println("Print: " + strings.Join(args, " "))
	},
}

var skyVMListCmd = &cobra.Command{
	Use:   "list",
	Short: "List SkyVMs",
	Run: func(cmd *cobra.Command, args []string) {
		listSkyVMs()
	},
}

func listSkyVMs() {

	kconfig := viper.GetStringMapString("kubeconfig")
	kubeconfig := kconfig["sky-manager"]
	// check if the file exists
	if _, err := os.Stat(kubeconfig); os.IsNotExist(err) {
		log.Fatalf("Kubeconfig file not found in the config: %v", err)
		return
	}

	config, err := clientcmd.BuildConfigFromFlags("", kubeconfig)
	if err != nil {
		log.Fatalf("Error building kubeconfig: %v", err)
	}

	dynamicClient, err := dynamic.NewForConfig(config)
	if err != nil {
		log.Fatalf("Error creating dynamic client: %v", err)
	}

	gvr := schema.GroupVersionResource{
		Group:    "xrds.skycluster.io",
		Version:  "v1alpha1", // Replace with actual version
		Resource: "skyvms",
	}

	ns := "skytest"
	resources, err := dynamicClient.Resource(gvr).Namespace(ns).List(context.Background(), metav1.ListOptions{
		LabelSelector: "skycluster.io/managed-by=skycluster",
	})
	if err != nil {
		log.Fatalf("Error listing resources: %v", err)
	}

	for _, resource := range resources.Items {
		stat, found, err := unstructured.NestedMap(resource.Object, "status", "network")
		if err != nil || !found {
			log.Fatalf("spec.status not found: %v", err)
		}
		fmt.Printf("Resource: %s\t%s\n", resource.GetName(), stat["privateIpAddress"])
	}
}
