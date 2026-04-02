package handlers

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/lark_dal/larkmsg"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/xmodel"
)

func TestBuildWordChunkListCardIncludesInteractiveControls(t *testing.T) {
	scope := wordCountScope{
		ChatID: "oc_test_chat",
		Start:  time.Date(2026, 3, 1, 0, 0, 0, 0, time.FixedZone("CST", 8*3600)),
		End:    time.Date(2026, 3, 7, 23, 59, 0, 0, time.FixedZone("CST", 8*3600)),
		Days:   7,
	}
	query := wordChunkQuery{
		ChatID:       scope.ChatID,
		Start:        scope.Start,
		End:          scope.End,
		Limit:        5,
		Sort:         WordCloudSortTypeTime,
		Intent:       WordChunkIntentTypeCasualChitchat,
		Sentiment:    WordChunkSentimentTypeNegative,
		QuestionMode: WordChunkQuestionModeQuestion,
	}
	card := buildWordChunkListCard(context.Background(), scope, query, []*xmodel.MessageChunkLogV3{
		{
			ID:      "chunk-1",
			Summary: "昨晚在讨论周末出行安排",
			Intent:  string(WordChunkIntentTypeCasualChitchat),
			Entities: &xmodel.Entities{
				MainTopicsOrActivities: []string{"周末出行", "拼车"},
				KeyConceptsAndNouns:    []string{"时间", "地点", "费用"},
			},
			InteractionAnalysis: &xmodel.InteractionAnalysis{
				IsQuestionPresent:   true,
				UnresolvedQuestions: []string{"周六几点集合？"},
				Participants: []*xmodel.Participant{
					{User: &xmodel.User{OpenID: "ou_1"}},
					{User: &xmodel.User{OpenID: "ou_2"}},
				},
			},
			SentimentAndTone: &xmodel.SentimentAndTone{
				Sentiment: string(WordChunkSentimentTypeNegative),
				Tones:     []string{"ANXIOUS"},
			},
			MsgIDs:      []string{"om_1", "om_2"},
			Timestamp:   "2026-03-06 20:30:00",
			TimestampV2: strPtr("2026-03-06T20:30:00+08:00"),
		},
	})

	raw, err := json.Marshal(card)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	jsonStr := string(raw)
	if !strings.Contains(jsonStr, `"tag":"form"`) {
		t.Fatalf("expected chunk list card to include filter form: %s", jsonStr)
	}
	if !strings.Contains(jsonStr, `"name":"sort"`) ||
		!strings.Contains(jsonStr, `"name":"intent"`) ||
		!strings.Contains(jsonStr, `"name":"sentiment"`) ||
		!strings.Contains(jsonStr, `"name":"question_mode"`) {
		t.Fatalf("expected chunk list card to expose typed filters: %s", jsonStr)
	}
	if !strings.Contains(jsonStr, `"content":"详情"`) {
		t.Fatalf("expected chunk list card to include detail action: %s", jsonStr)
	}
	if !strings.Contains(jsonStr, `"action":"wordcount.chunks.view"`) {
		t.Fatalf("expected chunk list card to include chunk list callback action: %s", jsonStr)
	}
	if !strings.Contains(jsonStr, `"action":"wordcount.chunk.detail"`) {
		t.Fatalf("expected chunk list card to include chunk detail callback action: %s", jsonStr)
	}
}

func TestBuildWordChunkListCardIncludesPaginationActions(t *testing.T) {
	view := normalizeWordChunkCardView(wordChunkCardView{
		ChatID:   "oc_test_chat",
		Start:    time.Date(2026, 3, 1, 0, 0, 0, 0, time.FixedZone("CST", 8*3600)),
		End:      time.Date(2026, 3, 7, 23, 59, 0, 0, time.FixedZone("CST", 8*3600)),
		Page:     2,
		PageSize: 5,
	})
	card := buildWordChunkListCardWithState(context.Background(), view, wordChunkSearchResult{
		Items: []*xmodel.MessageChunkLogV3{
			{ID: "chunk-1", Summary: "分页测试", TimestampV2: strPtr("2026-03-06T20:30:00+08:00")},
		},
		Total: 12,
	})

	raw, err := json.Marshal(card)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	jsonStr := string(raw)
	if !strings.Contains(jsonStr, `"content":"上一页"`) || !strings.Contains(jsonStr, `"content":"下一页"`) {
		t.Fatalf("expected chunk list card to include pagination buttons: %s", jsonStr)
	}
	if !strings.Contains(jsonStr, `"page":"1"`) || !strings.Contains(jsonStr, `"page":"3"`) {
		t.Fatalf("expected pagination buttons to carry previous/next page payloads: %s", jsonStr)
	}
}

func TestBuildWordChunkDetailCardIncludesBackAction(t *testing.T) {
	view := normalizeWordChunkCardView(wordChunkCardView{
		ChatID:   "oc_test_chat",
		Start:    time.Date(2026, 3, 1, 0, 0, 0, 0, time.FixedZone("CST", 8*3600)),
		End:      time.Date(2026, 3, 7, 23, 59, 0, 0, time.FixedZone("CST", 8*3600)),
		Page:     2,
		PageSize: 5,
	})
	card := buildWordChunkDetailCard(context.Background(), view, &xmodel.MessageChunkLogV3{
		ID:        "chunk-1",
		Summary:   "详情测试",
		Intent:    string(WordChunkIntentTypeCasualChitchat),
		Timestamp: "2026-03-06 20:30:00",
	}, nil)

	raw, err := json.Marshal(card)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	jsonStr := string(raw)
	if !strings.Contains(jsonStr, `"content":"返回列表"`) {
		t.Fatalf("expected detail card to provide back action: %s", jsonStr)
	}
	if !strings.Contains(jsonStr, `"action":"wordcount.chunks.view"`) {
		t.Fatalf("expected back action to return to chunk list state: %s", jsonStr)
	}
}

func TestBuildChunkTemplateDataLimitsToneTagsToTwo(t *testing.T) {
	chunk := &xmodel.MessageChunkLogV3{
		Intent: string(WordChunkIntentTypeCasualChitchat),
		SentimentAndTone: &xmodel.SentimentAndTone{
			Sentiment: string(WordChunkSentimentTypePositive),
			Tones:     []string{"HUMOROUS", "SUPPORTIVE", "FORMAL"},
		},
	}

	data := buildChunkTemplateData(chunk)
	if data == nil {
		t.Fatal("buildChunkTemplateData() returned nil")
	}

	wantIncluded := []string{
		larkmsg.TagText(GetToneStyle("HUMOROUS")),
		larkmsg.TagText(GetToneStyle("SUPPORTIVE")),
	}
	for _, item := range wantIncluded {
		if !strings.Contains(data.Tones, item) {
			t.Fatalf("Tones = %q, want to include %q", data.Tones, item)
		}
	}

	if strings.Contains(data.Tones, larkmsg.TagText(GetToneStyle("FORMAL"))) {
		t.Fatalf("Tones = %q, want at most first two tone tags", data.Tones)
	}
}

func TestBuildChunkTemplateDataSummarizesUnresolvedQuestions(t *testing.T) {
	chunk := &xmodel.MessageChunkLogV3{
		Intent: string(WordChunkIntentTypeCasualChitchat),
		InteractionAnalysis: &xmodel.InteractionAnalysis{
			UnresolvedQuestions: []string{
				"周六几点集合？",
				"谁来开车？",
				"预算怎么算？",
			},
		},
	}

	data := buildChunkTemplateData(chunk)
	if data == nil {
		t.Fatal("buildChunkTemplateData() returned nil")
	}
	if data.UnresolvedQuestions != "周六几点集合？ 等 3 个待回答问题" {
		t.Fatalf("UnresolvedQuestions = %q, want compact summary", data.UnresolvedQuestions)
	}
}

func strPtr(value string) *string {
	return &value
}
