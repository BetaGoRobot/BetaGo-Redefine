package reaction

import (
	"context"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/lark_dal/larkmsg"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/xhandler"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
)

// Handler  消息处理器
var Handler = &xhandler.Processor[larkim.P2MessageReactionCreatedV1, xhandler.BaseMetaData]{}

type (
	OpBase = xhandler.OperatorBase[larkim.P2MessageReactionCreatedV1, xhandler.BaseMetaData]
	Op     = xhandler.Operator[larkim.P2MessageReactionCreatedV1, xhandler.BaseMetaData]
)

func larkDeferFunc(ctx context.Context, err error, event *larkim.P2MessageReactionCreatedV1, metaData *xhandler.BaseMetaData) {
	larkmsg.SendRecoveredMsg(ctx, err, *event.Event.MessageId)
}

func init() {
	Handler = Handler.
		OnPanic(larkDeferFunc).
		AddParallelStages(&FollowReactionOperator{}).
		AddParallelStages(&RecordReactionOperator{})
}
