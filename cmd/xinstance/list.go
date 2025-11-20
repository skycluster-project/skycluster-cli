package xinstance

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
	watchFlag = xInstanceListCmd.PersistentFlags().BoolP("watch", "w", false, "Watch XInstances")
}

var xInstanceListCmd = &cobra.Command{
	Use:   "list",
	Short: "List XInstances",
	Run: func(cmd *cobra.Command, args []string) {
		ns := ""
		if *watchFlag {
			watchXInstances(ns)
			return
		}
		listXInstances(ns)
	},
}

func watchXInstances(ns string) {
	kubeconfig := viper.GetString("kubeconfig")
	dynamicClient, err := utils.GetDynamicClient(kubeconfig)
	if err != nil {
		log.Fatalf("Error creating dynamic client: %v", err)
		return
	}

	gvr := schema.GroupVersionResource{
		Group:    "skycluster.io",
		Version:  "v1alpha1",
		Resource: "xinstances",
	}
	writer := tabwriter.NewWriter(os.Stdout, 0, 0, 4, ' ', 0)
	fmt.Fprintln(writer, "NAME\tPRIVATE_IP\tPUBLIC_IP\tCIDR_BLOCK")

	watcher, err := dynamicClient.Resource(gvr).Namespace(ns).Watch(context.Background(), metav1.ListOptions{})
	// 	LabelSelector: "skycluster.io/managed-by=skycluster",
	if err != nil {
		fmt.Printf("Error setting up watch: %v\n", err)
		return
	}
	ch := watcher.ResultChan()
	for event := range ch {
		privateIp, publicIp, vpcCidr := "", "", ""
		obj := event.Object.(*unstructured.Unstructured)

		// New status layout: status.network.privateIp / status.network.publicIp
		if v, found, _ := unstructured.NestedString(obj.Object, "status", "network", "privateIp"); found {
			privateIp = v
		}
		if v, found, _ := unstructured.NestedString(obj.Object, "status", "network", "publicIp"); found {
			publicIp = v
		}

		if v, found, _ := unstructured.NestedString(obj.Object, "spec", "vpcCidr"); found {
			vpcCidr = v
		}

		fmt.Fprintf(writer, "%s\t%s\t%s\t%s\n", obj.GetName(), privateIp, publicIp, vpcCidr)
		writer.Flush()
	}
}

func listXInstances(ns string) {
	kubeconfig := viper.GetString("kubeconfig")
	dynamicClient, err := utils.GetDynamicClient(kubeconfig)
	if err != nil {
		log.Fatalf("Error creating dynamic client: %v", err)
		return
	}

	gvr := schema.GroupVersionResource{
		Group:    "skycluster.io",
		Version:  "v1alpha1",
		Resource: "xinstances",
	}

	resources, err := dynamicClient.Resource(gvr).Namespace(ns).List(context.Background(), metav1.ListOptions{})
	if err != nil {
		log.Fatalf("Error listing resources: %v", err)
		return
	}

	writer := tabwriter.NewWriter(os.Stdout, 0, 0, 4, ' ', 0)
	if len(resources.Items) == 0 {
		fmt.Printf("No XInstances found.\n")
		return
	} else {
		fmt.Fprintln(writer, "NAME\tPRIVATE_IP\tPUBLIC_IP\tCIDR_BLOCK")
	}

	for _, resource := range resources.Items {
		privateIp, publicIp := "", ""
		if v, found, _ := unstructured.NestedString(resource.Object, "status", "network", "privateIp"); found {
			privateIp = v
		}
		if v, found, _ := unstructured.NestedString(resource.Object, "status", "network", "publicIp"); found {
			publicIp = v
		}

		vpc, _, _ := unstructured.NestedString(resource.Object, "spec", "vpcCidr")

		fmt.Fprintf(writer, "%s\t%s\t%s\t%s\n", resource.GetName(), privateIp, publicIp, vpc)
	}
	writer.Flush()
}