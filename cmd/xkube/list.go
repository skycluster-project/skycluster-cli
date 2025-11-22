package xkube

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
	"k8s.io/client-go/dynamic"
)

var watchFlag *bool

func init() {
	watchFlag = xKubeListCmd.PersistentFlags().BoolP("watch", "w", false, "Watch XKube")
}

var xKubeListCmd = &cobra.Command{
	Use:   "list",
	Short: "List XKube",
	Run: func(cmd *cobra.Command, args []string) {
		ns, err := cmd.Root().PersistentFlags().GetString("namespace")
		if err != nil {
			log.Fatalf("error getting namespace: %v", err)
			return
		}
		if *watchFlag {
			watchXKubes(ns)
			return
		}
		listXKubes(ns)
	},
}

func watchXKubes(ns string) {
	kubeconfig := viper.GetString("kubeconfig")
	dynamicClient, err := utils.GetDynamicClient(kubeconfig)
	if err != nil {
		log.Fatalf("Error creating dynamic client: %v", err)
		return
	}

	gvr := schema.GroupVersionResource{
		Group:    "skycluster.io",
		Version:  "v1alpha1", 
		Resource: "xkubes",
	}
	writer := tabwriter.NewWriter(os.Stdout, 0, 0, 4, ' ', 0)
	fmt.Fprintln(writer, "NAME\tPLATFORM\tPOD_CIDR\tSERVICE_CIDR\tLOCATION\tEXTERNAL_NAME,\tREADY")

	watcher, err := dynamicClient.Resource(gvr).Namespace(ns).Watch(context.Background(), metav1.ListOptions{})
	// 	LabelSelector: "skycluster.io/managed-by=skycluster",
	if err != nil {
		fmt.Printf("Error setting up watch: %v\n", err)
		return
	}
	ch := watcher.ResultChan()
	for event := range ch {
		obj := event.Object.(*unstructured.Unstructured)
		
		podCidr, _, _ := unstructured.NestedString(obj.Object, "status", "podCidr")
		svcCidr, _, _ := unstructured.NestedString(obj.Object, "status", "serviceCidr")
		provPlatform, _, _ := unstructured.NestedString(obj.Object, "spec", "providerRef", "platform")
		provCfgZones, _, _ := unstructured.NestedStringMap(obj.Object, "spec", "providerRef", "zones")
		extName, _, _ := unstructured.NestedString(obj.Object, "status", "externalClusterName")

		// Conditions: get Sync (Synced) and Ready condition statuses
		readyStatus := utils.GetConditionStatus(obj, "Ready")

		fmt.Fprintf(writer, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n", obj.GetName(), provPlatform, podCidr, svcCidr, provCfgZones["primary"], extName, readyStatus)
		writer.Flush()
	}
}

func listXKubes(ns string) {
	kubeconfig := viper.GetString("kubeconfig")
	dynamicClient, err := utils.GetDynamicClient(kubeconfig)
	if err != nil {
		log.Fatalf("Error creating dynamic client: %v", err)
		return
	}

	gvr := schema.GroupVersionResource{
		Group:    "skycluster.io",
		Version:  "v1alpha1", 
		Resource: "xkubes",
	}
	var ri dynamic.ResourceInterface
	if ns != "" {
		ri = dynamicClient.Resource(gvr).Namespace(ns)
	} else {
		ri = dynamicClient.Resource(gvr)
	}

	resources, err := ri.List(context.Background(), metav1.ListOptions{})
	// 	LabelSelector: "skycluster.io/managed-by=skycluster",
	if err != nil {
		log.Fatalf("Error listing resources: %v", err)
		return
	}

	writer := tabwriter.NewWriter(os.Stdout, 0, 0, 4, ' ', 0)
	if len(resources.Items) == 0 {
		fmt.Printf("No XKube found.\n", ns)
		return
		} else {
		fmt.Fprintln(writer, "NAME\tPLATFORM\tPOD_CIDR\tSERVICE_CIDR\tLOCATION\tEXTERNAL_NAME,\tREADY")
	}

	for _, resource := range resources.Items {
		podCidr, _, _ := unstructured.NestedString(resource.Object, "status", "podCidr")
		svcCidr, _, _ := unstructured.NestedString(resource.Object, "status", "serviceCidr")
		provPlatform, _, _ := unstructured.NestedString(resource.Object, "spec", "providerRef", "platform")
		provCfgZones, _, _ := unstructured.NestedStringMap(resource.Object, "spec", "providerRef", "zones")
		extName, _, _ := unstructured.NestedString(resource.Object, "status", "externalClusterName")

		// Conditions: get Sync (Synced) and Ready condition statuses
		readyStatus := utils.GetConditionStatus(&resource, "Ready")

		fmt.Fprintf(writer, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n", resource.GetName(), provPlatform, podCidr, svcCidr, provCfgZones["primary"], extName, readyStatus)
	}
	writer.Flush()
}
