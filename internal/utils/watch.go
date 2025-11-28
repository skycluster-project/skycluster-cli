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

// DebugfFunc is a function type used for debug logging. The caller can provide
// its own implementation (or nil to disable).
type DebugfFunc func(format string, args ...interface{})

// ProgressSink is a callback used to report progress in a more "modern/dynamic"
// way. You can plug this into a TUI, spinner, etc.
type ProgressSink func(ev ProgressEvent)

// ProgressEvent describes the current state of the waiting process.
type ProgressEvent struct {
	// Human-readable description of what we're waiting for.
	Message string

	// Index of the current resource (1-based) and total resources.
	CurrentIndex int
	Total        int

	// Overall progress in percent [0,100].
	OverallPercent float64

	// Name and kind of the current resource.
	KindDescription string
	Namespace       string
	Name            string
	GVR             schema.GroupVersionResource

	// True when this particular resource just became Ready.
	ResourceCompleted bool

	// Error, if any, associated with this progress update.
	Err error
}

// WaitResourceSpec defines a resource that should become Ready=True (or any
// other condition) in order.
type WaitResourceSpec struct {
	KindDescription       string
	GVR                  schema.GroupVersionResource
	Namespace            string
	Name                 string        // resolved name of the Crossplane object / resource
	ManifestMetadataName string        // when Name is unknown
	ConditionType        string        // e.g. "Ready", "Available"
	Timeout              time.Duration // overall timeout per resource
	PollInterval         time.Duration // polling interval
}

// ResolveResourceNamesFromManifest performs the "pre-watch phase":
// For each spec where Name is empty and ManifestMetadataName is set, it lists
// the resources of that GVR (and namespace, if set) and finds the one whose
// manifest-derived name matches ManifestMetadataName, then fills spec.Name.
func ResolveResourceNamesFromManifest(
	ctx context.Context,
	dyn dynamic.Interface,
	resources []WaitResourceSpec,
	debugf DebugfFunc,
) error {
	for i := range resources {
		spec := &resources[i]
		if spec.Name != "" || spec.ManifestMetadataName == "" {
			continue
		}

		if debugf != nil {
			debugf("pre-watch: resolving %s via manifest name=%q in %s %s",
				spec.KindDescription,
				spec.ManifestMetadataName,
				spec.GVR.Resource,
				spec.Namespace,
			)
		}

		resClient := dyn.Resource(spec.GVR)

		var (
			list *unstructured.UnstructuredList
			err  error
		)
		if spec.Namespace == "" {
			list, err = resClient.List(ctx, meta.ListOptions{})
		} else {
			list, err = resClient.Namespace(spec.Namespace).List(ctx, meta.ListOptions{})
		}
		if err != nil {
			return fmt.Errorf("listing %s for %s: %w", spec.GVR.Resource, spec.KindDescription, err)
		}

		foundName := ""
		for _, item := range list.Items {
			manifestName, err := extractManifestName(item.Object, spec.GVR.Resource)
			if err != nil {
				return fmt.Errorf("extract manifest name for %s: %w", spec.KindDescription, err)
			}
			if manifestName == spec.ManifestMetadataName {
				foundName = item.GetName()
				if debugf != nil {
					debugf("pre-watch: %s matched Crossplane object %s/%s (manifest name=%q)",
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
				"could not resolve object name for %s (GVR=%s, ns=%s, manifest name=%q)",
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

// extractManifestName centralizes how we look up the "manifest name" for
// different Crossplane resource types.
func extractManifestName(obj map[string]interface{}, resource string) (string, error) {
	switch resource {
	case "objects":
		name, _, _ := unstructured.NestedString(
			obj, "spec", "forProvider", "manifest", "metadata", "name",
		)
		return name, nil
	case "releases":
		name, _, _ := unstructured.NestedString(
			obj, "spec", "forProvider", "chart", "name",
		)
		return name, nil
	case "instancetypes", "images":
		name, _, _ := unstructured.NestedString(
			obj, "metadata", "generateName",
		)
		return name, nil
	default:
		return "", fmt.Errorf("unsupported GVR resource %s for resolving manifest name", resource)
	}
}

// WaitForResourcesReadySequential waits for each resource in order and reports
// progress via progressSink. This is designed to be "dynamic" and can back a
// TUI, spinner, or any modern progress view.
func WaitForResourcesReadySequential(
	parentCtx context.Context,
	dyn dynamic.Interface,
	resources []WaitResourceSpec,
	progressSink ProgressSink,
	debugf DebugfFunc,
) error {
	if len(resources) == 0 {
		return nil
	}

	// no-op sink if nil
	if progressSink == nil {
		progressSink = func(ProgressEvent) {}
	}

	total := len(resources)
	completed := 0

	for i, spec := range resources {
		index := i + 1
		overallPercent := float64(completed) / float64(total) * 100

		progressSink(ProgressEvent{
			Message:          fmt.Sprintf("Waiting for %s", spec.KindDescription),
			CurrentIndex:     index,
			Total:            total,
			OverallPercent:   overallPercent,
			KindDescription:  spec.KindDescription,
			Namespace:        coalesce(spec.Namespace, "<cluster-scope>"),
			Name:             spec.Name,
			GVR:              spec.GVR,
			ResourceCompleted: false,
		})

		ctx, cancel := context.WithTimeout(parentCtx, spec.Timeout)
		err := waitForSingleResourceReady(ctx, dyn, spec, debugf)
		cancel()
		if err != nil {
			progressSink(ProgressEvent{
				Message:         fmt.Sprintf("Error waiting for %s", spec.KindDescription),
				CurrentIndex:    index,
				Total:           total,
				OverallPercent:  overallPercent,
				KindDescription: spec.KindDescription,
				Namespace:       coalesce(spec.Namespace, "<cluster-scope>"),
				Name:            spec.Name,
				GVR:             spec.GVR,
				Err:             err,
			})
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
		overallPercent = float64(completed) / float64(total) * 100

		progressSink(ProgressEvent{
			Message:          fmt.Sprintf("%s is Ready", spec.KindDescription),
			CurrentIndex:     index,
			Total:            total,
			OverallPercent:   overallPercent,
			KindDescription:  spec.KindDescription,
			Namespace:        coalesce(spec.Namespace, "<cluster-scope>"),
			Name:             spec.Name,
			GVR:              spec.GVR,
			ResourceCompleted: true,
		})
	}

	return nil
}

// waitForSingleResourceReady polls a single resource until the given condition
// is True. The first GET happens immediately (no wait).
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