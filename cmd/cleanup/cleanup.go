package cleanup

import (
	"context"
	"fmt"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/kubernetes"

	"github.com/etesami/skycluster-cli/internal/utils"
)

const namespace = "skycluster-system"

var secretsToDelete = []string{
	"skycluster-kubeconfig",
	"skycluster-keys",
}

func init() {
	// no flags for now; kept for symmetry/extension
}

func GetCleanupCmd() *cobra.Command {
	return cleanupCmd
}

var cleanupCmd = &cobra.Command{
	Use:   "cleanup",
	Short: "Cleanup commands",
	Run: func(cmd *cobra.Command, args []string) {
		kubeconfigPath := viper.GetString("kubeconfig")
		clientset, err := utils.GetClientset(kubeconfigPath)
		if err != nil {
			_ = fmt.Errorf("failed to create kubernetes client: %w", err)
		}

		ctx := context.Background()
		var errs []string

		for _, name := range secretsToDelete {
			if err := deleteSecretIfExists(ctx, clientset, namespace, name); err != nil {
				errs = append(errs, fmt.Sprintf("secret %s: %v", name, err))
			}
		}

		// remove any pods with label skycluster.io/job-type=istio-ca-certs
		if err := deletePodsWithLabel(ctx, clientset, namespace, "skycluster.io/job-type", "istio-ca-certs"); err != nil {
			errs = append(errs, fmt.Sprintf("pods: %v", err))
		}
		// remove any pods with label skycluster.io/job-type=istio-ca-certs
		if err := deletePodsWithLabel(ctx, clientset, namespace, "skycluster.io/job-type", "headscale-cert-gen"); err != nil {
			errs = append(errs, fmt.Sprintf("pods: %v", err))
		}

		submNs := "submariner-operator"
		// finally, delete the namespace itself
		if err := deleteNamespace(ctx, clientset, submNs); err != nil {
			errs = append(errs, fmt.Sprintf("namespace: %v", err))
		}
		// remove submariners.submainer.io objects if any
		// if err := deleteSubmariner(ctx, clientset); err != nil {
		// 	errs = append(errs, fmt.Sprintf("submariner objects: %v", err))
		// }

		if len(errs) > 0 {
			_ = fmt.Errorf("errors during cleanup: %s", strings.Join(errs, "; "))
		} else {
			fmt.Println("Requested secrets and matching pods removed (or already absent).")
		}
	},
}

// deleteSecretIfExists deletes the given secret in the provided namespace.
// If the secret does not exist, it is treated as success.
func deleteSecretIfExists(ctx context.Context, clientset *kubernetes.Clientset, ns, name string) error {
	svc := clientset.CoreV1().Secrets(ns)
	err := svc.Delete(ctx, name, metav1.DeleteOptions{})
	if err == nil {
		fmt.Printf("Deleted secret %s/%s\n", ns, name)
		return nil
	}
	if apierrors.IsNotFound(err) {
		fmt.Printf("Secret %s/%s not found; skipping\n", ns, name)
		return nil
	}
	return fmt.Errorf("delete failed: %w", err)
}

// deletePodsWithLabel finds pods in the namespace matching labelKey=labelValue and deletes them.
// If none found, it's treated as success.
func deletePodsWithLabel(ctx context.Context, clientset *kubernetes.Clientset, ns, labelKey, labelValue string) error {
	labelSelector := fmt.Sprintf("%s=%s", labelKey, labelValue)
	pods, err := clientset.CoreV1().Pods(ns).List(ctx, metav1.ListOptions{LabelSelector: labelSelector})
	if err != nil {
		return fmt.Errorf("listing pods failed: %w", err)
	}
	if len(pods.Items) == 0 {
		fmt.Printf("No pods found in %s with label %s\n", ns, labelSelector)
		return nil
	}

	var errs []string
	for _, p := range pods.Items {
		err := clientset.CoreV1().Pods(ns).Delete(ctx, p.Name, metav1.DeleteOptions{})
		if err == nil {
			fmt.Printf("Deleted pod %s/%s\n", ns, p.Name)
			continue
		}
		if apierrors.IsNotFound(err) {
			fmt.Printf("Pod %s/%s not found; skipping\n", ns, p.Name)
			continue
		}
		errs = append(errs, fmt.Sprintf("%s: %v", p.Name, err))
	}

	if len(errs) > 0 {
		return fmt.Errorf("errors deleting pods: %s", strings.Join(errs, "; "))
	}
	return nil
}

func deleteNamespace(ctx context.Context, clientset *kubernetes.Clientset, ns string) error {
	err := clientset.CoreV1().Namespaces().Delete(ctx, ns, metav1.DeleteOptions{})
	if err != nil {
		return fmt.Errorf("failed to delete namespace %s: %w", ns, err)
	}
	fmt.Printf("Deleted namespace %s\n", ns)
	return nil
}

func deleteSubmariner(ctx context.Context, clientset *kubernetes.Clientset) error {
	// Using dynamic client to delete submariner objects
	dynClient, err := utils.GetDynamicClient(viper.GetString("kubeconfig"))
	if err != nil {
		return fmt.Errorf("failed to create dynamic client: %w", err)
	}

	submGVR := schema.GroupVersionResource{
		Group:    "submariner.io",
		Version:  "v1alpha1",
		Resource: "submariners",
	}
	submList, err := dynClient.Resource(submGVR).Namespace("submariner-operator").List(ctx, metav1.ListOptions{})
	if err != nil {
		return fmt.Errorf("listing submariner objects failed: %w", err)
	}
	if len(submList.Items) == 0 {
		fmt.Println("No submariner objects found; skipping")
		return nil
	}

	var errs []string
	for _, item := range submList.Items {
		// remove finalizers to avoid stuck deletions
		item.SetFinalizers([]string{})
		_, err = dynClient.Resource(submGVR).Namespace("submariner-operator").Update(ctx, &item, metav1.UpdateOptions{})
		if err != nil {
			errs = append(errs, fmt.Sprintf("removing finalizers from %s: %v", item.GetName(), err))
			continue
		}

		err := dynClient.Resource(submGVR).Namespace("submariner-operator").Delete(ctx, item.GetName(), metav1.DeleteOptions{})
		if err != nil {
			errs = append(errs, fmt.Sprintf("%s: %v", item.GetName(), err))
			continue
		}
		fmt.Printf("Deleted submariner object %s/%s\n", "submariner-operator", item.GetName())
	}

	if len(errs) > 0 {
		return fmt.Errorf("errors deleting submariner objects: %s", strings.Join(errs, "; "))
	}
	return nil
}