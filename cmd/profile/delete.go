package profile

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
	profileDeleteCmd.PersistentFlags().StringSliceVarP(&pNames, "name", "n", nil, "Profile Names, seperated by comma")
}

var profileDeleteCmd = &cobra.Command{
	Use:   "delete",
	Short: "Delete Profiles",
	Run: func(cmd *cobra.Command, args []string) {
		ns := "skycluster-system"
		if len(pNames) > 0 {
			listProfilesByProfileNamesAndConfirm(ns, pNames)
			return
		}
		cmd.Help()
	},
}

func listProfilesByProfileNamesAndConfirm(ns string, pNames []string) {
	kubeconfig := viper.GetString("kubeconfig")
	dynamicClient, err := utils.GetDynamicClient(kubeconfig)
	if err != nil {
		log.Fatalf("Error getting dynamic client: %v", err)
		return
	}

	profileList := make([]*unstructured.Unstructured, 0)
	for _, n := range pNames {
		filteredProfiles := getProfileData(dynamicClient, ns, n)
		profileList = append(profileList, filteredProfiles)
	}
	confirmDeletion(dynamicClient, ns, profileList)
}

func getProfileData(dynamicClient dynamic.Interface, ns string, name string) *unstructured.Unstructured {
	gvr := schema.GroupVersionResource{
		Group:    "core.skycluster.io",
		Version:  "v1alpha1",
		Resource: "providerprofiles",
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

func confirmDeletion(dynamicClient dynamic.Interface, ns string, profileList []*unstructured.Unstructured) {
	writer := tabwriter.NewWriter(os.Stdout, 0, 0, 4, ' ', 0)
	if len(profileList) == 0 {
		fmt.Printf("No ProviderProfile found in the namespace [%s]\n", ns)
		return
	} else {
		fmt.Fprintln(writer, "NAME\tNAME\tNAMESPACE")
		for _, resource := range profileList {
			fmt.Fprintf(writer, "%s\t%s\n", resource.GetName(), resource.GetNamespace())
		}
		writer.Flush()

		fmt.Print("Deleting these ProviderProfiles? (y/N): ")
		reader := bufio.NewReader(os.Stdin)
		response, _ := reader.ReadString('\n')
		response = strings.TrimSpace(strings.ToLower(response))

		if response == "y" {
			// Add your deletion logic here
			fmt.Println("Deleting ProviderProfiles...")
			deleteProviderProfiles(dynamicClient, ns, profileList)
		} else {
			fmt.Println("Deletion cancelled.")
		}
	}
}

func deleteProviderProfiles(dynamicClient dynamic.Interface, ns string, items []*unstructured.Unstructured) {
	success := 0
	for _, resource := range items {
		err := dynamicClient.Resource(schema.GroupVersionResource{
			Group:    "core.skycluster.io",
			Version:  "v1alpha1",
			Resource: "providerprofiles",
		}).Namespace(ns).Delete(context.Background(), resource.GetName(), metav1.DeleteOptions{})
		if err != nil {
			log.Fatalf("Error deleting resource: %v", err)
		}
		success++
	}
	fmt.Printf("Deleted %d/%d ProviderProfiles\n", success, len(items))
}
