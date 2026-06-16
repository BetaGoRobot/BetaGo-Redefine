package messages

import (
	"context"

	appconfig "github.com/BetaGoRobot/BetaGo-Redefine/internal/application/config"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/botidentity"
	larkchunking "github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/chunking"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/messages/ops"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/messages/recording"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/lark_dal/larkchat"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/lark_dal/larkmsg"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/lark_dal/larkuser"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/utils"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/xhandler"

	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
)

type MessageHandler struct {
	processor *xhandler.Processor[larkim.P2MessageReceiveV1, xhandler.BaseMetaData]
}

// Handler 消息处理入口。
var Handler *MessageHandler

// ConfigManager 全局配置管理器（新代码应该使用依赖注入）
var ConfigManager *appconfig.Manager

var (
	getChatName     = larkchat.GetChatName
	getUserNameByID = larkuser.GetUserNameCache
)

func NewMessageProcessor(cfgManager *appconfig.Manager) *MessageHandler {
	if cfgManager == nil {
		cfgManager = appconfig.GetManager()
	}
	handler := &MessageHandler{
		processor: newMessageProcessorBase(cfgManager).
			AddAsync(&ops.ReplyChatOperator{}).
			AddAsync(&ops.CommandOperator{}).
			AddAsync(&ops.ChatMsgOperator{}),
	}
	cfgManager.SetGetFeaturesFunc(func() []appconfig.Feature {
		return collectMessageFeatures(handler.processor)
	})
	return handler
}

func (h *MessageHandler) Run(ctx context.Context, event *larkim.P2MessageReceiveV1) {
	if h == nil {
		return
	}
	processor := h.processor
	if processor == nil {
		return
	}
	processor.NewExecution().WithCtx(ctx).WithData(event).Run()
}

func newMessageProcessorBase(cfgManager *appconfig.Manager) *xhandler.Processor[larkim.P2MessageReceiveV1, xhandler.BaseMetaData] {
	return (&xhandler.Processor[larkim.P2MessageReceiveV1, xhandler.BaseMetaData]{}).
		OnPanic(func(ctx context.Context, err error, event *larkim.P2MessageReceiveV1, metaData *xhandler.BaseMetaData) {
			larkmsg.SendRecoveredMsg(ctx, err, *event.Event.Message.MessageId)
		}).
		WithMetaDataProcess(metaInit).
		WithPreRun(func(p *xhandler.Processor[larkim.P2MessageReceiveV1, xhandler.BaseMetaData]) {
			go utils.AddTrace2DB(p, *p.Data().Event.Message.MessageId)
		}).
		WithDefer(recording.CollectMessage).
		WithDefer(func(ctx context.Context, event *larkim.P2MessageReceiveV1, meta *xhandler.BaseMetaData) {
			if !meta.IsCommandMarked() {
				if privateModeEnabled, err := larkmsg.IsPrivateModeEnabled(ctx, *event.Event.Message.ChatId); err != nil {
					return
				} else if privateModeEnabled {
					return
				}
				_ = larkchunking.SubmitMessage(ctx, &larkchunking.LarkMessageEvent{P2MessageReceiveV1: event})
			}
		}).
		WithFeatureChecker(cfgManager.FeatureCheckFunc()).
		WithStageFilter(newMessageStageFilter()).
		AddAsync(&ops.RecordMsgOperator{}).
		AddAsync(&ops.RepeatMsgOperator{}).
		AddAsync(&ops.ReactMsgOperator{}).
		AddAsync(&ops.WordReplyMsgOperator{})
}

func collectMessageFeatures(processors ...*xhandler.Processor[larkim.P2MessageReceiveV1, xhandler.BaseMetaData]) []appconfig.Feature {
	features := make([]appconfig.Feature, 0)
	seen := make(map[string]struct{})
	for _, processor := range processors {
		if processor == nil {
			continue
		}
		for _, fi := range processor.ListFeatures() {
			if _, ok := seen[fi.ID]; ok {
				continue
			}
			seen[fi.ID] = struct{}{}
			features = append(features, appconfig.Feature{
				Name:           fi.ID,
				Description:    fi.Description,
				Category:       "message",
				DefaultEnabled: fi.Default,
			})
		}
	}
	return features
}

func init() {
	ConfigManager = appconfig.NewManager()
	Handler = NewMessageProcessor(ConfigManager)
}

func metaInit(event *larkim.P2MessageReceiveV1) *xhandler.BaseMetaData {
	chatID := *event.Event.Message.ChatId
	isP2P := *event.Event.Message.ChatType == "p2p"
	openID := botidentity.MessageSenderOpenID(event)
	chatName := resolveChatName(context.Background(), chatID, isP2P, openID)
	return &xhandler.BaseMetaData{
		ChatID:   chatID,
		IsP2P:    isP2P,
		ChatName: chatName,
		OpenID:   openID,
	}
}

func resolveChatName(ctx context.Context, chatID string, isP2P bool, openID string) string {
	if isP2P {
		if openID != "" {
			if name, err := getUserNameByID(ctx, chatID, openID); err == nil && name != "" {
				return "[单聊]" + name
			}
		}
		return "p2p"
	}
	if name := getChatName(ctx, chatID); name != "" {
		return name
	}
	return "unknown"
}
