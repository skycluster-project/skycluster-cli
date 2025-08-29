package xinstance

import (
	"bufio"
	"log"
	"strings"

	"context"
	"fmt"
	"os"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/etesami/skycluster-cli/internal/utils"
	"github.com/spf13/viper"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
)

var deleteAll *bool
var pNames []string
var vmNames []string

func init() {
	skyVMDeleteCmd.PersistentFlags().StringSliceVarP(&pNames, "provider-name", "p", nil, "Provider Names, seperated by comma")
	deleteAll = skyVMDeleteCmd.PersistentFlags().BoolP("all", "a", false, "Delete all SkyVMs")
}

var skyVMDeleteCmd = &cobra.Command{
	Use:   "delete",
	Short: "Delete SkyVMs",
	Run: func(cmd *cobra.Command, args []string) {
		ns, err := cmd.Root().PersistentFlags().GetString("namespace")
		if err != nil {
			log.Fatalf("error getting namespace: %v", err)
			return
		}
		if *deleteAll {
			listAllSkyVMsAndConfirm(ns)
			return
		}
		if len(pNames) > 0 {
			listSkyVMsByProviderNamesAndConfirm(ns, pNames)
			return
		}
		cmd.Help()
	},
}

func listSkyVMsByProviderNamesAndConfirm(ns string, pNames []string) {
	kconfig := viper.GetStringMapString("kubeconfig")
	kubeconfig := kconfig["sky-manager"]
	dynamicClient, err := utils.GetDynamicClient(kubeconfig)
	if err != nil {
		log.Fatalf("Error getting dynamic client: %v", err)
		return
	}

	vmList := make([]unstructured.Unstructured, 0)
	baseFilters := "skycluster.io/managed-by=skycluster"
	for _, n := range pNames {
		filters := baseFilters + ", skycluster.io/provider-name=" + n
		filteredVMs := getVMData(dynamicClient, ns, filters)
		vmList = append(vmList, filteredVMs...)
	}
	confirmDeletion(dynamicClient, ns, vmList)
}

func getVMData(dynamicClient dynamic.Interface, ns string, filters string) []unstructured.Unstructured {

	gvr := schema.GroupVersionResource{
		Group:    "xrds.skycluster.io",
		Version:  "v1alpha1",
		Resource: "xvms",
	}

	resources, err := dynamicClient.Resource(gvr).Namespace(ns).List(context.Background(), metav1.ListOptions{
		LabelSelector: filters,
	})
	if err != nil {
		log.Fatalf("Error listing resources: %v", err)
	}

	return resources.Items
}

func confirmDeletion(dynamicClient dynamic.Interface, ns string, vmList []unstructured.Unstructured) {
	writer := tabwriter.NewWriter(os.Stdout, 0, 8, 1, '\t', tabwriter.AlignRight)
	if len(vmList) == 0 {
		fmt.Printf("No SkyVM found in the namespace [%s]\n", ns)
		return
	} else {
		fmt.Fprintln(writer, "NAME\tPRIVATE_IP\tNAMESPACE")
		for _, resource := range vmList {
			fmt.Fprintf(writer, "%s\t%s\n", resource.GetName(), resource.GetNamespace())
		}
		writer.Flush()

		fmt.Print("Deleting these SkyVMs? (y/N): ")
		reader := bufio.NewReader(os.Stdin)
		response, _ := reader.ReadString('\n')
		response = strings.TrimSpace(strings.ToLower(response))

		if response == "y" {
			// Add your deletion logic here
			fmt.Println("Deleting SkyVMs...")
			deleteVMs(dynamicClient, ns, vmList)
		} else {
			fmt.Println("Deletion cancelled.")
		}
	}
}

func listAllSkyVMsAndConfirm(ns string) {
	kconfig := viper.GetStringMapString("kubeconfig")
	kubeconfig := kconfig["sky-manager"]
	dynamicClient, err := utils.GetDynamicClient(kubeconfig)
	if err != nil {
		log.Fatalf("Error getting dynamic client: %v", err)
		return
	}

	vmList := make([]unstructured.Unstructured, 0)
	baseFilters := "skycluster.io/managed-by=skycluster"
	filteredVMs := getVMData(dynamicClient, ns, baseFilters)
	vmList = append(vmList, filteredVMs...)
	confirmDeletion(dynamicClient, ns, vmList)
}

func deleteVMs(dynamicClient dynamic.Interface, ns string, items []unstructured.Unstructured) {
	success := 0
	for _, resource := range items {
		err := dynamicClient.Resource(schema.GroupVersionResource{
			Group:    "xrds.skycluster.io",
			Version:  "v1alpha1",
			Resource: "xvms",
		}).Namespace(ns).Delete(context.Background(), resource.GetName(), metav1.DeleteOptions{})
		if err != nil {
			log.Fatalf("Error deleting resource: %v", err)
		}
		success++
	}
	fmt.Printf("Deleted %d/%d SkyVMs\n", success, len(items))
}
