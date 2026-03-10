package reaction

import (
	"context"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/lark_dal/larkmsg"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/xhandler"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
)

// Handler 反应处理器模板（保留用于向后兼容）
var Handler *xhandler.Processor[larkim.P2MessageReactionCreatedV1, xhandler.BaseMetaData]

type (
	OpBase = xhandler.OperatorBase[larkim.P2MessageReactionCreatedV1, xhandler.BaseMetaData]
	Op     = xhandler.Operator[larkim.P2MessageReactionCreatedV1, xhandler.BaseMetaData]
)

func NewReactionProcessor() *xhandler.Processor[larkim.P2MessageReactionCreatedV1, xhandler.BaseMetaData] {
	return (&xhandler.Processor[larkim.P2MessageReactionCreatedV1, xhandler.BaseMetaData]{}).
		OnPanic(func(ctx context.Context, err error, event *larkim.P2MessageReactionCreatedV1, metaData *xhandler.BaseMetaData) {
			larkmsg.SendRecoveredMsg(ctx, err, *event.Event.MessageId)
		}).
		AddAsync(&FollowReactionOperator{}).
		AddAsync(&RecordReactionOperator{})
}

func init() {
	Handler = NewReactionProcessor()
}
