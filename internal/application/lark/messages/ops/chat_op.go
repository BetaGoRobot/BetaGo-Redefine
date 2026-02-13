package ops

import (
	"context"

	"github.com/pkg/errors"
	"go.uber.org/zap"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/command"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/handlers"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/db/query"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/otel"

	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/logs"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/xhandler"

	"github.com/BetaGoRobot/BetaGo/consts"
	"github.com/BetaGoRobot/BetaGo/utility"
	"github.com/BetaGoRobot/BetaGo/utility/larkutils"
	"github.com/BetaGoRobot/go_utils/reflecting"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
)

var _ Op = &ChatMsgOperator{}

// ChatMsgOperator  RepeatMsg Op
//
//	@author heyuhengmatt
//	@update 2024-07-17 01:35:51
type ChatMsgOperator struct {
	OpBase
}

func (r *ChatMsgOperator) Name() string {
	return "ChatMsgOperator"
}

// PreRun Repeat
//
//	@receiver r *ImitateMsgOperator
//	@param ctx context.Context
//	@param event *larkim.P2MessageReceiveV1
//	@return err error
//	@author heyuhengmatt
//	@update 2024-07-17 01:35:35
func (r *ChatMsgOperator) PreRun(ctx context.Context, event *larkim.P2MessageReceiveV1, meta *xhandler.BaseMetaData) (err error) {
	ctx, span := otel.T().Start(ctx, reflecting.GetCurrentFunc())
	defer span.End()
	defer func() { span.RecordError(err) }()
	// // 先判断群聊的功能启用情况
	// if !larkutils.CheckFunctionEnabling(*event.Event.Message.ChatId, consts.LarkFunctionRandomRepeat) {
	// 	return errors.Wrap(consts.ErrStageSkip, "ImitateMsgOperator: Not enabled")
	// }
	if command.LarkRootCommand.IsCommand(ctx, larkutils.PreGetTextMsg(ctx, event)) {
		return errors.Wrap(consts.ErrStageSkip, r.Name()+" Not Mentioned")
	}
	return
}

// Run Repeat
//
//	@receiver r *ImitateMsgOperator
//	@param ctx context.Context
//	@param event *larkim.P2MessageReceiveV1
//	@return err error
//	@author heyuhengmatt
//	@update 2024-07-17 01:35:41
func (r *ChatMsgOperator) Run(ctx context.Context, event *larkim.P2MessageReceiveV1, meta *xhandler.BaseMetaData) (err error) {
	ctx, span := otel.T().Start(ctx, reflecting.GetCurrentFunc())
	defer span.End()
	defer func() { span.RecordError(err) }()

	// 开始摇骰子, 默认概率10%
	realRate := utility.MustAtoI(utility.GetEnvWithDefault("IMITATE_DEFAULT_RATE", "10"))
	// 群聊定制化
	ins := query.Q.ImitateRateCustom
	config, err := ins.WithContext(ctx).Select(ins.GuildID.Eq(*event.Event.Message.ChatId)).First()
	if err != nil {
		logs.L().Ctx(ctx).Error("get imitate config from db failed", zap.Error(err))
		return
	}

	if config != nil {
		realRate = int(config.Rate)
	}

	if utility.Probability(float64(realRate) / 100) {
		// sendMsg
		err := handlers.ChatHandler("chat")(ctx, event, meta)
		if err != nil {
			return err
		}
	}
	return
}
