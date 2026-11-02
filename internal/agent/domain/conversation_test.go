package domain_test

import (
	"strings"
	"testing"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/domain"
)

func TestConversationEnvelopeRequiresAnExplicitBoundedChoice(t *testing.T) {
	for _, tc := range []struct {
		raw   string
		valid bool
	}{
		{`{"choice":"observe","messages":[]}`, true},
		{`{"choice":"reply","messages":[{"text":"嗯，确实。","reply_to_message_id":"m"},{"text":"但这里还有个例外。","reply_to_message_id":"m"}]}`, true},
		{`{"choice":"react","messages":[{"text":"😂","reply_to_message_id":"m"}]}`, true},
		{`{"choice":"expand","messages":[{"text":"完整解释","reply_to_message_id":"m"}]}`, true},
		{`{"choice":"observe","messages":[{"text":"漏出内部选择","reply_to_message_id":"m"}]}`, false},
		{`{"choice":"reply","messages":[]}`, false},
		{`{"choice":"observe"}`, false},
		{`{"choice":"observe","messages":null}`, false},
		{`{"choice":"reply","messages":[{"text":"","reply_to_message_id":"m"}]}`, false},
		{`{"choice":"reply","messages":[{"text":"hi","reply_to_message_id":""}]}`, false},
		{`{"choice":"reply","choice":"observe","messages":[]}`, false},
		{`{"choice":"observe","messages":[],"why":"private"}`, false},
		{`{"choice":"reply","messages":[{"text":"hi","reply_to_message_id":"m","chat_id":"other"}]}`, false},
		{`hello`, false},
		{`{"choice":"observe","messages":[]} trailing`, false},
		{`{"choice":"reply","messages":[` + strings.Repeat(`{"text":"hi","reply_to_message_id":"m"},`, 15) + `{"text":"sixteenth","reply_to_message_id":"m"}]}`, true},
		{`{"choice":"reply","messages":[` + strings.Repeat(`{"text":"hi","reply_to_message_id":"m"},`, 16) + `{"text":"seventeenth","reply_to_message_id":"m"}]}`, false},
		{`{"choice":"reply","messages":[{"text":"` + strings.Repeat("x", 28<<10) + `","reply_to_message_id":"m"}]}`, false},
	} {
		_, err := domain.ParseConversationPlan(tc.raw)
		if (err == nil) != tc.valid {
			t.Fatalf("valid=%v err=%v raw=%.160s", tc.valid, err, tc.raw)
		}
	}
}
