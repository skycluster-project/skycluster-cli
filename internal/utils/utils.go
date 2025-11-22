package utils

import (
	"errors"
	"fmt"
	"log"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/spf13/viper"
)

// helper to extract a condition's "status" (e.g. "True"/"False"/"Unknown")
func GetConditionStatus(obj *unstructured.Unstructured, condType string) string {
	if arr, found, _ := unstructured.NestedSlice(obj.Object, "status", "conditions"); found {
		for _, item := range arr {
			if m, ok := item.(map[string]interface{}); ok {
				if t, ok := m["type"].(string); ok && t == condType {
					if s, ok := m["status"].(string); ok {return s}
				}
			}
		}
	}
	return ""
}

func IntersectionOfMapValues(m map[string][]string, keys []string) []string {
	if len(m) == 0 {
		return nil
	}
	count := make(map[string]int)
	for _, k := range keys {
		unique := make(map[string]struct{})
		for _, item := range m[k] {
			unique[item] = struct{}{}
		}
		for item := range unique {
			count[item]++
		}
	}
	var inter []string
	for item, c := range count {
		if c == len(keys) {
			inter = append(inter, item)
		}
	}
	return inter
}

func GetMapStringKeys(m map[string][]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}

// Generate all combinations of a list of strings
func GetCombinations(arr []string) [][]string {
	result := [][]string{}
	n := len(arr)
	// There are 2^n possible combinations
	for i := 1; i < (1 << n); i++ {
		var combo []string
		for j := 0; j < n; j++ {
			if i&(1<<j) != 0 {
				combo = append(combo, arr[j])
			}
		}
		result = append(result, combo)
	}
	return result
}

func TraverseMapString(m map[string]interface{}, fields ...string) (string, error) {
	for _, field := range fields[:len(fields)-1] {
		v, ok := m[field]
		if !ok {
			return "", fmt.Errorf("the %s field is missing", field)
		}
		m, ok = v.(map[string]interface{})
		if !ok {
			return "", fmt.Errorf("the %s field is not an object", field)
		}
	}
	v, ok := m[fields[len(fields)-1]]
	if !ok {
		return "", fmt.Errorf("the %s field is missing", fields[len(fields)-1])
	}
	s, ok := v.(string)
	if !ok {
		return "", fmt.Errorf("the %s field is not a string", fields[len(fields)-1])
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
