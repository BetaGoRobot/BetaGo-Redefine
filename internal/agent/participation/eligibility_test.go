package participation

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/domain"
)

func freshMessage() domain.Message {
	return domain.Message{
		Source: domain.SourceRef{Scope: domain.Scope{TenantID: "t", AppID: "a", ChatID: "c", Visibility: "group"}, MessageID: "m", AuthorID: "member", Version: 1, OccurredAt: time.Now().UTC().Add(-time.Second)},
		Text:   "你觉得呢。。", Raw: json.RawMessage(`{"kind":"message","sender_type":"user"}`),
	}
}

func TestFreshShortFollowupNeedsNoRecentReplyOrClassifier(t *testing.T) {
	// No model or history dependency exists: first invitations, interrupted
	// feedback and resuming after any pause all receive a full-scene decision.
	for _, text := range []string{"你觉得呢。。", "机器人你觉得呢", "能改改", "吗", "有人知道吗？", "今天累死了"} {
		m := freshMessage()
		m.Text = text
		ok, err := EligibleForConversation(context.Background(), m)
		if err != nil || !ok {
			t.Fatalf("fresh utterance discarded before the scene: %q %v %v", text, ok, err)
		}
	}
}

func TestEligibilityRetainsIngressGuards(t *testing.T) {
	for _, name := range []string{"future", "stale", "deleted", "invalid_scope", "missing_author", "missing_id", "invalid_version", "empty", "oversized", "bad_metadata", "bot", "refresh", "control", "mentions_other", "mentions_self"} {
		t.Run(name, func(t *testing.T) {
			m := freshMessage()
			switch name {
			case "future":
				m.Source.OccurredAt = time.Now().Add(time.Minute)
			case "stale":
				m.Source.OccurredAt = time.Now().Add(-4 * time.Minute)
			case "deleted":
				m.Deleted = true
			case "invalid_scope":
				m.Source.Scope.ChatID = ""
			case "missing_author":
				m.Source.AuthorID = ""
			case "missing_id":
				m.Source.MessageID = ""
			case "invalid_version":
				m.Source.Version = 0
			case "empty":
				m.Text = " \n "
			case "oversized":
				m.Text = strings.Repeat("x", 4097)
			case "bad_metadata":
				m.Raw = json.RawMessage(`{`)
			case "bot":
				m.Raw = json.RawMessage(`{"kind":"message","sender_type":"app"}`)
			case "refresh":
				m.Raw = json.RawMessage(`{"kind":"refresh","sender_type":"user"}`)
			case "control":
				m.Raw = json.RawMessage(`{"kind":"control","sender_type":"user"}`)
			case "mentions_other":
				m.Raw = json.RawMessage(`{"kind":"message","sender_type":"user","mentions":[{"OpenID":"other"}]}`)
			case "mentions_self":
				m.Raw = json.RawMessage(`{"kind":"message","sender_type":"user","addressed":true,"mentions":[{"OpenID":"self"}]}`)
			}
			ok, err := EligibleForConversation(context.Background(), m)
			if err != nil || ok != (name == "mentions_self") {
				t.Fatalf("guard %s: eligible=%v err=%v", name, ok, err)
			}
		})
	}
}

func TestEligibilityPreservesCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	ok, err := EligibleForConversation(ctx, freshMessage())
	if ok || !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled input admitted or error swallowed: %v %v", ok, err)
	}
}
