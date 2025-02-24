package cmd

// import (
// 	"context"
// 	"fmt"
// 	"log"
// 	"strconv"
// 	"strings"

// 	internals "github.com/etesami/skycluster-cli/internal"
// 	"github.com/etesami/skycluster-cli/internal/utils"
// 	"github.com/spf13/cobra"
// 	"github.com/spf13/viper"
// 	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
// 	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
// 	"k8s.io/apimachinery/pkg/runtime/schema"
// 	clientset "k8s.io/client-go/kubernetes"
// 	"k8s.io/client-go/tools/clientcmd"
// )

// func init() {
// 	skyVMCmd.AddCommand(skyVMListCmd)
// 	skyVMCmd.AddCommand(flavorListCmd)
// 	var providerNames []string
// 	flavorListCmd.PersistentFlags().StringSliceVarP(&providerNames, "provider-name", "n", nil, "Provider Names")
// 	flavorListCmd.SetHelpTemplate(`Usage:
// 	skyvm flavor commands

// Available Commands:
// 	list		List flavors
// `)
// 	// flavorListCmd.SetUsageTemplate(flavorListCmd.HelpTemplate())
// }

// var skyVMCmd = &cobra.Command{
// 	Use:   "skyvm commands",
// 	Short: "SkyVM commands",
// 	// 	Long: `Overlay commands`,
// 	// Args: cobra.MinimumNArgs(1),
// 	Run: func(cmd *cobra.Command, args []string) {
// 		cmd.Help()
// 	},
// }

// var skyVMListCmd = &cobra.Command{
// 	Use:   "list",
// 	Short: "List SkyVMs",
// 	Run: func(cmd *cobra.Command, args []string) {
// 		listSkyVMs()
// 	},
// }

// var flavorListCmd = &cobra.Command{
// 	Use:       "flavor",
// 	Short:     "List Flavors",
// 	ValidArgs: []string{"list"},
// 	Args:      cobra.MinimumNArgs(1),
// 	Run: func(cmd *cobra.Command, args []string) {
// 		if args[0] == "list" {
// 			if len(args) == 1 {
// 				listFlavors()
// 			} else {

// 			}
// 		}
// 	},
// }

// func listSkyVMs() {

// 	kconfig := viper.GetStringMapString("kubeconfig")
// 	kubeconfig := kconfig["sky-manager"]
// 	dynamicClient, err := utils.GetDynamicClient(kubeconfig)
// 	if err != nil {
// 		log.Fatalf("Error getting dynamic client: %v", err)
// 		return
// 	}

// 	gvr := schema.GroupVersionResource{
// 		Group:    "xrds.skycluster.io",
// 		Version:  "v1alpha1", // Replace with actual version
// 		Resource: "skyvms",
// 	}

// 	ns := "skytest"
// 	resources, err := dynamicClient.Resource(gvr).Namespace(ns).List(context.Background(), metav1.ListOptions{
// 		LabelSelector: "skycluster.io/managed-by=skycluster",
// 	})
// 	if err != nil {
// 		log.Fatalf("Error listing resources: %v", err)
// 	}

// 	for _, resource := range resources.Items {
// 		stat, found, err := unstructured.NestedMap(resource.Object, "status", "network")
// 		if err != nil || !found {
// 			log.Fatalf("spec.status not found: %v", err)
// 		}
// 		fmt.Printf("Resource: %s\t%s\n", resource.GetName(), stat["privateIpAddress"])
// 	}
// }

// func listFlavors() {
// 	kconfig := viper.GetStringMapString("kubeconfig")
// 	kubeconfig := kconfig["sky-manager"]
// 	config, err := clientcmd.BuildConfigFromFlags("", kubeconfig)
// 	if err != nil {
// 		log.Fatalf("Error building config: %v", err)
// 		return
// 	}
// 	clientset, err := clientset.NewForConfig(config)
// 	if err != nil {
// 		log.Fatalf("Error getting clientset: %v", err)
// 		return
// 	}
// 	confgis, err := clientset.CoreV1().ConfigMaps(internals.SkyClusterName).List(context.Background(), metav1.ListOptions{
// 		LabelSelector: "skycluster.io/managed-by=skycluster, skycluster.io/config-type=provider-mappings",
// 	})

// 	if err != nil {
// 		log.Fatalf("Error listing configmaps: %v", err)
// 	}
// 	// Iterate over the configmaps and print the names
// 	flavorList := make(map[string][]string, 0)
// 	for _, cm := range confgis.Items {
// 		fList := make([]string, 0)
// 		pName := cm.Labels["skycluster.io/provider-name"]
// 		pRegion := cm.Labels["skycluster.io/provider-region"]
// 		pZone := cm.Labels["skycluster.io/provider-zone"]
// 		pID := pName + "_" + pRegion + "_" + pZone
// 		for d, _ := range cm.Data {
// 			if strings.Contains(d, "flavor") {
// 				fList = append(fList, d)
// 			}
// 		}
// 		if len(fList) > 0 {
// 			flavorList[pID] = fList
// 		}
// 	}
// 	availableFlavors := utils.IntersectionOfMapValues(flavorList, utils.GetMapStringKeys(flavorList))
// 	if len(availableFlavors) == 0 {
// 		fmt.Println("No flavors available")
// 	} else {
// 		fmt.Println("Available flavors across all providers:")
// 	}
// 	for i, f := range availableFlavors {
// 		fmt.Print(`
// 	` + strconv.Itoa(i+1) + `. ` + f)
// 	}
// 	fmt.Println()

// }
