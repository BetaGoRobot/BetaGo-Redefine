package larkmsg

import "encoding/json"

type RawCard map[string]any

func (c RawCard) JSON() (string, error) {
	data, err := json.Marshal(c)
	if err != nil {
		return "", err
	}
	return string(data), nil
}
