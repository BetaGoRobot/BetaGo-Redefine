package service_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/domain"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/service"
)

func TestNaturalParticipationOnlyCreatesActualSpeakersOwnRequest(t *testing.T) {
	for _, join := range []bool{false, true} {
		name := "stay_quiet"
		if join {
			name = "join_conversation"
		}
		t.Run(name, func(t *testing.T) {
			f := setup(t)
			calls := 0
			svc, err := service.New(service.Options{Store: f.store, Engine: f.eng, Messenger: f.messenger, Owner: "admission", Lease: time.Minute, TaskTimeout: time.Hour, EligibleForConversation: func(_ context.Context, m domain.Message) (bool, error) {
				calls++
				if m.Source.AuthorID != "another_member" {
					t.Error("speaker identity changed")
				}
				return join, nil
			}})
			if err != nil {
				t.Fatal(err)
			}
			f.event(t, "follow_up", "another_member", "你刚才说的是什么意思", "", domain.IngressMetadata{Kind: "message", SenderType: "user"})
			if err = svc.Step(context.Background(), domain.JobRoute); err != nil {
				t.Fatal(err)
			}
			task, _, err := f.store.TaskForMessage(context.Background(), f.scope, "follow_up")
			if calls != 1 {
				t.Fatal("participation decision not called once")
			}
			if !join {
				if !errors.Is(err, domain.ErrNotFound) {
					t.Fatal("quiet chatter created a task")
				}
				return
			}
			if err != nil || task.OwnerID != "another_member" || task.RequesterID != "another_member" || len(task.Delegates) != 0 {
				t.Fatal("joining granted another identity or task authority")
			}
			approvals, err := f.store.Approvals(context.Background(), f.scope, task.ID)
			if err != nil || len(approvals) != 0 {
				t.Fatal("participation created action approval")
			}
		})
	}
}

func TestExplicitAddressBypassesParticipationDecision(t *testing.T) {
	f := setup(t)
	svc, err := service.New(service.Options{Store: f.store, Engine: f.eng, Messenger: f.messenger, Owner: "direct", Lease: time.Minute, TaskTimeout: time.Hour, EligibleForConversation: func(context.Context, domain.Message) (bool, error) {
		t.Error("direct address should not depend on classifier")
		return false, errors.New("unavailable")
	}})
	if err != nil {
		t.Fatal(err)
	}
	f.event(t, "direct", "owner", "hello", "", domain.IngressMetadata{Kind: "message", SenderType: "user", Addressed: true})
	if err = svc.Step(context.Background(), domain.JobRoute); err != nil {
		t.Fatal(err)
	}
	if _, _, err = f.store.TaskForMessage(context.Background(), f.scope, "direct"); err != nil {
		t.Fatal("direct request lost")
	}
}
