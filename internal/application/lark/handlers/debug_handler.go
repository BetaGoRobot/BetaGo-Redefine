package handlers

import (
	"context"
	"errors"
	"fmt"
	"iter"
	"slices"
	"strings"
	"time"

	appconfig "github.com/BetaGoRobot/BetaGo-Redefine/internal/application/config"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/botidentity"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/carddebug"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/history"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/ark_dal"
	arktools "github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/ark_dal/tools"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/db/query"
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
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/xcommand"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/xhandler"
	commonutils "github.com/BetaGoRobot/go_utils/common_utils"
	"github.com/bytedance/sonic"
	"github.com/defensestation/osquery"
	larkcore "github.com/larksuite/oapi-sdk-go/v3/core"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
	"go.uber.org/zap"
)

const (
	getIDText      = "Quoted Msg OpenID is "
	getGroupIDText = "Current ChatID is "
)

type (
	debugGetIDArgs      struct{}
	debugGetGroupIDArgs struct{}
	debugTryPanicArgs   struct{}
	debugTraceArgs      struct{}
	debugRevertArgs     struct{}
	debugRepeatArgs     struct{}
	debugImageArgs      struct {
		Prompt string `cli:"prompt,input" help:"图片分析提示词"`
	}
	debugCardArgs struct {
		Spec         DebugCardSpec `cli:"spec" help:"内置调试 spec，例如 config/ratelimit.sample"`
		Template     string        `cli:"template" help:"卡片模板ID或名称"`
		VarJSON      string        `cli:"vars_json" help:"模板变量 JSON"`
		ToChatID     string        `cli:"to_chat_id" help:"发送目标 chat_id"`
		ToOpenID     string        `cli:"to_open_id" help:"发送目标 open_id"`
		ChatID       string        `cli:"chat_id" help:"业务上下文 chat_id"`
		ID           string        `cli:"id" help:"业务对象 ID，例如 schedule.task 的 task_id"`
		ActorOpenID  string        `cli:"actor_open_id" help:"业务上下文操作者 open_id"`
		TargetOpenID string        `cli:"target_open_id" help:"业务上下文目标 open_id"`
		Scope        ConfigScope   `cli:"scope" help:"业务上下文 scope，例如 chat/global/user"`
	}
)
type debugConversationArgs struct{}

type (
	debugGetIDHandler        struct{}
	debugGetGroupIDHandler   struct{}
	debugTryPanicHandler     struct{}
	debugTraceHandler        struct{}
	debugRevertHandler       struct{}
	debugRepeatHandler       struct{}
	debugImageHandler        struct{}
	debugConversationHandler struct{}
	debugCardHandler         struct{}
)

var (
	DebugGetID        debugGetIDHandler
	DebugGetGroupID   debugGetGroupIDHandler
	DebugTryPanic     debugTryPanicHandler
	DebugTrace        debugTraceHandler
	DebugRevert       debugRevertHandler
	DebugRepeat       debugRepeatHandler
	DebugImage        debugImageHandler
	DebugConversation debugConversationHandler
	DebugCard         debugCardHandler
)

func (debugGetIDHandler) CommandDescription() string {
	return "查看引用消息 ID"
}

func (debugGetGroupIDHandler) CommandDescription() string {
	return "查看当前会话 ID"
}

func (debugTryPanicHandler) CommandDescription() string {
	return "触发 panic 调试"
}

func (debugTraceHandler) CommandDescription() string {
	return "查看消息 trace"
}

func (debugRevertHandler) CommandDescription() string {
	return "撤回机器人消息"
}

func (debugRepeatHandler) CommandDescription() string {
	return "复发引用消息"
}

func (debugImageHandler) CommandDescription() string {
	return "分析引用图片"
}

func (debugConversationHandler) CommandDescription() string {
	return "查看对话上下文"
}

func (debugGetIDHandler) CommandExamples() []string {
	return []string{"/debug msgid"}
}

func (debugGetGroupIDHandler) CommandExamples() []string {
	return []string{"/debug chatid"}
}

func (debugTryPanicHandler) CommandExamples() []string {
	return []string{"/debug panic"}
}

func (debugTraceHandler) CommandExamples() []string {
	return []string{"/debug trace"}
}

func (debugRevertHandler) CommandExamples() []string {
	return []string{"/debug revert"}
}

func (debugRepeatHandler) CommandExamples() []string {
	return []string{"/debug repeat"}
}

func (debugImageHandler) CommandExamples() []string {
	return []string{
		"/debug image",
		"/debug image 这张图里有什么",
	}
}

func (debugConversationHandler) CommandExamples() []string {
	return []string{"/debug conver"}
}

func (debugCardHandler) CommandDescription() string {
	return "卡片调试工具，支持模板卡、内置管理卡和样例卡直发"
}

func (debugCardHandler) CommandExamples() []string {
	return []string{
		"/debug card",
		"/debug card --spec=ratelimit.sample",
		"/debug card --spec=config --chat_id=oc_abc123 --actor_open_id=ou_admin",
		"/debug card --spec=schedule.task --id=20260312093000-debugA --chat_id=oc_abc123",
		"/debug card --template=NormalCardReplyTemplate --vars_json='{\"content\":\"Hello World\"}' --to_open_id=ou_xxx",
	}
}

func (debugGetIDHandler) ParseCLI(args []string) (debugGetIDArgs, error) {
	return debugGetIDArgs{}, nil
}

func (debugGetIDHandler) Handle(ctx context.Context, data *larkim.P2MessageReceiveV1, metaData *xhandler.BaseMetaData, arg debugGetIDArgs) error {
	return handleDebugGetID(ctx, data, metaData)
}

func (debugGetGroupIDHandler) ParseCLI(args []string) (debugGetGroupIDArgs, error) {
	return debugGetGroupIDArgs{}, nil
}

func (debugGetGroupIDHandler) Handle(ctx context.Context, data *larkim.P2MessageReceiveV1, metaData *xhandler.BaseMetaData, arg debugGetGroupIDArgs) error {
	return handleDebugGetGroupID(ctx, data, metaData)
}

func (debugTryPanicHandler) ParseCLI(args []string) (debugTryPanicArgs, error) {
	return debugTryPanicArgs{}, nil
}

func (debugTryPanicHandler) Handle(ctx context.Context, data *larkim.P2MessageReceiveV1, metaData *xhandler.BaseMetaData, arg debugTryPanicArgs) error {
	return handleDebugTryPanic(ctx, data, metaData)
}

func (debugTraceHandler) ParseCLI(args []string) (debugTraceArgs, error) {
	return debugTraceArgs{}, nil
}

func (debugTraceHandler) Handle(ctx context.Context, data *larkim.P2MessageReceiveV1, metaData *xhandler.BaseMetaData, arg debugTraceArgs) error {
	return handleDebugTrace(ctx, data, metaData)
}

func (debugRevertHandler) ParseCLI(args []string) (debugRevertArgs, error) {
	return debugRevertArgs{}, nil
}

func (debugRevertHandler) ParseTool(raw string) (debugRevertArgs, error) {
	if err := parseEmptyToolArgs(raw); err != nil {
		return debugRevertArgs{}, err
	}
	return debugRevertArgs{}, nil
}

func (debugRevertHandler) ToolSpec() xcommand.ToolSpec {
	return xcommand.ToolSpec{
		Name:   "revert_message",
		Desc:   "可以撤回指定消息,调用时不需要任何参数，工具会判断要撤回的消息是什么，并且返回撤回的结果。如果不是机器人发出的消息,是不能撤回的",
		Params: arktools.NewParams("object"),
		Result: func(metaData *xhandler.BaseMetaData) string {
			result, _ := metaData.GetExtra("revert_result")
			return result
		},
	}
}

func (debugRevertHandler) Handle(ctx context.Context, data *larkim.P2MessageReceiveV1, metaData *xhandler.BaseMetaData, arg debugRevertArgs) error {
	return handleDebugRevert(ctx, data, metaData)
}

func (debugRepeatHandler) ParseCLI(args []string) (debugRepeatArgs, error) {
	return debugRepeatArgs{}, nil
}

func (debugRepeatHandler) Handle(ctx context.Context, data *larkim.P2MessageReceiveV1, metaData *xhandler.BaseMetaData, arg debugRepeatArgs) error {
	return handleDebugRepeat(ctx, data, metaData)
}

func (debugImageHandler) ParseCLI(args []string) (debugImageArgs, error) {
	_, input := parseArgs(args...)
	return debugImageArgs{Prompt: input}, nil
}

func (debugImageHandler) Handle(ctx context.Context, data *larkim.P2MessageReceiveV1, metaData *xhandler.BaseMetaData, arg debugImageArgs) error {
	if arg.Prompt == "" {
		return handleDebugImage(ctx, data, metaData)
	}
	return handleDebugImage(ctx, data, metaData, arg.Prompt)
}

func (debugConversationHandler) ParseCLI(args []string) (debugConversationArgs, error) {
	return debugConversationArgs{}, nil
}

func (debugConversationHandler) Handle(ctx context.Context, data *larkim.P2MessageReceiveV1, metaData *xhandler.BaseMetaData, arg debugConversationArgs) error {
	return handleDebugConversation(ctx, data, metaData)
}

func (debugCardHandler) ParseCLI(args []string) (debugCardArgs, error) {
	argsMap, _ := parseArgs(args...)
	spec, err := xcommand.ParseEnum[DebugCardSpec](firstNonEmpty(argsMap["spec"], argsMap["card"]))
	if err != nil {
		return debugCardArgs{}, err
	}
	scope, err := xcommand.ParseEnum[ConfigScope](argsMap["scope"])
	if err != nil {
		return debugCardArgs{}, err
	}
	return debugCardArgs{
		Spec:         spec,
		Template:     argsMap["template"],
		VarJSON:      firstNonEmpty(argsMap["vars_json"], argsMap["vars"], argsMap["var"]),
		ToChatID:     firstNonEmpty(argsMap["to_chat_id"], argsMap["chatid"]),
		ToOpenID:     argsMap["to_open_id"],
		ChatID:       argsMap["chat_id"],
		ID:           firstNonEmpty(argsMap["id"], argsMap["task_id"]),
		ActorOpenID:  argsMap["actor_open_id"],
		TargetOpenID: argsMap["target_open_id"],
		Scope:        scope,
	}, nil
}

func (debugCardHandler) Handle(ctx context.Context, data *larkim.P2MessageReceiveV1, metaData *xhandler.BaseMetaData, arg debugCardArgs) error {
	return handleDebugCard(ctx, data, metaData, arg)
}

type traceItem struct {
	TraceID    string `json:"trace_id"`
	CreateTime string `json:"create_time"`
}

// handleDebugGetID to be filled
//
//	@param ctx context.Context
//	@param data *larkim.P2MessageReceiveV1
//	@param args ...string
//	@return error
//	@author heyuhengmatt
//	@update 2024-08-06 08:27:33
func handleDebugGetID(ctx context.Context, data *larkim.P2MessageReceiveV1, metaData *xhandler.BaseMetaData, args ...string) (err error) {
	ctx, span := otel.Start(ctx)
	span.SetAttributes(otel.PreviewAttrs("event", larkcore.Prettify(data), 256)...)
	defer span.End()
	defer func() { otel.RecordError(span, err) }()

	if data.Event.Message.ParentId == nil {
		return errors.New("No parent Msg Quoted")
	}

	err = larkmsg.ReplyCardText(ctx, getIDText+*data.Event.Message.ParentId, *data.Event.Message.MessageId, "_getID", false)
	if err != nil {
		logs.L().Ctx(ctx).Error("ReplyMessage", zap.Error(err))
		return err
	}
	return nil
}

// handleDebugGetGroupID to be filled
//
//	@param ctx context.Context
//	@param data *larkim.P2MessageReceiveV1
//	@param args ...string
//	@return error
//	@author heyuhengmatt
//	@update 2024-08-06 08:27:29
func handleDebugGetGroupID(ctx context.Context, data *larkim.P2MessageReceiveV1, metaData *xhandler.BaseMetaData, args ...string) (err error) {
	ctx, span := otel.Start(ctx)
	span.SetAttributes(otel.PreviewAttrs("event", larkcore.Prettify(data), 256)...)
	defer span.End()
	defer func() { otel.RecordError(span, err) }()
	chatID := data.Event.Message.ChatId
	if chatID != nil {
		err := larkmsg.ReplyCardText(ctx, getGroupIDText+*chatID, *data.Event.Message.MessageId, "_getGroupID", false)
		if err != nil {
			logs.L().Ctx(ctx).Error("ReplyMessage", zap.Error(err))
			return err
		}
	}

	return nil
}

// handleDebugTryPanic to be filled
//
//	@param ctx context.Context
//	@param data *larkim.P2MessageReceiveV1
//	@param args ...string
//	@return error
//	@author heyuhengmatt
//	@update 2024-08-06 08:27:25
func handleDebugTryPanic(ctx context.Context, data *larkim.P2MessageReceiveV1, metaData *xhandler.BaseMetaData, args ...string) (err error) {
	ctx, span := otel.Start(ctx)
	span.SetAttributes(otel.PreviewAttrs("event", larkcore.Prettify(data), 256)...)
	defer span.End()
	defer func() { otel.RecordError(span, err) }()
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
	ctx, span := otel.Start(ctx)
	defer span.End()

	ins := query.Q.MsgTraceLog
	res, err := ins.WithContext(ctx).Where(
		query.Q.MsgTraceLog.MsgID.Eq(msgID),
	).Order(ins.CreatedAt.Desc()).Find()
	if err != nil {
		logs.L().Ctx(ctx).Error("AddTraceLog2DB", zap.Error(err))
		return nil, err
	}

	return func(yield func(*traceItem) bool) {
		for _, src := range res {
			if src.TraceID != "" {
				if !yield(&traceItem{src.TraceID, src.CreatedAt.Format(time.DateTime)}) {
					return
				}
			}
		}
	}, nil
}

// handleDebugTrace to be filled
//
//	@param ctx context.Context
//	@param data *larkim.P2MessageReceiveV1
//	@param args ...string
//	@return error
//	@author heyuhengmatt
//	@update 2024-08-06 08:27:23
func handleDebugTrace(ctx context.Context, data *larkim.P2MessageReceiveV1, metaData *xhandler.BaseMetaData, args ...string) (err error) {
	ctx, span := otel.Start(ctx)
	span.SetAttributes(otel.PreviewAttrs("event", larkcore.Prettify(data), 256)...)
	defer span.End()
	defer func() { otel.RecordError(span, err) }()
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
		logs.L().Ctx(ctx).Error("ReplyMessage", zap.Error(err))
		return err
	}
	return nil
}

// handleDebugRevert handleDebugTrace to be filled
//
//	@param ctx context.Context
//	@param data *larkim.P2MessageReceiveV1
//	@param args ...string
//	@return error
func handleDebugRevert(ctx context.Context, data *larkim.P2MessageReceiveV1, metaData *xhandler.BaseMetaData, args ...string) (err error) {
	ctx, span := otel.Start(ctx)
	span.SetAttributes(otel.PreviewAttrs("event", larkcore.Prettify(data), 256)...)
	defer span.End()
	defer func() { otel.RecordError(span, err) }()
	var res string = "撤回成功"
	defer func() { metaData.SetExtra("revert_result", res) }()

	if data.Event.Message.ThreadId != nil { // 话题模式，找到所有的traceID
		res = "话题模式的消息，所有的机器人发言都被撤回了"
		resp, err := lark_dal.Client().Im.Message.List(ctx, larkim.NewListMessageReqBuilder().ContainerIdType("thread").ContainerId(*data.Event.Message.ThreadId).Build())
		if err != nil {
			return err
		}
		currentBot := botidentity.Current()
		for _, msg := range resp.Data.Items {
			if currentBot.AppID != "" && *msg.Sender.Id == currentBot.AppID {
				resp, err := lark_dal.Client().Im.Message.Delete(ctx, larkim.NewDeleteMessageReqBuilder().MessageId(*msg.MessageId).Build())
				if err != nil {
					logs.L().Ctx(ctx).Error("DeleteMessage", zap.Error(err), zap.String("MessageID", *msg.MessageId))
					continue
				}
				if !resp.Success() {
					logs.L().Ctx(ctx).Error("DeleteMessage", zap.Error(errors.New(resp.Error())), zap.String("MessageID", *msg.MessageId))
				}
			}
		}
	} else if data.Event.Message.ParentId != nil {
		parID := data.Event.Message.ParentId

		currentBot := botidentity.Current()
		for parID != nil {
			resp := larkmsg.GetMsgFullByID(ctx, *parID)
			if !resp.Success() {
				logs.L().Ctx(ctx).Error("GetMsgFullByID", zap.Error(errors.New(resp.Error())), zap.String("MessageID", *parID))
				return errors.New("Failed to get message details")
			}
			msg := resp.Data.Items[0]
			parID = msg.ParentId
			if msg.Sender.Id == nil || currentBot.AppID == "" || *msg.Sender.Id != currentBot.AppID {
				res = "消息不是机器人发出的，不能撤回"
				logs.L().Ctx(ctx).Error("RevertMessage", zap.String("MessageID", *msg.MessageId), zap.String("SenderID", *msg.Sender.Id), zap.String("CurrentBotOpenID", currentBot.BotOpenID))
				continue
			}
			revertResp, err := lark_dal.Client().Im.Message.Delete(ctx, larkim.NewDeleteMessageReqBuilder().MessageId(*data.Event.Message.ParentId).Build())
			if err != nil {
				logs.L().Ctx(ctx).Error("DeleteMessage", zap.Error(err), zap.String("MessageID", *msg.MessageId))
				continue
			}
			if !revertResp.Success() {
				logs.L().Ctx(ctx).Error("DeleteMessage", zap.Error(errors.New(resp.Error())), zap.String("MessageID", *msg.MessageId))
				continue
			}
		}
	}
	return nil
}

func handleDebugRepeat(ctx context.Context, data *larkim.P2MessageReceiveV1, metaData *xhandler.BaseMetaData, args ...string) (err error) {
	ctx, span := otel.Start(ctx)
	span.SetAttributes(otel.PreviewAttrs("event", larkcore.Prettify(data), 256)...)
	defer span.End()
	defer func() { otel.RecordError(span, err) }()

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
		_, err = larkmsg.CreateMsgRawContentType(
			ctx,
			*msg.ChatId,
			*msg.MsgType,
			*msg.Body.Content,
			*msg.MessageId,
			"_debug_repeat",
		)
		if err != nil {
			if strings.Contains(err.Error(), "invalid image_key") {
				logs.L().Ctx(ctx).Error("repeatMessage", zap.Error(err))
				return nil
			}
			return err
		}
	}
	return nil
}

func handleDebugImage(ctx context.Context, data *larkim.P2MessageReceiveV1, metaData *xhandler.BaseMetaData, args ...string) (err error) {
	ctx, span := otel.Start(ctx)
	span.SetAttributes(otel.PreviewAttrs("event", larkcore.Prettify(data), 256)...)
	defer span.End()
	defer func() { otel.RecordError(span, err) }()
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

	dataSeq, err := ark_dal.
		New(*data.Event.Message.ChatId, currentOpenID(data, metaData), &data).
		Do(ctx, "", inputPrompt, urls...)
	if err != nil {
		return err
	}
	err = larkmsg.SendAndReplyStreamingCard(ctx, data.Event.Message, dataSeq, true)
	if err != nil {
		return err
	}
	return nil
}

func handleDebugConversation(ctx context.Context, data *larkim.P2MessageReceiveV1, metaData *xhandler.BaseMetaData, args ...string) (err error) {
	ctx, span := otel.Start(ctx)
	span.SetAttributes(otel.PreviewAttrs("event", larkcore.Prettify(data), 256)...)
	defer span.End()
	defer func() { otel.RecordError(span, err) }()

	msgs, err := larkmsg.GetAllParentMsg(ctx, data)
	if err != nil {
		return err
	}

	resp, err := opensearch.SearchData(ctx, appconfig.GetLarkMsgIndex(ctx, currentChatID(data, metaData), currentOpenID(data, metaData)),
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
				User:    &larktpl.User{UserID: msg.OpenID},
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
				commonutils.TransSlice(msgList, func(m *xmodel.MessageIndex) *larktpl.User { return &larktpl.User{UserID: m.OpenID} }),
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
		cardContent := larktpl.NewCardContentV2[larktpl.ChunkMetaData](ctx, tpl)
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

// handleDebugCard 卡片调试处理函数
//
//	@param ctx context.Context
//	@param data *larkim.P2MessageReceiveV1
//	@param metaData *xhandler.BaseMetaData
//	@param arg debugCardArgs
//	@return error
//	@author heyuhengmatt
//	@update 2026-03-12
func handleDebugCard(ctx context.Context, data *larkim.P2MessageReceiveV1, metaData *xhandler.BaseMetaData, arg debugCardArgs) (err error) {
	ctx, span := otel.Start(ctx)
	span.SetAttributes(otel.PreviewAttrs("event", larkcore.Prettify(arg), 256)...)
	defer span.End()
	defer func() { otel.RecordError(span, err) }()

	if strings.TrimSpace(string(arg.Spec)) == "" && strings.TrimSpace(arg.Template) == "" ||
		strings.TrimSpace(string(arg.Spec)) == "list" ||
		strings.TrimSpace(arg.Template) == "list" {
		var sb strings.Builder
		sb.WriteString("可用调试卡片入口：\n")
		sb.WriteString("\n内置 spec:\n")
		for _, spec := range carddebug.ListSpecs() {
			sb.WriteString(fmt.Sprintf("- `%s`: %s\n", spec.Name, spec.Description))
		}
		sb.WriteString("\n模板别名:\n")
		for _, tpl := range carddebug.ListTemplates() {
			sb.WriteString(fmt.Sprintf("- `%s`: `%s`\n", tpl.Name, tpl.ID))
		}
		sb.WriteString("\n示例:\n")
		sb.WriteString("`/debug card --spec=ratelimit.sample`\n")
		sb.WriteString("`/debug card --spec=config --chat_id=oc_abc123 --actor_open_id=ou_admin`\n")
		sb.WriteString("`/debug card --spec=schedule.task --id=20260312093000-debugA --chat_id=oc_abc123`\n")
		sb.WriteString("`/debug card --template=NormalCardReplyTemplate --vars_json='{\"content\":\"测试内容\"}' --to_open_id=ou_xxx`\n")

		return larkmsg.ReplyCardText(ctx, sb.String(), *data.Event.Message.MessageId, "_card_list", false)
	}

	target, err := carddebug.ResolveReceiveTarget(arg.ToChatID, arg.ToOpenID, currentChatID(data, metaData))
	if err != nil {
		return err
	}

	chatID := firstNonEmpty(arg.ChatID, currentChatID(data, metaData))
	actorOpenID := firstNonEmpty(arg.ActorOpenID, currentOpenID(data, metaData))
	targetOpenID := firstNonEmpty(arg.TargetOpenID, arg.ToOpenID)
	built, err := carddebug.Build(ctx, carddebug.BuildRequest{
		Spec:         strings.TrimSpace(string(arg.Spec)),
		Template:     strings.TrimSpace(arg.Template),
		VarsJSON:     strings.TrimSpace(arg.VarJSON),
		ChatID:       chatID,
		ID:           strings.TrimSpace(arg.ID),
		ActorOpenID:  actorOpenID,
		TargetOpenID: targetOpenID,
		Scope:        strings.TrimSpace(string(arg.Scope)),
	})
	if err != nil {
		return err
	}
	if err := carddebug.Send(ctx, target, built); err != nil {
		return fmt.Errorf("发送卡片失败：%w", err)
	}

	summary := fmt.Sprintf(
		"已发送 `%s`\n目标: `%s`\n上下文: chat=`%s` actor=`%s`",
		built.Label,
		target.String(),
		previewDebugID(chatID),
		previewDebugID(actorOpenID),
	)
	return larkmsg.ReplyCardText(ctx, summary, *data.Event.Message.MessageId, "_card_send_success", false)
}

func previewDebugID(id string) string {
	id = strings.TrimSpace(id)
	if len(id) <= 12 {
		return id
	}
	return id[:4] + "..." + id[len(id)-4:]
}

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
// 	metaData := xhandler.NewBaseMetaDataWithChatIDOpenID(ctx, meta.ChatID, meta.OpenID)
// 	if err := handleDebugRevert(ctx, meta.LarkData, metaData, argsSlice...); err != nil {
// 		return nil, err
// 	}
// 	return goption.Of(metaData.GetExtra("revert_result")).ValueOr("执行完成但没有结果"), nil
// }
