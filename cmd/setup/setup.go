package setup

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"github.com/spf13/viper"
	"k8s.io/client-go/kubernetes"

	"github.com/etesami/skycluster-cli/internal/utils"
)

var (
	publicKeyPath  string
	privateKeyPath string
	kubeconfigPath string
)

func init() {
	// Use Cobra flags (also support go test / `go run` style flags fallback)
	setupCmd.Flags().StringVar(&publicKeyPath, "public", "", "Path to public key (e.g. ~/.ssh/id_rsa.pub)")
	setupCmd.Flags().StringVar(&privateKeyPath, "private", "", "Path to private key (e.g. ~/.ssh/id_rsa)")
	setupCmd.Flags().StringVar(&kubeconfigPath, "kubeconfig", "", "Path to kubeconfig to store in secret")
	// make flags available to library using standard flag package (optional)
	_ = flag.CommandLine.Parse([]string{})
}

var setupCmd = &cobra.Command{
	Use:   "setup",
	Short: "Setup commands",
	RunE: func(cmd *cobra.Command, args []string) error {
		// Validate required flags
		if publicKeyPath == "" || privateKeyPath == "" || kubeconfigPath == "" {
			return errors.New("flags --public, --private and --kubeconfig are required")
		}

		// check files exist and read them
		pubBytes, err := os.ReadFile(expandPath(publicKeyPath))
		if err != nil {
			return fmt.Errorf("reading public key: %w", err)
		}
		privBytes, err := os.ReadFile(expandPath(privateKeyPath))
		if err != nil {
			return fmt.Errorf("reading private key: %w", err)
		}
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
					"skycluster.io/managed-by": "skycluster",
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
					"skycluster.io/managed-by": "skycluster",
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

		// Create client using in-cluster config
		kubeconfig := viper.GetString("kubeconfig")
		clientset, err := utils.GetClientset(kubeconfig)
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

		fmt.Println("Secrets created/updated successfully")
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