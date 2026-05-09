package utils

import (
	"cmp"
	"maps"
	"slices"
)

func SortMapToSlice[K cmp.Ordered, V any](m map[K]V) []V {
	// 1. 提前提取所有的 Key
	keys := slices.Collect(maps.Keys(m)) // 这里的效率比手动 range 略高，内部做了预分配

	// 2. 对 Key 进行就地排序
	// slices.Sort 比 sort.Strings 或 sort.Slice 更快，因为它利用了泛型，减少了接口转换开销
	slices.Sort(keys)

	// 3. 根据排序后的 Key 构建结果 Slice
	result := make([]V, 0, len(m))
	for _, k := range keys {
		result = append(result, m[k])
	}

	return result
}
