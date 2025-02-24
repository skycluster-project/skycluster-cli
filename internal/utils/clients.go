package utils

import (
	"os"

	"k8s.io/client-go/dynamic"
	clientset "k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
)

func GetDynamicClient(kubeconfig string) (dynamic.Interface, error) {
	// check if the file exists
	if _, err := os.Stat(kubeconfig); os.IsNotExist(err) {
		return nil, err
	}

	config, err := clientcmd.BuildConfigFromFlags("", kubeconfig)
	if err != nil {
		return nil, err
	}

	dynamicClient, err := dynamic.NewForConfig(config)
	if err != nil {
		return nil, err
	}
	return dynamicClient, nil
}

func GetClientset(kubeconfig string) (*clientset.Clientset, error) {
	// check if the file exists
	if _, err := os.Stat(kubeconfig); os.IsNotExist(err) {
		return nil, err
	}

	config, err := clientcmd.BuildConfigFromFlags("", kubeconfig)
	if err != nil {
		return nil, err
	}

	clientset, err := clientset.NewForConfig(config)
	if err != nil {
		return nil, err
	}
	return clientset, nil
}
