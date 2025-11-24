package xkube

import (
	"context"
	"encoding/base64"
	"fmt"
	"log"
	"os"
	"os/exec"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	authenticationv1 "k8s.io/api/authentication/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
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

type clientSets struct {
	dynamicClient dynamic.Interface
	clientSet     *kubernetes.Clientset
}

func init() {
	configShowCmd.PersistentFlags().StringSliceVarP(&kubeNames, "xkube", "k", nil, "Kube Names, separated by comma")
	configShowCmd.PersistentFlags().StringVarP(&outPath, "out", "o", "", "Output file path (required)")
	if err := configShowCmd.MarkPersistentFlagRequired("out"); err != nil {
		log.Fatalf("failed to mark 'out' flag required: %v", err)
	}
}

var configShowCmd = &cobra.Command{
	Use:   "config",
	Short: "Show current kubeconfig of the xkube (writes to file)",
	Run: func(cmd *cobra.Command, args []string) {
		ns, _ := cmd.Root().PersistentFlags().GetString("namespace")
		utils.RunWithSpinner("Fetching kubeconfigs", func() error {
			showConfigs(kubeNames, ns, outPath)
			return nil 
		})
	},
}

func showConfigs(kubeNames []string, ns string, outPath string) {
	kubeconfigPath := viper.GetString("kubeconfig")
	dynamicClient, err1 := utils.GetDynamicClient(kubeconfigPath)
	clientSet, err2 := utils.GetClientset(kubeconfigPath)
	if err1 != nil || err2 != nil {
		log.Fatalf("Error getting dynamic client: %v", err1)
		return
	}
	localClients := clientSets{
		dynamicClient: dynamicClient,
		clientSet:     clientSet,
	}

	if len(kubeNames) == 0 {kubeNames = ListXKubesNames(ns)}

	var kubeconfigs []string
	for _, c := range kubeNames {

		staticKubeconfig, err := fetchKubeconfig(c, localClients)
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

func GetConfig(kubeName string, ns string) (string, error) {
	kubeconfigPath := viper.GetString("kubeconfig")
	dynamicClient, err1 := utils.GetDynamicClient(kubeconfigPath)
	clientSet, err2 := utils.GetClientset(kubeconfigPath)
	if err1 != nil || err2 != nil {
		return "", err1
	}

	localClients := clientSets{
		dynamicClient: dynamicClient,
		clientSet:     clientSet,
	}

	staticKubeconfig, err := fetchKubeconfig(kubeName, localClients)
	if err != nil {
		return "", fmt.Errorf("error generating kubeconfig for [%s]: %v", kubeName, err)
	}
	
	return staticKubeconfig, nil
}

func fetchKubeconfig(xkubeName string, clientSets clientSets) (string, error) {
	dynamicClient := clientSets.dynamicClient
	gvr := schema.GroupVersionResource{Group: "skycluster.io", Version: "v1alpha1", Resource: "xkubes"}
	ri := dynamicClient.Resource(gvr)

	obj, err := ri.Get(context.Background(), xkubeName, metav1.GetOptions{})
	if err != nil {
		log.Printf("Error fetching config [%s]: %v", xkubeName, err)
		return "", err
	}
	
	clusterName, _, _ := unstructured.NestedString(obj.Object, "status", "externalClusterName")
	if clusterName == "" {return "", fmt.Errorf("externalClusterName not present for GCP platform")}

	// Check for existing static kubeconfig secret and its validity
	ns := ""
	existingSecret, err := fetchStaticKubeconfigSecret(clusterName, ns, clientSets.clientSet)
	if err == nil && len(existingSecret) > 0 {
		// found existing valid static kubeconfig secret
		return string(existingSecret), nil
	}

	// Determine platform from spec.providerRef.platform
	platform, _, _ := unstructured.NestedString(obj.Object, "spec", "providerRef", "platform")

	// If platform is gcp, use gcloud to obtain credentials (temporary kubeconfig)
	if platform == "gcp" {
		// Extract location from spec.providerRef.zones.primary
		provCfgZones, foundZones, err := unstructured.NestedStringMap(obj.Object, "spec", "providerRef", "zones")
		if err != nil {return "", err}
		if !foundZones {return "", fmt.Errorf("providerRef.zones not found")}
		
		location := provCfgZones["primary"]
		if location == "" {return "", fmt.Errorf("primary zone not set in providerRef.zones")}

		// Create a temporary kubeconfig file for gcloud to write into
		tmpFile, err := os.CreateTemp("", "gke-kubeconfig-*")
		if err != nil {
			return "", fmt.Errorf("failed to create temporary kubeconfig file for [%s]: %v", xkubeName, err)
		}
		tmpName := tmpFile.Name()
		tmpFile.Close()

		// Run gcloud with KUBECONFIG env pointing to tmpName
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
			log.Fatalf("failed to read kubeconfig written by gcloud for [%s]: %v", xkubeName, err)
		}

		// Store/retrieve static kubeconfig in secret (and respect expiry)
		staticKubeconfig, err := ensureStaticKubeconfig(kubeconfigBytes, xkubeName, "skycluster-system", clientSets)
		if err != nil {return "", err}

		return staticKubeconfig, nil
	}

	// Non-GCP path: look for secret reference in status.clusterSecretName
	secretName, found, err := unstructured.NestedString(obj.Object, "status", "clusterSecretName")
	if err != nil {return "", err}
	if !found {return "", fmt.Errorf("secret name not found for config [%s]", xkubeName)}

	// Secrets for xkube objects with kubeconfig are stored in skycluster-system
	skyclusterNamespace := "skycluster-system"
	// Fetch referenced secret
	gvr = schema.GroupVersionResource{Version: "v1", Resource: "secrets"}
	secret, err := dynamicClient.Resource(gvr).Namespace(skyclusterNamespace).
		Get(context.Background(), secretName, metav1.GetOptions{})
	if err != nil {
		return "", fmt.Errorf("error fetching secret %s for config [%s]: %v", secretName, xkubeName, err)
	}
	// Process the secret as needed
	kubeconfig_b64, found, err := unstructured.NestedString(secret.Object, "data", "kubeconfig")
	if err != nil {return "", fmt.Errorf("error fetching secret data for config [%s]: %v", xkubeName, err)}
	if !found {return "", fmt.Errorf("secret data not found for config [%s]", xkubeName)}

	kubeconfigBytes, err := base64.StdEncoding.DecodeString(kubeconfig_b64)
	if err != nil {return "", fmt.Errorf("error decoding kubeconfig for config [%s]: %v", xkubeName, err)}

	// Create or reuse static credentials: store the static kubeconfig in a secret (with expiry)
	staticKubeconfig, err := ensureStaticKubeconfig(kubeconfigBytes, xkubeName, skyclusterNamespace, clientSets)
	if err != nil {return "", fmt.Errorf("error creating static kubeconfig for [%s]: %v", xkubeName, err)}

	return staticKubeconfig, nil
}

// ensureStaticKubeconfig ensures a ServiceAccount and ClusterRoleBinding exist 
// in the target cluster, creates (or reuses) a service-account-token via 
// TokenRequest API and returns a kubeconfig that uses that static token.
// The resulting kubeconfig is persisted into a secret in the targetNamespace 
// named "<clusterID>-static-kubeconfig".
// The secret includes an expiry annotation that corresponds to the token expiration. 
// If the secret already exists and the stored expiry is still in the future, 
// the stored kubeconfig is returned instead of generating a new token.
func ensureStaticKubeconfig(kubeconfigBytes []byte, clusterID string, targetNamespace string, localClientSets clientSets) (string, error) {
	// use for secret creation/checks
	localClientSet := localClientSets.clientSet

	// Build client from given kubeconfig bytes
	restCfg, err := clientcmd.RESTConfigFromKubeConfig(kubeconfigBytes)
	if err != nil {return "", fmt.Errorf("building rest config from kubeconfig: %w", err)}

	clientset, err := kubernetes.NewForConfig(restCfg)
	if err != nil {return "", fmt.Errorf("creating kubernetes client: %w", err)}

	// Parse kubeconfig to discover server and CA data and current context
	parsedCfg, err := clientcmd.Load(kubeconfigBytes)
	if err != nil {return "", fmt.Errorf("parsing kubeconfig: %w", err)}

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
	if ctxName == "" {return "", fmt.Errorf("no context found in kubeconfig")}
	
	ctx := parsedCfg.Contexts[ctxName]
	clusterRef := ctx.Cluster
	clusterObj, ok := parsedCfg.Clusters[clusterRef]
	if !ok {return "", fmt.Errorf("cluster %q not found in kubeconfig", clusterRef)}

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

	// Create ServiceAccount if not exists (remote cluster)
	// Names for SA, CRB
	saName := "skycluster-static-sa-" + clusterID
	crbName := saName + "-crb"
	_, err = clientset.CoreV1().ServiceAccounts(targetNamespace).Get(context.Background(), saName, metav1.GetOptions{})
	if err != nil {
		if apierrors.IsNotFound(err) {
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
		} else {
			return "", fmt.Errorf("error checking serviceaccount %s/%s: %w", targetNamespace, saName, err)
		}
	}

	// Ensure ClusterRoleBinding exists granting cluster-admin to that SA (adjust role as needed)
	// (remote cluster)
	_, err = clientset.RbacV1().ClusterRoleBindings().Get(context.Background(), crbName, metav1.GetOptions{})
	if err != nil {
		if apierrors.IsNotFound(err) {
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
		} else {
			return "", fmt.Errorf("error checking clusterrolebinding %s: %w", crbName, err)
		}
	}

	// Generate token using TokenRequest API (Kubernetes v1.24+ compatible)
	tokenRequest := &authenticationv1.TokenRequest{
		Spec: authenticationv1.TokenRequestSpec{
			ExpirationSeconds: ptr.To[int64](86400),
		},
	}
	tokenResponse, err := clientset.CoreV1().ServiceAccounts(targetNamespace).CreateToken(context.Background(), saName, tokenRequest, metav1.CreateOptions{})
	if err != nil {return "", fmt.Errorf("creating service account token: %w", err)}
	
	token := []byte(tokenResponse.Status.Token)
	// Build a kubeconfig that uses this token and the cluster info
	outBytes, err := buildNewKubeconfig(clusterObj, clusterID, token)
	if err != nil {return "", fmt.Errorf("writing new kubeconfig: %w", err)}
	
	// Persist the kubeconfig into a secret with expiry set to token expiration	
	var expiryTime time.Time
	if tokenResponse.Status.ExpirationTimestamp.IsZero() {
		// fallback if unavailable: set expiry to now + requested duration (ExpirationSeconds)
	expiryTime = time.Now().UTC().Add(10 * time.Hour)
	} else {
		expiryTime = tokenResponse.Status.ExpirationTimestamp.Time.UTC()
	}

	// Check for existing secret and its expiry
	// secret name where we'll store the static kubeconfig + expiry
	secretName := clusterID + "-static-kubeconfig"
	secretObj := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      secretName,
			Namespace: targetNamespace,
			Labels: map[string]string{
				"skycluster.io/managed-by": "skycluster",
				"skycluster.io/secret-type": "static-kubeconfig",
				"skycluster.io/cluster-id":   clusterID,
			},
			Annotations: map[string]string{
				"skycluster.io/expiry": expiryTime.Format(time.RFC3339),
			},
		},
		Data: map[string][]byte{
			"kubeconfig": outBytes,
		},
		Type: corev1.SecretTypeOpaque,	
	}

	// Create or update secret
	_, err = localClientSet.CoreV1().Secrets(targetNamespace).Create(context.Background(), secretObj, metav1.CreateOptions{})
	if err != nil {
		// If create failed because it already exists (race), try update
		if apierrors.IsAlreadyExists(err) {
			// attempt to update
			_, err = localClientSet.CoreV1().Secrets(targetNamespace).Update(context.Background(), secretObj, metav1.UpdateOptions{})
			if err != nil {
				return "", fmt.Errorf("creating/updating secret %s/%s: %w", targetNamespace, secretName, err)
			}
		} else {
			return "", fmt.Errorf("creating secret %s/%s: %w", targetNamespace, secretName, err)
		}
	}

	return string(outBytes), nil
}

// return static kubeconfig (byte) from secret if exists and not expired
func fetchStaticKubeconfigSecret(clusterID string, targetNamespace string, localClientSet *kubernetes.Clientset) ([]byte, error) {
	// secret name where we'll store the static kubeconfig + expiry
	secretName := clusterID + "-static-kubeconfig"
	expiryAnnotation := "skycluster.io/expiry"

	// Check for existing secret and its expiry
	existingSecret, err := localClientSet.CoreV1().Secrets(targetNamespace).Get(context.Background(), secretName, metav1.GetOptions{})
	if err == nil {
		// Secret exists; check expiry annotation and kubeconfig presence
		if existingSecret.Data != nil {
			if kcBytes, ok := existingSecret.Data["kubeconfig"]; ok && len(kcBytes) > 0 {
				if ann := existingSecret.Annotations[expiryAnnotation]; ann != "" {
					expiryTime, perr := time.Parse(time.RFC3339, ann)
					if perr == nil {
						if time.Now().UTC().Before(expiryTime) {
							// Not expired: return stored kubeconfig
							return kcBytes, nil
						}
						// expired -> proceed to create a new token and update secret
					}
				}
			}
		}
	} else {
		return nil, fmt.Errorf("error checking existing secret %s/%s: %w", targetNamespace, secretName, err)
	}
	return nil, fmt.Errorf("static kubeconfig secret %s/%s not found or expired", targetNamespace, secretName)
}

func buildNewKubeconfig(clusterObj *api.Cluster, clusterID string, token []byte) ([]byte, error) {

	// Build a kubeconfig that uses this token and the cluster info
	newCfg := api.NewConfig()

	// choose unique names to avoid collision when merging multiple
	clusterOutName := clusterID + "-cluster"
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
		return nil, fmt.Errorf("writing new kubeconfig: %w", err)
	}

	return outBytes, nil
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