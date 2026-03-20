package agentruntime

import (
	"context"
	"errors"
	"fmt"
	"html/template"
	"iter"
	"strings"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/botidentity"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/history"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/mention"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/ark_dal"
	arktools "github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/ark_dal/tools"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/db/model"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/db/query"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/lark_dal/larkmsg"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/lark_dal/larkuser"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/otel"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/xmodel"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/logs"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/utils"
	commonutils "github.com/BetaGoRobot/go_utils/common_utils"
	jsonrepair "github.com/RealAlexandreAI/json-repair"
	"github.com/bytedance/sonic"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
	"github.com/tmc/langchaingo/schema"
	"go.uber.org/zap"
	"gorm.io/gorm"
)

type InitialChatGenerationRequest struct {
	Event   *larkim.P2MessageReceiveV1
	ModelID string
	Size    int
	Files   []string
	Input   []string
	Tools   *arktools.Impl[larkim.P2MessageReceiveV1]
}

type InitialChatExecutionPlan struct {
	Event       *larkim.P2MessageReceiveV1
	ModelID     string
	ChatID      string
	OpenID      string
	Prompt      string
	UserInput   string
	Files       []string
	Tools       *arktools.Impl[larkim.P2MessageReceiveV1]
	MessageList history.OpensearchMsgLogList
}

var (
	initialChatPromptTemplateLoader = defaultInitialChatPromptTemplateLoader
	initialChatUserNameLoader       = defaultInitialChatUserNameLoader
)

func GenerateInitialChatSeq(ctx context.Context, req InitialChatGenerationRequest) (res iter.Seq[*ark_dal.ModelStreamRespReasoning], err error) {
	ctx, span := otel.Start(ctx)
	defer span.End()
	defer func() { otel.RecordError(span, err) }()

	plan, err := BuildInitialChatExecutionPlan(ctx, req)
	if err != nil {
		return nil, err
	}

	stream, err := ExecuteInitialChatExecutionPlan(ctx, plan)
	if err != nil {
		return nil, err
	}
	return FinalizeInitialChatStream(ctx, plan, stream), nil
}

func BuildInitialChatExecutionPlan(ctx context.Context, req InitialChatGenerationRequest) (plan InitialChatExecutionPlan, err error) {
	ctx, span := otel.Start(ctx)
	defer span.End()
	defer func() { otel.RecordError(span, err) }()

	if req.Event == nil || req.Event.Event == nil || req.Event.Event.Message == nil {
		return InitialChatExecutionPlan{}, errors.New("chat event is required")
	}
	if strings.TrimSpace(req.ModelID) == "" {
		return InitialChatExecutionPlan{}, errors.New("model id is required")
	}
	if req.Tools == nil {
		return InitialChatExecutionPlan{}, errors.New("chat tools are required")
	}
	if req.Size <= 0 {
		req.Size = 20
	}

	chatID := generationChatID(req.Event)
	openID := generationOpenID(req.Event)
	replyScope, replyScoped, err := agenticChatReplyScopeLoader(ctx, req.Event)
	if err != nil {
		logs.L().Ctx(ctx).Warn("standard reply-scoped context lookup failed", zap.Error(err))
	}

	var messageList history.OpensearchMsgLogList
	if replyScoped && len(replyScope.MessageList) > 0 {
		messageList = replyScope.MessageList
	} else {
		messageList, err = agenticChatHistoryLoader(ctx, chatID, req.Size*3)
		if err != nil {
			return InitialChatExecutionPlan{}, err
		}
		replyScoped = false
		replyScope = agenticReplyScopeContext{}
	}

	promptTemplate, err := initialChatPromptTemplateLoader(ctx)
	if err != nil {
		return InitialChatExecutionPlan{}, err
	}
	fullTpl := xmodel.PromptTemplateArg{
		PromptTemplateArg: promptTemplate,
		CurrentTimeStamp:  time.Now().In(utils.UTC8Loc()).Format(time.DateTime),
	}
	tp, err := template.New("prompt").Parse(promptTemplate.TemplateStr)
	if err != nil {
		return InitialChatExecutionPlan{}, err
	}
	userName, err := initialChatUserNameLoader(ctx, chatID, openID)
	if err != nil {
		return InitialChatExecutionPlan{}, err
	}
	createTime := utils.EpoMil2DateStr(*req.Event.Event.Message.CreateTime)
	fullTpl.UserInput = []string{fmt.Sprintf("[%s](%s) <%s>: %s", createTime, openID, userName, larkmsg.PreGetTextMsg(ctx, req.Event).GetText())}
	fullTpl.HistoryRecords = messageList.ToLines()
	if !replyScoped && len(fullTpl.HistoryRecords) > req.Size {
		fullTpl.HistoryRecords = fullTpl.HistoryRecords[len(fullTpl.HistoryRecords)-req.Size:]
	}

	recallQuery := strings.TrimSpace(replyScope.RecallQuery)
	if recallQuery == "" && req.Event.Event.Message.Content != nil {
		recallQuery = strings.TrimSpace(*req.Event.Event.Message.Content)
	}
	if replyScoped {
		currentText := strings.TrimSpace(larkmsg.PreGetTextMsg(ctx, req.Event).GetText())
		if currentText != "" && !strings.Contains(recallQuery, currentText) {
			recallQuery = strings.TrimSpace(recallQuery + "\n" + currentText)
		}
	}
	recallTopK := 10
	if replyScoped {
		recallTopK = 6
	}
	docs, err := agenticChatRecallDocs(ctx, chatID, recallQuery, recallTopK)
	if err != nil {
		logs.L().Ctx(ctx).Error("RecallDocs err", zap.Error(err))
	}
	docContext := commonutils.TransSlice(docs, func(doc schema.Document) string {
		if doc.Metadata == nil {
			doc.Metadata = map[string]any{}
		}
		createTime, _ := doc.Metadata["create_time"].(string)
		openID, _ := doc.Metadata["user_id"].(string)
		userName, _ := doc.Metadata["user_name"].(string)
		return fmt.Sprintf("[%s](%s) <%s>: %s", createTime, openID, userName, doc.PageContent)
	})
	fullTpl.Context = append(trimNonEmptyLines(replyScope.ContextLines), docContext...)
	fullTpl.Topics = make([]string, 0)
	chunkIndex := strings.TrimSpace(agenticChatChunkIndexResolver(ctx, chatID, openID))
	for _, doc := range docs {
		if chunkIndex == "" {
			break
		}
		msgID, ok := doc.Metadata["msg_id"]
		if !ok {
			continue
		}
		summary, searchErr := agenticChatTopicSummaryLookup(ctx, chunkIndex, fmt.Sprint(msgID))
		if searchErr != nil {
			return InitialChatExecutionPlan{}, searchErr
		}
		if strings.TrimSpace(summary) != "" {
			fullTpl.Topics = append(fullTpl.Topics, summary)
		}
	}
	fullTpl.Topics = utils.Dedup(fullTpl.Topics)
	b := &strings.Builder{}
	if err := tp.Execute(b, fullTpl); err != nil {
		return InitialChatExecutionPlan{}, err
	}

	return InitialChatExecutionPlan{
		Event:       req.Event,
		ModelID:     strings.TrimSpace(req.ModelID),
		ChatID:      chatID,
		OpenID:      openID,
		Prompt:      b.String(),
		UserInput:   strings.Join(fullTpl.UserInput, "\n"),
		Files:       append([]string(nil), req.Files...),
		Tools:       req.Tools,
		MessageList: messageList,
	}, nil
}

func defaultInitialChatPromptTemplateLoader(ctx context.Context) (*model.PromptTemplateArg, error) {
	ins := query.Q.PromptTemplateArg
	tpls, err := ins.WithContext(ctx).Where(ins.PromptID.Eq(5)).Find()
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	}
	if len(tpls) == 0 {
		return nil, errors.New("prompt template not found")
	}
	return tpls[0], nil
}

func defaultInitialChatUserNameLoader(ctx context.Context, chatID, openID string) (string, error) {
	userInfo, err := larkuser.GetUserInfoCache(ctx, chatID, openID)
	if err != nil {
		return "", err
	}
	if userInfo == nil || userInfo.Name == nil || strings.TrimSpace(*userInfo.Name) == "" {
		return "NULL", nil
	}
	return *userInfo.Name, nil
}

func ExecuteInitialChatExecutionPlan(ctx context.Context, plan InitialChatExecutionPlan) (iter.Seq[*ark_dal.ModelStreamRespReasoning], error) {
	turn, err := ExecuteInitialChatTurn(ctx, InitialChatTurnRequest{
		Plan: plan,
	})
	if err != nil {
		return nil, err
	}
	return turn.Stream, nil
}

func FinalizeInitialChatStream(
	ctx context.Context,
	plan InitialChatExecutionPlan,
	stream iter.Seq[*ark_dal.ModelStreamRespReasoning],
) iter.Seq[*ark_dal.ModelStreamRespReasoning] {
	return finalizeGeneratedChatStream(ctx, plan.ChatID, plan.MessageList, stream)
}

func finalizeGeneratedChatStream(
	ctx context.Context,
	chatID string,
	messageList history.OpensearchMsgLogList,
	stream iter.Seq[*ark_dal.ModelStreamRespReasoning],
) iter.Seq[*ark_dal.ModelStreamRespReasoning] {
	return func(yield func(*ark_dal.ModelStreamRespReasoning) bool) {
		contentBuilder := &strings.Builder{}

		lastData := &ark_dal.ModelStreamRespReasoning{}
		for data := range stream {
			lastData = data
			contentBuilder.WriteString(data.Content)

			if !yield(data) {
				return
			}
		}

		fullContent := contentBuilder.String()
		parseErr := sonic.UnmarshalString(fullContent, &lastData.ContentStruct)
		if parseErr != nil {
			fullContent, parseErr = jsonrepair.RepairJSON(fullContent)
			if parseErr != nil {
				return
			}
			parseErr = sonic.UnmarshalString(fullContent, &lastData.ContentStruct)
			if parseErr != nil {
				return
			}
		}
		if normalizedReply, normalizeErr := mention.NormalizeReplyText(ctx, chatID, messageList, lastData.ContentStruct.Reply); normalizeErr == nil {
			lastData.ContentStruct.Reply = normalizedReply
		}
		if !yield(lastData) {
			return
		}
	}
}

func generationChatID(event *larkim.P2MessageReceiveV1) string {
	if event == nil || event.Event == nil || event.Event.Message == nil || event.Event.Message.ChatId == nil {
		return ""
	}
	return *event.Event.Message.ChatId
}

func generationOpenID(event *larkim.P2MessageReceiveV1) string {
	return botidentity.MessageSenderOpenID(event)
}
