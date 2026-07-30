package agentcard

import (
	"context"
	"testing"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/agentcardtool"
)

type authoringComposerFake struct {
	calls   int
	request AuthoringComposeRequest
	surface *CardSurface
	err     error
}

func (f *authoringComposerFake) Compose(
	_ context.Context,
	request AuthoringComposeRequest,
) (*CardSurface, error) {
	f.calls++
	f.request = request
	return f.surface, f.err
}

func TestToolServiceDiscoversComponentsDeterministically(t *testing.T) {
	service := NewToolService(ToolServiceOptions{Catalog: NewCatalog()})
	request := agentcardtool.DiscoverRequest{
		Version: VersionV1, Category: string(CategoryInput),
	}
	first, err := service.DiscoverComponents(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	second, err := service.DiscoverComponents(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if agentcardtool.MarshalResponse(first) != agentcardtool.MarshalResponse(second) ||
		len(first.Components) != 3 ||
		first.Components[0].Name != "multi_select" ||
		first.Components[2].Name != "text_input" {
		t.Fatalf("discover response = %#v", first)
	}
}

func TestToolServiceAllowsTwoValidationAttemptsThenRequiresTextFallback(t *testing.T) {
	composer := &authoringComposerFake{}
	service := NewToolService(ToolServiceOptions{
		Catalog: NewCatalog(), Composer: composer, MaxRepairAttempts: 2,
	})
	request := agentcardtool.ComposeRequest{
		Purpose: "confirmation",
		Card: agentcardtool.Card{
			Title: "", Blocks: []agentcardtool.Block{{
				Kind: "text_input", ID: "secret",
				FieldID: "password", Label: "Password",
			}},
		},
		Interaction: agentcardtool.Interaction{
			Mode: "ui_action", ExpiresInSeconds: 600,
		},
	}
	toolContext := agentcardtool.ComposeContext{
		ChatID: "chat-1", ActorOpenID: "owner-1",
		ReplyToMessageID: "message-1", TriggerEventID: "event-1",
	}
	first, err := service.ComposeCard(context.Background(), toolContext, request)
	if err != nil {
		t.Fatal(err)
	}
	second, err := service.ComposeCard(context.Background(), toolContext, request)
	if err != nil {
		t.Fatal(err)
	}
	if first.Status != "repair_required" || first.Attempt != 1 ||
		len(first.Issues) == 0 || first.Fallback != "" ||
		second.Status != "fallback_required" || second.Attempt != 2 ||
		second.Fallback == "" || composer.calls != 0 {
		t.Fatalf("first=%#v second=%#v calls=%d", first, second, composer.calls)
	}
}

func TestToolServiceComposesValidatedTypedCard(t *testing.T) {
	composer := &authoringComposerFake{surface: &CardSurface{
		ID: "surface-1", MessageID: "om-card",
		InteractionID: "interaction-1", Revision: 2, Status: SurfaceStatusSent,
	}}
	now := time.Date(2026, 7, 30, 8, 0, 0, 0, time.UTC)
	service := NewToolService(ToolServiceOptions{
		Catalog: NewCatalog(), Composer: composer,
		DefaultExpiry: 10 * time.Minute, Now: func() time.Time { return now },
	})
	response, err := service.ComposeCard(
		context.Background(),
		agentcardtool.ComposeContext{
			ChatID: "chat-1", ActorOpenID: "owner-1",
			ReplyToMessageID: "message-1", TriggerEventID: "event-1",
		},
		agentcardtool.ComposeRequest{
			Purpose: "collect_reason",
			Card: agentcardtool.Card{
				Title: "补充原因",
				Blocks: []agentcardtool.Block{{
					Kind: "text_input", ID: "reason_input",
					FieldID: "reason", FormID: "reason_form",
					Label: "原因", Required: true, MaxLength: 200,
				}},
				Actions: []agentcardtool.Action{{
					Kind: "submit", ID: "submit", Label: "提交",
					Mode: "ui_action", Intent: "submit_reason",
					FormRef: "reason_form",
				}},
			},
			Interaction: agentcardtool.Interaction{Mode: "ui_action"},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if response.Status != "sent" || response.CardRef != "surface-1" ||
		response.MessageID != "om-card" || composer.calls != 1 ||
		composer.request.Spec.Version != VersionV1 ||
		composer.request.Purpose != "collect_reason" ||
		composer.request.Context.ChatID != "chat-1" ||
		!composer.request.ExpiresAt.Equal(now.Add(10*time.Minute)) {
		t.Fatalf("response=%#v compose=%#v", response, composer.request)
	}
}
