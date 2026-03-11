package messages

import (
	"context"

	appconfig "github.com/BetaGoRobot/BetaGo-Redefine/internal/application/config"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/botidentity"
	larkchunking "github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/chunking"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/messages/ops"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/messages/recording"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/lark_dal/larkmsg"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/utils"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/xhandler"

	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
)

// Handler  消息处理器（保留用于向后兼容）
var Handler *xhandler.Processor[larkim.P2MessageReceiveV1, xhandler.BaseMetaData]

// ConfigManager 全局配置管理器（新代码应该使用依赖注入）
var ConfigManager *appconfig.Manager

// NewMessageProcessor 创建新的消息处理器（推荐使用）
func NewMessageProcessor(cfgManager *appconfig.Manager) *xhandler.Processor[larkim.P2MessageReceiveV1, xhandler.BaseMetaData] {
	processor := &xhandler.Processor[larkim.P2MessageReceiveV1, xhandler.BaseMetaData]{}

	processor = processor.
		OnPanic(func(ctx context.Context, err error, event *larkim.P2MessageReceiveV1, metaData *xhandler.BaseMetaData) {
			larkmsg.SendRecoveredMsg(ctx, err, *event.Event.Message.MessageId)
		}).
		WithMetaDataProcess(metaInit).
		WithPreRun(func(p *xhandler.Processor[larkim.P2MessageReceiveV1, xhandler.BaseMetaData]) {
			utils.AddTrace2DB(p, *p.Data().Event.Message.MessageId)
		}).
		WithDefer(recording.CollectMessage).
		WithDefer(func(ctx context.Context, event *larkim.P2MessageReceiveV1, meta *xhandler.BaseMetaData) {
			if !meta.IsCommandMarked() { // 过滤Command
				if privateModeEnabled, err := larkmsg.IsPrivateModeEnabled(ctx, *event.Event.Message.ChatId); err != nil {
					return
				} else if privateModeEnabled {
					return
				}
				larkchunking.M.SubmitMessage(ctx, &larkchunking.LarkMessageEvent{P2MessageReceiveV1: event})
			}
		}).
		WithFeatureChecker(cfgManager.FeatureCheckFunc()).
		AddAsync(&ops.RecordMsgOperator{}).
		AddAsync(&ops.RepeatMsgOperator{}).
		AddAsync(&ops.ReactMsgOperator{}).
		AddAsync(&ops.WordReplyMsgOperator{}).
		AddAsync(&ops.ReplyChatOperator{}).
		AddAsync(&ops.CommandOperator{}).
		AddAsync(&ops.ChatMsgOperator{})

	// 设置获取功能列表的回调
	cfgManager.SetGetFeaturesFunc(func() []appconfig.Feature {
		features := make([]appconfig.Feature, 0)
		for _, fi := range processor.ListFeatures() {
			features = append(features, appconfig.Feature{
				Name:           fi.ID,
				Description:    fi.Description,
				Category:       "message",
				DefaultEnabled: fi.Default,
			})
		}
		return features
	})

	return processor
}

func init() {
	// 向后兼容：初始化全局变量
	ConfigManager = appconfig.NewManager()
	Handler = NewMessageProcessor(ConfigManager)
}

func metaInit(event *larkim.P2MessageReceiveV1) *xhandler.BaseMetaData {
	meta := &xhandler.BaseMetaData{
		ChatID: *event.Event.Message.ChatId,
		IsP2P:  *event.Event.Message.ChatType == "p2p",
		OpenID: botidentity.MessageSenderOpenID(event),
	}
	return meta
}
