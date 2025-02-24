package skyvm

import (
	"context"
	"fmt"
	"log"
	"os"
	"text/tabwriter"

	"github.com/etesami/skycluster-cli/internal/utils"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

var skyVMListCmd = &cobra.Command{
	Use:   "list",
	Short: "List SkyVMs",
	Run: func(cmd *cobra.Command, args []string) {
		ns, err := cmd.Root().PersistentFlags().GetString("namespace")
		if err != nil {
			log.Fatalf("error getting namespace: %v", err)
			return
		}
		listSkyVMs(ns)
	},
}

func listSkyVMs(ns string) {

	kconfig := viper.GetStringMapString("kubeconfig")
	kubeconfig := kconfig["sky-manager"]
	dynamicClient, err := utils.GetDynamicClient(kubeconfig)
	if err != nil {
		log.Fatalf("Error getting dynamic client: %v", err)
		return
	}

	gvr := schema.GroupVersionResource{
		Group:    "xrds.skycluster.io",
		Version:  "v1alpha1",
		Resource: "skyvms",
	}

	resources, err := dynamicClient.Resource(gvr).Namespace(ns).List(context.Background(), metav1.ListOptions{
		LabelSelector: "skycluster.io/managed-by=skycluster",
	})
	if err != nil {
		log.Fatalf("Error listing resources: %v", err)
	}

	writer := tabwriter.NewWriter(os.Stdout, 0, 8, 1, '\t', tabwriter.AlignRight)
	if len(resources.Items) == 0 {
		fmt.Printf("No SkyVM found in the namespace [%s]\n", ns)
		return
	} else {
		fmt.Fprintln(writer, "NAME\tPRIVATE_IP\tNAMESPACE")
	}

	for _, resource := range resources.Items {
		stat, found, err := unstructured.NestedMap(resource.Object, "status", "network")
		if err != nil || !found {
			fmt.Fprintf(writer, "%s\t%s\t%s\n", resource.GetName(), "<not-ready>", ns)
			log.Fatalf("spec.status not found: %v", err)
		} else {
			fmt.Fprintf(writer, "%s\t%s\t%s\n", resource.GetName(), stat["privateIpAddress"], ns)
		}
	}
	writer.Flush()
}
