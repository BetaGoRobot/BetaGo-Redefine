package domain_test

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/domain"
)

func readablePlan(text string) domain.ConversationPlan {
	return domain.ConversationPlan{Choice: "reply", Messages: []domain.ConversationMessage{{Text: text, ReplyToMessageID: "member"}}}
}

func readableTexts(p domain.ConversationPlan) []string {
	out := make([]string, len(p.Messages))
	for i, m := range p.Messages {
		out[i] = m.Text
	}
	return out
}

func TestReadableConversationKeepsShortAnswersObservationAndMessageTargets(t *testing.T) {
	for _, p := range []domain.ConversationPlan{
		readablePlan(" 好的。🙂\n"),
		{Choice: "observe", Messages: []domain.ConversationMessage{}},
		{Choice: "expand", Messages: []domain.ConversationMessage{{Text: "第一段。\n\n第二段。", ReplyToMessageID: "first"}, {Text: "第三段。\n\n第四段。", ReplyToMessageID: "first"}}},
	} {
		before, _ := json.Marshal(p)
		got, err := domain.NormalizeConversationPlan(p)
		if err != nil || got.Choice != p.Choice {
			t.Fatalf("normalization failed: %+v %v", got, err)
		}
		want := p
		if len(p.Messages) == 2 {
			want.Messages = []domain.ConversationMessage{{Text: "第一段。", ReplyToMessageID: "first"}, {Text: "第二段。", ReplyToMessageID: "first"}, {Text: "第三段。", ReplyToMessageID: "first"}, {Text: "第四段。", ReplyToMessageID: "first"}}
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("text or reply target changed: got %+v want %+v", got, want)
		}
		after, _ := json.Marshal(p)
		if string(before) != string(after) {
			t.Fatal("normalization mutated its input")
		}
		again, err := domain.NormalizeConversationPlan(got)
		if err != nil || !reflect.DeepEqual(again, got) {
			t.Fatalf("normalization is not deterministic/idempotent: %+v %v", again, err)
		}
	}
}

func TestReadableConversationSplitsChineseAtSentenceAndClauseBoundaries(t *testing.T) {
	for _, tc := range []struct {
		name, text, first string
	}{
		{"sentence", strings.Repeat("甲", 99) + "。" + strings.Repeat("乙", 79) + "，" + strings.Repeat("丙", 39) + "。", strings.Repeat("甲", 99) + "。"},
		{"clause", strings.Repeat("甲", 119) + "，" + strings.Repeat("乙", 119) + "。", strings.Repeat("甲", 119) + "，"},
		{"space", strings.Repeat("a", 119) + " " + strings.Repeat("b", 119), strings.Repeat("a", 119) + " "},
		{"runes", strings.Repeat("你🙂", 130), strings.Repeat("你🙂", 90)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := domain.NormalizeConversationPlan(readablePlan(tc.text))
			if err != nil || len(got.Messages) < 2 || got.Messages[0].Text != tc.first {
				t.Fatalf("readable boundary missed: %+v %v", readableTexts(got), err)
			}
			if strings.Join(readableTexts(got), "") != tc.text {
				t.Fatal("long paragraph lost or changed content")
			}
			for _, m := range got.Messages {
				if !utf8.ValidString(m.Text) || utf8.RuneCountInString(m.Text) > 180 || m.ReplyToMessageID != "member" {
					t.Fatalf("invalid readable chunk: %+v", m)
				}
			}
		})
	}
}

func TestReadableConversationSplitsSemanticParagraphsIncludingCRLF(t *testing.T) {
	for _, separator := range []string{"\n\n", "\n \t\n", "\r\n\r\n"} {
		got, err := domain.NormalizeConversationPlan(readablePlan("第一段。" + separator + "第二段。" + separator + "第三段。"))
		if err != nil || !reflect.DeepEqual(readableTexts(got), []string{"第一段。", "第二段。", "第三段。"}) {
			t.Fatalf("paragraphs not preserved: %+v %v", readableTexts(got), err)
		}
	}
}

func TestReadableConversationPreservesIndivisibleCodeURLsAndMarkdownLinks(t *testing.T) {
	for _, protected := range []string{
		"```go\n" + strings.Repeat("fmt.Println(\"hello\")\n\n", 20) + "```",
		"~~~~python\n" + strings.Repeat("print('hello')\n\n", 20) + "~~~~",
		"```text\n" + strings.Repeat("unclosed fence\n\n", 20),
		"https://example.test/" + strings.Repeat("long-path/", 40) + "?value=1",
		"<https://example.test/" + strings.Repeat("long-path/", 40) + ">",
		"[完整说明](https://example.test/" + strings.Repeat("long-path/", 40) + "file_(detail))",
		"![图示](https://example.test/" + strings.Repeat("long-path/", 40) + "image.png)",
		"`" + strings.Repeat("inline_code_", 30) + "`",
	} {
		text := "先看这里。\n\n" + protected
		if !strings.HasPrefix(protected, "```text") {
			text += "\n\n然后再讨论。"
		}
		got, err := domain.NormalizeConversationPlan(readablePlan(text))
		if err != nil {
			t.Fatal(err)
		}
		found := false
		for _, m := range got.Messages {
			found = found || m.Text == protected
		}
		if !found {
			t.Fatalf("protected text was split or rewritten: %+v", readableTexts(got))
		}
	}
	link := "[资料](https://example.test/" + strings.Repeat("p", 220) + ")"
	text := strings.Repeat("前", 130) + "，" + link + strings.Repeat("后", 100) + "。"
	got, err := domain.NormalizeConversationPlan(readablePlan(text))
	if err != nil || strings.Join(readableTexts(got), "") != text {
		t.Fatalf("inline link lost content: %+v %v", readableTexts(got), err)
	}
	for _, m := range got.Messages {
		if strings.Contains(m.Text, "https://") && !strings.Contains(m.Text, link) {
			t.Fatalf("link split across bubbles: %q", m.Text)
		}
	}
}

func TestReadableConversationBareURLStopsAtChinesePunctuation(t *testing.T) {
	url := "https://example.test/path"
	text := url + "。" + strings.Repeat("这里继续说明正常文字，不应当成为网址的一部分。", 20)
	got, err := domain.NormalizeConversationPlan(readablePlan(text))
	if err != nil || len(got.Messages) < 2 || strings.Join(readableTexts(got), "") != text {
		t.Fatalf("bare URL swallowed the following prose: %+v %v", readableTexts(got), err)
	}
	for _, m := range got.Messages {
		if utf8.RuneCountInString(m.Text) > 180 {
			t.Fatalf("ordinary prose was treated as an indivisible URL: %q", m.Text)
		}
		if strings.Contains(m.Text, "https://") && !strings.Contains(m.Text, url) {
			t.Fatalf("URL split across bubbles: %q", m.Text)
		}
	}
}

func TestReadableConversationKeepsMarkdownDestinationAndTitleParenthesesIntact(t *testing.T) {
	base := "https://example.test/" + strings.Repeat("p", 220)
	for name, link := range map[string]string{
		"double_quoted_title":             "[资料](" + base + ` "display ) more")`,
		"single_quoted_title":             "[资料](" + base + ` 'display ) more')`,
		"angle_destination":               "[资料](<" + base + "a)b>)",
		"angle_open_parenthesis":          "[资料](<" + base + "a(b>)",
		"angle_and_title":                 "[资料](<" + base + `a)b> "display ) more")`,
		"escaped_destination_parenthesis": "[资料](" + base + `a\)b)`,
		"escaped_title_quote":             "[资料](" + base + ` "display \" ) more")`,
		"apostrophe_in_destination":       "[资料](" + base + `o'reilly "display ) more")`,
	} {
		t.Run(name, func(t *testing.T) {
			got, err := domain.NormalizeConversationPlan(readablePlan("说明。\n\n" + link + "\n\n后文。"))
			want := []string{"说明。", link, "后文。"}
			if err != nil || !reflect.DeepEqual(readableTexts(got), want) {
				t.Fatalf("Markdown link or title split across bubbles: got %q want %q err=%v", readableTexts(got), want, err)
			}
		})
	}
}

func TestReadableConversationRejectsWholePlanWhenSplittingExceedsResourceBudget(t *testing.T) {
	encodedOverhead := strings.TrimSuffix(strings.Repeat("```\n"+strings.Repeat("x", 1772)+"\n```\n\n", 16), "\n\n")
	if readablePlan(encodedOverhead).Validate() != nil {
		t.Fatal("fixture must fit before per-message encoding overhead")
	}
	for _, p := range []domain.ConversationPlan{
		readablePlan(strings.Repeat("一段。\n\n", 16) + "第十七段。"),
		readablePlan(strings.Repeat("中", 180*16+1)),
		readablePlan(strings.Repeat("x", domain.MaxConversationBytes)),
		readablePlan(string([]byte{0xff, 0xfe})),
		readablePlan(encodedOverhead),
	} {
		got, err := domain.NormalizeConversationPlan(p)
		if err == nil || len(got.Messages) != 0 {
			t.Fatalf("unrepresentable answer was truncated or partially accepted: messages=%d err=%v", len(got.Messages), err)
		}
	}
	p := readablePlan(strings.TrimSuffix(strings.Repeat("一段。\n\n", 16), "\n\n"))
	got, err := domain.NormalizeConversationPlan(p)
	if err != nil || len(got.Messages) != 16 || got.Validate() != nil {
		t.Fatalf("sixteen readable bubbles should fit: %d %v", len(got.Messages), err)
	}
	// Protected units may exceed the reading target, subject to the same total
	// transport budget, which counts the actual encoded delivery content.
	p = readablePlan("```\n" + strings.Repeat("x", domain.MaxConversationBytes-21) + "\n```")
	got, err = domain.NormalizeConversationPlan(p)
	if err != nil || len(got.Messages) != 1 {
		t.Fatalf("within-budget indivisible code rejected: %d %v", len(got.Messages), err)
	}
}
