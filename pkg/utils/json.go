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

func UnmarshalStrPre[T any](s string, val *T) error {
	err := sonic.UnmarshalString(s, &val)
	if err != nil {
		return err
	}
	return nil
}

func JSON2Map(s string) (map[string]any, error) {
	var m map[string]any
	err := sonic.UnmarshalString(s, &m)
	if err != nil {
		return nil, err
	}
	return m, nil
}

func MustUnmarshalString[T any](s string) *T {
	t := new(T)
	err := sonic.UnmarshalString(s, &t)
	if err != nil {
		panic(err)
	}
	return t
}

func UnmarshalStringPre[T any](s string, val *T) error {
	err := sonic.UnmarshalString(s, &val)
	if err != nil {
		return err
	}
	return nil
}

func Struct2Map(v any) (map[string]any, error) {
	m := make(map[string]any)
	s, err := sonic.Marshal(v)
	if err != nil {
		return nil, err
	}
	err = sonic.Unmarshal(s, &m)
	if err != nil {
		return nil, err
	}
	return m, nil
}
