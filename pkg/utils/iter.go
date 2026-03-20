package utils

import "iter"

func NilIter[T any]() iter.Seq[T] {
	return func(func(T) bool) {}
}
