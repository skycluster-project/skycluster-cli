package cleanup

import (
	"context"
	"fmt"
	"log"
	"os"
	"slices"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	apiextv1 "k8s.io/apiextensions-apiserver/pkg/client/clientset/clientset"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"

	xk "github.com/etesami/skycluster-cli/cmd/xkube"
	"github.com/etesami/skycluster-cli/internal/utils"
)

const namespace = "skycluster-system"

var secretsToDelete = []string{
	"skycluster-kubeconfig",
	"skycluster-keys",
}

type clientSets struct {
	dynamicClient dynamic.Interface
	clientSet     *kubernetes.Clientset
}

// debug controls debug output; can be enabled by tests or callers.
var debug bool

// debugf prints debug messages to stderr when debug is enabled.
func debugf(format string, args ...interface{}) {
	if debug {
		_, _ = fmt.Fprintf(os.Stderr, "DEBUG: "+format+"\n", args...)
	}
}

func init() {
	// no flags for now; kept for symmetry/extension
}

func GetCleanupCmd() *cobra.Command {
	return cleanupCmd
}

func SetDebug(d bool) {
	debug = d
}

var cleanupCmd = &cobra.Command{
	Use:   "cleanup",
	Short: "Cleans up skycluster-related secrets and pods from the cluster(s)",
	Run: func(cmd *cobra.Command, args []string) {

		kubeconfigPath := viper.GetString("kubeconfig")
		debugf("cleanup invoked with kubeconfig=%q", kubeconfigPath)
		clientset, err1 := utils.GetClientset(kubeconfigPath)
		dyn, err2 := utils.GetDynamicClient(kubeconfigPath)
		if err1 != nil || err2 != nil {
			debugf("error creating clients: clientsetErr=%v dynamicErr=%v", err1, err2)
			_ = fmt.Errorf("failed to create kubernetes client")
		}

		localClientSets := &clientSets{
			dynamicClient: dyn,
			clientSet:     clientset,
		}

		// best-effort cleanup of prior installations with progress indicator
		debugf("starting preCleanup (overlay)")
		utils.RunWithSpinner("Cleaning up prior configurations (overlay)", func() error {
			_ = preCleanup(localClientSets) // best-effort; ignore errors
			return nil
		})

		// best-effort cleanup istio
		debugf("starting performIstioCleanup")
		utils.RunWithSpinner("Cleaning up prior configurations (istio)", func() error {
			performIstioCleanup() // best-effort; ignore errors
			return nil
		})

		debugf("cleanup command completed")
	},
}

func preCleanup(clientSets *clientSets) error {
	ctx := context.Background()
	var errs []string

	clientSet := clientSets.clientSet
	debugf("preCleanup: clientSet present=%v dynamicClient present=%v", clientSets.clientSet != nil, clientSets.dynamicClient != nil)

	for _, name := range secretsToDelete {
		debugf("preCleanup: attempting delete secret %s/%s", namespace, name)
		if err := deleteSecretIfExists(ctx, clientSet, namespace, name); err != nil {
			debugf("preCleanup: delete secret %s failed: %v", name, err)
			errs = append(errs, fmt.Sprintf("secret %s: %v", name, err))
		}
	}

	label := "skycluster.io/job-type"
	labelValue := "istio-ca-certs"
	debugf("preCleanup: deleting pods with label %s=%s", label, labelValue)
	if err := deletePodsWithLabel(ctx, clientSet, namespace, label, labelValue); err != nil {
		debugf("preCleanup: delete pods failed: %v", err)
		errs = append(errs, fmt.Sprintf("pods: %v", err))
	}

	labelValue = "headscale-cert-gen"
	debugf("preCleanup: deleting pods with label %s=%s", label, labelValue)
	if err := deletePodsWithLabel(ctx, clientSet, namespace, label, labelValue); err != nil {
		debugf("preCleanup: delete pods failed: %v", err)
		errs = append(errs, fmt.Sprintf("pods: %v", err))
	}

	submNs := "submariner-operator"
	debugf("preCleanup: deleting namespace %s", submNs)
	// finally, delete the namespace itself
	if err := deleteNamespace(ctx, clientSet, submNs); err != nil {
		debugf("preCleanup: delete namespace %s failed: %v", submNs, err)
		errs = append(errs, fmt.Sprintf("namespace: %v", err))
	}
	// remove submariners.submainer.io objects if any
	debugf("preCleanup: deleting submariner objects")
	if err := deleteSubmariner(ctx, clientSets.dynamicClient); err != nil {
		debugf("preCleanup: deleteSubmariner failed: %v", err)
		errs = append(errs, fmt.Sprintf("submariner objects: %v", err))
	}

	if len(errs) > 0 {
		debugf("preCleanup encountered errors: %v", errs)
		_ = fmt.Errorf("errors during cleanup: %s", strings.Join(errs, "; "))
	} else {
		fmt.Println("Requested secrets and matching pods removed (or already absent).")
		debugf("preCleanup completed with no errors")
	}
	return nil
}

// deleteSecretIfExists deletes the given secret in the provided namespace.
// If the secret does not exist, it is treated as success.
func deleteSecretIfExists(ctx context.Context, clientset *kubernetes.Clientset, ns, name string) error {
	svc := clientset.CoreV1().Secrets(ns)
	debugf("deleteSecretIfExists: deleting %s/%s", ns, name)
	err := svc.Delete(ctx, name, metav1.DeleteOptions{})
	if err == nil {
		fmt.Printf("Deleted secret %s/%s\n", ns, name)
		debugf("deleteSecretIfExists: deleted %s/%s", ns, name)
		return nil
	}
	if apierrors.IsNotFound(err) {
		fmt.Printf("Secret %s/%s not found; skipping\n", ns, name)
		debugf("deleteSecretIfExists: secret %s/%s not found", ns, name)
		return nil
	}
	debugf("deleteSecretIfExists: delete failed for %s/%s: %v", ns, name, err)
	return fmt.Errorf("delete failed: %w", err)
}

// deletePodsWithLabel finds pods in the namespace matching labelKey=labelValue and deletes them.
// If none found, it's treated as success.
func deletePodsWithLabel(ctx context.Context, clientset *kubernetes.Clientset, ns, labelKey, labelValue string) error {
	labelSelector := fmt.Sprintf("%s=%s", labelKey, labelValue)
	debugf("deletePodsWithLabel: listing pods in %s with selector %s", ns, labelSelector)
	pods, err := clientset.CoreV1().Pods(ns).List(ctx, metav1.ListOptions{LabelSelector: labelSelector})
	if err != nil {
		debugf("deletePodsWithLabel: listing pods failed: %v", err)
		return fmt.Errorf("listing pods failed: %w", err)
	}
	if len(pods.Items) == 0 {
		fmt.Printf("No pods found in %s with label %s\n", ns, labelSelector)
		debugf("deletePodsWithLabel: no pods found for selector %s", labelSelector)
		return nil
	}

	var errs []string
	for _, p := range pods.Items {
		debugf("deletePodsWithLabel: deleting pod %s/%s", ns, p.Name)
		err := clientset.CoreV1().Pods(ns).Delete(ctx, p.Name, metav1.DeleteOptions{})
		if err == nil {
			fmt.Printf("Deleted pod %s/%s\n", ns, p.Name)
			continue
		}
		if apierrors.IsNotFound(err) {
			fmt.Printf("Pod %s/%s not found; skipping\n", ns, p.Name)
			continue
		}
		debugf("deletePodsWithLabel: deleting pod %s failed: %v", p.Name, err)
		errs = append(errs, fmt.Sprintf("%s: %v", p.Name, err))
	}

	if len(errs) > 0 {
		debugf("deletePodsWithLabel: encountered errors: %v", errs)
		return fmt.Errorf("errors deleting pods: %s", strings.Join(errs, "; "))
	}
	debugf("deletePodsWithLabel: completed successfully for selector %s", labelSelector)
	return nil
}

func deleteNamespace(ctx context.Context, clientset *kubernetes.Clientset, ns string) error {
	debugf("deleteNamespace: deleting namespace %s", ns)
	err := clientset.CoreV1().Namespaces().Delete(ctx, ns, metav1.DeleteOptions{})
	if err != nil {
		debugf("deleteNamespace: failed deleting namespace %s: %v", ns, err)
		return fmt.Errorf("failed to delete namespace %s: %w", ns, err)
	}
	fmt.Printf("Deleted namespace %s\n", ns)
	debugf("deleteNamespace: deleted namespace %s", ns)
	return nil
}

// Istio cleanup stuff
func performIstioCleanup() {
	debugf("performIstioCleanup: starting")
	// local management cluster
	kubeconfig := viper.GetString("kubeconfig")
	debugf("performIstioCleanup: kubeconfig=%q", kubeconfig)
	cs, err1 := utils.GetClientset(kubeconfig)
	csExt, err2 := utils.GetClientsetExtended(kubeconfig)
	if err1 == nil && err2 == nil {
		debugf("performIstioCleanup: cleaning up chart on management cluster")
		_ = cleanupChart(cs, csExt)
	} else {
		debugf("performIstioCleanup: skipping cleanupChart on management cluster, client errors: %v %v", err1, err2)
	}

	dyn, err := utils.GetDynamicClient(kubeconfig)
	if err == nil {
		debugf("performIstioCleanup: deleting submariner endpoints not matching cluster ID")
		_ = deleteSubmarinerEndpointsNotMatchingClusterID(context.Background(), dyn)
	} else {
		debugf("performIstioCleanup: skipped submariner endpoint cleanup: %v", err)
	}

	// remote clusters
	xkubesNames := xk.ListXKubesNames("")
	debugf("performIstioCleanup: found remote xkubes: %v", xkubesNames)
	cleanupKubeconfigSecrets(context.Background(), cs)

	for _, name := range xkubesNames {
		log.Printf("Preparing on xkube %s\n", name)
		kConfig, err := xk.GetConfig(name, "")
		if err != nil {
			fmt.Printf("warning getting kubeconfig for xkube %s: %v\n", name, err)
			debugf("performIstioCleanup: GetConfig failed for %s: %v", name, err)
			continue
		}
		cs, err1 := utils.GetClientsetFromString(kConfig)
		_, err2 := utils.GetClientsetExtendedFromString(kConfig)
		if err1 != nil || err2 != nil {
			fmt.Printf("warning creating clientset for xkube %s: %v %v\n", name, err1, err2)
			debugf("performIstioCleanup: clientset creation failed for %s: %v %v", name, err1, err2)
			continue
		}
		// cleanupChart(cs, csExt)

		dyn, err := utils.GetDynamicClientFromString(kConfig)
		if err != nil {
			fmt.Printf("warning creating dynamic client for xkube %s: %v\n", name, err)
			debugf("performIstioCleanup: dynamic client creation failed for %s: %v", name, err)
			continue
		}
		_ = deleteSubmariner(context.Background(), dyn)
		_ = cleanupSubmarinerDaemonSets(context.Background(), cs)
	}
	debugf("performIstioCleanup: completed")
}

func cleanupChart(cs *kubernetes.Clientset, csExt *apiextv1.Clientset) error {
	debugf("cleanupChart: starting")
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
		debugf("cleanupChart: processing chart %s (namespace=%s)", ch.Name, ch.Namespace)
		if ch.Name == "istiod" {
			_ = deleteIstioReaderServiceAccount(context.Background(), cs)
		}
		_ = deleteClusterRolesByPrefix(context.Background(), cs, ch.PrefixObj)
		_ = deleteClusterRoleBindingsByPrefix(context.Background(), cs, ch.PrefixObj)
		_ = deleteCRDsForChart(context.Background(), csExt, ch.Name)
	}
	debugf("cleanupChart: completed")
	return nil
}

func deleteIstioReaderServiceAccount(ctx context.Context, cs *kubernetes.Clientset) error {
	debugf("deleteIstioReaderServiceAccount: starting")
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
		saObj, err := cs.CoreV1().ServiceAccounts(sa.Namespace).Get(ctx, sa.Name, metav1.GetOptions{})
		if apierrors.IsNotFound(err) {
			debugf("serviceaccount %s/%s not found", sa.Namespace, sa.Name)
			return nil
		}
		if err != nil {
			debugf("error getting serviceaccount %s/%s: %v", sa.Namespace, sa.Name, err)
			continue
		}

		// ---- 3. Remove finalizers if any ----
		if len(saObj.Finalizers) > 0 {
			debugf("removing finalizers from %s/%s", saObj.Namespace, saObj.Name)
			saObj.Finalizers = []string{}
			_, _ = cs.CoreV1().ServiceAccounts(sa.Namespace).Update(ctx, saObj, metav1.UpdateOptions{})
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

	debugf("deleteIstioReaderServiceAccount: completed")
	return nil
}

// deleteClusterRolesByPrefix deletes clusterroles whose name starts with prefix.
func deleteClusterRolesByPrefix(ctx context.Context, cs *kubernetes.Clientset, prefix string) error {
	debugf("deleteClusterRolesByPrefix: prefix=%q", prefix)
	if prefix == "" {
		return nil
	}

	crList, err := cs.RbacV1().ClusterRoles().List(ctx, metav1.ListOptions{})
	if err != nil {
		debugf("list clusterroles failed: %v", err)
		return nil
	}

	for _, cr := range crList.Items {
		if strings.HasPrefix(cr.Name, prefix) {
			debugf("deleting clusterrole %s", cr.Name)
			_ = cs.RbacV1().ClusterRoles().Delete(ctx, cr.Name, metav1.DeleteOptions{})
		}
	}
	debugf("deleteClusterRolesByPrefix: completed for prefix=%q", prefix)
	return nil
}

// deleteClusterRoleBindingsByPrefix deletes ClusterRoleBindings whose name starts with prefix.
// It tries normal delete, patches finalizers if necessary, deletes again, and as last resort force deletes.
func deleteClusterRoleBindingsByPrefix(ctx context.Context, cs *kubernetes.Clientset, prefix string) error {
	debugf("deleteClusterRoleBindingsByPrefix: prefix=%q", prefix)
	if prefix == "" {
		return nil
	}

	crbList, err := cs.RbacV1().ClusterRoleBindings().List(ctx, metav1.ListOptions{})
	if err != nil {
		debugf("list clusterrolebindings failed: %v", err)
		return nil
	}

	toDelete := []string{}
	for _, crb := range crbList.Items {
		if strings.HasPrefix(crb.Name, prefix) {
			toDelete = append(toDelete, crb.Name)
		}
	}

	if len(toDelete) == 0 {
		debugf("no clusterrolebindings to delete for prefix=%q", prefix)
		return nil
	}

	for _, name := range toDelete {
		debugf("deleting clusterrolebinding %s", name)
		_ = cs.RbacV1().ClusterRoleBindings().Delete(ctx, name, metav1.DeleteOptions{})

		// If it lingers, remove finalizers then delete again
		crb, err := cs.RbacV1().ClusterRoleBindings().Get(ctx, name, metav1.GetOptions{})
		if err == nil && len(crb.Finalizers) > 0 {
			debugf("removing finalizers from clusterrolebinding %s", name)
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

	debugf("deleteClusterRoleBindingsByPrefix: completed for prefix=%q", prefix)
	return nil
}

// deleteCRDsForChart deletes CRDs 
// if chartName == "base", match CRDs whose spec.group contains "istio".
func deleteCRDsForChart(ctx context.Context, apiExtClient *apiextv1.Clientset, chartName string) error {
	debugf("deleteCRDsForChart: chartName=%q", chartName)
	if chartName != "base" {
		debugf("deleteCRDsForChart: skipping since chartName != base")
		return nil
	}

	pattern := "istio"

	crdList, err := apiExtClient.ApiextensionsV1().CustomResourceDefinitions().List(ctx, metav1.ListOptions{})
	if err != nil {
		debugf("list CRDs failed: %v", err)
		return nil
	}

	matched := []string{}
	for _, crd := range crdList.Items {
		if strings.Contains(crd.Spec.Group, pattern) {
			matched = append(matched, crd.Name)
		}
	}

	if len(matched) == 0 {
		debugf("deleteCRDsForChart: no matching CRDs found for pattern %q", pattern)
		return nil
	}
	for _, crdName := range matched {
		debugf("deleting CRD %s", crdName)
		_ = apiExtClient.ApiextensionsV1().CustomResourceDefinitions().Delete(ctx, crdName, metav1.DeleteOptions{})
	}

	debugf("deleteCRDsForChart: completed, deleted %d CRDs", len(matched))
	return nil
}

func deleteSubmarinerEndpointsNotMatchingClusterID(ctx context.Context, dyn dynamic.Interface) error {
	debugf("deleteSubmarinerEndpointsNotMatchingClusterID: starting")
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
		debugf("processing GVR %s/%s/%s", gvr.Group, gvr.Version, gvr.Resource)

		// List across namespace "skycluster-system"
		ns := "skycluster-system"
		list, err := dyn.Resource(gvr).Namespace(ns).List(ctx, metav1.ListOptions{})
		if err != nil {
			debugf("listing resources for %s failed: %v", gvr.Resource, err)
			return err
		}

		for _, item := range list.Items {
			labels := item.GetLabels()
			if val, ok := labels["submariner-io/clusterID"]; ok && val == clusterIDtoSkip {
				// keep endpoints that match the desired clusterID
				debugf("skipping item %s due to clusterID match %s", item.GetName(), val)
				continue
			}

			name := item.GetName()
			loc := name
			if ns != "" {
				loc = ns + "/" + name
			}

			var res dynamic.ResourceInterface
			if ns == "" {
				res = dyn.Resource(gvr)
			} else {
				res = dyn.Resource(gvr).Namespace(ns)
			}

			debugf("attempting normal delete for %s", loc)
			// 1. Best-effort normal delete
			_ = res.Delete(ctx, name, metav1.DeleteOptions{})

			// 2. Check if still exists
			obj, err := res.Get(ctx, name, metav1.GetOptions{})
			if apierrors.IsNotFound(err) {
				debugf("%s not found after delete", loc)
				continue
			}

			// 3. Remove finalizers if any
			if err == nil && len(obj.GetFinalizers()) > 0 {
				debugf("removing finalizers from %s", loc)
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
				debugf("force deleted %s", loc)
			}
		}
	}

	debugf("deleteSubmarinerEndpointsNotMatchingClusterID: completed")
	return nil
}

func cleanupSubmarinerDaemonSets(ctx context.Context, cs *kubernetes.Clientset) error {
	debugf("cleanupSubmarinerDaemonSets: starting")
	dsNames := []string{
		"submariner-gateway",
		"submariner-routeagent",
		"submariner-lighthouse-agent",
		"submariner-lighthouse-coredns",
		"submariner-metrics-proxy",
	}
	ns := "submariner-operator"

	for _, name := range dsNames {
		debugf("cleanupSubmarinerDaemonSets: deleting daemonset %s/%s", ns, name)
		// 1. Best-effort normal delete
		_ = cs.AppsV1().DaemonSets(ns).Delete(ctx, name, metav1.DeleteOptions{})
	}

	debugf("cleanupSubmarinerDaemonSets: completed")
	return nil
}

func cleanupKubeconfigSecrets(ctx context.Context, cs *kubernetes.Clientset) error {
	debugf("cleanupKubeconfigSecrets: starting")
	secretList, err := cs.CoreV1().Secrets("skycluster-system").List(ctx, metav1.ListOptions{
		LabelSelector: "skycluster.io/secret-type=static-kubeconfig",
	})
	if err != nil {
		debugf("cleanupKubeconfigSecrets: listing secrets failed: %v", err)
		return err
	}
	debugf("cleanupKubeconfigSecrets: found %d secrets", len(secretList.Items))

	extNames := xk.ListXKubesNames("")
	debugf("cleanupKubeconfigSecrets: external xkube names: %v", extNames)

	for _, secret := range secretList.Items {
		// if there is an existing xkube with this cluster-id, skip deletion
		clusterID := secret.Labels["skycluster.io/cluster-id"]
		if slices.Contains(extNames, clusterID) {
			debugf("cleanupKubeconfigSecrets: skipping secret %s with cluster-id %q", secret.Name, clusterID)
			continue
		}

		debugf("cleanupKubeconfigSecrets: deleting secret %s", secret.Name)
		// 1. Best-effort normal delete
		_ = cs.CoreV1().Secrets("skycluster-system").Delete(ctx, secret.Name, metav1.DeleteOptions{})
	}

	debugf("cleanupKubeconfigSecrets: completed")
	return nil
}

func deleteSubmariner(ctx context.Context, dyn dynamic.Interface) error {
	debugf("deleteSubmariner: starting")
	gvrs := []schema.GroupVersionResource{
		{
			Group:    "submariner.io",
			Version:  "v1alpha1",
			Resource: "submariners",
		},
	}

	for _, gvr := range gvrs {
		debugf("deleteSubmariner: processing GVR %s/%s/%s", gvr.Group, gvr.Version, gvr.Resource)

		list, err := dyn.Resource(gvr).Namespace("submariner-operator").List(ctx, metav1.ListOptions{})
		if err != nil {
			debugf("deleteSubmariner: list failed for %s: %v", gvr.Resource, err)
			return err
		}

		for _, item := range list.Items {
			name := item.GetName()
			debugf("deleteSubmariner: attempting delete for submariner %s", name)
			// 1. Best-effort normal delete
			_ = dyn.Resource(gvr).Namespace("submariner-operator").Delete(ctx, name, metav1.DeleteOptions{})

			// 2. Check if still exists
			obj, err := dyn.Resource(gvr).Namespace("submariner-operator").Get(ctx, name, metav1.GetOptions{})
			if apierrors.IsNotFound(err) {
				debugf("deleteSubmariner: %s not found after delete", name)
				continue
			}

			// 3. Remove finalizers if any
			if err == nil && len(obj.GetFinalizers()) > 0 {
				debugf("deleteSubmariner: removing finalizers from %s", name)
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
				debugf("deleteSubmariner: force deleted %s", name)
			}
		}
	}

	debugf("deleteSubmariner: completed")
	return nil
}