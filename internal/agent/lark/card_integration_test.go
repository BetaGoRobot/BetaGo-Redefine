package lark

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/domain"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/pgstore"
	"github.com/larksuite/oapi-sdk-go/v3/event/dispatcher/callback"
	_ "github.com/lib/pq"
	"os"
	"testing"
	"time"
)

func TestCardProviderMicrosecondsPersist(t *testing.T) {
	var card callback.CardActionTriggerEvent
	// The card.action.trigger provider header is in microseconds, unlike IM receive timestamps.
	const stamp = int64(1792650000123456)
	if err := json.Unmarshal([]byte(`{"schema":"2.0","header":{"event_id":"quality-card-micro","event_type":"card.action.trigger","app_id":"app","tenant_key":"tenant","create_time":"1792650000123456"},"event":{"operator":{"open_id":"alice","tenant_key":"tenant"},"context":{"open_chat_id":"group","open_message_id":"parent"},"action":{"value":{"action":"approve","task_id":"task","revision":"1","approval_id":"approval"}}}}`), &card); err != nil {
		t.Fatal(err)
	}
	event, err := normalizer(t).Card(&card)
	if err != nil {
		t.Fatal(err)
	}
	t.Run("timestamp", func(t *testing.T) {
		want := time.UnixMicro(stamp).UTC()
		if !event.OccurredAt.Equal(want) {
			t.Fatalf("provider card timestamp parsed as year %d; want %s", event.OccurredAt.Year(), want)
		}
	})
	t.Run("durable_accept", func(t *testing.T) {
		dsn := os.Getenv("AGENT_TEST_POSTGRES_DSN")
		if dsn == "" {
			t.Skip("isolated PostgreSQL fixture required")
		}
		db, err := sql.Open("postgres", dsn)
		if err != nil {
			t.Fatal("open fixture")
		}
		defer db.Close()
		schema := fmt.Sprintf("quality_card_%d", time.Now().UnixNano())
		store, err := pgstore.New(db, schema)
		if err != nil {
			t.Fatal(err)
		}
		defer db.Exec(`DROP SCHEMA IF EXISTS "` + schema + `" CASCADE`)
		if err := store.Migrate(context.Background()); err != nil {
			t.Fatal(err)
		}
		ing, err := NewIngress(IngressConfig{Normalize: NormalizeConfig{AppID: "app", BotOpenID: "bot"}, AppSecret: "synthetic", Persist: func(ctx context.Context, e domain.Event) error { _, err := store.Accept(ctx, e); return err }})
		if err != nil {
			t.Fatal(err)
		}
		body, err := json.Marshal(card)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = ing.Dispatcher().Do(context.Background(), body); err != nil {
			t.Fatalf("provider card cannot reach durable ACK: %v", err)
		}
		stored, err := store.Message(context.Background(), event.Message.Source.Scope, event.Message.Source.MessageID)
		if err != nil || !stored.Source.OccurredAt.Equal(time.UnixMicro(stamp).UTC()) || stored.Source.AuthorID != "alice" {
			t.Fatal("ACK did not preserve card source and timestamp")
		}

	})
}
