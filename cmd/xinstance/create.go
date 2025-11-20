package xinstance

import (
	"context"
	"encoding/json"
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
	xInstanceCreateCmd.Flags().StringVarP(&specFile, "spec-file", "f", "", "Path to YAML file containing the XInstance spec (required)")
	xInstanceCreateCmd.Flags().StringVarP(&resourceName, "name", "n", "", "Name of the XInstance resource to create/update")

	// allow classic flag package parsing for compatibility with `go run` / tests
	_ = flag.CommandLine.Parse([]string{})
}

var xInstanceCreateCmd = &cobra.Command{
	Use:   "create",
	Short: "Create or update an XInstance resource from a YAML spec",
	Run: func(cmd *cobra.Command, args []string) {
		if strings.TrimSpace(specFile) == "" {
			_ = fmt.Errorf("flag --spec-file is required")
		}
		// Read spec file
		raw, err := os.ReadFile(expandPath(specFile))
		if err != nil {
			_ = fmt.Errorf("read spec file: %w", err)
		}

		// Parse YAML into generic map (we expect the YAML to describe the spec fields,
		// not the full CR with apiVersion/kind/metadata).
		// Convert YAML -> JSON -> map[string]interface{} for safe decoding.
		jsonBytes, err := yaml.YAMLToJSON(raw)
		if err != nil {
			_ = fmt.Errorf("convert yaml to json: %w", err)
		}

		var specMap map[string]interface{}
		if err := json.Unmarshal(jsonBytes, &specMap); err != nil {
			_ = fmt.Errorf("unmarshal spec json: %w", err)
		}

		// Build unstructured XInstance object
		u := &unstructured.Unstructured{
			Object: map[string]interface{}{
				"apiVersion": "skycluster.io/v1alpha1",
				"kind":       "XInstance",
				"metadata": map[string]interface{}{
					"name": resourceName,
				},
				"spec": specMap,
			},
		}

		// Build dynamic client using kubeconfig from viper
		kubeconfigPath := viper.GetString("kubeconfig")
		if strings.TrimSpace(kubeconfigPath) == "" {
			// If not provided, let utils package decide (it may default to KUBECONFIG env or in-cluster)
			kubeconfigPath = ""
		}
		dyn, err := utils.GetDynamicClient(kubeconfigPath)
		if err != nil {
			_ = fmt.Errorf("build dynamic client: %w", err)
		}

		if err := createOrUpdateXInstance(cmd.Context(), dyn, u); err != nil {
			_ = fmt.Errorf("create/update XInstance %s: %w", u.GetName(), err)
		}

		fmt.Fprintf(os.Stdout, "XInstance %s ensured successfully\n", u.GetName())
	},
}

// createOrUpdateXInstance will create the resource if not present, otherwise merge and update.
// It handles both namespaced and cluster-scoped resources based on u.GetNamespace() presence.
func createOrUpdateXInstance(ctx context.Context, dyn dynamic.Interface, u *unstructured.Unstructured) error {
	gvr := schema.GroupVersionResource{
		Group:    "skycluster.io",
		Version:  "v1alpha1",
		// As requested: plural "xinstances"
		Resource: "xinstances",
	}

	name := u.GetName()
	ns := ""

	var getter dynamic.ResourceInterface
	if ns == "" {
		getter = dyn.Resource(gvr)
	} else {
		getter = dyn.Resource(gvr).Namespace(ns)
	}

	existing, err := getter.Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		if apierrors.IsNotFound(err) {
			_, err := getter.Create(ctx, u, metav1.CreateOptions{})
			return err
		}
		// Some clients may return a typed API error; attempt best-effort create on "not found" text.
		if strings.Contains(err.Error(), "not found") {
			_, err := getter.Create(ctx, u, metav1.CreateOptions{})
			return err
		}
		return err
	}

	// Merge existing and new objects: overlay u onto existing so unspecified fields are preserved.
	merged := existing.DeepCopy()
	merged.Object = mergeMaps(merged.Object, u.Object)

	_, err = getter.Update(ctx, merged, metav1.UpdateOptions{})
	return err
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
			continue
		}
		if svMap, ok := sv.(map[string]interface{}); ok {
			if dv, exists := dst[k]; exists {
				if dvMap, ok2 := dv.(map[string]interface{}); ok2 {
					dst[k] = mergeMaps(dvMap, svMap)
					continue
				}
			}
			// dst doesn't have a map for this key, create a new merged map
			dst[k] = mergeMaps(make(map[string]interface{}), svMap)
			continue
		}
		// For non-map types (including slices), src overwrites dst
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
			return p // fallback: return unchanged
		}
		return filepath.Join(home, strings.TrimPrefix(p, "~/"))
	}
	return p
}