// file: internal/utils/watch.go
package utils

import (
	"context"
	"fmt"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	meta "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
)

// DebugfFunc is a function type used for debug logging. The caller (e.g. setup package)
// can provide its own implementation (or nil to disable).
type DebugfFunc func(format string, args ...interface{})

// WaitResourceSpec defines a resource that should become Ready=True (or any condition) in order.
type WaitResourceSpec struct {
	KindDescription       string
	GVR                  schema.GroupVersionResource
	Namespace            string
	Name                 string        // resolved name of the Crossplane object / resource
	ManifestMetadataName string        // spec.forProvider.manifest.metadata.name when Name is unknown
	ConditionType        string        // e.g. "Ready", "Available"
	Timeout              time.Duration // overall timeout per resource
	PollInterval         time.Duration // polling interval
}

// ResolveResourceNamesFromManifest performs the "pre-watch phase":
// For each spec where Name is empty and ManifestMetadataName is set, it lists
// the resources of that GVR (and namespace, if set) and finds the one whose
// spec.forProvider.manifest.metadata.name == ManifestMetadataName,
// then fills spec.Name in-place.
func ResolveResourceNamesFromManifest(
	ctx context.Context,
	dyn dynamic.Interface,
	resources []WaitResourceSpec,
	debugf DebugfFunc,
) error {
	for i := range resources {
		spec := &resources[i]
		if spec.Name != "" || spec.ManifestMetadataName == "" {
			// nothing to resolve
			continue
		}

		if debugf != nil {
			debugf("pre-watch: resolving %s via spec.forProvider.manifest.metadata.name=%q in %s %s",
				spec.KindDescription,
				spec.ManifestMetadataName,
				spec.GVR.Resource,
				spec.Namespace,
			)
		}

		resClient := dyn.Resource(spec.GVR)

		var list *unstructured.UnstructuredList
		var err error
		if spec.Namespace == "" {
			list, err = resClient.List(ctx, meta.ListOptions{})
		} else {
			list, err = resClient.Namespace(spec.Namespace).List(ctx, meta.ListOptions{})
		}
		if err != nil {
			return fmt.Errorf("listing %s for %s: %w", spec.GVR.Resource, spec.KindDescription, err)
		}

		foundName := ""
		manifestName := ""
		for _, item := range list.Items {
			switch spec.GVR.Resource {
			case "objects":
				manifestName, _, _ = unstructured.NestedString(
					item.Object, "spec", "forProvider", "manifest", "metadata", "name",
				)
			case "releases":
				manifestName, _, _ = unstructured.NestedString(
					item.Object, "spec", "forProvider", "chart", "name",
				)
			// Add more resource types here as needed
			default:
				return fmt.Errorf("unsupported GVR resource %s for resolving manifest name", spec.GVR.Resource)
			}
			if manifestName == spec.ManifestMetadataName {
				foundName = item.GetName()
				if debugf != nil {
					debugf("pre-watch: %s matched Crossplane object %s/%s (manifest.metadata.name=%q)",
						spec.KindDescription,
						item.GetNamespace(),
						item.GetName(),
						manifestName,
					)
				}
				break
			}
		}

		if foundName == "" {
			return fmt.Errorf(
				"could not resolve Crossplane object name for %s (GVR=%s, ns=%s, manifest.metadata.name=%q)",
				spec.KindDescription,
				spec.GVR.Resource,
				spec.Namespace,
				spec.ManifestMetadataName,
			)
		}

		spec.Name = foundName
	}

	return nil
}

// WaitForResourcesReadySequential waits for each resource in order, showing progress via printFn.
func WaitForResourcesReadySequential(
	parentCtx context.Context,
	dyn dynamic.Interface,
	resources []WaitResourceSpec,
	printFn func(format string, args ...interface{}),
	debugf DebugfFunc,
) error {
	if len(resources) == 0 {
		return nil
	}

	if printFn == nil {
		printFn = func(string, ...interface{}) {}
	}

	total := len(resources)
	completed := 0

	for i, spec := range resources {
		index := i + 1
		progress := float64(completed) / float64(total) * 100
		printFn("[%.0f%%] Waiting for %s (%d/%d): %s/%s %s\n",
			progress,
			spec.KindDescription,
			index, total,
			coalesce(spec.Namespace, "<cluster-scope>"),
			spec.Name,
			spec.GVR.Resource,
		)

		ctx, cancel := context.WithTimeout(parentCtx, spec.Timeout)
		err := waitForSingleResourceReady(ctx, dyn, spec, debugf)
		cancel()
		if err != nil {
			return fmt.Errorf("resource %s (%s %s/%s) did not become %s=True: %w",
				spec.KindDescription,
				spec.GVR.Resource,
				coalesce(spec.Namespace, "<cluster-scope>"),
				spec.Name,
				spec.ConditionType,
				err,
			)
		}

		completed++
		progress = float64(completed) / float64(total) * 100
		printFn("[%.0f%%] %s is Ready: %s/%s %s\n",
			progress,
			spec.KindDescription,
			coalesce(spec.Namespace, "<cluster-scope>"),
			spec.Name,
			spec.GVR.Resource,
		)
	}

	return nil
}

// waitForSingleResourceReady polls a single resource until the given condition is True.
// IMPORTANT: the first GET happens immediately, without waiting for PollInterval.
func waitForSingleResourceReady(
	ctx context.Context,
	dyn dynamic.Interface,
	spec WaitResourceSpec,
	debugf DebugfFunc,
) error {
	resClient := dyn.Resource(spec.GVR)
	getFn := func() (*unstructured.Unstructured, error) {
		if spec.Namespace == "" {
			return resClient.Get(ctx, spec.Name, meta.GetOptions{})
		}
		return resClient.Namespace(spec.Namespace).Get(ctx, spec.Name, meta.GetOptions{})
	}

	// First call immediately (no waiting for PollInterval)
	obj, err := getFn()
	if apierrors.IsNotFound(err) {
		if debugf != nil {
			debugf("wait: initial GET - resource %s %s/%s %s not found yet",
				spec.KindDescription,
				coalesce(spec.Namespace, "<cluster-scope>"),
				spec.Name,
				spec.GVR.Resource,
			)
		}
	} else if err != nil {
		if debugf != nil {
			debugf("wait: initial GET - error getting %s %s/%s %s: %v",
				spec.KindDescription,
				coalesce(spec.Namespace, "<cluster-scope>"),
				spec.Name,
				spec.GVR.Resource,
				err,
			)
		}
	} else {
		if isConditionTrue(obj, spec.ConditionType) {
			if debugf != nil {
				debugf("wait: initial GET - resource %s %s/%s %s condition %s=True",
					spec.KindDescription,
					coalesce(spec.Namespace, "<cluster-scope>"),
					spec.Name,
					spec.GVR.Resource,
					spec.ConditionType,
				)
			}
			return nil
		}
		if debugf != nil {
			debugf("wait: initial GET - resource %s %s/%s %s not ready yet (condition %s!=True)",
				spec.KindDescription,
				coalesce(spec.Namespace, "<cluster-scope>"),
				spec.Name,
				spec.GVR.Resource,
				spec.ConditionType,
			)
		}
	}

	// Then poll with interval
	ticker := time.NewTicker(spec.PollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return fmt.Errorf("timeout or context cancelled while waiting for %s %s/%s %s condition %s=True: %w",
				spec.KindDescription,
				coalesce(spec.Namespace, "<cluster-scope>"),
				spec.Name,
				spec.GVR.Resource,
				spec.ConditionType,
				ctx.Err(),
			)
		case <-ticker.C:
			obj, err := getFn()
			if apierrors.IsNotFound(err) {
				if debugf != nil {
					debugf("wait: resource %s %s/%s %s not found yet",
						spec.KindDescription,
						coalesce(spec.Namespace, "<cluster-scope>"),
						spec.Name,
						spec.GVR.Resource,
					)
				}
				continue
			}
			if err != nil {
				if debugf != nil {
					debugf("wait: error getting %s %s/%s %s: %v",
						spec.KindDescription,
						coalesce(spec.Namespace, "<cluster-scope>"),
						spec.Name,
						spec.GVR.Resource,
						err,
					)
				}
				continue
			}

			if isConditionTrue(obj, spec.ConditionType) {
				if debugf != nil {
					debugf("wait: resource %s %s/%s %s condition %s=True",
						spec.KindDescription,
						coalesce(spec.Namespace, "<cluster-scope>"),
						spec.Name,
						spec.GVR.Resource,
						spec.ConditionType,
					)
				}
				return nil
			}
			if debugf != nil {
				debugf("wait: resource %s %s/%s %s not ready yet (condition %s!=True)",
					spec.KindDescription,
					coalesce(spec.Namespace, "<cluster-scope>"),
					spec.Name,
					spec.GVR.Resource,
					spec.ConditionType,
				)
			}
		}
	}
}

// IsConditionTrue checks status.conditions[*].type == condType && status == "True".
func IsConditionTrue(obj *unstructured.Unstructured, condType string) bool {
	return isConditionTrue(obj, condType)
}

// internal helper, reused by Wait* functions above.
func isConditionTrue(obj *unstructured.Unstructured, condType string) bool {
	if obj == nil {
		return false
	}

	status, found, err := unstructured.NestedMap(obj.Object, "status")
	if err != nil || !found {
		return false
	}

	conds, found, err := unstructured.NestedSlice(status, "conditions")
	if err != nil || !found {
		return false
	}

	for _, c := range conds {
		m, ok := c.(map[string]interface{})
		if !ok {
			continue
		}
		t, _, _ := unstructured.NestedString(m, "type")
		s, _, _ := unstructured.NestedString(m, "status")
		if t == condType && stringsEqualFoldTrue(s) {
			return true
		}
	}
	return false
}

func stringsEqualFoldTrue(s string) bool {
	return len(s) == 4 && (s == "True" || s == "TRUE" || s == "true")
}

func coalesce(s, fallback string) string {
	if s == "" {
		return fallback
	}
	return s
}