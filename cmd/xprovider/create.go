package xprovider

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"

	"sigs.k8s.io/yaml"

	"github.com/etesami/skycluster-cli/internal/utils"
)

var (
	specFile     string
	resourceName string
)

func init() {
	// Cobra flags for this command
	xProviderCreateCmd.Flags().StringVarP(&specFile, "spec-file", "f", "", "Path to YAML file containing the XProvider spec (required)")
	xProviderCreateCmd.Flags().StringVarP(&resourceName, "name", "n", "", "Name of the XProvider resource to create/update")

	// allow classic flag package parsing for compatibility with `go run` / tests
	_ = flag.CommandLine.Parse([]string{})
}

var xProviderCreateCmd = &cobra.Command{
	Use:   "create",
	Short: "Create or update an XProvider resource from a YAML spec",
	RunE: func(cmd *cobra.Command, args []string) error {
		debugf("xprovider create invoked: spec-file=%q name=%q", specFile, resourceName)

		if strings.TrimSpace(specFile) == "" {
			debugf("missing required flag --spec-file")
			return errors.New("flag --spec-file is required")
		}
		// Read spec file
		raw, err := os.ReadFile(expandPath(specFile))
		if err != nil {
			debugf("read spec file %s failed: %v", specFile, err)
			return fmt.Errorf("read spec file: %w", err)
		}
		debugf("read %d bytes from spec file %s", len(raw), specFile)

		// Parse YAML into generic map (we expect the YAML to describe the spec fields,
		// not the full CR with apiVersion/kind/metadata).
		// Convert YAML -> JSON -> map[string]interface{} for safe decoding.
		jsonBytes, err := yaml.YAMLToJSON(raw)
		if err != nil {
			debugf("yaml to json conversion failed: %v", err)
			return fmt.Errorf("convert yaml to json: %w", err)
		}
		debugf("converted YAML to JSON (%d bytes)", len(jsonBytes))

		var specMap map[string]interface{}
		if err := json.Unmarshal(jsonBytes, &specMap); err != nil {
			debugf("unmarshal spec json failed: %v; json: %s", err, string(jsonBytes))
			return fmt.Errorf("unmarshal spec json: %w", err)
		}
		debugf("parsed spec keys: %v", mapKeys(specMap))

		// Build unstructured XProvider object
		u := &unstructured.Unstructured{
			Object: map[string]interface{}{
				"apiVersion": "skycluster.io/v1alpha1",
				"kind":       "XProvider",
				"metadata": map[string]interface{}{
					"name": resourceName,
				},
				"spec": specMap,
			},
		}
		if j, err := json.MarshalIndent(u.Object, "", "  "); err == nil {
			debugf("constructed unstructured object: %s", string(j))
		} else {
			debugf("could not marshal constructed object for debug: %v", err)
		}

		// Build dynamic client using kubeconfig from viper
		kubeconfigPath := viper.GetString("kubeconfig")
		if strings.TrimSpace(kubeconfigPath) == "" {
			// If not provided, let utils package decide (it may default to KUBECONFIG env or in-cluster)
			kubeconfigPath = ""
		}
		debugf("using kubeconfig: %q", kubeconfigPath)

		dyn, err := utils.GetDynamicClient(kubeconfigPath)
		if err != nil {
			debugf("failed to build dynamic client with kubeconfig %q: %v", kubeconfigPath, err)
			return fmt.Errorf("build dynamic client: %w", err)
		}
		debugf("dynamic client initialized")

		if err := createOrUpdateXProvider(cmd.Context(), dyn, u); err != nil {
			debugf("createOrUpdateXProvider failed for %s: %v", u.GetName(), err)
			return fmt.Errorf("create/update XProvider %s: %w", u.GetName(), err)
		}

		fmt.Fprintf(os.Stdout, "XProvider %s ensured successfully\n", u.GetName())
		return nil
	},
}

// createOrUpdateXProvider will create the resource if not present, otherwise merge and update.
// It handles both namespaced and cluster-scoped resources based on u.GetNamespace() presence.
func createOrUpdateXProvider(ctx context.Context, dyn dynamic.Interface, u *unstructured.Unstructured) error {
	gvr := schema.GroupVersionResource{
		Group:    "skycluster.io",
		Version:  "v1alpha1",
		Resource: "xproviders",
	}

	name := u.GetName()
	ns := u.GetNamespace()
	debugf("ensuring XProvider %s (namespace=%q)", name, ns)

	var getter dynamic.ResourceInterface
	if ns == "" {
		getter = dyn.Resource(gvr)
		debugf("using cluster-scoped resource interface for %s", gvr.Resource)
	} else {
		getter = dyn.Resource(gvr).Namespace(ns)
		debugf("using namespaced resource interface for namespace %s", ns)
	}

	debugf("attempting to GET existing resource %s", name)
	existing, err := getter.Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		debugf("GET returned error: %v", err)
		if apierrors.IsNotFound(err) {
			debugf("resource %s not found, creating", name)
			created, createErr := getter.Create(ctx, u, metav1.CreateOptions{})
			if createErr != nil {
				debugf("create failed for %s: %v", name, createErr)
				return createErr
			}
			debugf("created resource %s (uid: %v)", name, created.GetUID())
			return nil
		}
		// Some clients may not return typed errors; do a best-effort string check.
		if strings.Contains(err.Error(), "not found") {
			debugf("GET error contains 'not found', attempting create for %s", name)
			created, createErr := getter.Create(ctx, u, metav1.CreateOptions{})
			if createErr != nil {
				debugf("create failed for %s after not-found string match: %v", name, createErr)
				return createErr
			}
			debugf("created resource %s (uid: %v) after not-found string match", name, created.GetUID())
			return nil
		}
		return err
	}

	debugf("resource %s exists (uid: %v), preparing to merge", name, existing.GetUID())

	// Merge existing and new objects: overlay u onto existing so unspecified fields are preserved.
	merged := existing.DeepCopy()
	merged.Object = mergeMaps(merged.Object, u.Object)
	if j, err := json.MarshalIndent(merged.Object, "", "  "); err == nil {
		debugf("merged object: %s", string(j))
	} else {
		debugf("could not marshal merged object for debug: %v", err)
	}

	updated, err := getter.Update(ctx, merged, metav1.UpdateOptions{})
	if err != nil {
		debugf("update failed for %s: %v", name, err)
		return err
	}
	debugf("updated resource %s (uid: %v)", name, updated.GetUID())
	return nil
}

// mergeMaps overlays src onto dst recursively. For keys where both dst and src are maps,
// the merge is performed recursively. Other values from src overwrite dst. dst is mutated
// and returned as the resulting map.
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

// expandPath expands leading '~' to the user home directory.
func expandPath(p string) string {
	if p == "" {
		return p
	}
	if strings.HasPrefix(p, "~/") || p == "~" {
		home, err := os.UserHomeDir()
		if err != nil {
			debugf("expandPath: failed to determine user home dir: %v", err)
			return p // fallback: return unchanged
		}
		// If p is exactly "~", TrimPrefix will return "", and Join(home, "") => home
		out := filepath.Join(home, strings.TrimPrefix(p, "~/"))
		debugf("expandPath: %q -> %q", p, out)
		return out
	}
	return p
}

// mapKeys returns the keys of a map for lightweight debugging output.
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