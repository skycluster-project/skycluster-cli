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

	// debug flag controls debug output (can be set by package that uses this, or tests)
	debug bool
)

// debugf prints debug messages to stderr when debug is enabled.
func debugf(format string, args ...interface{}) {
	if debug {
		_, _ = fmt.Fprintf(os.Stderr, "DEBUG: "+format+"\n", args...)
	}
}

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

// SetDebug sets package-level debug flag after CLI flags are parsed.
func SetDebug(d bool) {
	debug = d
}

var setupCmd = &cobra.Command{
	Use:   "setup",
	Short: "Setup commands",
	RunE: func(cmd *cobra.Command, args []string) error {
		debugf("setup command started")
		// Validate required flags
		if publicKeyPath == "" || privateKeyPath == "" {
			debugf("missing required key paths: public=%q private=%q", publicKeyPath, privateKeyPath)
			return errors.New("flags --public, --private are required")
		}
		if strings.TrimSpace(xsetupAPIServer) == "" {
			debugf("missing required apiserver flag")
			return errors.New("flag --apiserver is required")
		}

		debugf("validating api server %q", xsetupAPIServer)
		// normalize api server (add default port if missing) and validate/reachability
		apiServerNormalized, insecureUsed, err := validateAndCheckAPIServer(xsetupAPIServer)
		if err != nil {
			debugf("api server validation failed: %v", err)
			return fmt.Errorf("api server validation failed: %w", err)
		}
		if insecureUsed {
			debugf("API server probe required insecure TLS skip (InsecureSkipVerify=true)")
		} else {
			debugf("API server probe used strict TLS verification")
		}

		// check files exist and read them
		debugf("reading public key from %q", publicKeyPath)
		pubBytes, err := os.ReadFile(expandPath(publicKeyPath))
		if err != nil {
			debugf("failed reading public key: %v", err)
			return fmt.Errorf("reading public key: %w", err)
		}
		debugf("read %d bytes from public key", len(pubBytes))

		debugf("reading private key from %q", privateKeyPath)
		privBytes, err := os.ReadFile(expandPath(privateKeyPath))
		if err != nil {
			debugf("failed reading private key: %v", err)
			return fmt.Errorf("reading private key: %w", err)
		}
		debugf("read %d bytes from private key", len(privBytes))

		kubeconfigPath := viper.GetString("kubeconfig")
		debugf("reading kubeconfig from %q", kubeconfigPath)
		kubeBytes, err := os.ReadFile(expandPath(kubeconfigPath))
		if err != nil {
			debugf("failed reading kubeconfig: %v", err)
			return fmt.Errorf("reading kubeconfig: %w", err)
		}
		debugf("read %d bytes from kubeconfig", len(kubeBytes))

		// Prepare values
		pubStr := strings.TrimSpace(string(pubBytes))
		privB64 := base64.StdEncoding.EncodeToString(privBytes)
		debugf("prepared public key string length %d and base64 private key length %d", len(pubStr), len(privB64))

		// JSON config for first secret
		cfg := map[string]string{
			"publicKey":  pubStr,
			"privateKey": privB64,
		}
		cfgBytes, err := json.Marshal(cfg)
		if err != nil {
			debugf("failed to marshal keypair json: %v", err)
			return fmt.Errorf("marshal keypair json: %w", err)
		}
		debugf("marshalled keypair json (%d bytes)", len(cfgBytes))

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
			StringData: map[string]string{
				"config": string(cfgBytes),
			},
		}

		secret2 := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: ns,
				Name:      "skycluster-management",
				Labels: map[string]string{
					"skycluster.io/managed-by":   "skycluster",
					"skycluster.io/secret-type":  "k8s-connection-data",
					"skycluster.io/cluster-name": "skycluster-management",
				},
			},
			Type: corev1.SecretTypeOpaque,
			Data: map[string][]byte{
				"kubeconfig": kubeBytes,
			},
		}

		// Create client using kubeconfig
		debugf("building kubernetes clientset with kubeconfig %q", kubeconfigPath)
		clientset, err := utils.GetClientset(kubeconfigPath)
		if err != nil {
			debugf("failed to build kubernetes clientset: %v", err)
			return fmt.Errorf("build kubernetes client: %w", err)
		}
		debugf("kubernetes clientset initialized")

		ctx := context.Background()

		// Ensure namespaces exist (best effort; ignore AlreadyExists)
		debugf("ensuring namespace %s exists", ns)
		if err := createOrUpdateNamespace(ctx, clientset, ns); err != nil {
			debugf("createOrUpdateNamespace failed for %s: %v", ns, err)
			return fmt.Errorf("ensure namespace %s: %w", ns, err)
		}
		debugf("ensuring namespace %s exists", "submariner-operator")
		if err := createOrUpdateNamespace(ctx, clientset, "submariner-operator"); err != nil {
			debugf("createOrUpdateNamespace failed for submariner-operator: %v", err)
			return fmt.Errorf("ensure namespace %s: %w", "submariner-operator", err)
		}

		debugf("creating/updating secret %s/%s", secret1.Namespace, secret1.Name)
		if err := createOrUpdateSecret(ctx, clientset, secret1); err != nil {
			debugf("createOrUpdateSecret failed for %s: %v", secret1.Name, err)
			return fmt.Errorf("create/update secret %s: %w", secret1.Name, err)
		}
		debugf("created/updated secret %s/%s", secret1.Namespace, secret1.Name)

		debugf("creating/updating secret %s/%s", secret2.Namespace, secret2.Name)
		if err := createOrUpdateSecret(ctx, clientset, secret2); err != nil {
			debugf("createOrUpdateSecret failed for %s: %v", secret2.Name, err)
			return fmt.Errorf("create/update secret %s: %w", secret2.Name, err)
		}
		debugf("created/updated secret %s/%s", secret2.Namespace, secret2.Name)

		// Now create/update the XSetup resource (cluster-scoped)
		debugf("building dynamic client with kubeconfig %q", kubeconfigPath)
		dyn, err := utils.GetDynamicClient(kubeconfigPath)
		if err != nil {
			debugf("failed to build dynamic client: %v", err)
			return fmt.Errorf("build dynamic client: %w", err)
		}
		debugf("dynamic client initialized")

		// Use the normalized API server address in the CR
		xsetup := buildXSetupUnstructured("mycluster", apiServerNormalized, xsetupSubmariner)
		if j, err := json.MarshalIndent(xsetup.Object, "", "  "); err == nil {
			debugf("constructed XSetup object: %s", string(j))
		} else {
			debugf("could not marshal XSetup for debug: %v", err)
		}
		if err := createOrUpdateXSetup(ctx, dyn, xsetup); err != nil {
			debugf("createOrUpdateXSetup failed for %s: %v", xsetup.GetName(), err)
			return fmt.Errorf("create/update XSetup %s: %w", xsetup.GetName(), err)
		}

		fmt.Println("Secrets created/updated successfully and XSetup ensured")

		// --------------------------------------------------------------------
		// PRE-WATCH PHASE + WATCHING PROCESS FOR STATICALLY DEFINED RESOURCES
		// --------------------------------------------------------------------
		fmt.Println("Resolving resources to watch (pre-watch phase)...")

		// These specs use the *underlying* manifest name (spec.forProvider.manifest.metadata.name),
		// which we know, but not the Crossplane object name itself.
		// So Name is left empty and ManifestMetadataName is used to resolve it.
		watchList := []utils.WaitResourceSpec{
			{
				KindDescription: "Istio root CA certs generator",
				GVR: schema.GroupVersionResource{
					Group:    "kubernetes.crossplane.io",
					Version:  "v1alpha2",
					Resource: "objects",
				},
				ManifestMetadataName: "istio-root-ca-certs-generator", // == spec.forProvider.manifest.metadata.name
				ConditionType:        "Ready",
				Timeout:              1 * time.Minute,
				PollInterval:         5 * time.Second,
			},
			{
				KindDescription: "Headscale cert generator",
				GVR: schema.GroupVersionResource{
					Group:    "kubernetes.crossplane.io",
					Version:  "v1alpha2",
					Resource: "objects",
				},
				ManifestMetadataName: "headscale-cert-gen",
				ConditionType:        "Ready",
				Timeout:              3 * time.Minute,
				PollInterval:         10 * time.Second,
			},
			{
				KindDescription: "Headscale server",
				GVR: schema.GroupVersionResource{
					Group:    "kubernetes.crossplane.io",
					Version:  "v1alpha2",
					Resource: "objects",
				},
				ManifestMetadataName: "headscale-server",
				ConditionType:        "Ready",
				Timeout:              5 * time.Minute,
				PollInterval:         10 * time.Second,
			},
			{
				KindDescription: "Headscale connection secret",
				GVR: schema.GroupVersionResource{
					Group:    "kubernetes.crossplane.io",
					Version:  "v1alpha2",
					Resource: "objects",
				},
				ManifestMetadataName: "headscale-connection-secret",
				ConditionType:        "Ready",
				Timeout:              2 * time.Minute,
				PollInterval:         5 * time.Second,
			},
			// For these Helm releases we *do* know the name directly.
			{
				KindDescription: "Submariner Operator Release",
				GVR: schema.GroupVersionResource{
					Group:    "helm.crossplane.io",
					Version:  "v1beta1",
					Resource: "releases",
				},
				ManifestMetadataName: "submariner-k8s-broker",
				ConditionType: "Ready",
				Timeout:       4 * time.Minute,
				PollInterval:  10 * time.Second,
			},
			{
				KindDescription: "Submariner operator",
				GVR: schema.GroupVersionResource{
					Group:    "helm.crossplane.io",
					Version:  "v1beta1",
					Resource: "releases",
				},
				ManifestMetadataName: "submariner-operator",
				ConditionType: "Ready",
				Timeout:       4 * time.Minute,
				PollInterval:  10 * time.Second,
			},
		}

		// Create and start TUI renderer
		renderer := utils.NewTUIRenderer()
		if err := renderer.Start(); err != nil {
			// fallback to plain output if TUI fails
			fmt.Printf("Failed to start TUI renderer: %v\n", err)
			// simple fallback ProgressSink
			plainSink := func(ev utils.ProgressEvent) {
        if ev.Err != nil {
            fmt.Printf("[ERROR] %s (%s/%s %s): %v\n",
                ev.KindDescription,
                ev.Namespace,
                ev.Name,
                ev.GVR.Resource,
                ev.Err,
            )
            return
        }
        status := "waiting"
        if ev.ResourceCompleted {
            status = "ready"
        }
        fmt.Printf("[%.0f%%] (%d/%d) %-30s %-6s %s/%s %s\n",
            ev.OverallPercent,
            ev.CurrentIndex,
            ev.Total,
            ev.KindDescription,
            status,
            ev.Namespace,
            ev.Name,
            ev.GVR.Resource,
        )
			}
			// Pre-watch phase: resolve names via spec.forProvider.manifest.metadata.name
			if err := utils.ResolveResourceNamesFromManifest(ctx, dyn, watchList, debugf); err != nil {
				return fmt.Errorf("pre-watch resolution failed: %w", err)
			}

			if err := utils.WaitForResourcesReadySequential(ctx, dyn, watchList, plainSink, debugf); err != nil {
				return err
			}
			return nil
		}

		// Pre-watch phase: resolve names via spec.forProvider.manifest.metadata.name
		if err := utils.ResolveResourceNamesFromManifest(ctx, dyn, watchList, debugf); err != nil {
			return fmt.Errorf("pre-watch resolution failed: %w", err)
		}
		
		// Use the TUI renderer as the ProgressSink
		err = utils.WaitForResourcesReadySequential(ctx, dyn, watchList, renderer.Sink, debugf)
		renderer.Stop(err)
		if err != nil {
				return err
		}
		return nil
	},
}

func GetSetupCmd() *cobra.Command { return setupCmd }

// createOrUpdateSecret will create the secret or update it if already exists.
func createOrUpdateSecret(ctx context.Context, c *kubernetes.Clientset, s *corev1.Secret) error {
	svc := c.CoreV1().Secrets(s.Namespace)
	debugf("attempting to GET secret %s/%s", s.Namespace, s.Name)
	existing, err := svc.Get(ctx, s.Name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		debugf("secret %s/%s not found, creating", s.Namespace, s.Name)
		_, err := svc.Create(ctx, s, metav1.CreateOptions{})
		if err != nil {
			debugf("create secret %s/%s failed: %v", s.Namespace, s.Name, err)
		} else {
			debugf("created secret %s/%s", s.Namespace, s.Name)
		}
		return err
	}
	if err != nil {
		debugf("error getting secret %s/%s: %v", s.Namespace, s.Name, err)
		return err
	}

	debugf("secret %s/%s exists, updating", s.Namespace, s.Name)
	// preserve resource version and update fields
	existing.ObjectMeta.Labels = s.ObjectMeta.Labels
	existing.StringData = s.StringData
	existing.Data = s.Data
	existing.Type = s.Type

	_, err = svc.Update(ctx, existing, metav1.UpdateOptions{})
	if err != nil {
		debugf("update secret %s/%s failed: %v", s.Namespace, s.Name, err)
	} else {
		debugf("updated secret %s/%s", s.Namespace, s.Name)
	}
	return err
}

func createOrUpdateNamespace(ctx context.Context, c *kubernetes.Clientset, ns string) error {
	debugf("checking namespace %s", ns)
	_, err := c.CoreV1().Namespaces().Get(ctx, ns, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		debugf("namespace %s not found, creating", ns)
		_, err = c.CoreV1().Namespaces().Create(ctx, &corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{Name: ns},
		}, metav1.CreateOptions{})
		if err != nil {
			debugf("create namespace %s failed: %v", ns, err)
			return fmt.Errorf("create namespace %s: %w", ns, err)
		}
		debugf("created namespace %s", ns)
	} else if err != nil {
		debugf("error checking namespace %s: %v", ns, err)
		return fmt.Errorf("check namespace %s: %w", ns, err)
	} else {
		debugf("namespace %s already exists", ns)
	}
	return nil
}

// buildXSetupUnstructured builds an unstructured.Unstructured representing the XSetup CR.
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

func createOrUpdateXSetup(ctx context.Context, dyn dynamic.Interface, u *unstructured.Unstructured) error {
	gvr := schema.GroupVersionResource{
		Group:    "skycluster.io",
		Version:  "v1alpha1",
		Resource: "xsetups", // plural form; adjust if CRD uses a different plural
	}

	name := u.GetName()
	debugf("ensuring XSetup %s (cluster-scoped)", name)

	// Try to get existing (cluster-scoped)
	debugf("attempting to GET existing XSetup %s", name)
	existing, err := dyn.Resource(gvr).Get(ctx, name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		debugf("XSetup %s not found, creating", name)
		_, err := dyn.Resource(gvr).Create(ctx, u, metav1.CreateOptions{})
		if err != nil {
			debugf("create XSetup %s failed: %v", name, err)
		} else {
			debugf("created XSetup %s", name)
		}
		return err
	}
	if err != nil {
		debugf("error getting XSetup %s: %v", name, err)
		return err
	}

	debugf("XSetup %s exists, preparing to merge", name)
	// Merge existing and new objects: overlay u onto existing so unspecified fields are preserved.
	merged := existing.DeepCopy()
	merged.Object = mergeMaps(merged.Object, u.Object)
	if j, err := json.MarshalIndent(merged.Object, "", "  "); err == nil {
		debugf("merged XSetup object: %s", string(j))
	} else {
		debugf("could not marshal merged XSetup for debug: %v", err)
	}

	_, err = dyn.Resource(gvr).Update(ctx, merged, metav1.UpdateOptions{})
	if err != nil {
		debugf("update XSetup %s failed: %v", name, err)
	} else {
		debugf("updated XSetup %s", name)
	}
	return err
}

// mergeMaps overlays src onto dst recursively.
func mergeMaps(dst, src map[string]interface{}) map[string]interface{} {
	if dst == nil {
		dst = make(map[string]interface{})
	}
	for k, sv := range src {
		if sv == nil {
			// skip nil values in src (do not delete existing)
			debugf("merge: skipping nil value for key %s", k)
			continue
		}
		if svMap, ok := sv.(map[string]interface{}); ok {
			if dv, exists := dst[k]; exists {
				if dvMap, ok2 := dv.(map[string]interface{}); ok2 {
					debugf("merge: recursively merging key %s", k)
					dst[k] = mergeMaps(dvMap, svMap)
					continue
				}
			}
			// dst doesn't have a map for this key, create a new merged map
			debugf("merge: copying map for key %s", k)
			dst[k] = mergeMaps(make(map[string]interface{}), svMap)
			continue
		}
		// For non-map types (including slices), src overwrites dst
		debugf("merge: setting key %s to value (type %T)", k, sv)
		dst[k] = sv
	}
	return dst
}

// validateAndCheckAPIServer validates the apiServer string and checks reachability and basic Kubernetes API validity.
func validateAndCheckAPIServer(apiServer string) (string, bool, error) {
	apiServer = strings.TrimSpace(apiServer)
	debugf("validateAndCheckAPIServer input: %q", apiServer)
	if apiServer == "" {
		debugf("validateAndCheckAPIServer: api server is empty")
		return "", false, errors.New("api server is empty")
	}

	normalized := normalizeHostPort(apiServer, "6443")
	debugf("normalized api server to %q", normalized)

	// Quick host resolution check
	host, _, _ := net.SplitHostPort(normalized)
	if host == "" {
		debugf("invalid api server host extracted from %q", apiServer)
		return "", false, fmt.Errorf("invalid api server host: %q", apiServer)
	}
	// Resolve host (best-effort)
	if ip := net.ParseIP(host); ip == nil {
		debugf("host %q is not an IP, attempting DNS lookup", host)
		_, err := net.LookupHost(host)
		if err != nil {
			debugf("DNS lookup for host %q failed (non-fatal here): %v", host, err)
		} else {
			debugf("DNS lookup for host %q succeeded", host)
		}
	} else {
		debugf("host %q is a literal IP (%s)", host, ip.String())
	}

	// Try HTTPS GET /version with TLS verification
	url := "https://" + normalized + "/version"
	debugf("probing Kubernetes version at %s (strict TLS)", url)
	ok, insecureUsed, err := probeKubernetesVersionURL(url, false)
	if err == nil && ok {
		debugf("probe succeeded with strict TLS for %s", url)
		return normalized, insecureUsed, nil
	}
	// If TLS verification error, retry with InsecureSkipVerify true
	if err != nil {
		debugf("probe with strict TLS failed for %s: %v; retrying with InsecureSkipVerify", url, err)
		ok2, insecureUsed2, err2 := probeKubernetesVersionURL(url, true)
		if err2 == nil && ok2 {
			debugf("probe succeeded with InsecureSkipVerify for %s", url)
			return normalized, insecureUsed2, nil
		}
		debugf("probe with insecure also failed for %s: %v", url, err2)
		return "", false, fmt.Errorf("failed to contact API server %s: %v; retry with insecure: %v", normalized, err, err2)
	}
	debugf("api server %s did not present a valid Kubernetes version response", normalized)
	return "", false, fmt.Errorf("api server %s did not present a valid Kubernetes version response", normalized)
}

// normalizeHostPort ensures host[:port] is returned (adds defaultPort if missing)
func normalizeHostPort(raw, defaultPort string) string {
	debugf("normalizeHostPort input: %q defaultPort=%q", raw, defaultPort)
	raw = strings.TrimSpace(raw)
	// If contains scheme, strip it
	raw = strings.TrimPrefix(raw, "https://")
	raw = strings.TrimPrefix(raw, "http://")
	if _, _, err := net.SplitHostPort(raw); err == nil {
		debugf("normalizeHostPort returning existing host:port %q", raw)
		return raw
	}
	// If error, it may be missing port. Append default.
	if strings.Contains(raw, ":") && strings.Count(raw, ":") > 1 && !strings.HasPrefix(raw, "[") {
		// IPv6 address without brackets, wrap in brackets
		raw = "[" + raw + "]"
	}
	out := net.JoinHostPort(raw, defaultPort)
	debugf("normalizeHostPort returning %q", out)
	return out
}

// probeKubernetesVersionURL GETs the /version endpoint and verifies JSON contains gitVersion.
func probeKubernetesVersionURL(url string, insecure bool) (bool, bool, error) {
	debugf("probeKubernetesVersionURL: url=%q insecure=%v", url, insecure)
	client := &http.Client{
		Timeout: 5 * time.Second,
	}
	transport := &http.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: insecure},
	}
	client.Transport = transport

	resp, err := client.Get(url)
	if err != nil {
		debugf("HTTP GET %s failed: %v", url, err)
		return false, insecure, err
	}
	defer resp.Body.Close()

	debugf("HTTP GET %s returned status %d", url, resp.StatusCode)
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		debugf("non-200 body from %s: %s", url, string(body))
		return false, insecure, fmt.Errorf("unexpected status %d from %s: %s", resp.StatusCode, url, string(body))
	}

	b, err := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if err != nil {
		debugf("reading body from %s failed: %v", url, err)
		return false, insecure, err
	}
	debugf("read %d bytes from %s", len(b), url)

	var parsed map[string]interface{}
	if err := json.Unmarshal(b, &parsed); err != nil {
		debugf("invalid JSON from %s: %v", url, err)
		return false, insecure, fmt.Errorf("invalid JSON from %s: %w", url, err)
	}
	if _, ok := parsed["gitVersion"]; !ok {
		debugf("response from %s missing gitVersion field; parsed keys: %v", url, mapKeys(parsed))
		return false, insecure, fmt.Errorf("response from %s missing gitVersion field", url)
	}
	debugf("probeKubernetesVersionURL: %s OK (insecure=%v)", url, insecure)
	return true, insecure, nil
}

// expandPath expands ~ to home directory (simple implementation)
func expandPath(p string) string {
	if p == "" {
		return p
	}
	if strings.HasPrefix(p, "~/") || p == "~" {
		home, err := os.UserHomeDir()
		if err != nil || home == "" {
			debugf("expandPath: failed to determine user home dir: %v", err)
			return p
		}
		out := strings.Replace(p, "~", home, 1)
		debugf("expandPath: %q -> %q", p, out)
		return out
	}
	return p
}

// mapKeys returns the keys of a generic map for lightweight debugging output.
func mapKeys(m map[string]interface{}) []string {
	if m == nil {
		return nil
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}