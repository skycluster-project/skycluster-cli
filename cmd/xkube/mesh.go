package xkube

import (
	"context"
	"fmt"
	"log"

	"github.com/etesami/skycluster-cli/internal/utils"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

// init registers the command and flags. Hook this command into your root command assembly.
func init() {
	xkubeMeshCmd.PersistentFlags().Bool("enable", false, "Enable mesh (create/update the single XkubeMesh)")
	xkubeMeshCmd.PersistentFlags().Bool("disable", false, "Disable mesh (delete the single XkubeMesh)")
	// local cluster CIDRs - user can override; defaults taken from your example
	xkubeMeshCmd.PersistentFlags().String("pod-cidr", "10.0.0.0/19", "local cluster Pod CIDR")
	xkubeMeshCmd.PersistentFlags().String("service-cidr", "10.0.32.0/19", "local cluster Service CIDR")
}

// xkubeMeshCmd implements `xkube mesh --enable|--disable`
var xkubeMeshCmd = &cobra.Command{
	Use:   "mesh",
	Short: "Create/Update/Delete the single XkubeMesh that references all Xkubes in the cluster",
	Run: func(cmd *cobra.Command, args []string) {
		enable, _ := cmd.Flags().GetBool("enable")
		disable, _ := cmd.Flags().GetBool("disable")
		podCIDR, _ := cmd.Flags().GetString("pod-cidr")
		serviceCIDR, _ := cmd.Flags().GetString("service-cidr")

		if enable == disable {
			log.Fatalf("please specify exactly one of --enable or --disable")
			return
		}

		// namespace is empty string per your guideline
		ns := ""

		if enable {
			if err := enableInterconnect(ns, podCIDR, serviceCIDR); err != nil {
				log.Fatalf("error enabling mesh: %v", err)
			}
		} else {
			if err := disableInterconnect(ns); err != nil {
				log.Fatalf("error disabling mesh: %v", err)
			}
		}
	},
}

// enableInterconnect lists all xkubes.skycluster.io objects and upserts a single
// xkubemesh (static name) whose spec.clusterNames contains all xkube metadata.names
// and whose spec.localCluster contains the provided pod/service CIDRs.
func enableInterconnect(ns string, podCIDR, serviceCIDR string) error {
	kubeconfig := viper.GetString("kubeconfig")
	dyn, err := utils.GetDynamicClient(kubeconfig)
	if err != nil {
		return fmt.Errorf("creating dynamic client: %w", err)
	}

	// GVR for xkubes
	xkubesGVR := schema.GroupVersionResource{
		Group:    "skycluster.io",
		Version:  "v1alpha1",
		Resource: "xkubes",
	}

	// list xkubes in the given namespace (empty = cluster default / all in some contexts)
	xkubes, err := dyn.Resource(xkubesGVR).Namespace(ns).List(context.Background(), metav1.ListOptions{})
	if err != nil {
		return fmt.Errorf("listing xkubes: %w", err)
	}

	var clusterNames []interface{}
	for _, it := range xkubes.Items {
		// use metadata.name
		clusterNames = append(clusterNames, it.GetName())
	}

	if len(clusterNames) == 0 {
		// You may choose to still create an empty mesh - here we create with empty list but warn.
		fmt.Println("warning: no xkubes found; creating xkubemesh with an empty clusterNames list")
		return nil
	}

	// Build desired xkubemesh unstructured object
	meshName := "xkube-cluster-mesh"
	xkubemesh := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "skycluster.io/v1alpha1",
			"kind":       "XKubeMesh",
			"metadata": map[string]interface{}{
				"name": meshName,
			},
			"spec": map[string]interface{}{
				// clusterNames is an array of strings
				"clusterNames": clusterNames,
				"localCluster": map[string]interface{}{
					"podCidr":     podCIDR,
					"serviceCidr": serviceCIDR,
				},
			},
		},
	}

	// GVR for xkubemeshes
	meshGVR := schema.GroupVersionResource{
		Group:    "skycluster.io",
		Version:  "v1alpha1",
		Resource: "xkubemeshes",
	}

	// Try to get existing object
	ctx := context.Background()
	existing, err := dyn.Resource(meshGVR).Namespace(ns).Get(ctx, meshName, metav1.GetOptions{})
	if err != nil {
		if apierrors.IsNotFound(err) {
			// Create
			_, err = dyn.Resource(meshGVR).Namespace(ns).Create(ctx, xkubemesh, metav1.CreateOptions{})
			if err != nil {
				return fmt.Errorf("creating xkubemesh %s: %w", meshName, err)
			}
			fmt.Printf("created xkubemesh/%s (clusterNames: %d)\n", meshName, len(clusterNames))
			return nil
		}
		return fmt.Errorf("getting existing xkubemesh: %w", err)
	}

	// Update: set spec on existing and call Update
	if err := unstructured.SetNestedField(existing.Object, clusterNames, "spec", "clusterNames"); err != nil {
		return fmt.Errorf("setting spec.clusterNames: %w", err)
	}
	if err := unstructured.SetNestedField(existing.Object, podCIDR, "spec", "localCluster", "podCidr"); err != nil {
		return fmt.Errorf("setting spec.localCluster.podCidr: %w", err)
	}
	if err := unstructured.SetNestedField(existing.Object, serviceCIDR, "spec", "localCluster", "serviceCidr"); err != nil {
		return fmt.Errorf("setting spec.localCluster.serviceCidr: %w", err)
	}

	_, err = dyn.Resource(meshGVR).Namespace(ns).Update(ctx, existing, metav1.UpdateOptions{})
	if err != nil {
		return fmt.Errorf("updating xkubemesh %s: %w", meshName, err)
	}
	fmt.Printf("updated xkubemesh/%s (clusterNames: %d)\n", meshName, len(clusterNames))
	return nil
}

// disableInterconnect deletes the single static xkubemesh if it exists.
func disableInterconnect(ns string) error {
	kubeconfig := viper.GetString("kubeconfig")
	dyn, err := utils.GetDynamicClient(kubeconfig)
	if err != nil {
		return fmt.Errorf("creating dynamic client: %w", err)
	}

	meshGVR := schema.GroupVersionResource{
		Group:    "skycluster.io",
		Version:  "v1alpha1",
		Resource: "xkubemeshes",
	}
	meshName := "xkube-cluster-mesh"

	ctx := context.Background()
	err = dyn.Resource(meshGVR).Namespace(ns).Delete(ctx, meshName, metav1.DeleteOptions{})
	if err != nil {
		if apierrors.IsNotFound(err) {
			fmt.Printf("xkubemesh/%s already deleted or not present\n", meshName)
			return nil
		}
		return fmt.Errorf("deleting xkubemesh %s: %w", meshName, err)
	}
	fmt.Printf("deleted xkubemesh/%s\n", meshName)
	return nil
}