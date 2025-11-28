package xprovider

import (
	"bufio"
	"context"
	"fmt"
	"log"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/etesami/skycluster-cli/internal/utils"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
)

var pNames []string

func init() {
	xProviderDeleteCmd.PersistentFlags().StringSliceVarP(&pNames, "provider-name", "n", nil, "Provider Names, separated by comma")
}

var xProviderDeleteCmd = &cobra.Command{
	Use:   "delete",
	Short: "Delete XProviders",
	Run: func(cmd *cobra.Command, args []string) {
		ns := ""
		debugf("delete command invoked: ns=%q pNames=%v", ns, pNames)
		if len(pNames) > 0 {
			listXProvidersByProviderNamesAndConfirm(ns, pNames)
			return
		}
		_ = cmd.Help()
	},
}

func listXProvidersByProviderNamesAndConfirm(ns string, pNames []string) {
	debugf("listXProvidersByProviderNamesAndConfirm: ns=%q pNames=%v", ns, pNames)
	kubeconfig := viper.GetString("kubeconfig")
	debugf("using kubeconfig: %q", kubeconfig)
	dynamicClient, err := utils.GetDynamicClient(kubeconfig)
	if err != nil {
		debugf("GetDynamicClient failed: %v", err)
		log.Fatalf("Error getting dynamic client: %v", err)
		return
	}
	debugf("dynamic client initialized")

	providerList := make([]*unstructured.Unstructured, 0)
	for _, n := range pNames {
		debugf("fetching provider data for name=%q", n)
		filteredProviders := getProviderData(dynamicClient, ns, n)
		if filteredProviders != nil {
			providerList = append(providerList, filteredProviders)
			debugf("appended provider %q", filteredProviders.GetName())
		} else {
			debugf("no provider returned for name=%q", n)
		}
	}
	confirmDeletion(dynamicClient, ns, providerList)
}

func getProviderData(dynamicClient dynamic.Interface, ns string, name string) *unstructured.Unstructured {
	debugf("getProviderData: ns=%q name=%q", ns, name)
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
		debugf("error getting resource %s/%s: %v", ns, name, err)
		log.Fatalf("Error listing resources: %v", err)
		return nil
	}
	debugf("got resource %s (uid=%v)", resource.GetName(), resource.GetUID())
	return resource
}

func confirmDeletion(dynamicClient dynamic.Interface, ns string, providerList []*unstructured.Unstructured) {
	debugf("confirmDeletion: ns=%q providerCount=%d", ns, len(providerList))
	writer := tabwriter.NewWriter(os.Stdout, 0, 0, 4, ' ', 0)
	if len(providerList) == 0 {
		fmt.Printf("No SkyProvider found.\n")
		debugf("no providers to display")
		return
	} else {
		fmt.Fprintln(writer, "NAME")
		for _, resource := range providerList {
			fmt.Fprintf(writer, "%s\n", resource.GetName())
			debugf("displaying provider %s", resource.GetName())
		}
		writer.Flush()

		fmt.Print("Deleting these XProviders? (y/N): ")
		reader := bufio.NewReader(os.Stdin)
		response, _ := reader.ReadString('\n')
		response = strings.TrimSpace(strings.ToLower(response))
		debugf("user response: %q", response)

		if response == "y" {
			debugf("user confirmed deletion")
			fmt.Println("Deleting XProviders...")
			deleteXProviders(dynamicClient, ns, providerList)
		} else {
			debugf("user cancelled deletion")
			fmt.Println("Deletion cancelled.")
		}
	}
}

func deleteXProviders(dynamicClient dynamic.Interface, ns string, items []*unstructured.Unstructured) {
	debugf("deleteXProviders: ns=%q items=%d", ns, len(items))
	success := 0
	for _, resource := range items {
		name := resource.GetName()
		debugf("deleting resource %s/%s", ns, name)
		err := dynamicClient.Resource(schema.GroupVersionResource{
			Group:    "skycluster.io",
			Version:  "v1alpha1",
			Resource: "xproviders",
		}).Namespace(ns).Delete(context.Background(), name, metav1.DeleteOptions{})
		if err != nil {
			debugf("error deleting resource %s: %v", name, err)
			log.Fatalf("Error deleting resource: %v", err)
		}
		success++
		debugf("deleted resource %s successfully", name)
	}
	fmt.Printf("Deleted %d/%d XProviders\n", success, len(items))
	debugf("deleteXProviders completed: deleted=%d total=%d", success, len(items))
}