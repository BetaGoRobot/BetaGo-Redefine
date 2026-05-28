package ark_dal

import (
	"context"
	"testing"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/llmusage"
	"github.com/volcengine/volcengine-go-sdk/service/arkruntime/model"
)

func TestRecordEmbeddingUsageWritesTokenUsage(t *testing.T) {
	store := &arkUsageStore{}
	llmusage.SetDefaultRecorder(llmusage.NewRecorderWithStore(store))
	t.Cleanup(func() {
		llmusage.SetDefaultRecorder(nil)
	})
	oldNow := utilsNow
	utilsNow = func() time.Time {
		return time.Date(2026, 5, 28, 9, 10, 0, 0, time.UTC)
	}
	t.Cleanup(func() {
		utilsNow = oldNow
	})

	scope := llmusage.Scope{
		ChatID:     "oc_chat",
		ChatName:   "Test Chat",
		OpenID:     "ou_user",
		UserName:   "Alice",
		SourceType: llmusage.SourceTypeUser,
		Source:     "message_recording",
	}
	recordEmbeddingUsage(context.Background(), scope, "embedding-model", model.Usage{
		PromptTokens:     9,
		CompletionTokens: 0,
		TotalTokens:      9,
	}, nil)

	if len(store.rows) != 1 {
		t.Fatalf("usage row count = %d, want 1", len(store.rows))
	}
	row := store.rows[0]
	if row.Kind != string(llmusage.KindEmbedding) {
		t.Fatalf("kind = %q, want %q", row.Kind, llmusage.KindEmbedding)
	}
	if row.PromptTokens != 9 || row.TotalTokens != 9 {
		t.Fatalf("tokens = %d/%d, want 9/9", row.PromptTokens, row.TotalTokens)
	}
	if row.ChatID != "oc_chat" || row.OpenID != "ou_user" || row.Source != "message_recording" {
		t.Fatalf("scope row = %+v", row)
	}
}
