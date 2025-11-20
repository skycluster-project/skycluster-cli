package xinstance

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

var xNames []string

func init() {
	xInstanceDeleteCmd.PersistentFlags().StringSliceVarP(&xNames, "instance-name", "n", nil, "XInstance Names, separated by comma")
}

var xInstanceDeleteCmd = &cobra.Command{
	Use:   "delete",
	Short: "Delete XInstances",
	Run: func(cmd *cobra.Command, args []string) {
		ns := ""
		if len(xNames) > 0 {
			listXInstancesByNamesAndConfirm(ns, xNames)
			return
		}
		cmd.Help()
	},
}

func listXInstancesByNamesAndConfirm(ns string, names []string) {
	kubeconfig := viper.GetString("kubeconfig")
	dynamicClient, err := utils.GetDynamicClient(kubeconfig)
	if err != nil {
		log.Fatalf("Error getting dynamic client: %v", err)
		return
	}

	instanceList := make([]*unstructured.Unstructured, 0, len(names))
	for _, n := range names {
		inst := getXInstanceData(dynamicClient, ns, n)
		instanceList = append(instanceList, inst)
	}
	confirmDeletion(dynamicClient, ns, instanceList)
}

func getXInstanceData(dynamicClient dynamic.Interface, ns string, name string) *unstructured.Unstructured {
	gvr := schema.GroupVersionResource{
		Group:    "skycluster.io",
		Version:  "v1alpha1",
		Resource: "xinstances",
	}
	resource, err := dynamicClient.
		Resource(gvr).
		Namespace(ns).
		Get(context.Background(), name, metav1.GetOptions{})
	if err != nil {
		log.Fatalf("Error getting XInstance %q: %v", name, err)
	}
	return resource
}

func confirmDeletion(dynamicClient dynamic.Interface, ns string, instances []*unstructured.Unstructured) {
	writer := tabwriter.NewWriter(os.Stdout, 0, 0, 4, ' ', 0)
	if len(instances) == 0 {
		fmt.Printf("No XInstances found in the namespace [%s]\n", ns)
		return
	}

	fmt.Fprintln(writer, "NAME\tNAMESPACE")
	for _, resource := range instances {
		fmt.Fprintf(writer, "%s\t%s\n", resource.GetName(), resource.GetNamespace())
	}
	writer.Flush()

	fmt.Print("Deleting these XInstances? (y/N): ")
	reader := bufio.NewReader(os.Stdin)
	response, _ := reader.ReadString('\n')
	response = strings.TrimSpace(strings.ToLower(response))

	if response == "y" {
		fmt.Println("Deleting XInstances...")
		deleteXInstances(dynamicClient, ns, instances)
	} else {
		fmt.Println("Deletion cancelled.")
	}
}

func deleteXInstances(dynamicClient dynamic.Interface, ns string, items []*unstructured.Unstructured) {
	success := 0
	gvr := schema.GroupVersionResource{
		Group:    "skycluster.io",
		Version:  "v1alpha1",
		Resource: "xinstances",
	}
	for _, resource := range items {
		err := dynamicClient.Resource(gvr).Namespace(ns).Delete(context.Background(), resource.GetName(), metav1.DeleteOptions{})
		if err != nil {
			log.Fatalf("Error deleting XInstance %q: %v", resource.GetName(), err)
		}
		success++
	}
	fmt.Printf("Deleted %d/%d XInstances\n", success, len(items))
}