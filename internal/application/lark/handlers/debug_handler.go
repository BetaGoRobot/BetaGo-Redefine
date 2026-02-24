package handlers

import (
	"context"
	"errors"
	"fmt"
	"iter"
	"slices"
	"strings"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/history"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/ark_dal"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/config"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/lark_dal"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/lark_dal/larkimg"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/lark_dal/larkmsg"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/lark_dal/larkmsg/larkcontent"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/lark_dal/larkmsg/larktpl"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/opensearch"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/otel"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/xmodel"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/logs"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/utils"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/xchunk"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/xhandler"
	commonutils "github.com/BetaGoRobot/go_utils/common_utils"
	"github.com/BetaGoRobot/go_utils/reflecting"
	"github.com/bytedance/sonic"
	"github.com/defensestation/osquery"
	larkcore "github.com/larksuite/oapi-sdk-go/v3/core"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
	"go.opentelemetry.io/otel/attribute"
	"go.uber.org/zap"
)

const (
	getIDText      = "Quoted Msg OpenID is "
	getGroupIDText = "Current ChatID is "
)

type traceItem struct {
	TraceID    string `json:"trace_id"`
	CreateTime string `json:"create_time"`
}

// DebugGetIDHandler to be filled
//
//	@param ctx context.Context
//	@param data *larkim.P2MessageReceiveV1
//	@param args ...string
//	@return error
//	@author heyuhengmatt
//	@update 2024-08-06 08:27:33
func DebugGetIDHandler(ctx context.Context, data *larkim.P2MessageReceiveV1, metaData *xhandler.BaseMetaData, args ...string) (err error) {
	ctx, span := otel.T().Start(ctx, reflecting.GetCurrentFunc())
	span.SetAttributes(attribute.Key("event").String(larkcore.Prettify(data)))
	defer span.End()
	defer func() { span.RecordError(err) }()

	if data.Event.Message.ParentId == nil {
		return errors.New("No parent Msg Quoted")
	}

	err = larkmsg.ReplyCardText(ctx, getIDText+*data.Event.Message.ParentId, *data.Event.Message.MessageId, "_getID", false)
	if err != nil {
		logs.L().Ctx(ctx).Error("ReplyMessage", zap.Error(err), zap.String("TraceID", span.SpanContext().TraceID().String()))
		return err
	}
	return nil
}

// DebugGetGroupIDHandler to be filled
//
//	@param ctx context.Context
//	@param data *larkim.P2MessageReceiveV1
//	@param args ...string
//	@return error
//	@author heyuhengmatt
//	@update 2024-08-06 08:27:29
func DebugGetGroupIDHandler(ctx context.Context, data *larkim.P2MessageReceiveV1, metaData *xhandler.BaseMetaData, args ...string) (err error) {
	ctx, span := otel.T().Start(ctx, reflecting.GetCurrentFunc())
	span.SetAttributes(attribute.Key("event").String(larkcore.Prettify(data)))
	defer span.End()
	defer func() { span.RecordError(err) }()
	chatID := data.Event.Message.ChatId
	if chatID != nil {
		err := larkmsg.ReplyCardText(ctx, getGroupIDText+*chatID, *data.Event.Message.MessageId, "_getGroupID", false)
		if err != nil {
			logs.L().Ctx(ctx).Error("ReplyMessage", zap.Error(err), zap.String("TraceID", span.SpanContext().TraceID().String()))
			return err
		}
	}

	return nil
}

// DebugTryPanicHandler to be filled
//
//	@param ctx context.Context
//	@param data *larkim.P2MessageReceiveV1
//	@param args ...string
//	@return error
//	@author heyuhengmatt
//	@update 2024-08-06 08:27:25
func DebugTryPanicHandler(ctx context.Context, data *larkim.P2MessageReceiveV1, metaData *xhandler.BaseMetaData, args ...string) (err error) {
	ctx, span := otel.T().Start(ctx, reflecting.GetCurrentFunc())
	span.SetAttributes(attribute.Key("event").String(larkcore.Prettify(data)))
	defer span.End()
	defer func() { span.RecordError(err) }()
	panic(errors.New("try panic!"))
}

func (t *traceItem) TraceURLMD() string {
	return strings.Join([]string{t.CreateTime, ": [Trace-", t.TraceID[:8], "]", "(", utils.GenTraceURL(t.TraceID), ")"}, "")
}

// GetTraceFromMsgID to be filled
//
//	@param ctx context.Context
//	@param msgID string
//	@return []string
//	@return error
//	@author heyuhengmatt
//	@update 2024-08-06 08:27:37
func GetTraceFromMsgID(ctx context.Context, msgID string) (iter.Seq[*traceItem], error) {
	ctx, span := otel.T().Start(ctx, reflecting.GetCurrentFunc())
	defer span.End()

	query := osquery.Search().
		Query(
			osquery.Bool().Must(
				osquery.Term("message_id", msgID),
			),
		).
		SourceIncludes("create_time", "trace_id").
		Sort("create_time", "desc")
	resp, err := opensearch.SearchData(
		ctx, config.Get().OpensearchConfig.LarkMsgIndex, query,
	)
	if err != nil {
		return nil, err
	}
	return func(yield func(*traceItem) bool) {
		for _, hit := range resp.Hits.Hits {
			src := &xmodel.MessageIndex{}
			err = sonic.Unmarshal(hit.Source, &src)
			if err != nil {
				return
			}
			if src.TraceID != "" {
				if !yield(&traceItem{src.TraceID, src.CreateTime}) {
					return
				}
			}
		}
	}, nil
}

// DebugTraceHandler to be filled
//
//	@param ctx context.Context
//	@param data *larkim.P2MessageReceiveV1
//	@param args ...string
//	@return error
//	@author heyuhengmatt
//	@update 2024-08-06 08:27:23
func DebugTraceHandler(ctx context.Context, data *larkim.P2MessageReceiveV1, metaData *xhandler.BaseMetaData, args ...string) (err error) {
	ctx, span := otel.T().Start(ctx, reflecting.GetCurrentFunc())
	span.SetAttributes(attribute.Key("event").String(larkcore.Prettify(data)))
	defer span.End()
	defer func() { span.RecordError(err) }()
	var (
		m             = map[string]struct{}{}
		traceIDs      = make([]string, 0)
		replyInThread bool
	)
	if data.Event.Message.ThreadId != nil { // 话题模式，找到所有的traceID
		replyInThread = true
		resp, err := lark_dal.Client().Im.Message.List(ctx,
			larkim.NewListMessageReqBuilder().
				ContainerId(*data.Event.Message.ThreadId).
				ContainerIdType("thread").
				Build(),
		)
		if err != nil {
			return err
		}
		for _, msg := range resp.Data.Items {
			traceIters, err := GetTraceFromMsgID(ctx, *msg.MessageId)
			if err != nil {
				return err
			}
			for item := range traceIters {
				if _, ok := m[item.TraceID]; ok {
					continue
				}
				m[item.TraceID] = struct{}{}
				traceIDs = append(traceIDs, item.TraceURLMD())
			}
		}
	} else if data.Event.Message.ParentId != nil {
		traceIters, err := GetTraceFromMsgID(ctx, *data.Event.Message.ParentId)
		if err != nil {
			return err
		}
		for item := range traceIters {
			if _, ok := m[item.TraceID]; ok {
				continue
			}
			m[item.TraceID] = struct{}{}
			traceIDs = append(traceIDs, item.TraceURLMD())
		}
	}
	if len(traceIDs) == 0 {
		return errors.New("No traceID found")
	}
	traceIDStr := "TraceIDs:\n" + strings.Join(traceIDs, "\n")
	err = larkmsg.ReplyCardText(ctx, traceIDStr, *data.Event.Message.MessageId, "_trace", replyInThread)
	if err != nil {
		logs.L().Ctx(ctx).Error("ReplyMessage", zap.Error(err), zap.String("TraceID", span.SpanContext().TraceID().String()))
		return err
	}
	return nil
}

// DebugRevertHandler DebugTraceHandler to be filled
//
//	@param ctx context.Context
//	@param data *larkim.P2MessageReceiveV1
//	@param args ...string
//	@return error
func DebugRevertHandler(ctx context.Context, data *larkim.P2MessageReceiveV1, metaData *xhandler.BaseMetaData, args ...string) (err error) {
	ctx, span := otel.T().Start(ctx, reflecting.GetCurrentFunc())
	span.SetAttributes(attribute.Key("event").String(larkcore.Prettify(data)))
	defer span.End()
	defer func() { span.RecordError(err) }()
	var res string = "撤回成功"
	defer func() { metaData.SetExtra("revert_result", res) }()

	if data.Event.Message.ThreadId != nil { // 话题模式，找到所有的traceID
		res = "话题模式的消息，所有的机器人发言都被撤回了"
		resp, err := lark_dal.Client().Im.Message.List(ctx, larkim.NewListMessageReqBuilder().ContainerIdType("thread").ContainerId(*data.Event.Message.ThreadId).Build())
		if err != nil {
			return err
		}
		for _, msg := range resp.Data.Items {
			if *msg.Sender.Id == config.Get().LarkConfig.BotOpenID {
				resp, err := lark_dal.Client().Im.Message.Delete(ctx, larkim.NewDeleteMessageReqBuilder().MessageId(*msg.MessageId).Build())
				if err != nil {
					return err
				}
				if !resp.Success() {
					logs.L().Ctx(ctx).Error("DeleteMessage", zap.Error(errors.New(resp.Error())), zap.String("MessageID", *msg.MessageId))
				}
			}
		}
	} else if data.Event.Message.ParentId != nil {
		respMsg := larkmsg.GetMsgFullByID(ctx, *data.Event.Message.ParentId)
		msg := respMsg.Data.Items[0]
		if msg == nil {
			res = "没有圈选消息，不能撤回"
			return errors.New("No parent message found")
		}
		if msg.Sender.Id == nil || *msg.Sender.Id != config.Get().LarkConfig.BotOpenID {
			res = "消息不是机器人发出的，不能撤回"
			return errors.New("Parent message is not sent by bot")
		}
		resp, err := lark_dal.Client().Im.Message.Delete(ctx, larkim.NewDeleteMessageReqBuilder().MessageId(*data.Event.Message.ParentId).Build())
		if err != nil {
			return err
		}
		if !resp.Success() {
			return errors.New(resp.Error())
		}
	}
	return nil
}

func DebugRepeatHandler(ctx context.Context, data *larkim.P2MessageReceiveV1, metaData *xhandler.BaseMetaData, args ...string) (err error) {
	ctx, span := otel.T().Start(ctx, reflecting.GetCurrentFunc())
	span.SetAttributes(attribute.Key("event").String(larkcore.Prettify(data)))
	defer span.End()
	defer func() { span.RecordError(err) }()

	if data.Event.Message.ThreadId != nil {
		return nil
	} else if data.Event.Message.ParentId != nil {
		respMsg := larkmsg.GetMsgFullByID(ctx, *data.Event.Message.ParentId)
		msg := respMsg.Data.Items[0]
		if msg == nil {
			return errors.New("No parent message found")
		}
		if msg.Sender.Id == nil {
			return errors.New("Parent message is not sent by bot")
		}
		repeatReq := larkim.NewCreateMessageReqBuilder().
			Body(
				larkim.NewCreateMessageReqBodyBuilder().
					MsgType(*msg.MsgType).
					Content(
						*msg.Body.Content,
					).
					ReceiveId(*msg.ChatId).
					Build(),
			).
			ReceiveIdType(larkim.ReceiveIdTypeChatId).
			Build()
		resp, err := lark_dal.Client().Im.V1.Message.Create(ctx, repeatReq)
		if err != nil {
			return err
		}
		if !resp.Success() {
			if strings.Contains(resp.Error(), "invalid image_key") {
				logs.L().Ctx(ctx).Error("repeatMessage", zap.Error(err), zap.String("TraceID", span.SpanContext().TraceID().String()))
				return nil
			}
			return errors.New(resp.Error())
		}
		go larkmsg.RecordMessage2Opensearch(ctx, resp)
	}
	return nil
}

func DebugImageHandler(ctx context.Context, data *larkim.P2MessageReceiveV1, metaData *xhandler.BaseMetaData, args ...string) (err error) {
	ctx, span := otel.T().Start(ctx, reflecting.GetCurrentFunc())
	span.SetAttributes(attribute.Key("event").String(larkcore.Prettify(data)))
	defer span.End()
	defer func() { span.RecordError(err) }()
	seq, err := larkimg.GetAllImgURLFromParent(ctx, data)
	if err != nil {
		return err
	}
	if seq == nil {
		return nil
	}
	urls := make([]string, 0)
	for url := range seq {
		// url = strings.ReplaceAll(url, "kmhomelab.cn", "kevinmatt.top")
		urls = append(urls, url)
	}
	var inputPrompt string
	if _, input := parseArgs(args...); input == "" {
		inputPrompt = "图里都是些什么？"
	} else {
		inputPrompt = input
	}

	dataSeq, err := ark_dal.New[*larkim.P2MessageReceiveV1](
		"chat_id", "user_id", nil,
	).Do(ctx, "", inputPrompt)
	if err != nil {
		return err
	}
	err = larkmsg.SendAndReplyStreamingCard(ctx, data.Event.Message, dataSeq, true)
	if err != nil {
		return err
	}
	return nil
}

func DebugConversationHandler(ctx context.Context, data *larkim.P2MessageReceiveV1, metaData *xhandler.BaseMetaData, args ...string) (err error) {
	ctx, span := otel.T().Start(ctx, reflecting.GetCurrentFunc())
	span.SetAttributes(attribute.Key("event").String(larkcore.Prettify(data)))
	defer span.End()
	defer func() { span.RecordError(err) }()

	msgs, err := larkmsg.GetAllParentMsg(ctx, data)
	if err != nil {
		return err
	}

	resp, err := opensearch.SearchData(ctx, config.Get().OpensearchConfig.LarkMsgIndex,
		map[string]any{
			"query": map[string]any{
				"bool": map[string]any{
					"must": map[string]any{
						"terms": map[string]any{
							"msg_ids": commonutils.TransSlice(msgs, func(msg *larkim.Message) string { return *msg.MessageId }),
						},
					},
				},
			},
			"sort": map[string]any{
				"timestamp_v2": map[string]any{
					"order": "desc",
				},
			},
		})
	if err != nil {
		return err
	}
	for _, hit := range resp.Hits.Hits {
		chunkLog := &xmodel.MessageChunkLogV3{}
		err = sonic.Unmarshal(hit.Source, chunkLog)
		if err != nil {
			return err
		}

		msgList, err := history.New(ctx).Query(
			osquery.Bool().Must(
				osquery.Terms("message_id", commonutils.TransSlice(chunkLog.MsgIDs, func(s string) any { return s })...),
			),
		).
			Source("raw_message", "mentions", "message_str", "create_time", "user_id", "chat_id", "user_name", "message_type").GetAll()
		if err != nil {
			return err
		}
		tpl := larktpl.GetTemplateV2[larktpl.ChunkMetaData](ctx, larktpl.ChunkMetaTemplate) // make sure template is loaded
		msgLines := commonutils.TransSlice(msgList, func(msg *xmodel.MessageIndex) *larktpl.MsgLine {
			msgTrunc := make([]string, 0)
			for item := range larkcontent.Trans2Item(msg.MessageType, msg.RawMessage) {
				switch item.Tag {
				case "image", "sticker":
					msgTrunc = append(msgTrunc, fmt.Sprintf("![something](%s)", item.Content))
				case "text":
					msgTrunc = append(msgTrunc, item.Content)
				}
			}
			return &larktpl.MsgLine{
				Time:    msg.CreateTime,
				User:    &larktpl.User{UserID: msg.UserID},
				Content: strings.Join(msgTrunc, " "),
			}
		})
		slices.SortFunc(msgLines, func(a, b *larktpl.MsgLine) int {
			return strings.Compare(a.Time, b.Time)
		})
		metaData := &larktpl.ChunkMetaData{
			Summary: chunkLog.Summary,

			Intent: xchunk.Translate(chunkLog.Intent),
			Participants: Dedup(
				commonutils.TransSlice(msgList, func(m *xmodel.MessageIndex) *larktpl.User { return &larktpl.User{UserID: m.UserID} }),
				func(u *larktpl.User) string { return u.UserID },
			),

			Sentiment: xchunk.Translate(chunkLog.SentimentAndTone.Sentiment),
			Tones:     commonutils.TransSlice(chunkLog.SentimentAndTone.Tones, func(tone string) *larktpl.ToneData { return &larktpl.ToneData{Tone: xchunk.Translate(tone)} }),
			Questions: commonutils.TransSlice(chunkLog.InteractionAnalysis.UnresolvedQuestions, func(question string) *larktpl.Questions { return &larktpl.Questions{Question: question} }),

			MsgList: msgLines,

			// PlansAndSuggestion: ,
			MainTopicsOrActivities:         commonutils.TransSlice(chunkLog.Entities.MainTopicsOrActivities, larktpl.ToObjTextArray),
			KeyConceptsAndNouns:            commonutils.TransSlice(chunkLog.Entities.KeyConceptsAndNouns, larktpl.ToObjTextArray),
			MentionedGroupsOrOrganizations: commonutils.TransSlice(chunkLog.Entities.MentionedGroupsOrOrganizations, larktpl.ToObjTextArray),
			MentionedPeople:                commonutils.TransSlice(chunkLog.Entities.MentionedPeople, larktpl.ToObjTextArray),
			LocationsAndVenues:             commonutils.TransSlice(chunkLog.Entities.LocationsAndVenues, larktpl.ToObjTextArray),
			MediaAndWorks: commonutils.TransSlice(chunkLog.Entities.MediaAndWorks, func(m *xmodel.MediaAndWork) *larktpl.MediaAndWork {
				return &larktpl.MediaAndWork{m.Title, m.Type}
			}),

			Timestamp: chunkLog.Timestamp,
			MsgID:     *data.Event.Message.MessageId,
		}

		tpl.WithData(metaData)
		cardContent := larktpl.NewCardContentV2[larktpl.ChunkMetaData](ctx, tpl.TemplateID)
		err = larkmsg.ReplyCard(ctx, cardContent, *data.Event.Message.MessageId, "_replyGet", false)
		if err != nil {
			return err
		}
	}

	return err
}

func Map[T any, U any](slice []T, f func(int, T) U) []U {
	result := make([]U, 0, len(slice))
	for idx, v := range slice {
		result = append(result, f(idx, v))
	}
	return result
}

func Dedup[T, K comparable](slice []T, keyFunc func(T) K) []T {
	seen := make(map[K]struct{})
	result := make([]T, 0, len(slice))
	for _, v := range slice {
		key := keyFunc(v)
		if _, ok := seen[key]; !ok {
			seen[key] = struct{}{}
			result = append(result, v)
		}
	}
	return result
}

// func init() {
// 	params := tools.NewParameters("object")
// 	fcu := tools.NewFunctionCallUnit().
// 		Name("revert_message").Desc("可以撤回指定消息,调用时不需要任何参数，工具会判断要撤回的消息是什么，并且返回撤回的结果。如果不是机器人发出的消息,是不能撤回的").Params(params).Func(revertWrap)
// 	tools.M().Add(fcu)
// }

// func revertWrap(ctx context.Context, meta *tools.FunctionCallMeta, args string) (any, error) {
// 	s := struct {
// 		Time   string `json:"time"`
// 		Cancel bool   `json:"cancel"`
// 	}{}
// 	err := utils.UnmarshalStrPre(args, &s)
// 	if err != nil {
// 		return nil, err
// 	}
// 	argsSlice := make([]string, 0)
// 	if s.Cancel {
// 		argsSlice = append(argsSlice, "--cancel")
// 	}
// 	if s.Time != "" {
// 		argsSlice = append(argsSlice, "--t="+s.Time)
// 	}
// 	metaData := xhandler.NewBaseMetaDataWithChatIDUID(ctx, meta.ChatID, meta.UserID)
// 	if err := DebugRevertHandler(ctx, meta.LarkData, metaData, argsSlice...); err != nil {
// 		return nil, err
// 	}
// 	return goption.Of(metaData.GetExtra("revert_result")).ValueOr("执行完成但没有结果"), nil
// }
