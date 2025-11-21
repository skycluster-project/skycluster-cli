package xinstance

import (
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

// helper to extract a condition's "status" (e.g. "True"/"False"/"Unknown")
func getConditionStatus(obj *unstructured.Unstructured, condType string) string {
	if arr, found, _ := unstructured.NestedSlice(obj.Object, "status", "conditions"); found {
		for _, item := range arr {
			if m, ok := item.(map[string]interface{}); ok {
				if t, ok := m["type"].(string); ok && t == condType {
					if s, ok := m["status"].(string); ok {
						return s
					}
				}
			}
		}
	}
	return ""
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
	// Removed CIDR_BLOCK, added SYNC and READY columns
	fmt.Fprintln(writer, "NAME\tPROVIDER\tPRIVATE_IP\tPUBLIC_IP\tSPOT\tSYNC\tREADY")

	watcher, err := dynamicClient.Resource(gvr).Namespace(ns).Watch(context.Background(), metav1.ListOptions{})
	//	LabelSelector: "skycluster.io/managed-by=skycluster",
	if err != nil {
		fmt.Printf("Error setting up watch: %v\n", err)
		return
	}
	ch := watcher.ResultChan()
	for event := range ch {
		privateIp, publicIp, providerName, spot := "-", "-", "", "-"
		obj := event.Object.(*unstructured.Unstructured)

		// New status layout: status.network.privateIp / status.network.publicIp
		if v, found, _ := unstructured.NestedString(obj.Object, "status", "network", "privateIp"); found {
			privateIp = v
		}
		if v, found, _ := unstructured.NestedString(obj.Object, "status", "network", "publicIp"); found {
			publicIp = v
		}
		if v, found, _ := unstructured.NestedString(obj.Object, "status", "providerName"); found {
			providerName = v
		}
		if v, found, _ := unstructured.NestedBool(obj.Object, "status", "spotInstance"); found {
			s := fmt.Sprintf("%v", v)
			if len(s) > 0 { 
				spot = strings.ToUpper(s[:1]) + s[1:] 
			} else { spot = s }
		}

		// Conditions: get Sync (Synced) and Ready condition statuses
		syncStatus := getConditionStatus(obj, "Synced") // example uses "Synced"
		if syncStatus == "" {
			// fallback to "Sync" type if resource uses that name
			syncStatus = getConditionStatus(obj, "Sync")
		}
		readyStatus := getConditionStatus(obj, "Ready")

		fmt.Fprintf(writer, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n", obj.GetName(), providerName, privateIp, publicIp, spot, syncStatus, readyStatus)
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
		// Removed CIDR_BLOCK, added SYNC and READY columns
		fmt.Fprintln(writer, "NAME\tPROVIDER\tPRIVATE_IP\tPUBLIC_IP\tSPOT\tSYNC\tREADY")
	}

	for _, resource := range resources.Items {
		privateIp, publicIp, providerName, spot := "-", "-", "", "-"
		if v, found, _ := unstructured.NestedString(resource.Object, "status", "network", "privateIp"); found {
			privateIp = v
		}
		if v, found, _ := unstructured.NestedString(resource.Object, "status", "network", "publicIp"); found {
			publicIp = v
		}
		if v, found, _ := unstructured.NestedString(resource.Object, "status", "providerName"); found {
			providerName = v
		}
		if v, found, _ := unstructured.NestedBool(resource.Object, "status", "spotInstance"); found {
			s := fmt.Sprintf("%v", v)
			if len(s) > 0 { 
				spot = strings.ToUpper(s[:1]) + s[1:] 
			} else { spot = s }
		}

		// Conditions: get Sync (Synced) and Ready condition statuses
		syncStatus := getConditionStatus(&resource, "Synced")
		if syncStatus == "" {
			syncStatus = getConditionStatus(&resource, "Sync")
		}
		readyStatus := getConditionStatus(&resource, "Ready")

		fmt.Fprintf(writer, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n", resource.GetName(), providerName, privateIp, publicIp, spot, syncStatus, readyStatus)
	}
	writer.Flush()
}