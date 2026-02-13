package utils

func AddrOrNil[P any, T *P](input T) P {
	if input == nil {
		return *new(P)
	}
	return *input
}
