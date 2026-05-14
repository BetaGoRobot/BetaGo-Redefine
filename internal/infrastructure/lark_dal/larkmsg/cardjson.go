package larkmsg

import "github.com/bytedance/sonic"

type RawCard map[string]any

func (c RawCard) JSON() (string, error) {
	data, err := sonic.Marshal(c)
	if err != nil {
		return "", err
	}
	return string(data), nil
}
