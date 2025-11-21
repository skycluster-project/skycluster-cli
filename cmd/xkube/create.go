package xkube

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
	xKubeCreateCmd.Flags().StringVarP(&specFile, "spec-file", "f", "", "Path to YAML file containing the XKube spec (required)")
	xKubeCreateCmd.Flags().StringVarP(&resourceName, "name", "n", "", "Name of the XKube resource to create/update")

	// allow classic flag package parsing for compatibility with `go run` / tests
	_ = flag.CommandLine.Parse([]string{})
}

var xKubeCreateCmd = &cobra.Command{
	Use:   "create",
	Short: "Create or update an XKube resource from a YAML spec",
	RunE: func(cmd *cobra.Command, args []string) error {
		if strings.TrimSpace(specFile) == "" {
			return errors.New("flag --spec-file is required")
		}
		// Read spec file
		raw, err := os.ReadFile(expandPath(specFile))
		if err != nil {
			return fmt.Errorf("read spec file: %w", err)
		}

		// Parse YAML into generic map (we expect the YAML to describe the spec fields,
		// not the full CR with apiVersion/kind/metadata).
		// Convert YAML -> JSON -> map[string]interface{} for safe decoding.
		jsonBytes, err := yaml.YAMLToJSON(raw)
		if err != nil {
			return fmt.Errorf("convert yaml to json: %w", err)
		}

		var specMap map[string]interface{}
		if err := json.Unmarshal(jsonBytes, &specMap); err != nil {
			return fmt.Errorf("unmarshal spec json: %w", err)
		}

		// Build unstructured XKube object
		u := &unstructured.Unstructured{
			Object: map[string]interface{}{
				"apiVersion": "skycluster.io/v1alpha1",
				"kind":       "XKube",
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
			return fmt.Errorf("build dynamic client: %w", err)
		}

		if err := createOrUpdateXKube(cmd.Context(), dyn, u); err != nil {
			return fmt.Errorf("create/update XKube %s: %w", u.GetName(), err)
		}

		fmt.Fprintf(os.Stdout, "XKube %s ensured successfully\n", u.GetName())
		return nil
	},
}

// createOrUpdateXKube will create the resource if not present, otherwise merge and update.
// It handles both namespaced and cluster-scoped resources based on u.GetNamespace() presence.
func createOrUpdateXKube(ctx context.Context, dyn dynamic.Interface, u *unstructured.Unstructured) error {
	gvr := schema.GroupVersionResource{
		Group:    "skycluster.io",
		Version:  "v1alpha1",
		Resource: "xkubes",
	}

	name := u.GetName()
	ns := u.GetNamespace()

	var (
		getter dynamic.ResourceInterface
	)

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
		if err != nil {
			return err
		}

		// many clients return a typed API error; use apierrors.IsNotFound when available.
		// As we didn't import apierrors here (not strictly necessary), do a best-effort create on any error that mentions "not found".
		if strings.Contains(err.Error(), "not found") {
			_, err := getter.Create(ctx, u, metav1.CreateOptions{})
			return err
		}
		// Otherwise return error
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