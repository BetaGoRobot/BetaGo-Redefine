package domain_test

import (
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/domain"
	"testing"
)

func TestConversationMemeAttributionIsOptionalAndDoesNotInvalidateReply(t *testing.T) {
	for _, tc := range []struct {
		field     string
		reported  bool
		malformed bool
	}{{"", false, false}, {`,"meme_use_ids":[]`, true, false}, {`,"meme_use_ids":["known","unknown"]`, true, false}, {`,"meme_use_ids":"invalid"`, false, true}, {`,"meme_use_ids":null`, false, true}} {
		p, err := domain.ParseConversationPlan(`{"choice":"reply","messages":[{"text":"normal answer","reply_to_message_id":"human"}]` + tc.field + `}`)
		if err != nil || (p.MemeUseIDs != nil) != tc.reported || p.MemeUseIDsMalformed != tc.malformed || p.Messages[0].Text != "normal answer" {
			t.Fatalf("attribution changed ordinary reply: %v %+v", err, p)
		}
	}
}
