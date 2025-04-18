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

var watchFlag *bool

func init() {
	watchFlag = skyVMListCmd.PersistentFlags().BoolP("watch", "w", false, "Watch SkyVMs")
}

var skyVMListCmd = &cobra.Command{
	Use:   "list",
	Short: "List SkyVMs",
	Run: func(cmd *cobra.Command, args []string) {
		ns, err := cmd.Root().PersistentFlags().GetString("namespace")
		if err != nil {
			log.Fatalf("error getting namespace: %v", err)
			return
		}
		if *watchFlag {
			watchSkyVMs(ns)
			return
		}
		listSkyVMs(ns)
	},
}

func watchSkyVMs(ns string) {
	kconfig := viper.GetStringMapString("kubeconfig")
	kubeconfig := kconfig["sky-manager"]
	dynamicClient, err := utils.GetDynamicClient(kubeconfig)
	if err != nil {
		log.Fatalf("Error creating dynamic client: %v", err)
		return
	}

	gvr := schema.GroupVersionResource{
		Group:    "xrds.skycluster.io",
		Version:  "v1alpha1", // Replace with actual version
		Resource: "xvms",
	}
	writer := tabwriter.NewWriter(os.Stdout, 12, 8, 1, '\t', tabwriter.AlignRight)
	fmt.Fprintln(writer, "NAME\tPRIVATE_IP")
	writer.Flush()

	watcher, err := dynamicClient.Resource(gvr).Namespace(ns).Watch(context.Background(), metav1.ListOptions{
		LabelSelector: "skycluster.io/managed-by=skycluster",
	})
	if err != nil {
		fmt.Printf("Error setting up watch: %v\n", err)
		return
	}
	ch := watcher.ResultChan()
	for event := range ch {
		obj := event.Object.(*unstructured.Unstructured)
		stat, found, err := unstructured.NestedMap(obj.Object, "status", "network")
		if err == nil && found {
			_, ok1 := stat["privateIpAddress"]
			if ok1 {
				fmt.Fprintf(writer, "%s\t%s\n", obj.GetName(), stat["privateIpAddress"])
			} else {
				fmt.Fprintf(writer, "%s\t%s\n", obj.GetName(), "<not-ready>")
			}
		} else {
			fmt.Fprintf(writer, "%s\t%s\n", obj.GetName(), "<not-ready>")
		}
		writer.Flush()
	}
	writer.Flush()
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
		Resource: "xvms",
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
		fmt.Fprintln(writer, "NAME\tPRIVATE_IP")
	}

	for _, resource := range resources.Items {
		stat, found, err := unstructured.NestedMap(resource.Object, "status", "network")
		if err != nil || !found {
			fmt.Fprintf(writer, "%s\t%s\n", resource.GetName(), "<not-ready>")
			log.Fatalf("spec.status not found: %v", err)
		} else {
			fmt.Fprintf(writer, "%s\t%s\n", resource.GetName(), stat["privateIpAddress"])
		}
	}
	writer.Flush()
}
