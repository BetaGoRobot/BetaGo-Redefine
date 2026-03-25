package larkmsg

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	larkcore "github.com/larksuite/oapi-sdk-go/v3/core"
)

func TestDeleteEphemeralMessagePostsMessageID(t *testing.T) {
	originalPost := ephemeralAPIPost
	t.Cleanup(func() {
		ephemeralAPIPost = originalPost
	})

	ephemeralAPIPost = func(ctx context.Context, path string, req any) (*larkcore.ApiResp, error) {
		if path != ephemeralDeletePath {
			t.Fatalf("path = %q, want %q", path, ephemeralDeletePath)
		}
		payload, ok := req.(deleteEphemeralMessageReq)
		if !ok {
			t.Fatalf("request type = %T, want deleteEphemeralMessageReq", req)
		}
		if payload.MessageID != "om_ephemeral_card" {
			t.Fatalf("message id = %q, want %q", payload.MessageID, "om_ephemeral_card")
		}
		raw, err := json.Marshal(map[string]any{
			"code": 0,
			"msg":  "success",
		})
		if err != nil {
			t.Fatalf("Marshal() error = %v", err)
		}
		return &larkcore.ApiResp{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			RawBody:    raw,
		}, nil
	}

	if err := DeleteEphemeralMessage(context.Background(), "om_ephemeral_card"); err != nil {
		t.Fatalf("DeleteEphemeralMessage() error = %v", err)
	}
}
