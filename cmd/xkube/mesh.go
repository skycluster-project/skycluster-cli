package xkube

import (
	"context"
	"fmt"
	"log"
	"slices"
	"strings"

	"github.com/etesami/skycluster-cli/internal/utils"
	apiextv1 "k8s.io/apiextensions-apiserver/pkg/client/clientset/clientset"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
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
			// best-effort cleanup of prior installations with progress indicator
			runWithSpinner("Cleaning up prior installations", func() error {
				performCleanup()
				return nil 
			})

			// enable interconnect (wrap with spinner)
			if err := runWithSpinner("Enabling interconnect", func() error {
				return enableInterconnect(ns, podCIDR, serviceCIDR)
			}); err != nil {
				log.Fatalf("error enabling mesh: %v", err)
			}
		} else {
			// disable interconnect with spinner
			if err := runWithSpinner("Disabling interconnect", func() error {
				return disableInterconnect(ns)
			}); err != nil {
				log.Fatalf("error disabling mesh: %v", err)
			}
		}
	},
}


func performCleanup() {
	// local management cluster
	kubeconfig := viper.GetString("kubeconfig")
	cs, err1 := utils.GetClientset(kubeconfig)	
	csExt, err2 := utils.GetClientsetExtended(kubeconfig)
	if err1 == nil && err2 == nil {
		cleanupChart(cs, csExt)
	}

	dyn, err := utils.GetDynamicClient(kubeconfig)
	if err == nil {
		deleteSubmarinerEndpointsNotMatchingClusterID(context.Background(), dyn)
	}

	// remote clusters
	xkubesNames := listXKubesNames("")
	cleanupKubeconfigSecrets(context.Background(), cs)

	for _, name := range xkubesNames {
		log.Printf("Preparing on xkube %s\n", name)
		kConfig, err := getConfig(name, "")
		if err != nil {
			fmt.Printf("warning getting kubeconfig for xkube %s: %v\n", name, err)
			continue
		}
		cs, err1 := utils.GetClientsetFromString(kConfig)
		_, err2 := utils.GetClientsetExtendedFromString(kConfig)
		if err1 != nil || err2 != nil {
			fmt.Printf("warning creating clientset for xkube %s: %v %v\n", name, err1, err2)
			continue
		}
		// cleanupChart(cs, csExt)

		dyn, err := utils.GetDynamicClientFromString(kConfig)
		if err != nil {
			fmt.Printf("warning creating dynamic client for xkube %s: %v\n", name, err)
			continue
		}
		deleteSubmariner(context.Background(), dyn)
		cleanupSubmarinerDaemonSets(context.Background(), cs)
	}
}

func cleanupChart(cs *kubernetes.Clientset, csExt *apiextv1.Clientset) error {
	// ChartSpec represents the static chart metadata you provided.
	type ChartSpec struct {
		Label       string
		Version     string
		Repo        string
		Name        string
		Namespace   string
		BlockingObj string // space-separated "Kind/name"
		PrefixObj   string
	}
	
	// Static definitions based on your input
	var chartsToCleanup []ChartSpec
	
	// submariner
	subm := ChartSpec{
		Label:       "subm",
		Version:     "0.20.1",
		Repo:        "https://submariner-io.github.io/submariner-charts/charts",
		Name:        "submariner-operator",
		Namespace:   "submariner-operator",
		BlockingObj: "Submariner/submariner",
		PrefixObj:   "submariner",
	}
	
	// istio: produce blocking objects list for "base" and "istiod"
	istioBlockingCRDs := []string{
		"wasmplugins.extensions.istio.io",
		"destinationrules.networking.istio.io",
		"envoyfilters.networking.istio.io",
		"gateways.networking.istio.io",
		"proxyconfigs.networking.istio.io",
		"serviceentries.networking.istio.io",
		"sidecars.networking.istio.io",
		"virtualservices.networking.istio.io",
		"workloadentries.networking.istio.io",
		"authorizationpolicies.security.istio.io",
		"peerauthentications.security.istio.io",
		"requestauthentications.security.istio.io",
		"telemetries.telemetry.istio.io",
	}
	// build space-separated "CustomResourceDefinition/<name>" list
	var crdList []string
	for _, s := range istioBlockingCRDs {
		crdList = append(crdList, fmt.Sprintf("CustomResourceDefinition/%s", s))
	}
	crdBlockingStr := strings.Join(crdList, " ")
	
	// Two istio charts: base and istiod
	istioBase := ChartSpec{
		Label:       "base",
		Version:     "1.27.0",
		Repo:        "https://istio-release.storage.googleapis.com/charts",
		Name:        "base",
		Namespace:   "istio-system",
		BlockingObj: crdBlockingStr,
		PrefixObj:   "istio",
	}
	istiod := ChartSpec{
		Label:       "istiod",
		Version:     "1.27.0",
		Repo:        "https://istio-release.storage.googleapis.com/charts",
		Name:        "istiod",
		Namespace:   "istio-system",
		BlockingObj: crdBlockingStr, // same CRDs are relevant
		PrefixObj:   "istio",
	}

	chartsToCleanup = []ChartSpec{subm, istioBase, istiod}
	for _, ch := range chartsToCleanup {
		if ch.Name == "istiod" {
			_ = deleteIstioReaderServiceAccount(context.Background(), cs)
		}
		deleteClusterRolesByPrefix(context.Background(), cs, ch.PrefixObj)
		deleteClusterRoleBindingsByPrefix(context.Background(), cs, ch.PrefixObj)
		deleteCRDsForChart(context.Background(), csExt, ch.Name)
	}
	return nil
}

func deleteIstioReaderServiceAccount(ctx context.Context, cs *kubernetes.Clientset) error {
	type svcAcc struct {
		Namespace string
		Name      string
	}
	svcAccs := []svcAcc{
		{
			Namespace: "istio-system",
			Name:      "istio-reader-service-account",
		},
		{
			Namespace: "",
			Name:      "istio-reader-clusterrole-istio-system",
		},
	}
	for _, sa := range svcAccs {

		// ---- 1. Best-effort normal delete ----
		_ = cs.CoreV1().ServiceAccounts(sa.Namespace).Delete(ctx, sa.Name, metav1.DeleteOptions{})

		// ---- 2. Check if still exists ----
		sa, err := cs.CoreV1().ServiceAccounts(sa.Namespace).Get(ctx, sa.Name, metav1.GetOptions{})
		if apierrors.IsNotFound(err) {
			return nil
		}

		// ---- 3. Remove finalizers if any ----
		if len(sa.Finalizers) > 0 {
			sa.Finalizers = []string{}
			_, _ = cs.CoreV1().ServiceAccounts(sa.Namespace).Update(ctx, sa, metav1.UpdateOptions{})
		}

		// ---- 4. Delete again ----
		_ = cs.CoreV1().ServiceAccounts(sa.Namespace).Delete(ctx, sa.Name, metav1.DeleteOptions{})
		// ---- 5. Force delete if still present ----
		_, err = cs.CoreV1().ServiceAccounts(sa.Namespace).Get(ctx, sa.Name, metav1.GetOptions{})
		if err == nil {
			fmt.Printf("Force deleting %s/%s\n", sa.Namespace, sa.Name)
			zero := int64(0)
			_ = cs.CoreV1().ServiceAccounts(sa.Namespace).Delete(ctx, sa.Name, metav1.DeleteOptions{
				GracePeriodSeconds: &zero,
			})
		}
	}

	return nil
}

// deleteClusterRolesByPrefix deletes clusterroles whose name starts with prefix.
func deleteClusterRolesByPrefix(ctx context.Context, cs *kubernetes.Clientset, prefix string) error {
	if prefix == "" {return nil}

	crList, err := cs.RbacV1().ClusterRoles().List(ctx, metav1.ListOptions{})
	if err != nil {return nil}

	for _, cr := range crList.Items {
		if strings.HasPrefix(cr.Name, prefix) {
			_ = cs.RbacV1().ClusterRoles().Delete(ctx, cr.Name, metav1.DeleteOptions{})
		}
	}
	return nil
}

// deleteClusterRoleBindingsByPrefix deletes ClusterRoleBindings whose name starts with prefix.
// It tries normal delete, patches finalizers if necessary, deletes again, and as last resort force deletes.
func deleteClusterRoleBindingsByPrefix(ctx context.Context, cs *kubernetes.Clientset, prefix string) error {
	if prefix == "" {return nil}

	crbList, err := cs.RbacV1().ClusterRoleBindings().List(ctx, metav1.ListOptions{})
	if err != nil {return nil}

	toDelete := []string{}
	for _, crb := range crbList.Items {
		if strings.HasPrefix(crb.Name, prefix) {
			toDelete = append(toDelete, crb.Name)
		}
	}

	if len(toDelete) == 0 {return nil}

	for _, name := range toDelete {
		_ = cs.RbacV1().ClusterRoleBindings().Delete(ctx, name, metav1.DeleteOptions{})

		// If it lingers, remove finalizers then delete again
		crb, err := cs.RbacV1().ClusterRoleBindings().Get(ctx, name, metav1.GetOptions{})
		if err == nil && len(crb.Finalizers) > 0 {
			crb.Finalizers = []string{}
			_, _ = cs.RbacV1().ClusterRoleBindings().Update(ctx, crb, metav1.UpdateOptions{})
			_ = cs.RbacV1().ClusterRoleBindings().Delete(ctx, name, metav1.DeleteOptions{})
		}

		// Last resort force delete
		_, err = cs.RbacV1().ClusterRoleBindings().Get(ctx, name, metav1.GetOptions{})
		if err == nil {
			fmt.Printf("Force deleting clusterrolebinding/%s\n", name)
			zero := int64(0)
			_ = cs.RbacV1().ClusterRoleBindings().Delete(ctx, name, metav1.DeleteOptions{
				GracePeriodSeconds: &zero,
			})
		}
	}

	return nil
}

// deleteCRDsForChart deletes CRDs 
// if chartName == "base", match CRDs whose spec.group contains "istio".
func deleteCRDsForChart(ctx context.Context, apiExtClient *apiextv1.Clientset, chartName string) error {
	if chartName != "base" {return nil}

	pattern := "istio"

	crdList, err := apiExtClient.ApiextensionsV1().CustomResourceDefinitions().List(ctx, metav1.ListOptions{})
	if err != nil {return nil}

	matched := []string{}
	for _, crd := range crdList.Items {
		if strings.Contains(crd.Spec.Group, pattern) {
			matched = append(matched, crd.Name)
		}
	}

	if len(matched) == 0 {return nil}
	for _, crdName := range matched {
		_ = apiExtClient.ApiextensionsV1().CustomResourceDefinitions().Delete(ctx, crdName, metav1.DeleteOptions{})
	}

	return nil
}

func deleteSubmarinerEndpointsNotMatchingClusterID(ctx context.Context, dyn dynamic.Interface) error {

	clusterIDtoSkip := "broker-skycluster"
	gvrs := []schema.GroupVersionResource{
		{
			Group:    "submariner.io",
			Version:  "v1",
			Resource: "endpoints", // plural resource name of the CRD
		},
		{
			Group:    "submariner.io",
			Version:  "v1",
			Resource: "clusters", // plural resource name of the CRD
		},
	}
	
	for _, gvr := range gvrs {
		
		// List across all namespaces (works for both namespaced and cluster-scoped resources)
		ns := "skycluster-system"
		list, err := dyn.Resource(gvr).Namespace(ns).List(ctx, metav1.ListOptions{})
		if err != nil {return err}
	
		for _, item := range list.Items {
			labels := item.GetLabels()
			if val, ok := labels["submariner-io/clusterID"]; ok && val == clusterIDtoSkip {
				// keep endpoints that match the desired clusterID
				continue
			}
	
			name := item.GetName()
			loc := name
			if ns != "" {loc = ns + "/" + name}
	
			var res dynamic.ResourceInterface
			if ns == "" {
				res = dyn.Resource(gvr)
			} else {
				res = dyn.Resource(gvr).Namespace(ns)
			}
	
			// 1. Best-effort normal delete
			_ = res.Delete(ctx, name, metav1.DeleteOptions{})
	
			// 2. Check if still exists
			obj, err := res.Get(ctx, name, metav1.GetOptions{})
			if apierrors.IsNotFound(err) {continue}
	
			// 3. Remove finalizers if any
			if err == nil && len(obj.GetFinalizers()) > 0 {
				obj.SetFinalizers([]string{})
				_, _ = res.Update(ctx, obj, metav1.UpdateOptions{})
			}
	
			// 4. Delete again
			_ = res.Delete(ctx, name, metav1.DeleteOptions{})
	
			// 5. Force delete if still present
			_, err = res.Get(ctx, name, metav1.GetOptions{})
			if err == nil {
				fmt.Printf("Force deleting submariner endpoint %s\n", loc)
				zero := int64(0)
				_ = res.Delete(ctx, name, metav1.DeleteOptions{
					GracePeriodSeconds: &zero,
				})
			}
		}
	}

	return nil
}

func cleanupSubmarinerDaemonSets(ctx context.Context, cs *kubernetes.Clientset) error {
	dsNames := []string{
		"submariner-gateway",
		"submariner-routeagent",
		"submariner-lighthouse-agent",
		"submariner-lighthouse-coredns",
		"submariner-metrics-proxy",
	}
	ns := "submariner-operator"

	for _, name := range dsNames {
		// 1. Best-effort normal delete
		_ = cs.AppsV1().DaemonSets(ns).Delete(ctx, name, metav1.DeleteOptions{})
	}

	return nil
}

func listXKubesExternalNames(ns string) []string {
	kubeconfig := viper.GetString("kubeconfig")
	dynamicClient, err := utils.GetDynamicClient(kubeconfig)
	if err != nil {
		return nil
	}

	gvr := schema.GroupVersionResource{
		Group:    "skycluster.io",
		Version:  "v1alpha1", 
		Resource: "xkubes",
	}
	ri := dynamicClient.Resource(gvr)
	
	resources, err := ri.List(context.Background(), metav1.ListOptions{})
	if err != nil {return nil}

	names := []string{}
	for _, resource := range resources.Items {
		extNames, _, err := unstructured.NestedString(resource.Object, "status", "externalClusterName")
		if err != nil {continue}
		names = append(names, extNames)
	}
	return names
}

func cleanupKubeconfigSecrets(ctx context.Context, cs *kubernetes.Clientset) error {
	secretList, err := cs.CoreV1().Secrets("skycluster-system").List(ctx, metav1.ListOptions{
		LabelSelector: "skycluster.io/secret-type=static-kubeconfig",
	})
	if err != nil {return err}

	extNames := listXKubesNames("")

	for _, secret := range secretList.Items {

		// if there is an existing xkube with this cluster-id, skip deletion
		if slices.Contains(extNames, secret.Labels["skycluster.io/cluster-id"]) {continue}
		
		// 1. Best-effort normal delete
		_ = cs.CoreV1().Secrets("skycluster-system").Delete(ctx, secret.Name, metav1.DeleteOptions{})
	}

	return nil
}

func deleteSubmariner(ctx context.Context, dyn dynamic.Interface) error {

	gvrs := []schema.GroupVersionResource{
		{
			Group:    "submariner.io",
			Version:  "v1alpha1",
			Resource: "submariners",
		},
	}
	
	for _, gvr := range gvrs {
		
		list, err := dyn.Resource(gvr).Namespace("submariner-operator").List(ctx, metav1.ListOptions{})
		if err != nil {
			return err
		}
	
		for _, item := range list.Items {
			
			name := item.GetName()	
			// 1. Best-effort normal delete
			dyn.Resource(gvr).Namespace("submariner-operator").Delete(ctx, name, metav1.DeleteOptions{})
	
			// 2. Check if still exists
			obj, err := dyn.Resource(gvr).Namespace("submariner-operator").Get(ctx, name, metav1.GetOptions{})
			if apierrors.IsNotFound(err) {
				continue
			}
	
			// 3. Remove finalizers if any
			if err == nil && len(obj.GetFinalizers()) > 0 {
				obj.SetFinalizers([]string{})
				_, _ = dyn.Resource(gvr).Namespace("submariner-operator").Update(ctx, obj, metav1.UpdateOptions{})
			}
	
			// 4. Delete again
			_ = dyn.Resource(gvr).Namespace("submariner-operator").Delete(ctx, name, metav1.DeleteOptions{})
	
			// 5. Force delete if still present
			_, err = dyn.Resource(gvr).Namespace("submariner-operator").Get(ctx, name, metav1.GetOptions{})
			if err == nil {
				fmt.Printf("Force deleting submariner endpoint %s\n", name)
				zero := int64(0)
				_ = dyn.Resource(gvr).Namespace("submariner-operator").Delete(ctx, name, metav1.DeleteOptions{
					GracePeriodSeconds: &zero,
				})
			}
		}
	}

	return nil
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