package agentcard

import (
	"encoding/json"
	"testing"
)

func FuzzValidateCardSpec(f *testing.F) {
	valid, err := json.Marshal(validValidationSpec())
	if err != nil {
		f.Fatalf("marshal seed: %v", err)
	}
	f.Add(valid)
	f.Add([]byte(`{"version":"agent-card/v1","title":"x","blocks":[{"kind":"unknown"}]}`))
	f.Add([]byte{0xff, 0xfe, 0xfd})
	f.Fuzz(func(t *testing.T, raw []byte) {
		if len(raw) > 64*1024 {
			t.Skip()
		}
		var spec CardSpec
		if err := json.Unmarshal(raw, &spec); err != nil {
			return
		}
		_ = ValidateCardSpec(spec)
		_ = CheckPolicy(spec, PolicyConfig{
			AllowedHTTPSDomains: []string{"example.com"},
			RiskyIntents:        []string{"delete"},
		})
	})
}
