package cleanup

import (
	"context"
	"fmt"
	"strings"

	"github.com/spf13/cobra"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"github.com/spf13/viper"

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
				errs = append(errs, fmt.Sprintf("%s: %v", name, err))
			}
		}

		if len(errs) > 0 {
			return fmt.Errorf("errors deleting secrets: %s", strings.Join(errs, "; "))
		}

		fmt.Println("Requested secrets removed (or already absent).")
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