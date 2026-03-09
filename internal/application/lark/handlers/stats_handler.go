package handlers

import (
	"context"
	"errors"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/db/query"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/otel"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/xcommand"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/xhandler"
	"github.com/BetaGoRobot/go_utils/reflecting"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
	"gorm.io/gorm"
)

type StatsGetArgs struct{}

type statsGetHandler struct{}

var StatsGet statsGetHandler

func (statsGetHandler) ParseCLI(args []string) (StatsGetArgs, error) {
	return StatsGetArgs{}, nil
}

func (statsGetHandler) Handle(ctx context.Context, data *larkim.P2MessageReceiveV1, metaData *xhandler.BaseMetaData, arg StatsGetArgs) (err error) {
	ctx, span := otel.T().Start(ctx, reflecting.GetCurrentFunc())
	defer span.End()
	defer func() { span.RecordError(err) }()
	ins := query.Q.InteractionStat
	resList, err := ins.WithContext(ctx).Where(ins.GuildID.Eq(metaData.ChatID)).Find()
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return err
	}

	for _, res := range resList {
		_ = res
	}
	return nil
}

var _ xcommand.CLIArgHandler[*larkim.P2MessageReceiveV1, StatsGetArgs] = statsGetHandler{}
