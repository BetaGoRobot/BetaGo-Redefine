package service_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/domain"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/service"
)

func TestGroupConversationSwitchSuppressesAmbientAndKeepsExplicitRecovery(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	e := domain.CatalogEntry{Kind: domain.CatalogFeature, Name: "conversation", Revision: 1, CatalogValue: domain.CatalogValue{Feature: &domain.CatalogFeatureValue{Enabled: false}}}
	body, _ := json.Marshal(e)
	if _, err := f.db.Exec(`INSERT INTO "`+f.schema+`".catalog_entries(scope_key,kind,name,revision,creator,body,updated_at)VALUES($1,$2,$3,1,'admin',$4,clock_timestamp())`, f.scope.Key(), e.Kind, e.Name, body); err != nil {
		t.Fatal(err)
	}
	svc, err := service.New(service.Options{Store: f.store, Engine: f.eng, Messenger: f.messenger, Owner: "settings", Lease: time.Minute, TaskTimeout: time.Hour, EligibleForConversation: func(context.Context, domain.Message) (bool, error) { return true, nil }})
	if err != nil {
		t.Fatal(err)
	}
	f.event(t, "ambient", "owner", "随便说说", "", domain.IngressMetadata{Kind: "message", SenderType: "user"})
	if err = svc.Step(ctx, domain.JobRoute); err != nil {
		t.Fatal(err)
	}
	if _, _, err = f.store.TaskForMessage(ctx, f.scope, "ambient"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatal("disabled ambient task was created")
	}
	f.event(t, "explicit", "owner", "查看群配置", "", domain.IngressMetadata{Kind: "message", SenderType: "user", Addressed: true})
	if err = svc.Step(ctx, domain.JobRoute); err != nil {
		t.Fatal(err)
	}
	if _, _, err = f.store.TaskForMessage(ctx, f.scope, "explicit"); err != nil {
		t.Fatal("explicit recovery request was suppressed")
	}
}
