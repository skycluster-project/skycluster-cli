package xprovider

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

var pNames []string

func init() {
	xProviderDeleteCmd.PersistentFlags().StringSliceVarP(&pNames, "provider-name", "p", nil, "Provider Names, seperated by comma")
}

var xProviderDeleteCmd = &cobra.Command{
	Use:   "delete",
	Short: "Delete XProviders",
	Run: func(cmd *cobra.Command, args []string) {
		ns, err := cmd.Root().PersistentFlags().GetString("namespace")
		if err != nil {
			log.Fatalf("error getting namespace: %v", err)
			return
		}
		if len(pNames) > 0 {
			listXProvidersByProviderNamesAndConfirm(ns, pNames)
			return
		}
		cmd.Help()
	},
}

func listXProvidersByProviderNamesAndConfirm(ns string, pNames []string) {
	kubeconfig := viper.GetString("kubeconfig")
	dynamicClient, err := utils.GetDynamicClient(kubeconfig)
	if err != nil {
		log.Fatalf("Error getting dynamic client: %v", err)
		return
	}

	providerList := make([]*unstructured.Unstructured, 0)
	// baseFilters := "skycluster.io/managed-by=skycluster"
	for _, n := range pNames {
		// filters := baseFilters + ", skycluster.io/provider-name=" + n
		filteredProviders := getProviderData(dynamicClient, ns, n)
		providerList = append(providerList, filteredProviders)
	}
	confirmDeletion(dynamicClient, ns, providerList)
}

func getProviderData(dynamicClient dynamic.Interface, ns string, name string) *unstructured.Unstructured {
	gvr := schema.GroupVersionResource{
		Group:    "skycluster.io",
		Version:  "v1alpha1",
		Resource: "xproviders",
	}
	resource, err := dynamicClient.
		Resource(gvr).
		Namespace(ns).
		Get(context.Background(), name, metav1.GetOptions{})
	if err != nil {
		log.Fatalf("Error listing resources: %v", err)
	}

	return resource
}

func confirmDeletion(dynamicClient dynamic.Interface, ns string, providerList []*unstructured.Unstructured) {
	writer := tabwriter.NewWriter(os.Stdout, 0, 0, 4, ' ', 0)
	if len(providerList) == 0 {
		fmt.Printf("No SkyProvider found in the namespace [%s]\n", ns)
		return
	} else {
		fmt.Fprintln(writer, "NAME\tNAME\tNAMESPACE")
		for _, resource := range providerList {
			fmt.Fprintf(writer, "%s\t%s\n", resource.GetName(), resource.GetNamespace())
		}
		writer.Flush()

		fmt.Print("Deleting these XProviders? (y/N): ")
		reader := bufio.NewReader(os.Stdin)
		response, _ := reader.ReadString('\n')
		response = strings.TrimSpace(strings.ToLower(response))

		if response == "y" {
			// Add your deletion logic here
			fmt.Println("Deleting XProviders...")
			deleteXProviders(dynamicClient, ns, providerList)
		} else {
			fmt.Println("Deletion cancelled.")
		}
	}
}

func deleteXProviders(dynamicClient dynamic.Interface, ns string, items []*unstructured.Unstructured) {
	success := 0
	for _, resource := range items {
		err := dynamicClient.Resource(schema.GroupVersionResource{
			Group:    "skycluster.io",
			Version:  "v1alpha1",
			Resource: "XProviders",
		}).Namespace(ns).Delete(context.Background(), resource.GetName(), metav1.DeleteOptions{})
		if err != nil {
			log.Fatalf("Error deleting resource: %v", err)
		}
		success++
	}
	fmt.Printf("Deleted %d/%d XProviders\n", success, len(items))
}
