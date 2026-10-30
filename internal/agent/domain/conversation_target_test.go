package domain_test

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/domain"
)

func TestConversationPlanRejectsMixedReplyTargetsBeforeRendering(t *testing.T) {
	for _, choice := range []string{"react", "reply", "expand"} {
		for _, targets := range [][2]string{{"first", "second"}, {"", "second"}, {"first", ""}} {
			t.Run(choice+"/"+targets[0]+"/"+targets[1], func(t *testing.T) {
				plan := domain.ConversationPlan{Choice: choice, Messages: []domain.ConversationMessage{
					{Text: "先说结论。", ReplyToMessageID: targets[0]},
					{Text: "再补充细节。", ReplyToMessageID: targets[1]},
				}}
				if err := plan.Validate(); !errors.Is(err, domain.ErrInvalid) {
					t.Errorf("mixed physical reply targets validated: %v", err)
				}
				raw, err := json.Marshal(plan)
				if err != nil {
					t.Fatal(err)
				}
				if _, err = domain.ParseConversationPlan(string(raw)); !errors.Is(err, domain.ErrInvalid) {
					t.Errorf("mixed physical reply targets parsed: %v", err)
				}
				if frames, err := domain.ConversationFrames(plan); !errors.Is(err, domain.ErrInvalid) || len(frames) != 0 {
					t.Errorf("invalid plan produced partial frames: count=%d err=%v", len(frames), err)
				}
				if normalized, err := domain.NormalizeConversationPlan(plan); !errors.Is(err, domain.ErrInvalid) || len(normalized.Messages) != 0 {
					t.Errorf("invalid plan produced partial normalized content: count=%d err=%v", len(normalized.Messages), err)
				}
			})
		}
	}
}
