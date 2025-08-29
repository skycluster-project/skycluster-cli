package xprovider

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
	watchFlag = xProviderListCmd.PersistentFlags().BoolP("watch", "w", false, "Watch XProviders")
}

var xProviderListCmd = &cobra.Command{
	Use:   "list",
	Short: "List XProviders",
	Run: func(cmd *cobra.Command, args []string) {
		ns, err := cmd.Root().PersistentFlags().GetString("namespace")
		if err != nil {
			log.Fatalf("error getting namespace: %v", err)
			return
		}
		if *watchFlag {
			watchXProviders(ns)
			return
		}
		listXProviders(ns)
	},
}

func watchXProviders(ns string) {
	kubeconfig := viper.GetString("kubeconfig")
	dynamicClient, err := utils.GetDynamicClient(kubeconfig)
	if err != nil {
		log.Fatalf("Error creating dynamic client: %v", err)
		return
	}

	gvr := schema.GroupVersionResource{
		Group:    "skycluster.io",
		Version:  "v1alpha1", 
		Resource: "xproviders",
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
		
		stat, found, err := unstructured.NestedStringMap(obj.Object, "status", "gateway")
		if err == nil && found {
			privIp, ok := stat["privateIp"]
			privateIp = lo.Ternary(ok, privIp, "")
			pubIp, ok := stat["publicIp"]
			publicIp = lo.Ternary(ok, pubIp, "")
		}

		vpc, found, err := unstructured.NestedString(obj.Object, "spec", "vpcCidr")
		if err == nil && found {
			vpcCidr = vpc
		}

		fmt.Fprintf(writer, "%s\t%s\t%s\t%s\n", obj.GetName(), privateIp, publicIp, vpcCidr)
		writer.Flush()
	}
}

func listXProviders(ns string) {
	kubeconfig := viper.GetString("kubeconfig")
	dynamicClient, err := utils.GetDynamicClient(kubeconfig)
	if err != nil {
		log.Fatalf("Error creating dynamic client: %v", err)
		return
	}

	gvr := schema.GroupVersionResource{
		Group:    "skycluster.io",
		Version:  "v1alpha1", 
		Resource: "xproviders",
	}

	resources, err := dynamicClient.Resource(gvr).Namespace(ns).List(context.Background(), metav1.ListOptions{})
	// 	LabelSelector: "skycluster.io/managed-by=skycluster",
	if err != nil {
		log.Fatalf("Error listing resources: %v", err)
		return
	}

	writer := tabwriter.NewWriter(os.Stdout, 0, 0, 4, ' ', 0)
	if len(resources.Items) == 0 {
		fmt.Printf("No XProviders found in the namespace [%s]\n", ns)
		return
		} else {
		fmt.Fprintln(writer, "NAME\tPRIVATE_IP\tPUBLIC_IP\tCIDR_BLOCK")
	}

	for _, resource := range resources.Items {
		stat, found, err := unstructured.NestedStringMap(resource.Object, "status", "gateway")
		privateIp, publicIp := "", ""
		if err == nil && found {
			privIp, ok := stat["privateIp"]
			privateIp = lo.Ternary(ok, privIp, "")
			pubIp, ok := stat["publicIp"]
			publicIp = lo.Ternary(ok, pubIp, "")
		}

		vpc, _, _ := unstructured.NestedString(resource.Object, "spec", "vpcCidr")

		fmt.Fprintf(writer, "%s\t%s\t%s\t%s\n", resource.GetName(), privateIp, publicIp, vpc)
	}
	writer.Flush()
}
