package ops

import (
	"context"
	"strconv"
	"strings"
	"time"

	appconfig "github.com/BetaGoRobot/BetaGo-Redefine/internal/application/config"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/history"
	"github.com/pkg/errors"
	"go.uber.org/zap"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/intent"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/otel"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/logs"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/xerror"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/xhandler"
	"github.com/defensestation/osquery"

	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
)

var (
	_ Op                                                                 = &IntentRecognizeOperator{}
	_ xhandler.Fetcher[larkim.P2MessageReceiveV1, xhandler.BaseMetaData] = &IntentRecognizeOperator{}
)

// IntentRecognizeOperator 意图识别 Operator（同时也是 Fetcher）
//
//	@author heyuhengmatt
//	@update 2025-03-02
type IntentRecognizeOperator struct {
	OpBase
	configAccessor      func(context.Context, *larkim.P2MessageReceiveV1, *xhandler.BaseMetaData) intentRecognizeConfig
	analyzer            func(context.Context, string, []string) (*intent.IntentAnalysis, error)
	recentContextLoader func(context.Context, *larkim.P2MessageReceiveV1, int) ([]string, error)
}

type intentRecognizeConfig interface {
	IntentRecognitionEnabled() bool
	ChatMode() appconfig.ChatMode
}

// 全局单例 IntentRecognizeFetcher
var IntentRecognizeFetcher = &IntentRecognizeOperator{}

const intentRecentContextLimit = 3

func (r *IntentRecognizeOperator) Name() string {
	return "IntentRecognizeOperator"
}

// FeatureInfo 返回功能信息
func (r *IntentRecognizeOperator) FeatureInfo() *xhandler.FeatureInfo {
	return &xhandler.FeatureInfo{
		ID:          "intent_recognize",
		Name:        "意图识别功能",
		Description: "使用AI识别用户消息意图",
		Default:     true,
	}
}

// Fetch 实现 Fetcher 接口，执行意图识别
func (r *IntentRecognizeOperator) Fetch(ctx context.Context, event *larkim.P2MessageReceiveV1, meta *xhandler.BaseMetaData) (err error) {
	ctx, span := otel.Start(ctx)
	defer span.End()
	defer otel.RecordErrorPtr(span, &err)

	accessor := r.intentConfigAccessor(ctx, event, meta)
	_ = accessor

	// 检查是否启用了意图识别（TOML 配置的总开关）
	if !accessor.IntentRecognitionEnabled() {
		return errors.Wrap(xerror.ErrStageSkip, r.Name()+" intent recognition disabled")
	}

	text := strings.TrimSpace(messageText(ctx, event))
	if text == "" {
		logs.L().Ctx(ctx).Warn("empty message, skip intent recognition")
		return nil
	}

	recentLines, recentErr := r.loadRecentContext(ctx, event, intentRecentContextLimit)
	if recentErr != nil {
		logs.L().Ctx(ctx).Warn("load recent context for intent recognition failed", zap.Error(recentErr))
	}

	// 调用意图识别
	analysis, err := r.analyzeIntent(ctx, text, recentLines)
	if err != nil {
		logs.L().Ctx(ctx).Error("intent analysis failed", zap.Error(err))
		return nil
	}

	analysis.Sanitize()
	if meta != nil {
		meta.SetIntentAnalysis(analysis)
	}
	logs.L().Ctx(ctx).Info("intent recognition completed",
		zap.String("intent_type", string(analysis.IntentType)),
		zap.Bool("need_reply", analysis.NeedReply),
		zap.Int("confidence", analysis.ReplyConfidence),
	)

	return nil
}

func (r *IntentRecognizeOperator) intentConfigAccessor(ctx context.Context, event *larkim.P2MessageReceiveV1, meta *xhandler.BaseMetaData) intentRecognizeConfig {
	if r != nil && r.configAccessor != nil {
		return r.configAccessor(ctx, event, meta)
	}
	return messageConfigAccessor(ctx, event, meta)
}

func (r *IntentRecognizeOperator) analyzeIntent(ctx context.Context, text string, recentLines []string) (*intent.IntentAnalysis, error) {
	if r != nil && r.analyzer != nil {
		return r.analyzer(ctx, text, recentLines)
	}
	return intent.AnalyzeMessage(ctx, text, recentLines)
}

func (r *IntentRecognizeOperator) loadRecentContext(
	ctx context.Context,
	event *larkim.P2MessageReceiveV1,
	size int,
) ([]string, error) {
	if size <= 0 {
		return nil, nil
	}
	if r != nil && r.recentContextLoader != nil {
		return r.recentContextLoader(ctx, event, size)
	}
	return loadIntentRecentContext(ctx, event, size)
}

func loadIntentRecentContext(ctx context.Context, event *larkim.P2MessageReceiveV1, size int) ([]string, error) {
	chatID := messageChatID(event, nil)
	if chatID == "" {
		return nil, nil
	}

	cutoff := time.Now()
	if event != nil && event.Event != nil && event.Event.Message != nil && event.Event.Message.CreateTime != nil {
		if createdAtMillis, err := strconv.ParseInt(*event.Event.Message.CreateTime, 10, 64); err == nil {
			cutoff = time.UnixMilli(createdAtMillis)
		}
	}

	messageList, err := history.New(ctx).
		Query(osquery.Bool().Must(
			osquery.Term("chat_id", chatID),
			osquery.Range("create_time_v2").Lt(cutoff),
		)).
		Source("raw_message", "mentions", "create_time", "create_time_v2", "user_id", "chat_id", "user_name", "message_type").
		Size(uint64(size*3)).
		Sort("create_time_v2", "desc").
		GetMsg()
	if err != nil {
		return nil, err
	}

	lines := messageList.ToLines()
	if len(lines) > size {
		lines = lines[len(lines)-size:]
	}
	return lines, nil
}

// PreRun 意图识别前置检查（作为 Operator 时使用）
func (r *IntentRecognizeOperator) PreRun(ctx context.Context, event *larkim.P2MessageReceiveV1, meta *xhandler.BaseMetaData) (err error) {
	// 作为 Operator 时，直接跳过，因为已经作为 Fetcher 执行过了
	return errors.Wrap(xerror.ErrStageSkip, r.Name()+" already executed as fetcher")
}

// Run 执行意图识别（作为 Operator 时使用）
func (r *IntentRecognizeOperator) Run(ctx context.Context, event *larkim.P2MessageReceiveV1, meta *xhandler.BaseMetaData) (err error) {
	return nil
}

// PostRun 意图识别后置处理（作为 Operator 时使用）
func (r *IntentRecognizeOperator) PostRun(ctx context.Context, event *larkim.P2MessageReceiveV1, meta *xhandler.BaseMetaData) (err error) {
	return nil
}

// GetIntentAnalysisFromMeta 从 meta 中获取意图分析结果
func GetIntentAnalysisFromMeta(meta *xhandler.BaseMetaData) (*intent.IntentAnalysis, bool) {
	if meta == nil {
		return nil, false
	}
	return meta.GetIntentAnalysis()
}
