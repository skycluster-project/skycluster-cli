package xinstance

import (
	"context"
	"fmt"
	"log"
	"os"
	"text/tabwriter"

	lo "github.com/samber/lo"

	"github.com/etesami/skycluster-cli/internal/utils"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

var watchFlag *bool

func init() {
	watchFlag = xInstnaceListCmd.PersistentFlags().BoolP("watch", "w", false, "Watch XInstances")
}

var xInstnaceListCmd = &cobra.Command{
	Use:   "list",
	Short: "List XInstances",
	Run: func(cmd *cobra.Command, args []string) {
		ns, err := cmd.Root().PersistentFlags().GetString("namespace")
		if err != nil {
			log.Fatalf("error getting namespace: %v", err)
			return
		}
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
		Version:  "v1alpha1", // Replace with actual version
		Resource: "xinstances",
	}
	writer := tabwriter.NewWriter(os.Stdout, 0, 0, 4, ' ', 0)
	fmt.Fprintln(writer, "NAME\tPUBLIC_IP\tPRIVATE_IP")
	writer.Flush()

	watcher, err := dynamicClient.Resource(gvr).Namespace(ns).Watch(context.Background(), metav1.ListOptions{})
	if err != nil {
		fmt.Printf("Error setting up watch: %v\n", err)
		return
	}
	ch := watcher.ResultChan()
	for event := range ch {
		obj := event.Object.(*unstructured.Unstructured)
		net, _, _ := unstructured.NestedStringMap(obj.Object, "status", "network")
		privateIp := net["privateIp"]
		publicIp := net["publicIp"]
		fmt.Fprintf(writer, "%s\t%s\t%s\n", obj.GetName(), publicIp, privateIp)
	}
	writer.Flush()
}

func listXInstances(ns string) {

	kubeconfig := viper.GetString("kubeconfig")
	dynamicClient, err := utils.GetDynamicClient(kubeconfig)
	if err != nil {
		log.Fatalf("Error getting dynamic client: %v", err)
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
	}

	writer := tabwriter.NewWriter(os.Stdout, 0, 0, 4, ' ', 0)
	if len(resources.Items) == 0 {
		fmt.Printf("No XInstance found in the namespace [%s]\n", ns)
		return
	} else {
		fmt.Fprintln(writer, "NAME\tPUBLIC_IP\tPRIVATE_IP")
	}

	for _, resource := range resources.Items {
		stat, _, _ := unstructured.NestedMap(resource.Object, "status", "network")
		privateIp := lo.Ternary(stat["privateIp"] != nil, stat["privateIp"], "")
		publicIp := lo.Ternary(stat["publicIp"] != nil, stat["publicIp"], "")
		fmt.Fprintf(writer, "%s\t%s\t%s\n", resource.GetName(), publicIp, privateIp)
	}
	writer.Flush()
}
