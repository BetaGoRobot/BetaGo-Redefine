package webui

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestModelOptionsConfigValidation(t *testing.T) {
	for _, tc := range []struct {
		value  string
		status int
	}{
		{`{"target":{"service_tier":"flex","reasoning_effort":"high"}}`, http.StatusOK},
		{`{}`, http.StatusOK},
		{`{"target":{"service_tier":"typo"}}`, http.StatusBadRequest},
		{`{"target":{"reasoning_effort":"typo"}}`, http.StatusBadRequest},
		{`{"target":{"temperature":1}}`, http.StatusBadRequest},
		{`{" ":{"service_tier":"flex"}}`, http.StatusBadRequest},
		{`null`, http.StatusBadRequest},
		{`[]`, http.StatusBadRequest},
		{`{"target":null}`, http.StatusBadRequest},
	} {
		t.Run(tc.value, func(t *testing.T) {
			srv, cfg, _ := newTestServer(t, "")
			payload, _ := json.Marshal(map[string]string{"value": tc.value})
			rec := httptest.NewRecorder()
			srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodPut, "/api/chats/global/configs/ark_model_options", strings.NewReader(string(payload))))
			if rec.Code != tc.status {
				t.Fatalf("status = %d, want %d: %s", rec.Code, tc.status, rec.Body.String())
			}
			if tc.status == http.StatusOK && cfg.values["ark_model_options"] == "" {
				t.Fatal("options not stored")
			}
		})
	}
}
