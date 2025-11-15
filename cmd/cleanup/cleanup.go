package cleanup

import (
	"context"
	"fmt"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
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
	RunE: func(cmd *cobra.Command, args []string) error {
		kubeconfigPath := viper.GetString("kubeconfig")
		clientset, err := utils.GetClientset(kubeconfigPath)
		if err != nil {
			return fmt.Errorf("failed to create kubernetes client: %w", err)
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

		if len(errs) > 0 {
			return fmt.Errorf("errors during cleanup: %s", strings.Join(errs, "; "))
		}

		fmt.Println("Requested secrets and matching pods removed (or already absent).")
		return nil
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