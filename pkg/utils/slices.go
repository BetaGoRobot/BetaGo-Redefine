package utils

import "math/rand"

func SampleSlice[T any](slices []T) T {
	return slices[rand.Intn(len(slices))]
}

func Chunk[T any](slices []T, size int) [][]T {
	if size <= 0 {
		return nil
	}
	var chunks [][]T
	for i := 0; i < len(slices); i += size {
		end := i + size
		if end > len(slices) {
			end = len(slices)
		}
		chunks = append(chunks, slices[i:end])
	}
	return chunks
}
