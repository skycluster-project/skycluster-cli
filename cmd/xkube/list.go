package xkube

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
		fmt.Printf("No XKube found in the namespace [%s]\n", ns)
		return
		} else {
		fmt.Fprintln(writer, "NAME\tGATEWAY\tPOD_CIDR\tSERVICE_CIDR\tLOCATION\tEXTERNAL_NAME")
	}

	for _, resource := range resources.Items {
		podCidr, _, _ := unstructured.NestedString(resource.Object, "status", "podCidr")
		svcCidr, _, _ := unstructured.NestedString(resource.Object, "status", "serviceCidr")
		// agents, _, _ := unstructured.NestedSlice(resource.Object, "status", "agents")
		ctrls, _, _ := unstructured.NestedSlice(resource.Object, "status", "controllers")
		provCfgZones, _, _ := unstructured.NestedStringMap(resource.Object, "spec", "providerRef", "zones")
		extName, _, _ := unstructured.NestedString(resource.Object, "status", "externalClusterName")
		ctrlIp := ""
		for _, c := range ctrls {
			m, ok := c.(map[string]interface{})
			if ok {
				ctrlIp = m["publicIp"].(string)
				break // only one controller is expected
			}
		}

		fmt.Fprintf(writer, "%s\t%s\t%s\t%s\t%s\t%s\n", resource.GetName(), ctrlIp, podCidr, svcCidr, provCfgZones["primary"], extName)
	}
	writer.Flush()
}
