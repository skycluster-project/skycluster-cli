package cmd

import (
	"errors"
	"fmt"
	"log"

	"github.com/spf13/viper"
)

var cfgFile string

func traverseMapString(m map[string]interface{}, fields ...string) (string, error) {
	for _, field := range fields[:len(fields)-1] {
		v, ok := m[field]
		if !ok {
			return "", errors.New(fmt.Sprintf("The %s field is missing", field))
		}
		m, ok = v.(map[string]interface{})
		if !ok {
			return "", errors.New(fmt.Sprintf("The %s field is not an object", field))
		}
	}
	v, ok := m[fields[len(fields)-1]]
	if !ok {
		return "", errors.New(fmt.Sprintf("The %s field is missing", fields[len(fields)-1]))
	}
	s, ok := v.(string)
	if !ok {
		return "", errors.New(fmt.Sprintf("The %s field is not a string", fields[len(fields)-1]))
	}
	return s, nil
}

func getKubeconfig(name string) (string, error) {
	kubeCfgs, ok := viper.Get("kubeconfig").(map[string]interface{})
	if !ok {
		log.Fatalf("Error getting kubeconfig: %v", ok)
		return "", errors.New("Error getting kubeconfig")
	}
	skyKubeCfg, ok := kubeCfgs["sky-manager"].(string)
	if !ok {
		log.Fatalf("Error getting sky-manager kubeconfig: %v", ok)
		return "", errors.New("Error getting sky-manager kubeconfig")
	}
	return skyKubeCfg, nil
}
