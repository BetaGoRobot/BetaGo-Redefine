package utils

func Dedup[T comparable](slice []T) []T {
	seen := make(map[T]struct{})
	result := make([]T, 0)
	for _, item := range slice {
		if _, ok := seen[item]; !ok {
			seen[item] = struct{}{}
			result = append(result, item)
		}
	}
	return result
}
