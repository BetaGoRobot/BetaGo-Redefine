package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestModelOptionsLoadValidation(t *testing.T) {
	old := config
	t.Cleanup(func() { config = old })
	for _, tc := range []struct {
		name, body string
		valid      bool
	}{
		{"valid", "service_tier = 'flex'\nreasoning_effort = 'high'", true},
		{"invalid tier", "service_tier = 'typo'", false},
		{"invalid effort", "reasoning_effort = 'typo'", false},
		{"unknown field", "temperature = 1", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.toml")
			if err := os.WriteFile(path, []byte("[ark_config.model_options.target]\n"+tc.body), 0600); err != nil {
				t.Fatal(err)
			}
			_, err := LoadFileE(path)
			if (err == nil) != tc.valid {
				t.Fatalf("LoadFileE error = %v, valid = %v", err, tc.valid)
			}
		})
	}
}
