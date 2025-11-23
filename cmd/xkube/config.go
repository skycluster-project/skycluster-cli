package xkube

import (
	"context"
	"encoding/base64"
	"fmt"
	"log"
	"os"
	"os/exec"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	authenticationv1 "k8s.io/api/authentication/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/tools/clientcmd/api"
	"k8s.io/utils/ptr"

	utils "github.com/etesami/skycluster-cli/internal/utils"
)

var kubeNames []string
var outPath string

func init() {
	configShowCmd.PersistentFlags().StringSliceVarP(&kubeNames, "xkube", "k", nil, "Kube Names, separated by comma")
	configShowCmd.PersistentFlags().StringVarP(&outPath, "out", "o", "", "Output file path (required)")
	if err := configShowCmd.MarkPersistentFlagRequired("out"); err != nil {
		log.Fatalf("failed to mark 'out' flag required: %v", err)
	}
	// configCmd.AddCommand(configShowCmd)
}

var configShowCmd = &cobra.Command{
	Use:   "config",
	Short: "Show current kubeconfig of the xkube (writes to file)",
	Run: func(cmd *cobra.Command, args []string) {
		ns, _ := cmd.Root().PersistentFlags().GetString("namespace")
		showConfigs(kubeNames, ns, outPath)
	},
}

func showConfigs(kubeNames []string, ns string, outPath string) {
	kubeconfigPath := viper.GetString("kubeconfig")
	dynamicClient, err := utils.GetDynamicClient(kubeconfigPath)
	if err != nil {
		log.Fatalf("Error getting dynamic client: %v", err)
		return
	}

	if len(kubeNames) == 0 {
		kubeNames = listXKubesNames(ns)
	}

	var kubeconfigs []string
	for _, c := range kubeNames {
		
		staticKubeconfig, err := generateKubeconfig(c, dynamicClient, ns)
		if err != nil {
			log.Printf("Error generating kubeconfig for [%s]: %v", c, err)
			continue
		}
		kubeconfigs = append(kubeconfigs, staticKubeconfig)
	}

	if len(kubeconfigs) == 0 {
		log.Fatalf("no kubeconfigs produced; nothing to write")
	}

	// Prepare output bytes
	var outBytes []byte
	mergedBytes, err := mergeKubeconfigs(kubeconfigs)
	if err != nil {
		log.Fatalf("Error merging kubeconfigs: %v", err)
	}
	outBytes = mergedBytes

	if outPath != "" {
		// Write to the required output path (do not print to screen)
		if err := os.WriteFile(outPath, outBytes, 0o600); err != nil {
			log.Fatalf("Error writing kubeconfig to file %s: %v", outPath, err)
		}
	}

	// Optionally, you can print a small success message to stderr (not stdout), or omit entirely.
	fmt.Fprintf(os.Stderr, "Wrote kubeconfig to %s\n", outPath)
}

func getConfig(kubeName string, ns string) (string, error) {
	kubeconfigPath := viper.GetString("kubeconfig")
	dynamicClient, err := utils.GetDynamicClient(kubeconfigPath)
	if err != nil {
		log.Fatalf("Error getting dynamic client: %v", err)
		return "", err
	}

	staticKubeconfig, err := generateKubeconfig(kubeName, dynamicClient, ns)
	if err != nil {
		return "", fmt.Errorf("Error generating kubeconfig for [%s]: %v", kubeName, err)
	}
	
	return staticKubeconfig, nil
}

func generateKubeconfig(c string, dynamicClient dynamic.Interface, ns string) (string, error) {
	gvr := schema.GroupVersionResource{Group: "skycluster.io", Version: "v1alpha1", Resource: "xkubes"}
	var ri dynamic.ResourceInterface
	if ns != "" {
		ri = dynamicClient.Resource(gvr).Namespace(ns)
	} else {
		ri = dynamicClient.Resource(gvr)
	}

	obj, err := ri.Get(context.Background(), c, metav1.GetOptions{})
	if err != nil {
		log.Printf("Error fetching config [%s]: %v", c, err)
		return "", err
	}

	// Determine platform from spec.providerRef.platform
	platform, _, _ := unstructured.NestedString(obj.Object, "spec", "providerRef", "platform")

	// If platform is gcp, use gcloud to obtain credentials (temporary kubeconfig)
	if platform == "gcp" {
		// Expect status.clusterName to exist for GKE clusters
		clusterName, _, _ := unstructured.NestedString(obj.Object, "status", "externalClusterName")
		if clusterName == "" {
			return "", fmt.Errorf("externalClusterName not present for GCP platform")
		}

		// Extract location from spec.providerRef.zones.primary
		provCfgZones, foundZones, err := unstructured.NestedStringMap(obj.Object, "spec", "providerRef", "zones")
		if err != nil {
			return "", err
		}
		if !foundZones {
			return "", fmt.Errorf("providerRef.zones not found")
		}
		location := provCfgZones["primary"]
		if location == "" {
			log.Printf("primary zone not set in providerRef.zones for [%s]; cannot determine GKE location", c)
			return "", fmt.Errorf("primary zone not set in providerRef.zones")
		}

		// Create a temporary kubeconfig file for gcloud to write into
		tmpFile, err := os.CreateTemp("", "gke-kubeconfig-*")
		if err != nil {
			return "", fmt.Errorf("failed to create temporary kubeconfig file for [%s]: %v", c, err)
		}
		tmpName := tmpFile.Name()
		if err := tmpFile.Close(); err != nil {
			// not fatal, but log
			log.Printf("warning: closing temp file %s: %v", tmpName, err)
		}

		// Run gcloud with KUBECONFIG env pointing to tmpName
		// Using --zone; if your clusters are regional you may want to use --region instead.
		gcCmd := exec.Command("gcloud", "container", "clusters", "get-credentials", clusterName, "--location", location)
		gcCmd.Env = append(os.Environ(), "KUBECONFIG="+tmpName)
		out, err := gcCmd.CombinedOutput()
		if err != nil {
			// Per your request, on gcloud errors we print and terminate.
			log.Fatalf("gcloud failed to get credentials for cluster %s (location=%s): %v\nOutput: %s", clusterName, location, err, string(out))
		}

		kubeconfigBytes, err := os.ReadFile(tmpName)
		// Attempt to remove temp file immediately after reading (ignore removal error)
		_ = os.Remove(tmpName)
		if err != nil {
			log.Fatalf("failed to read kubeconfig written by gcloud for [%s]: %v", c, err)
		}

		staticKubeconfig, err := ensureStaticKubeconfig(kubeconfigBytes, c, "skycluster-system")
		if err != nil {
			return "", err
		}

		return staticKubeconfig, nil
	}

	// Non-GCP path: look for secret reference in status.clusterSecretName
	secretName, found, err := unstructured.NestedString(obj.Object, "status", "clusterSecretName")
	if err != nil {
		return "", err
	}
	if !found {
		return "", fmt.Errorf("secret name not found for config [%s]", c)
	}

	// Secrets for xkube objects with kubeconfig are stored in skycluster-system
	skyclusterNamespace := "skycluster-system"
	// Fetch referenced secret
	gvr = schema.GroupVersionResource{Version: "v1", Resource: "secrets"}
	secret, err := dynamicClient.Resource(gvr).Namespace(skyclusterNamespace).
		Get(context.Background(), secretName, metav1.GetOptions{})
	if err != nil {
		return "", fmt.Errorf("error fetching secret %s for config [%s]: %v", secretName, c, err)
	}
	// Process the secret as needed
	kubeconfig_b64, found, err := unstructured.NestedString(secret.Object, "data", "kubeconfig")
	if err != nil {
		return "", fmt.Errorf("error fetching secret data for config [%s]: %v", c, err)
	}
	if !found {
		return "", fmt.Errorf("secret data not found for config [%s]", c)
	}
	kubeconfigBytes, err := base64.StdEncoding.DecodeString(kubeconfig_b64)
	if err != nil {
		return "", fmt.Errorf("error decoding kubeconfig for config [%s]: %v", c, err)
	}

	// Create static credentials (ServiceAccount + ClusterRoleBinding + token secret) on the remote cluster
	staticKubeconfig, err := ensureStaticKubeconfig(kubeconfigBytes, c, skyclusterNamespace)
	if err != nil {
		return "", fmt.Errorf("error creating static kubeconfig for [%s]: %v", c, err)
	}

	return staticKubeconfig, nil
}

// ensureStaticKubeconfig ensures a ServiceAccount and ClusterRoleBinding exist in the target cluster,
// creates (or reuses) a service-account-token secret and waits for a token to be available, then
// returns a kubeconfig that uses that static token.
func ensureStaticKubeconfig(kubeconfigBytes []byte, clusterID string, targetNamespace string) (string, error) {
	// Build client from given kubeconfig bytes
	restCfg, err := clientcmd.RESTConfigFromKubeConfig(kubeconfigBytes)
	if err != nil {
		return "", fmt.Errorf("building rest config from kubeconfig: %w", err)
	}
	clientset, err := kubernetes.NewForConfig(restCfg)
	if err != nil {
		return "", fmt.Errorf("creating kubernetes client: %w", err)
	}

	// Parse kubeconfig to discover server and CA data and current context
	parsedCfg, err := clientcmd.Load(kubeconfigBytes)
	if err != nil {
		return "", fmt.Errorf("parsing kubeconfig: %w", err)
	}
	// Pick current context if available, otherwise first context
	var ctxName string
	if parsedCfg.CurrentContext != "" {
		ctxName = parsedCfg.CurrentContext
	} else {
		for k := range parsedCfg.Contexts {
			ctxName = k
			break
		}
	}
	if ctxName == "" {
		return "", fmt.Errorf("no context found in kubeconfig")
	}
	ctx := parsedCfg.Contexts[ctxName]
	clusterRef := ctx.Cluster
	clusterObj, ok := parsedCfg.Clusters[clusterRef]
	if !ok {
		return "", fmt.Errorf("cluster %q not found in kubeconfig", clusterRef)
	}

	// ensure target namespace
	_, err = clientset.CoreV1().Namespaces().Get(context.Background(), targetNamespace, metav1.GetOptions{})
	if err != nil {
		_, err = clientset.CoreV1().Namespaces().Create(context.Background(), &corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{
				Name: targetNamespace,
			},
		}, metav1.CreateOptions{})
		if err != nil {
			return "", fmt.Errorf("creating namespace %s: %w", targetNamespace, err)
		}
	}

	// Names for SA, CRB
	saName := "skycluster-static-sa-" + clusterID
	crbName := saName + "-crb"

	// Create ServiceAccount if not exists
	_, err = clientset.CoreV1().ServiceAccounts(targetNamespace).Get(context.Background(), saName, metav1.GetOptions{})
	if err != nil {
		_, err = clientset.CoreV1().ServiceAccounts(targetNamespace).Create(context.Background(), &corev1.ServiceAccount{
			ObjectMeta: metav1.ObjectMeta{
				Name:      saName,
				Namespace: targetNamespace,
				Labels: map[string]string{
					"skycluster.io/managed-by": "skycluster",
				},
			},
		}, metav1.CreateOptions{})
		if err != nil {
			return "", fmt.Errorf("creating serviceaccount %s/%s: %w", targetNamespace, saName, err)
		}
	}

	// Ensure ClusterRoleBinding exists granting cluster-admin to that SA (adjust role as needed)
	_, err = clientset.RbacV1().ClusterRoleBindings().Get(context.Background(), crbName, metav1.GetOptions{})
	if err != nil {
		crb := &rbacv1.ClusterRoleBinding{
			ObjectMeta: metav1.ObjectMeta{
				Name: crbName,
			},
			Subjects: []rbacv1.Subject{
				{
					Kind:      "ServiceAccount",
					Name:      saName,
					Namespace: targetNamespace,
				},
			},
			RoleRef: rbacv1.RoleRef{
				APIGroup: "rbac.authorization.k8s.io",
				Kind:     "ClusterRole",
				Name:     "cluster-admin",
			},
		}
		_, err = clientset.RbacV1().ClusterRoleBindings().Create(context.Background(), crb, metav1.CreateOptions{})
		if err != nil {
			return "", fmt.Errorf("creating clusterrolebinding %s: %w", crbName, err)
		}
	}

	// Generate token using TokenRequest API (Kubernetes v1.24+ compatible)
	tokenRequest := &authenticationv1.TokenRequest{
		Spec: authenticationv1.TokenRequestSpec{
			ExpirationSeconds: ptr.To[int64](86400),
		},
	}
	tokenResponse, err := clientset.CoreV1().ServiceAccounts(targetNamespace).CreateToken(context.Background(), saName, tokenRequest, metav1.CreateOptions{})
	if err != nil {
		return "", fmt.Errorf("creating service account token: %w", err)
	}
	token := []byte(tokenResponse.Status.Token)

	// Build a kubeconfig that uses this token and the cluster info
	newCfg := api.NewConfig()

	// choose unique names to avoid collision when merging multiple
	clusterOutName := clusterID + "-" + clusterRef + "-cluster"
	userOutName := clusterID 
	contextOutName := clusterID

	newCfg.Clusters[clusterOutName] = &api.Cluster{
		Server:                   clusterObj.Server,
		CertificateAuthorityData: clusterObj.CertificateAuthorityData,
		InsecureSkipTLSVerify:    clusterObj.InsecureSkipTLSVerify,
	}

	newCfg.AuthInfos[userOutName] = &api.AuthInfo{
		Token: string(token),
	}

	newCfg.Contexts[contextOutName] = &api.Context{
		Cluster:  clusterOutName,
		AuthInfo: userOutName,
	}

	newCfg.CurrentContext = contextOutName

	outBytes, err := clientcmd.Write(*newCfg)
	if err != nil {
		return "", fmt.Errorf("writing new kubeconfig: %w", err)
	}

	return string(outBytes), nil
}

// Merge kubeconfig strings into one single kubeconfig YAML
func mergeKubeconfigs(kubeconfigs []string) ([]byte, error) {
	merged := api.NewConfig()

	for _, raw := range kubeconfigs {
		cfg, err := clientcmd.Load([]byte(raw))
		if err != nil {
			log.Printf("Error parsing kubeconfig: %v", err)
			continue
		}

		// Merge clusters
		for name, cluster := range cfg.Clusters {
			merged.Clusters[name] = cluster
		}

		// Merge auth infos (users)
		for name, user := range cfg.AuthInfos {
			merged.AuthInfos[name] = user
		}

		// Merge contexts
		for name, ctx := range cfg.Contexts {
			merged.Contexts[name] = ctx
		}

		// Use the first non-empty current-context found
		if merged.CurrentContext == "" && cfg.CurrentContext != "" {
			merged.CurrentContext = cfg.CurrentContext
		}
	}

	// Serialize
	return clientcmd.Write(*merged)
}