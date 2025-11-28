package xkube

import (
	"context"
	"fmt"
	"log"
	"os"

	"github.com/etesami/skycluster-cli/internal/utils"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

// debugf prints debug messages to stderr when debug is enabled.
func debugf(format string, args ...interface{}) {
	if debug {
		_, _ = fmt.Fprintf(os.Stderr, "DEBUG: "+format+"\n", args...)
	}
}

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
	Short: "Enable or disable interconnect mesh for xkube clusters",
	Run: func(cmd *cobra.Command, args []string) {
		enable, _ := cmd.Flags().GetBool("enable")
		disable, _ := cmd.Flags().GetBool("disable")
		podCIDR, _ := cmd.Flags().GetString("pod-cidr")
		serviceCIDR, _ := cmd.Flags().GetString("service-cidr")

		debugf("mesh command invoked: enable=%v disable=%v podCIDR=%q serviceCIDR=%q", enable, disable, podCIDR, serviceCIDR)

		if enable == disable {
			debugf("invalid flags: enable equals disable (%v)", enable)
			log.Fatalf("please specify exactly one of --enable or --disable")
			return
		}

		// namespace is empty string per your guideline
		ns := ""
		if enable {
			debugf("enabling interconnect in namespace %q", ns)
			// enable interconnect (wrap with spinner)
			if err := utils.RunWithSpinner("Enabling interconnect", func() error {
				return enableInterconnect(ns, podCIDR, serviceCIDR)
			}); err != nil {
				debugf("enableInterconnect failed: %v", err)
				log.Fatalf("error enabling mesh: %v", err)
			}

			// wait for activation and then install remote secrets
			debugf("waiting for activation and running controller")
			if err := utils.RunWithSpinner("Waiting for activation", func() error {
				c, err := NewController(viper.GetString("kubeconfig"), ns)
				if err != nil {
					debugf("NewController returned error: %v", err)
					return err
				}

				debugf("running controller")
				err = c.Run(context.Background())
				if err != nil {
					debugf("controller run returned error: %v", err)
					return err
				}

				debugf("controller run completed")
				return nil
			}); err != nil {
				debugf("post-enable controller failed: %v", err)
				log.Fatalf("error enabling mesh: %v", err)
			}

		} else {
			debugf("disabling interconnect in namespace %q", ns)
			// disable interconnect with spinner
			if err := utils.RunWithSpinner("Disabling interconnect", func() error {
				return disableInterconnect(ns)
			}); err != nil {
				debugf("disableInterconnect failed: %v", err)
				log.Fatalf("error disabling mesh: %v", err)
			}
		}
	},
}

func listXKubesExternalNames(ns string) []string {
	debugf("listXKubesExternalNames: kubeconfig=%q ns=%q", viper.GetString("kubeconfig"), ns)
	kubeconfig := viper.GetString("kubeconfig")
	dynamicClient, err := utils.GetDynamicClient(kubeconfig)
	if err != nil {
		debugf("GetDynamicClient failed: %v", err)
		return nil
	}
	debugf("dynamic client initialized")

	gvr := schema.GroupVersionResource{
		Group:    "skycluster.io",
		Version:  "v1alpha1",
		Resource: "xkubes",
	}
	ri := dynamicClient.Resource(gvr)

	resources, err := ri.List(context.Background(), metav1.ListOptions{})
	if err != nil {
		debugf("listing xkubes failed: %v", err)
		return nil
	}
	debugf("found %d xkubes", len(resources.Items))

	names := []string{}
	for _, resource := range resources.Items {
		extNames, _, err := unstructured.NestedString(resource.Object, "status", "externalClusterName")
		if err != nil {
			debugf("getting status.externalClusterName for %s failed: %v", resource.GetName(), err)
			continue
		}
		names = append(names, extNames)
		debugf("xkube %s externalClusterName=%q", resource.GetName(), extNames)
	}
	return names
}

// enableInterconnect lists all xkubes.skycluster.io objects and upserts a single
// xkubemesh (static name) whose spec.clusterNames contains all xkube metadata.names
// and whose spec.localCluster contains the provided pod/service CIDRs.
func enableInterconnect(ns string, podCIDR, serviceCIDR string) error {
	debugf("enableInterconnect: ns=%q podCIDR=%q serviceCIDR=%q", ns, podCIDR, serviceCIDR)
	kubeconfig := viper.GetString("kubeconfig")
	dyn, err := utils.GetDynamicClient(kubeconfig)
	if err != nil {
		debugf("GetDynamicClient failed: %v", err)
		return fmt.Errorf("creating dynamic client: %w", err)
	}
	debugf("dynamic client initialized")

	// GVR for xkubes
	xkubesGVR := schema.GroupVersionResource{
		Group:    "skycluster.io",
		Version:  "v1alpha1",
		Resource: "xkubes",
	}

	// list xkubes in the given namespace (empty = cluster default / all in some contexts)
	debugf("listing xkubes in namespace %q", ns)
	xkubes, err := dyn.Resource(xkubesGVR).Namespace(ns).List(context.Background(), metav1.ListOptions{})
	if err != nil {
		debugf("listing xkubes failed: %v", err)
		return fmt.Errorf("listing xkubes: %w", err)
	}
	debugf("listed %d xkubes", len(xkubes.Items))

	var clusterNames []interface{}
	for _, it := range xkubes.Items {
		// use metadata.name
		clusterNames = append(clusterNames, it.GetName())
		debugf("adding clusterName %s", it.GetName())
	}

	if len(clusterNames) == 0 {
		// You may choose to still create an empty mesh - here we create with empty list but warn.
		debugf("no xkubes found; warning and returning without creating mesh")
		fmt.Println("warning: no xkubes found; creating xkubemesh with an empty clusterNames list")
		return nil
	}

	// Build desired xkubemesh unstructured object
	meshName := "xkube-cluster-mesh"
	debugf("constructing xkubemesh %s with %d clusterNames", meshName, len(clusterNames))
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
	debugf("getting existing xkubemesh %s", meshName)
	existing, err := dyn.Resource(meshGVR).Namespace(ns).Get(ctx, meshName, metav1.GetOptions{})
	if err != nil {
		if apierrors.IsNotFound(err) {
			debugf("xkubemesh %s not found, creating", meshName)
			// Create
			_, err = dyn.Resource(meshGVR).Namespace(ns).Create(ctx, xkubemesh, metav1.CreateOptions{})
			if err != nil {
				debugf("creating xkubemesh %s failed: %v", meshName, err)
				return fmt.Errorf("creating xkubemesh %s: %w", meshName, err)
			}
			fmt.Printf("created xkubemesh/%s (clusterNames: %d)\n", meshName, len(clusterNames))
			debugf("created xkubemesh %s successfully", meshName)
			return nil
		}
		debugf("getting existing xkubemesh failed: %v", err)
		return fmt.Errorf("getting existing xkubemesh: %w", err)
	}

	debugf("xkubemesh %s exists; updating spec", meshName)
	// Update: set spec on existing and call Update
	if err := unstructured.SetNestedField(existing.Object, clusterNames, "spec", "clusterNames"); err != nil {
		debugf("setting spec.clusterNames failed: %v", err)
		return fmt.Errorf("setting spec.clusterNames: %w", err)
	}
	if err := unstructured.SetNestedField(existing.Object, podCIDR, "spec", "localCluster", "podCidr"); err != nil {
		debugf("setting spec.localCluster.podCidr failed: %v", err)
		return fmt.Errorf("setting spec.localCluster.podCidr: %w", err)
	}
	if err := unstructured.SetNestedField(existing.Object, serviceCIDR, "spec", "localCluster", "serviceCidr"); err != nil {
		debugf("setting spec.localCluster.serviceCidr failed: %v", err)
		return fmt.Errorf("setting spec.localCluster.serviceCidr: %w", err)
	}

	debugf("updating xkubemesh %s", meshName)
	_, err = dyn.Resource(meshGVR).Namespace(ns).Update(ctx, existing, metav1.UpdateOptions{})
	if err != nil {
		debugf("updating xkubemesh %s failed: %v", meshName, err)
		return fmt.Errorf("updating xkubemesh %s: %w", meshName, err)
	}
	fmt.Printf("updated xkubemesh/%s (clusterNames: %d)\n", meshName, len(clusterNames))
	debugf("updated xkubemesh %s successfully", meshName)
	return nil
}

// disableInterconnect deletes the single static xkubemesh if it exists.
func disableInterconnect(ns string) error {
	debugf("disableInterconnect: ns=%q", ns)
	kubeconfig := viper.GetString("kubeconfig")
	dyn, err := utils.GetDynamicClient(kubeconfig)
	if err != nil {
		debugf("GetDynamicClient failed: %v", err)
		return fmt.Errorf("creating dynamic client: %w", err)
	}
	debugf("dynamic client initialized")

	meshGVR := schema.GroupVersionResource{
		Group:    "skycluster.io",
		Version:  "v1alpha1",
		Resource: "xkubemeshes",
	}
	meshName := "xkube-cluster-mesh"

	ctx := context.Background()
	debugf("deleting xkubemesh %s", meshName)
	err = dyn.Resource(meshGVR).Namespace(ns).Delete(ctx, meshName, metav1.DeleteOptions{})
	if err != nil {
		if apierrors.IsNotFound(err) {
			fmt.Printf("xkubemesh/%s already deleted or not present\n", meshName)
			debugf("xkubemesh %s not found (already deleted)", meshName)
			return nil
		}
		debugf("deleting xkubemesh %s failed: %v", meshName, err)
		return fmt.Errorf("deleting xkubemesh %s: %w", meshName, err)
	}
	fmt.Printf("deleted xkubemesh/%s\n", meshName)
	debugf("deleted xkubemesh %s successfully", meshName)
	return nil
}