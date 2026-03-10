package utils

import (
	"context"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/db/model"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/db/query"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/otel"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/logs"
	"github.com/BetaGoRobot/go_utils/reflecting"
	"go.uber.org/zap"
)

func AddTrace2DB(ctx context.Context, msgID string) {
	ctx, span := otel.T().Start(ctx, reflecting.GetCurrentFunc())
	defer span.End()

	logs.L().Ctx(ctx).Info("AddTraceLog2DB",
		zap.String("msgID", msgID),
	)
	ins := query.Q.MsgTraceLog
	err := ins.WithContext(ctx).Create(&model.MsgTraceLog{
		MsgID:   msgID,
		TraceID: span.SpanContext().TraceID().String(),
	})
	if err != nil {
		logs.L().Ctx(ctx).Error("AddTraceLog2DB", zap.Error(err))
	}
}
