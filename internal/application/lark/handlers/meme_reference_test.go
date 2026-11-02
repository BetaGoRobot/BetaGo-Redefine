package handlers

import (
	"context"
	"encoding/json"
	"iter"
	"strings"
	"testing"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/history"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/memereference"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/memes"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/ark_dal"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/llmusage"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/xhandler"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
)

func referenceEvent() *larkim.P2MessageReceiveV1 {
	chatID, messageID, kind, raw, created, speaker := "chat", "current", "text", `{"text":"原话\n只是提议，还没做。"}`, "1793232000000", "human"
	return &larkim.P2MessageReceiveV1{Event: &larkim.P2MessageReceiveV1Data{Message: &larkim.EventMessage{ChatId: &chatID, MessageId: &messageID, MessageType: &kind, Content: &raw, CreateTime: &created}, Sender: &larkim.EventSender{SenderId: &larkim.UserId{OpenId: &speaker}}}}
}

type referenceExecutor struct {
	do func(context.Context, string, string) iter.Seq[*ark_dal.ModelStreamRespReasoning]
}

func (f referenceExecutor) Do(ctx context.Context, _ llmusage.Scope, system, user string, _ ...string) (iter.Seq[*ark_dal.ModelStreamRespReasoning], error) {
	return f.do(ctx, system, user), nil
}
func referenceStream(chunks ...string) iter.Seq[*ark_dal.ModelStreamRespReasoning] {
	return func(yield func(*ark_dal.ModelStreamRespReasoning) bool) {
		for _, chunk := range chunks {
			if !yield(&ark_dal.ModelStreamRespReasoning{Content: chunk}) {
				return
			}
		}
	}
}

func installReferenceProvider(t *testing.T, provider memereference.Provider) {
	t.Helper()
	ConfigureMemeReferences(provider)
	t.Cleanup(func() { ConfigureMemeReferences(nil) })
}

func TestMemeReferenceBothGeneratorsReceiveCardsOnceWithoutScores(t *testing.T) {
	for _, plain := range []bool{false, true} {
		t.Run(map[bool]string{false: "standard_json", true: "two_phase_text"}[plain], func(t *testing.T) {
			calls := 0
			installReferenceProvider(t, func(_ context.Context, chatID, messageID string, dialogue []memes.Turn) memes.ReferenceResult {
				calls++
				if chatID != "chat" || messageID != "current" || len(dialogue) != 1 || dialogue[0].Speaker != "human" || dialogue[0].Text != "原话\n只是提议，还没做。" {
					t.Fatalf("provider input changed: %s %s %+v", chatID, messageID, dialogue)
				}
				result := memes.ReferenceResult{Status: memes.ReferenceStatusReady, Cards: []memes.UseCard{{UseID: "wiki.one", Name: "唯一梗名称", Description: "Wiki 原始解释。", Template: "原始核心表达"}}}
				result.Retrieval.Fused = []memes.RankedHit{{UseID: "wiki.one", Score: 987654, Rank: 1}}
				return result
			})
			executor := referenceExecutor{do: func(_ context.Context, system, user string) iter.Seq[*ark_dal.ModelStreamRespReasoning] {
				if !strings.Contains(system, memereference.Instruction) || strings.Count(user, "唯一梗名称") != 1 || !strings.Contains(user, "Wiki 原始解释。") {
					t.Fatalf("reference missing/duplicated: %s %s", system, user)
				}
				for _, forbidden := range []string{`"score"`, `"rank"`, "987654", "prepared_reply_id", "meme_reply_prepare"} {
					if strings.Contains(system+user, forbidden) {
						t.Fatalf("internal protocol/diagnostic exposed: %s", forbidden)
					}
				}
				if plain {
					return referenceStream(`{"reply":"模型自由`, `改造的回复。","meme_use_ids":["wiki.one"]}`)
				}
				return referenceStream(`{"decision":"reply",`, `"reply":"模型自由改造的回复。","meme_use_ids":["wiki.one"]}`)
			}}
			plan := StandardChatPlan{chatID: "chat", openID: "human", userName: "name", SystemPrompt: "original system", UserPrompt: "original user"}
			var seq iter.Seq[*ark_dal.ModelStreamRespReasoning]
			var err error
			if plain {
				seq, err = executeTwoPhaseReferenceGenerator(context.Background(), referenceEvent(), plan, llmusage.Scope{}, executor)
			} else {
				seq, err = executeStandardChatPlanWithExecutorFactory(context.Background(), referenceEvent(), &xhandler.BaseMetaData{ChatName: "name"}, plan, func(string) standardChatExecutor { return executor })
			}
			if err != nil {
				t.Fatal(err)
			}
			chunks := 0
			for data := range seq {
				chunks++
				if strings.Contains(data.Content, "meme_use_ids") || strings.Contains(data.ContentStruct.Reply, "wiki.one") {
					t.Fatalf("selection protocol leaked: %+v", data)
				}
			}
			if calls != 1 || chunks != 1 {
				t.Fatalf("lookup calls=%d chunks=%d; internal envelope must resolve before visible output", calls, chunks)
			}
		})
	}
}

func TestMemeReferenceInternalSelectionLogsWithoutVisibleMetadata(t *testing.T) {
	for _, plain := range []bool{false, true} {
		t.Run(map[bool]string{false: "standard", true: "two_phase"}[plain], func(t *testing.T) {
			core, entries := observer.New(zap.InfoLevel)
			logger := zap.New(core)
			originalLogger := memeReferenceLog
			memeReferenceLog = func(_ context.Context, message string, fields ...zap.Field) { logger.Info(message, fields...) }
			t.Cleanup(func() { memeReferenceLog = originalLogger })
			installReferenceProvider(t, func(context.Context, string, string, []memes.Turn) memes.ReferenceResult {
				return memes.ReferenceResult{Status: memes.ReferenceStatusReady, Cards: []memes.UseCard{{UseID: "wiki.one", Name: "梗名", Description: "只供模型的候选完整内容"}}}
			})
			executor := referenceExecutor{do: func(_ context.Context, system, user string) iter.Seq[*ark_dal.ModelStreamRespReasoning] {
				if !strings.Contains(system, "meme_use_ids") {
					t.Fatal("no internal selection field requested")
				}
				raw := `{"decision":"reply","reply":"自由接住这一句。","meme_use_ids":["unseen","wiki.one"]}`
				return referenceStream(raw[:20], raw[20:49], raw[49:])
			}}
			plan := StandardChatPlan{chatID: "chat", openID: "human", userName: "name"}
			var stream iter.Seq[*ark_dal.ModelStreamRespReasoning]
			var err error
			if plain {
				stream, err = executeTwoPhaseReferenceGenerator(context.Background(), referenceEvent(), plan, llmusage.Scope{}, executor)
			} else {
				stream, err = executeStandardChatPlanWithExecutorFactory(context.Background(), referenceEvent(), &xhandler.BaseMetaData{ChatName: "name"}, plan, func(string) standardChatExecutor { return executor })
			}
			if err != nil {
				t.Fatal(err)
			}
			if plain {
				stream = wrapTwoPhaseStream(context.Background(), stream, "intent", nil, "chat")
			}
			for data := range stream {
				visible, _ := json.Marshal(data)
				for _, forbidden := range []string{"meme_use_ids", "wiki.one", "unseen", "候选完整内容"} {
					if strings.Contains(string(visible), forbidden) {
						t.Fatalf("metadata delivered to visible stream: %s", visible)
					}
				}
				if data.ContentStruct.Reply != "自由接住这一句。" {
					t.Fatalf("ordinary answer changed: %+v", data)
				}
			}
			selection := entries.FilterMessage("meme reference selection").All()
			if len(selection) != 1 {
				t.Fatalf("selection logs=%+v", selection)
			}
			fields := selection[0].ContextMap()
			if fields["selection_source"] != "model_reported" || fields["selection_status"] != "reported_with_rejections" {
				t.Fatalf("false selection claim: %+v", fields)
			}
			selected, _ := json.Marshal(fields["selected_ids"])
			rejected, _ := json.Marshal(fields["rejected_ids"])
			if string(selected) != `["wiki.one"]` || string(rejected) != `["unseen"]` {
				t.Fatalf("selection not filtered: selected=%s rejected=%s", selected, rejected)
			}
			all, _ := json.Marshal(entries.All())
			if strings.Contains(string(all), "原话") || strings.Contains(string(all), "候选完整内容") {
				t.Fatal("private dialogue/card body logged")
			}
		})
	}
}

func TestMemeReferenceMalformedAttributionPreservesOrdinaryReplySafely(t *testing.T) {
	for _, raw := range []string{
		`{"reply":"普通实际回复。","meme_use_ids":"bad type"}`,
		`{"reply":"普通实际回复。","meme_use_ids":["wiki.one"]`,
		"普通实际回复。",
	} {
		decoded := decodeMemeReferenceReply(raw)
		if decoded.Reply != "普通实际回复。" || decoded.SelectionStatus != "parse_failed" || len(decoded.UseIDs) != 0 {
			t.Fatalf("malformed selection lost reply or claimed use: %+v", decoded)
		}
	}
	for _, raw := range []string{`[{"use_id":"wiki.one","name":"候选列表"}]`, `{"cards":[{"use_id":"wiki.one"}]}`, "```json\n{\"meme_use_ids\":[\"wiki.one\"]}\n```"} {
		if decoded := decodeMemeReferenceReply(raw); decoded.Reply != "" {
			t.Fatalf("raw candidate payload escaped: %+v", decoded)
		}
	}
}

func TestMemeReferenceCandidateListsAndSelectionNarrationStayPrivate(t *testing.T) {
	cards := []memes.UseCard{{UseID: "wiki.one", Name: "原地起飞"}, {UseID: "wiki.two", Name: "直接开摆"}}
	for _, text := range []string{
		"候选列表：原地起飞、直接开摆",
		"1. 原地起飞\n2. 直接开摆",
		"我选择了原地起飞这个梗。",
		`{"cards":[{"use_id":"wiki.one"}]}`,
	} {
		if safeReferenceVisibleReply(text, cards) {
			t.Errorf("candidate/selection disclosure allowed: %s", text)
		}
	}
	if !safeReferenceVisibleReply("这下原地起飞了。", cards) {
		t.Fatal("normal recognizable meme reply blocked")
	}
	if got := decodeMemeReferenceReply(`{"reply":null,"meme_use_ids":[]}`); got.SelectionStatus != "parse_failed" {
		t.Fatalf("null reply accepted as valid attribution: %+v", got)
	}
}

func TestMemeReferenceBadEnvelopeRecoversAnswerWithoutClaimingSelection(t *testing.T) {
	for _, plain := range []bool{false, true} {
		t.Run(map[bool]string{false: "standard", true: "two_phase"}[plain], func(t *testing.T) {
			core, entries := observer.New(zap.InfoLevel)
			logger := zap.New(core)
			original := memeReferenceLog
			memeReferenceLog = func(_ context.Context, message string, fields ...zap.Field) { logger.Info(message, fields...) }
			t.Cleanup(func() { memeReferenceLog = original })
			installReferenceProvider(t, func(context.Context, string, string, []memes.Turn) memes.ReferenceResult {
				return memes.ReferenceResult{Status: memes.ReferenceStatusReady, Cards: []memes.UseCard{{UseID: "wiki.one", Name: "梗名", Description: "候选正文"}}}
			})
			calls := 0
			executor := referenceExecutor{do: func(_ context.Context, system, user string) iter.Seq[*ark_dal.ModelStreamRespReasoning] {
				calls++
				if calls == 1 {
					return referenceStream(`{"reply":"候选列表：梗名","meme_use_ids":["wiki.one"]}`)
				}
				if strings.Contains(system, "meme_use_ids") || strings.Contains(user, "候选正文") {
					t.Fatal("ordinary recovery still contains candidates")
				}
				if plain {
					return referenceStream("恢复普通回答。")
				}
				return referenceStream(`{"decision":"reply","reply":"恢复普通回答。"}`)
			}}
			plan := StandardChatPlan{chatID: "chat", openID: "human", userName: "name", SystemPrompt: "ordinary", UserPrompt: "ordinary"}
			var stream iter.Seq[*ark_dal.ModelStreamRespReasoning]
			var err error
			if plain {
				stream, err = executeTwoPhaseReferenceGenerator(context.Background(), referenceEvent(), plan, llmusage.Scope{}, executor)
			} else {
				stream, err = executeStandardChatPlanWithExecutorFactory(context.Background(), referenceEvent(), &xhandler.BaseMetaData{ChatName: "name"}, plan, func(string) standardChatExecutor { return executor })
			}
			if err != nil {
				t.Fatal(err)
			}
			for data := range stream {
				if data.ContentStruct.Reply != "恢复普通回答。" {
					t.Fatalf("unsafe reply leaked: %+v", data)
				}
			}
			if calls != 2 {
				t.Fatalf("bounded recovery calls=%d", calls)
			}
			selection := entries.FilterMessage("meme reference selection").All()
			if len(selection) != 1 || selection[0].ContextMap()["selection_status"] != "parse_failed" {
				t.Fatalf("recovered answer falsely attributed: %+v", selection)
			}
			selected, _ := json.Marshal(selection[0].ContextMap()["selected_ids"])
			if string(selected) != "[]" {
				t.Fatalf("recovery retained selected IDs: %s", selected)
			}
		})
	}
}

func TestMemeReferenceOrdinaryAmbientRecoveryPreservesSkip(t *testing.T) {
	for _, plain := range []bool{false, true} {
		t.Run(map[bool]string{false: "standard", true: "two_phase"}[plain], func(t *testing.T) {
			core, entries := observer.New(zap.InfoLevel)
			logger := zap.New(core)
			original := memeReferenceLog
			memeReferenceLog = func(_ context.Context, message string, fields ...zap.Field) { logger.Info(message, fields...) }
			t.Cleanup(func() { memeReferenceLog = original })
			installReferenceProvider(t, func(context.Context, string, string, []memes.Turn) memes.ReferenceResult {
				return memes.ReferenceResult{Status: memes.ReferenceStatusReady, Cards: []memes.UseCard{{UseID: "wiki.one", Name: "梗名"}}}
			})
			calls := 0
			executor := referenceExecutor{do: func(context.Context, string, string) iter.Seq[*ark_dal.ModelStreamRespReasoning] {
				calls++
				if calls == 1 {
					return referenceStream(`{"cards":[{"use_id":"wiki.one","name":"梗名"}]}`)
				}
				if plain {
					return referenceStream()
				}
				return referenceStream(`{"decision":"skip","reply":""}`)
			}}
			event := referenceEvent()
			ambient := "group"
			event.Event.Message.ChatType = &ambient
			plan := StandardChatPlan{chatID: "chat", openID: "human", userName: "name"}
			var stream iter.Seq[*ark_dal.ModelStreamRespReasoning]
			var err error
			if plain {
				stream, err = executeTwoPhaseReferenceGenerator(context.Background(), event, plan, llmusage.Scope{}, executor)
			} else {
				stream, err = executeStandardChatPlanWithExecutorFactory(context.Background(), event, &xhandler.BaseMetaData{ChatName: "name"}, plan, func(string) standardChatExecutor { return executor })
			}
			if err != nil {
				t.Fatal(err)
			}
			if plain {
				stream = wrapTwoPhaseStream(context.Background(), stream, "ambient", nil, "chat")
			}
			for data := range stream {
				if data.ContentStruct.Decision != "skip" || data.ContentStruct.Reply != "" || data.Content != "" {
					t.Fatalf("ordinary silence replaced with reply: %+v", data)
				}
			}
			if calls != 2 {
				t.Fatalf("recovery attempts=%d", calls)
			}
			selection := entries.FilterMessage("meme reference selection").All()
			if len(selection) != 1 || selection[0].ContextMap()["selection_status"] != "parse_failed" || selection[0].ContextMap()["reply_decision"] != "skip" {
				t.Fatalf("ordinary skip concealed failed attribution: %+v", selection)
			}
		})
	}
}

func TestMemeReferenceOrdinaryDirectRecoveryPreservesActualAnswer(t *testing.T) {
	installReferenceProvider(t, func(context.Context, string, string, []memes.Turn) memes.ReferenceResult {
		return memes.ReferenceResult{Status: memes.ReferenceStatusReady, Cards: []memes.UseCard{{UseID: "wiki.one", Name: "梗名"}}}
	})
	event := referenceEvent()
	direct := "p2p"
	event.Event.Message.ChatType = &direct
	question := `{"text":"这个问题应该怎么处理？"}`
	event.Event.Message.Content = &question
	calls := 0
	executor := referenceExecutor{do: func(context.Context, string, string) iter.Seq[*ark_dal.ModelStreamRespReasoning] {
		calls++
		if calls == 1 {
			return referenceStream(`{"cards":[{"use_id":"wiki.one"}]}`)
		}
		return referenceStream(`{"decision":"reply","reply":"实际问题的普通答案。"}`)
	}}
	stream, err := executeStandardChatPlanWithExecutorFactory(context.Background(), event, &xhandler.BaseMetaData{ChatName: "name"}, StandardChatPlan{chatID: "chat", openID: "human", userName: "name"}, func(string) standardChatExecutor { return executor })
	if err != nil {
		t.Fatal(err)
	}
	for data := range stream {
		if data.ContentStruct.Decision != "reply" || data.ContentStruct.Reply != "实际问题的普通答案。" {
			t.Fatalf("actual ordinary answer lost: %+v", data)
		}
	}
}

func TestMemeReferenceRetrievalLogsBoundedChannelStatusWithoutQueryBodies(t *testing.T) {
	for _, rawQueries := range []bool{false, true} {
		t.Run(map[bool]string{false: "query_fallback", true: "raw_queries"}[rawQueries], func(t *testing.T) {
			core, entries := observer.New(zap.InfoLevel)
			logger := zap.New(core)
			original := memeReferenceLog
			memeReferenceLog = func(_ context.Context, message string, fields ...zap.Field) { logger.Info(message, fields...) }
			t.Cleanup(func() { memeReferenceLog = original })
			installReferenceProvider(t, func(context.Context, string, string, []memes.Turn) memes.ReferenceResult {
				result := memes.ReferenceResult{Status: memes.ReferenceStatusEmpty, Degraded: true}
				result.Retrieval.Queries = []memes.QueryResult{{Query: memes.SearchQuery{Field: memes.FieldEmotion, Text: "private-query-body"}, Channels: []memes.ChannelResult{{Channel: "fulltext", Status: "ok", Error: "private-error-body"}}}}
				if rawQueries {
					result.Retrieval.RawQueries = []memes.QueryResult{{Query: memes.SearchQuery{Field: memes.FieldRelation, Text: "private-query-body"}, Channels: []memes.ChannelResult{{Channel: "vector", Status: "ok"}, {Channel: "fulltext", Status: "error", Error: "private-error-body", Hits: []memes.RawHit{{Full: memes.UseCard{Description: "private-card-body"}}}}}}}
				}
				return result
			})
			_ = withMemeReferencePlan(context.Background(), referenceEvent(), StandardChatPlan{})
			events := entries.FilterMessage("meme reference retrieval").All()
			if len(events) != 1 {
				t.Fatalf("retrieval logs=%+v", events)
			}
			fields := events[0].ContextMap()
			channels, _ := json.Marshal(fields["channel_statuses"])
			want := `["emotion_chain:fulltext:ok"]`
			if rawQueries {
				want = `["relation:vector:ok","relation:fulltext:error"]`
			}
			if string(channels) != want || fields["degraded"] != true {
				t.Fatalf("missing channel health: %+v want=%s", fields, want)
			}
			all, _ := json.Marshal(events)
			for _, private := range []string{"private-query-body", "private-error-body", "private-card-body", "原话"} {
				if strings.Contains(string(all), private) {
					t.Fatalf("private retrieval input logged: %s", all)
				}
			}
		})
	}
	queries := make([]memes.QueryResult, 100)
	for i := range queries {
		queries[i] = memes.QueryResult{Query: memes.SearchQuery{Field: "private-field"}, Status: "private-status", Channels: []memes.ChannelResult{{Channel: "private-channel", Status: "private-status"}}}
	}
	core, entries := observer.New(zap.InfoLevel)
	logger := zap.New(core)
	original := memeReferenceLog
	memeReferenceLog = func(_ context.Context, message string, fields ...zap.Field) { logger.Info(message, fields...) }
	t.Cleanup(func() { memeReferenceLog = original })
	installReferenceProvider(t, func(context.Context, string, string, []memes.Turn) memes.ReferenceResult {
		return memes.ReferenceResult{Status: memes.ReferenceStatusEmpty, Retrieval: memes.RetrievalSnapshot{RawQueries: queries}}
	})
	_ = withMemeReferencePlan(context.Background(), referenceEvent(), StandardChatPlan{})
	channelsJSON, _ := json.Marshal(entries.FilterMessage("meme reference retrieval").All()[0].ContextMap()["channel_statuses"])
	var channels []string
	_ = json.Unmarshal(channelsJSON, &channels)
	if len(channels) > 13 || len(channels) == 0 || strings.Contains(strings.Join(channels, ","), "private") {
		t.Fatalf("channel summary not bounded/sanitized: %v", channels)
	}
}

func TestMemeReferenceFailureLeavesBothModelPromptsAndRepliesUnchanged(t *testing.T) {
	for _, status := range []memes.ReferenceStatus{memes.ReferenceStatusEmpty, memes.ReferenceStatusError, memes.ReferenceStatusUnresolved} {
		for _, plain := range []bool{false, true} {
			t.Run(string(status)+map[bool]string{false: "/standard", true: "/two_phase"}[plain], func(t *testing.T) {
				installReferenceProvider(t, func(context.Context, string, string, []memes.Turn) memes.ReferenceResult {
					return memes.ReferenceResult{Status: status}
				})
				executor := referenceExecutor{do: func(_ context.Context, system, user string) iter.Seq[*ark_dal.ModelStreamRespReasoning] {
					if system != "original system" || user != "original user" {
						t.Fatalf("failed lookup changed prompt: %q %q", system, user)
					}
					if plain {
						return referenceStream("普通回复。")
					}
					return referenceStream(`{"decision":"reply","reply":"普通回复。"}`)
				}}
				plan := StandardChatPlan{chatID: "chat", openID: "human", userName: "name", SystemPrompt: "original system", UserPrompt: "original user"}
				var seq iter.Seq[*ark_dal.ModelStreamRespReasoning]
				var err error
				if plain {
					seq, err = executeTwoPhaseReferenceGenerator(context.Background(), referenceEvent(), plan, llmusage.Scope{}, executor)
				} else {
					seq, err = executeStandardChatPlanWithExecutorFactory(context.Background(), referenceEvent(), &xhandler.BaseMetaData{ChatName: "name"}, plan, func(string) standardChatExecutor { return executor })
				}
				if err != nil {
					t.Fatal(err)
				}
				var final *ark_dal.ModelStreamRespReasoning
				if plain {
					seq = wrapTwoPhaseStream(context.Background(), seq, "intent", nil, "chat")
				}
				for data := range seq {
					final = data
				}
				if final.ContentStruct.Reply != "普通回复。" || final.ContentStruct.Decision != "reply" {
					t.Fatalf("ordinary response lost: %+v", final)
				}
			})
		}
	}
}

func TestMemeReferenceRawContextHasNoFutureForeignOrDisplayHistory(t *testing.T) {
	event := referenceEvent()
	anchor, _ := eventAnchorTime(event)
	raw, foreign, future := "@_user_1 没升级\n只是打算试试。", "别群消息", "未来才发生"
	messages := history.OpensearchMsgLogList{
		{ChatID: "chat", MessageID: "before", OpenID: "a", CreateTimeV2: anchor.Add(-time.Minute).Format(time.RFC3339Nano), RawText: &raw},
		{ChatID: "other", MessageID: "foreign", OpenID: "b", CreateTimeV2: anchor.Add(-time.Minute).Format(time.RFC3339Nano), RawText: &foreign},
		{ChatID: "chat", MessageID: "future", OpenID: "b", CreateTimeV2: anchor.Add(time.Minute).Format(time.RFC3339Nano), RawText: &future},
		{ChatID: "chat", MessageID: "display", OpenID: "b", CreateTimeV2: anchor.Add(-time.Minute).Format(time.RFC3339Nano), MsgList: []string{"总结改写，不是原话"}},
	}
	installReferenceProvider(t, func(_ context.Context, _, _ string, dialogue []memes.Turn) memes.ReferenceResult {
		if len(dialogue) != 2 || dialogue[0].Text != raw || dialogue[1].Text != "原话\n只是提议，还没做。" {
			t.Fatalf("fabricated/raw context changed: %+v", dialogue)
		}
		return memes.ReferenceResult{Status: memes.ReferenceStatusEmpty}
	})
	_ = withMemeReferencePlan(context.Background(), event, StandardChatPlan{messageList: messages})
}

func TestMemeReferencesDoNotOverrideStandardSkip(t *testing.T) {
	installReferenceProvider(t, func(context.Context, string, string, []memes.Turn) memes.ReferenceResult {
		return memes.ReferenceResult{Status: memes.ReferenceStatusReady, Cards: []memes.UseCard{{UseID: "wiki.one", Description: "候选"}}}
	})
	executor := referenceExecutor{do: func(context.Context, string, string) iter.Seq[*ark_dal.ModelStreamRespReasoning] {
		return referenceStream(`{"decision":"skip","reply":""}`)
	}}
	seq, err := executeStandardChatPlanWithExecutorFactory(context.Background(), referenceEvent(), &xhandler.BaseMetaData{ChatName: "name"}, StandardChatPlan{chatID: "chat", openID: "human", userName: "name"}, func(string) standardChatExecutor { return executor })
	if err != nil {
		t.Fatal(err)
	}
	var final *ark_dal.ModelStreamRespReasoning
	for data := range seq {
		final = data
	}
	if final.ContentStruct.Decision != "skip" {
		t.Fatalf("skip overridden: %+v", final)
	}
}

func TestMemeReferenceNoContextAndDeadlineStillAllowOrdinaryReply(t *testing.T) {
	event := referenceEvent()
	anchor, _ := eventAnchorTime(event)
	raw := "不应携带的历史"
	calls := 0
	installReferenceProvider(t, func(_ context.Context, _, _ string, dialogue []memes.Turn) memes.ReferenceResult {
		calls++
		if len(dialogue) != 1 || dialogue[0].TurnID != "current" {
			t.Fatalf("no-context request included history: %+v", dialogue)
		}
		return memes.ReferenceResult{Status: memes.ReferenceStatusEmpty}
	})
	plan := StandardChatPlan{SystemPrompt: "original", noMemeReferenceHistory: true, messageList: history.OpensearchMsgLogList{{ChatID: "chat", MessageID: "before", OpenID: "a", CreateTimeV2: anchor.Add(-time.Minute).Format(time.RFC3339Nano), RawText: &raw}}}
	got := withMemeReferencePlan(context.Background(), event, plan)
	if calls != 1 || got.SystemPrompt != "original" {
		t.Fatal("ordinary no-context flow changed")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	got = withMemeReferencePlan(ctx, event, plan)
	if calls != 1 || got.SystemPrompt != "original" {
		t.Fatal("optional lookup consumed reserved reply time")
	}
}
