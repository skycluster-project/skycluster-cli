package xkube

import (
	"context"
	"encoding/base64"
	"fmt"
	"log"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/tools/clientcmd/api"

	utils "github.com/etesami/skycluster-cli/internal/utils"
)

var kubeNames []string
var merge *bool

func init() {
	configShowCmd.PersistentFlags().StringSliceVarP(&kubeNames, "xkube", "k", nil, "Kube Names, seperated by comma")
	merge = configShowCmd.PersistentFlags().BoolP("merge", "m", false, "Merge multiple kubeconfigs")
	// configCmd.AddCommand(configShowCmd)
}

var configShowCmd = &cobra.Command{
	Use:   "config",
	Short: "Show current kubeconfig of the xkube",
	Run: func(cmd *cobra.Command, args []string) {
		ns, _ := cmd.Root().PersistentFlags().GetString("namespace")
		showConfigs(kubeNames, ns)
	},
}

func showConfigs(kubeNames []string, ns string) {
	kubeconfig := viper.GetString("kubeconfig")
	dynamicClient, err := utils.GetDynamicClient(kubeconfig)
	if err != nil {
		log.Fatalf("Error getting dynamic client: %v", err)
		return
	}

	var kubeconfigs []string
	for _, c := range kubeNames {
		gvr := schema.GroupVersionResource{Group: "skycluster.io", Version: "v1alpha1", Resource: "xkubes"}
		var ri dynamic.ResourceInterface
		if ns != "" {
			ri = dynamicClient.Resource(gvr).Namespace(ns)
		} else {
			ri = dynamicClient.Resource(gvr)
		}
		
		obj, err := ri.Get(context.Background(), c, metav1.GetOptions{})
		if err != nil {
			log.Printf("Error fetching config [%s]: %v", c, err)
			continue
		}
		// Process the object as needed
		secretName, found, err := unstructured.NestedString(obj.Object, "status", "clusterSecretName")
		if err != nil {
			log.Printf("Error fetching secret name for config [%s]: %v", c, err)
			continue
		}
		if !found {
			log.Printf("Secret name not found for config [%s]", c)
			continue
		}

		// Secrets for xkube objects  with kubeconfig are stored in skycluster-system 
		// namespace.
		skyclusterNamespace := "skycluster-system"

		// Fetch referenced secret
		gvr = schema.GroupVersionResource{Version: "v1", Resource: "secrets"}
		secret, err := dynamicClient.Resource(gvr).Namespace(skyclusterNamespace).
			Get(context.Background(), secretName, metav1.GetOptions{})
		if err != nil {
			log.Printf("Error fetching secret [%s]: %v", secretName, err)
			continue
		}
		// Process the secret as needed
		kubeconfig_b64, found, err := unstructured.NestedString(secret.Object, "data", "kubeconfig")
		if err != nil {
			log.Printf("Error fetching secret data for config [%s]: %v", c, err)
			continue
		}
		if !found {
			log.Printf("Secret data not found for config [%s]", c)
			continue
		}
		kubeconfig, err := base64.StdEncoding.DecodeString(kubeconfig_b64)
		if err != nil {
			panic(err)
		}
		kubeconfigs = append(kubeconfigs, string(kubeconfig))
		// Print the secret data
	}
	
	if *merge {
		mergedConfig := api.NewConfig()
		// Merge the kubeconfigs
		for _, c := range kubeconfigs {
			// Parse the kubeconfig
			config, err := clientcmd.Load([]byte(c))
			if err != nil {
				log.Panicf("Error parsing kubeconfig [%s]: %v", c, err)
				continue
			}
			// Merge clusters, authinfos, and contexts with prefixed names
			for name, cluster := range config.Clusters {
				mergedConfig.Clusters[name] = cluster
			}
			for name, authInfo := range config.AuthInfos {
				mergedConfig.AuthInfos[name] = authInfo
			}
			for name, ctx := range config.Contexts {
				mergedConfig.Contexts[name] = ctx
			}
		}
		// Print the merged kubeconfig
		mergedKubeconfig, err := clientcmd.Write(*mergedConfig)
		if err != nil {
			log.Printf("Error writing merged kubeconfig: %v", err)
			return
		}
		fmt.Printf("%s", mergedKubeconfig)
		return
	}

	fmt.Printf("%s", strings.Join(kubeconfigs, "\n---\n"))
}

