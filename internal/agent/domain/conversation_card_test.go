package domain_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/domain"
)

func TestConversationCardsUseUnicodeThresholdAndPreserveSummaryAndProtectedBody(t *testing.T) {
	for _, n := range []int{280, 281} {
		p := readablePlan(strings.Repeat("你", n))
		frames, err := domain.ConversationFrames(p)
		if err != nil || len(frames) != 1 {
			t.Fatalf("unicode threshold: %d %+v %v", n, frames, err)
		}
		want := "text"
		if n == 281 {
			want = "interactive"
		}
		wantText := p.Messages[0].Text
		if n == 281 {
			wantText = strings.Repeat("你", 120) + "…\n\n" + wantText
		}
		if frames[0].MessageType != want || frames[0].Text != wantText {
			t.Fatalf("unicode threshold truncated answer or used bytes: %d", n)
		}
	}
	protected := "```go\n" + strings.Repeat("fmt.Println(1)\n", 30) + "```\n\n[资料](https://example.test/" + strings.Repeat("path", 100) + ")"
	p, err := domain.ParseConversationPlan(`{"choice":"expand","summary":"先验证上下文。","messages":[{"text":` + mustConversationJSON(protected) + `,"reply_to_message_id":"member"}]}`)
	if err != nil {
		t.Fatal(err)
	}
	p, err = domain.NormalizeConversationPlan(p)
	if err != nil || p.Summary != "先验证上下文。" {
		t.Fatalf("summary lost in normalization: %v", err)
	}
	frames, err := domain.ConversationFrames(p)
	if err != nil || len(frames) != 2 {
		t.Fatalf("protected content frame mismatch: %v", err)
	}
	if frames[1].Text != p.Summary+"\n\n"+protected {
		t.Fatal("code or link truncated/reordered")
	}
	var card struct {
		Config struct {
			Update bool `json:"update_multi"`
		}
		Body struct {
			Elements []struct {
				Content  string
				Elements []struct{ Content string }
			}
		}
	}
	if json.Unmarshal([]byte(frames[1].Content), &card) != nil || !card.Config.Update || card.Body.Elements[0].Content != p.Summary || card.Body.Elements[1].Elements[0].Content != p.Messages[0].Text || card.Body.Elements[1].Elements[1].Content != p.Messages[1].Text {
		t.Fatal("card did not preserve full detail and concise visible summary")
	}
}
func mustConversationJSON(s string) string { b, _ := json.Marshal(s); return string(b) }

func TestConversationSummaryPreservedInShortAndCardVisibleText(t *testing.T) {
	cases := []struct {
		name, choice, summary string
		sections              []string
		typ                   string
		want                  []string
	}{
		{"short_unique", "reply", "独立的限制条件。", []string{"好的。"}, "text", []string{"独立的限制条件。\n\n好的。"}},
		{"short_substring_preserved", "reply", "好的", []string{"好的。"}, "text", []string{"好的\n\n好的。"}},
		{"short_exact_repeat", "reply", "好的。", []string{"好的。"}, "text", []string{"好的。"}},
		{"short_section_repeat", "reply", "第一段。", []string{"第一段。", "第二段。"}, "text", []string{"第一段。\n\n第二段。"}},
		{"combined_visible_threshold", "reply", strings.Repeat("甲", 140), []string{strings.Repeat("乙", 140)}, "interactive", []string{strings.Repeat("甲", 140) + "\n\n" + strings.Repeat("乙", 140)}},
		{"card_unique", "expand", "尚待复核。", []string{"第一段。", "第二段。"}, "interactive", []string{"尚待复核。\n\n第一段。", "尚待复核。\n\n第一段。\n\n第二段。"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := domain.ConversationPlan{Choice: tc.choice, Summary: tc.summary}
			for _, text := range tc.sections {
				p.Messages = append(p.Messages, domain.ConversationMessage{Text: text, ReplyToMessageID: "member"})
			}
			frames, err := domain.ConversationFrames(p)
			if err != nil || len(frames) != len(tc.want) {
				t.Fatalf("summary output shape: frames=%d want=%d err=%v", len(frames), len(tc.want), err)
			}
			for i, frame := range frames {
				if frame.MessageType != tc.typ || frame.Text != tc.want[i] {
					t.Fatalf("summary dropped/duplicated in frame %d: type=%s text=%q want=%q", i, frame.MessageType, frame.Text, tc.want[i])
				}
				if tc.typ == "text" {
					var body struct{ Text string }
					if json.Unmarshal([]byte(frame.Content), &body) != nil || body.Text != tc.want[i] {
						t.Fatal("short summary missing from actual text content")
					}
				}
			}
		})
	}
}
