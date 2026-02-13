package utils

import "github.com/bytedance/sonic"

func MustMarshalString(v any) string {
	s, err := sonic.MarshalString(v)
	if err != nil {
		panic(err)
	}
	return s
}

func MustMarshal(v any) []byte {
	s, err := sonic.Marshal(v)
	if err != nil {
		panic(err)
	}
	return s
}

func UnmarshallStrPre[T any](s string, val *T) error {
	err := sonic.UnmarshalString(s, &val)
	if err != nil {
		return err
	}
	return nil
}
