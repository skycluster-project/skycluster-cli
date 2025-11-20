package profile

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
	watchFlag = profileListCmd.PersistentFlags().BoolP("watch", "w", false, "Watch ProviderProfiles")
}

var profileListCmd = &cobra.Command{
	Use:   "list",
	Short: "List ProviderProfiles",
	Run: func(cmd *cobra.Command, args []string) {
		ns, err := cmd.Root().PersistentFlags().GetString("namespace")
		if err != nil {
			log.Fatalf("error getting namespace: %v", err)
			return
		}
		if *watchFlag {
			watchProviderProfiles(ns)
			return
		}
		listProviderProfiles(ns)
	},
}

func watchProviderProfiles(ns string) {
	kubeconfig := viper.GetString("kubeconfig")
	dynamicClient, err := utils.GetDynamicClient(kubeconfig)
	if err != nil {
		log.Fatalf("Error creating dynamic client: %v", err)
		return
	}

	gvr := schema.GroupVersionResource{
		Group:    "core.skycluster.io",
		Version:  "v1alpha1",
		Resource: "providerprofiles",
	}
	writer := tabwriter.NewWriter(os.Stdout, 0, 0, 4, ' ', 0)
	fmt.Fprintln(writer, "NAME\tPLATFORM\tREGION\tREADY")

	watcher, err := dynamicClient.Resource(gvr).Namespace(ns).Watch(context.Background(), metav1.ListOptions{})
	if err != nil {
		fmt.Printf("Error setting up watch: %v\n", err)
		return
	}
	ch := watcher.ResultChan()
	for event := range ch {
		platform, region, ready := "", "", ""
		obj := event.Object.(*unstructured.Unstructured)

		if p, found, err := unstructured.NestedString(obj.Object, "status", "platform"); err == nil && found {
			platform = p
		}
		if r, found, err := unstructured.NestedString(obj.Object, "status", "region"); err == nil && found {
			region = r
		}

		conds, found, err := unstructured.NestedSlice(obj.Object, "status", "conditions")
		if err == nil && found {
			for _, c := range conds {
				if cm, ok := c.(map[string]interface{}); ok {
					if t, _ := cm["type"].(string); t == "Ready" {
						if s, _ := cm["status"].(string); s != "" {
							ready = s
						}
						break
					}
				}
			}
		}

		fmt.Fprintf(writer, "%s\t%s\t%s\t%s\n", obj.GetName(), platform, region, ready)
		writer.Flush()
	}
}

func listProviderProfiles(ns string) {
	kubeconfig := viper.GetString("kubeconfig")
	dynamicClient, err := utils.GetDynamicClient(kubeconfig)
	if err != nil {
		log.Fatalf("Error creating dynamic client: %v", err)
		return
	}

	gvr := schema.GroupVersionResource{
		Group:    "core.skycluster.io",
		Version:  "v1alpha1",
		Resource: "providerprofiles",
	}

	resources, err := dynamicClient.Resource(gvr).Namespace(ns).List(context.Background(), metav1.ListOptions{})
	if err != nil {
		log.Fatalf("Error listing resources: %v", err)
		return
	}

	writer := tabwriter.NewWriter(os.Stdout, 0, 0, 4, ' ', 0)
	if len(resources.Items) == 0 {
		fmt.Printf("No ProviderProfiles found in the namespace [%s]\n", ns)
		return
	} else {
		fmt.Fprintln(writer, "NAME\tPLATFORM\tREGION\tREADY")
	}

	for _, resource := range resources.Items {
		platform, region, ready := "", "", ""

		if p, found, err := unstructured.NestedString(resource.Object, "status", "platform"); err == nil && found {
			platform = p
		}
		if r, found, err := unstructured.NestedString(resource.Object, "status", "region"); err == nil && found {
			region = r
		}

		conds, found, err := unstructured.NestedSlice(resource.Object, "status", "conditions")
		if err == nil && found {
			for _, c := range conds {
				if cm, ok := c.(map[string]interface{}); ok {
					if t, _ := cm["type"].(string); t == "Ready" {
						if s, _ := cm["status"].(string); s != "" {
							ready = s
						}
						break
					}
				}
			}
		}

		fmt.Fprintf(writer, "%s\t%s\t%s\t%s\n", resource.GetName(), platform, region, ready)
	}
	writer.Flush()
}