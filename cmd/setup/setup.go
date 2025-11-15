package setup

import (
	"context"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"

	"github.com/etesami/skycluster-cli/internal/utils"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
)

var (
	publicKeyPath    string
	privateKeyPath   string
	xsetupAPIServer  string
	xsetupSubmariner bool
)

func init() {
	// Use Cobra flags (also support go test / `go run` style flags fallback)
	setupCmd.Flags().StringVar(&publicKeyPath, "public", "", "Path to public key (e.g. ~/.ssh/id_rsa.pub)")
	setupCmd.Flags().StringVar(&privateKeyPath, "private", "", "Path to private key (e.g. ~/.ssh/id_rsa)")
	// flags for XSetup resource
	setupCmd.Flags().StringVar(&xsetupAPIServer, "apiserver", "", "API server address to put in XSetup.spec.apiServer (host[:port])")
	setupCmd.Flags().BoolVar(&xsetupSubmariner, "submariner", true, "Whether to enable submariner in XSetup.spec.submariner.enabled")

	// make flags available to library using standard flag package (optional)
	_ = flag.CommandLine.Parse([]string{})
}

var setupCmd = &cobra.Command{
	Use:   "setup",
	Short: "Setup commands",
	RunE: func(cmd *cobra.Command, args []string) error {
		// Validate required flags
		if publicKeyPath == "" || privateKeyPath == "" {
			return errors.New("flags --public, --private are required")
		}
		if strings.TrimSpace(xsetupAPIServer) == "" {
			return errors.New("flag --apiserver is required")
		}

		// normalize api server (add default port if missing) and validate/reachability
		apiServerNormalized, _, err := validateAndCheckAPIServer(xsetupAPIServer)
		if err != nil {
			return fmt.Errorf("api server validation failed: %w", err)
		}
		// if insecureUsed {
		// 	fmt.Println("warning: reached API server by skipping TLS verification (InsecureSkipVerify); server certificate may be self-signed")
		// }

		// check files exist and read them
		pubBytes, err := os.ReadFile(expandPath(publicKeyPath))
		if err != nil {
			return fmt.Errorf("reading public key: %w", err)
		}
		privBytes, err := os.ReadFile(expandPath(privateKeyPath))
		if err != nil {
			return fmt.Errorf("reading private key: %w", err)
		}

		kubeconfigPath := viper.GetString("kubeconfig")
		kubeBytes, err := os.ReadFile(expandPath(kubeconfigPath))
		if err != nil {
			return fmt.Errorf("reading kubeconfig: %w", err)
		}

		// Prepare values
		pubStr := strings.TrimSpace(string(pubBytes))
		privB64 := base64.StdEncoding.EncodeToString(privBytes)

		// JSON config for first secret
		cfg := map[string]string{
			"publicKey":  pubStr,
			"privateKey": privB64,
		}
		cfgBytes, err := json.Marshal(cfg)
		if err != nil {
			return fmt.Errorf("marshal keypair json: %w", err)
		}

		// Build secrets
		ns := "skycluster-system"
		secret1 := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: ns,
				Name:      "skycluster-keys",
				Labels: map[string]string{
					"skycluster.io/managed-by":  "skycluster",
					"skycluster.io/secret-type": "default-keypair",
				},
			},
			Type: corev1.SecretTypeOpaque,
			// we want the YAML to show the JSON config as plain string -> StringData is used
			StringData: map[string]string{
				"config": string(cfgBytes),
			},
		}

		secret2 := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: ns,
				Name:      "skycluster-kubeconfig",
				Labels: map[string]string{
					"skycluster.io/managed-by":  "skycluster",
					"skycluster.io/secret-type": "skycluster-kubeconfig",
					"skycluster.io/cluster-name": "skycluster-management",
				},
			},
			Type: corev1.SecretTypeOpaque,
			Data: map[string][]byte{
				// store raw kubeconfig bytes; Kubernetes stores secret data base64-encoded
				"kubeconfig": kubeBytes,
			},
		}

		// Create client using kubeconfig
		clientset, err := utils.GetClientset(kubeconfigPath)
		if err != nil {
			return fmt.Errorf("build kubernetes client: %w", err)
		}

		ctx := context.Background()

		// Ensure namespace exists (best effort; ignore AlreadyExists)
		_, err = clientset.CoreV1().Namespaces().Get(ctx, ns, metav1.GetOptions{})
		if apierrors.IsNotFound(err) {
			_, err = clientset.CoreV1().Namespaces().Create(ctx, &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{Name: ns},
			}, metav1.CreateOptions{})
			if err != nil {
				return fmt.Errorf("create namespace %s: %w", ns, err)
			}
		} else if err != nil {
			return fmt.Errorf("check namespace %s: %w", ns, err)
		}

		if err := createOrUpdateSecret(ctx, clientset, secret1); err != nil {
			return fmt.Errorf("create/update secret %s: %w", secret1.Name, err)
		}
		if err := createOrUpdateSecret(ctx, clientset, secret2); err != nil {
			return fmt.Errorf("create/update secret %s: %w", secret2.Name, err)
		}

		// Now create/update the XSetup resource (cluster-scoped)
		dyn, err := utils.GetDynamicClient(kubeconfigPath)
		if err != nil {
			return fmt.Errorf("build dynamic client: %w", err)
		}

		// Use the normalized API server address in the CR
		xsetup := buildXSetupUnstructured("mycluster", apiServerNormalized, xsetupSubmariner)
		if err := createOrUpdateXSetup(ctx, dyn, xsetup); err != nil {
			return fmt.Errorf("create/update XSetup %s: %w", xsetup.GetName(), err)
		}

		fmt.Println("Secrets created/updated successfully and XSetup ensured")
		return nil
	},
}

func GetSetupCmd() *cobra.Command { return setupCmd }

// createOrUpdateSecret will create the secret or update it if already exists.
func createOrUpdateSecret(ctx context.Context, c *kubernetes.Clientset, s *corev1.Secret) error {
	svc := c.CoreV1().Secrets(s.Namespace)
	existing, err := svc.Get(ctx, s.Name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		_, err := svc.Create(ctx, s, metav1.CreateOptions{})
		return err
	}
	if err != nil {
		return err
	}

	// preserve resource version and update fields
	existing.ObjectMeta.Labels = s.ObjectMeta.Labels
	existing.StringData = s.StringData
	existing.Data = s.Data
	existing.Type = s.Type

	_, err = svc.Update(ctx, existing, metav1.UpdateOptions{})
	return err
}

// buildXSetupUnstructured builds an unstructured.Unstructured representing the XSetup CR shown:
// apiVersion: skycluster.io/v1alpha1
// kind: XSetup
func buildXSetupUnstructured(name, apiServer string, submarinerEnabled bool) *unstructured.Unstructured {
	u := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "skycluster.io/v1alpha1",
			"kind":       "XSetup",
			"metadata": map[string]interface{}{
				"name": name,
				"labels": map[string]interface{}{
					"skycluster.io/managed-by": "skycluster",
				},
			},
			"spec": map[string]interface{}{
				"apiServer": apiServer,
				"submariner": map[string]interface{}{
					"enabled": submarinerEnabled,
				},
			},
		},
	}
	return u
}

// createOrUpdateXSetup creates or updates the XSetup custom resource (cluster scoped).
func createOrUpdateXSetup(ctx context.Context, dyn dynamic.Interface, u *unstructured.Unstructured) error {
	gvr := schema.GroupVersionResource{
		Group:    "skycluster.io",
		Version:  "v1alpha1",
		Resource: "xsetups", // plural form; adjust if CRD uses a different plural
	}

	name := u.GetName()

	// Try to get existing (cluster-scoped)
	existing, err := dyn.Resource(gvr).Get(ctx, name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		_, err := dyn.Resource(gvr).Create(ctx, u, metav1.CreateOptions{})
		return err
	}
	if err != nil {
		return err
	}

	// Preserve resourceVersion for update
	if existing != nil {
		if rv := existing.GetResourceVersion(); rv != "" {
			u.SetResourceVersion(rv)
		}
		// Optionally preserve other fields from existing if needed

		_, err = dyn.Resource(gvr).Update(ctx, u, metav1.UpdateOptions{})
		return err
	}

	return nil
}

// validateAndCheckAPIServer validates the apiServer string and checks reachability and basic Kubernetes API validity.
// Returns the normalized apiServer (host:port), a bool indicating whether InsecureSkipVerify was used, and error.
func validateAndCheckAPIServer(apiServer string) (string, bool, error) {
	apiServer = strings.TrimSpace(apiServer)
	if apiServer == "" {
		return "", false, errors.New("api server is empty")
	}

	normalized := normalizeHostPort(apiServer, "6443")

	// Quick host resolution check
	host, _, _ := net.SplitHostPort(normalized)
	if host == "" {
		return "", false, fmt.Errorf("invalid api server host: %q", apiServer)
	}
	// Resolve host (best-effort)
	if ip := net.ParseIP(host); ip == nil {
		// try DNS lookup; it's okay if this fails sometimes (e.g., ephemeral DNS), but warn if can't resolve
		_, err := net.LookupHost(host)
		if err != nil {
			// we'll still attempt an HTTP check; don't fail here outright
			// but include the resolution error in diagnostic if final reachability fails
			_ = err
		}
	}

	// Try HTTPS GET /version with TLS verification
	url := "https://" + normalized + "/version"
	ok, insecureUsed, err := probeKubernetesVersionURL(url, false)
	if err == nil && ok {
		return normalized, insecureUsed, nil
	}
	// If TLS verification error, retry with InsecureSkipVerify true
	if err != nil {
		// Try with insecure skip only if the error hints at TLS cert issues or network; attempt anyway
		ok2, insecureUsed2, err2 := probeKubernetesVersionURL(url, true)
		if err2 == nil && ok2 {
			return normalized, insecureUsed2, nil
		}
		// return earlier error context (combine)
		return "", false, fmt.Errorf("failed to contact API server %s: %v; retry with insecure: %v", normalized, err, err2)
	}
	return "", false, fmt.Errorf("api server %s did not present a valid Kubernetes version response", normalized)
}

// normalizeHostPort ensures host[:port] is returned (adds defaultPort if missing)
func normalizeHostPort(raw, defaultPort string) string {
	raw = strings.TrimSpace(raw)
	// If contains scheme, strip it
	raw = strings.TrimPrefix(raw, "https://")
	raw = strings.TrimPrefix(raw, "http://")
	if _, _, err := net.SplitHostPort(raw); err == nil {
		return raw
	}
	// If error, it may be missing port. Append default.
	// If raw contains IPv6 in brackets with port omitted, we still append default.
	if strings.Contains(raw, ":") && strings.Count(raw, ":") > 1 && !strings.HasPrefix(raw, "[") {
		// IPv6 address without brackets, wrap in brackets
		raw = "[" + raw + "]"
	}
	return net.JoinHostPort(raw, defaultPort)
}

// probeKubernetesVersionURL GETs the /version endpoint and verifies JSON contains gitVersion.
// If insecure is true, TLS verification will be skipped. Returns (ok, insecureUsed, err)
func probeKubernetesVersionURL(url string, insecure bool) (bool, bool, error) {
	client := &http.Client{
		Timeout: 5 * time.Second,
	}
	transport := &http.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: insecure},
	}
	client.Transport = transport

	resp, err := client.Get(url)
	if err != nil {
		return false, insecure, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return false, insecure, fmt.Errorf("unexpected status %d from %s: %s", resp.StatusCode, url, string(body))
	}

	b, err := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if err != nil {
		return false, insecure, err
	}
	var parsed map[string]interface{}
	if err := json.Unmarshal(b, &parsed); err != nil {
		return false, insecure, fmt.Errorf("invalid JSON from %s: %w", url, err)
	}
	// Kubernetes /version returns fields like "gitVersion"
	if _, ok := parsed["gitVersion"]; !ok {
		return false, insecure, fmt.Errorf("response from %s missing gitVersion field", url)
	}
	return true, insecure, nil
}

// expandPath expands ~ to home directory (simple implementation)
func expandPath(p string) string {
	if strings.HasPrefix(p, "~/") || p == "~" {
		home, _ := os.UserHomeDir()
		if home == "" {
			return p
		}
		return strings.Replace(p, "~", home, 1)
	}
	return p
}