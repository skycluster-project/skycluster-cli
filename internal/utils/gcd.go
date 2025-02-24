package utils

func Intersect(arrs [][]string) []string {
	if len(arrs) == 0 {
		return []string{}
	}
	countMap := make(map[string]int)
	for _, arr := range arrs {
		seen := make(map[string]bool)
		for _, item := range arr {
			if !seen[item] {
				countMap[item]++
				seen[item] = true
			}
		}
	}
	var result []string
	for key, count := range countMap {
		if count == len(arrs) {
			result = append(result, key)
		}
	}
	return result
}
