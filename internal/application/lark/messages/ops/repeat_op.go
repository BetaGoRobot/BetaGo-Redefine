package ops

import (
	"context"
	"sort"
	"strings"

	"github.com/pkg/errors"
	"go.uber.org/zap"
	"gorm.io/gorm"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/command"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/handlers"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/config"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/db/query"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/lark_dal"
	redis_dal "github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/redis"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/lark_dal/larkmsg"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/otel"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/logs"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/utils"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/xerror"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/xhandler"
	"github.com/BetaGoRobot/go_utils/reflecting"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
)

var _ Op = &RepeatMsgOperator{}

// RepeatMsgOperator  RepeatMsg Op
//
//	@author heyuhengmatt
//	@update 2024-07-17 01:35:51
type RepeatMsgOperator struct {
	OpBase
}

func (r *RepeatMsgOperator) Name() string {
	return "RepeatMsgOperator"
}

// PreRun Repeat
//
//	@receiver r *RepeatMsgOperator
//	@param ctx context.Context
//	@param event *larkim.P2MessageReceiveV1
//	@return err error
//	@author heyuhengmatt
//	@update 2024-07-17 01:35:35
func (r *RepeatMsgOperator) PreRun(ctx context.Context, event *larkim.P2MessageReceiveV1, meta *xhandler.BaseMetaData) (err error) {
	ctx, span := otel.T().Start(ctx, reflecting.GetCurrentFunc())
	defer span.End()
	defer func() { span.RecordError(err) }()

	if command.LarkRootCommand.IsCommand(ctx, larkmsg.PreGetTextMsg(ctx, event).GetText()) {
		return errors.Wrap(xerror.ErrStageSkip, r.Name()+" Not Mentioned")
	}
	if ext, err := redis_dal.GetRedisClient().
		Exists(ctx, handlers.MuteRedisKeyPrefix+*event.Event.Message.ChatId).Result(); err != nil {
		return err
	} else if ext != 0 {
		return errors.Wrap(xerror.ErrStageSkip, "RepeatMsgOperator: Muted")
	}
	return
}

// Run Repeat
//
//	@receiver r *RepeatMsgOperator
//	@param ctx context.Context
//	@param event *larkim.P2MessageReceiveV1
//	@return err error
//	@author heyuhengmatt
//	@update 2024-07-17 01:35:41
func (r *RepeatMsgOperator) Run(ctx context.Context, event *larkim.P2MessageReceiveV1, meta *xhandler.BaseMetaData) (err error) {
	ctx, span := otel.T().Start(ctx, reflecting.GetCurrentFunc())
	defer span.End()
	defer func() { span.RecordError(err) }()
	defer func() { span.RecordError(err) }()

	// Repeat
	msg := larkmsg.PreGetTextMsg(ctx, event).GetText()

	// 开始摇骰子, 默认概率10%
	realRate := config.Get().RateConfig.RepeatDefaultRate
	// 群聊定制化
	ins := query.Q.RepeatWordsRateCustom
	config, err := ins.WithContext(ctx).Where(
		query.RepeatWordsRateCustom.GuildID.Eq(*event.Event.Message.ChatId),
		query.RepeatWordsRateCustom.Word.Eq(msg),
	).First()
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return err
	}
	if config != nil {
		realRate = int(config.Rate)
	} else {
		ins := query.Q.RepeatWordsRate
		config, err := ins.WithContext(ctx).Where(
			query.RepeatWordsRate.Word.Eq(msg),
		).First()
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		if config != nil {
			realRate = int(config.Rate)
		}
	}

	if utils.Prob(float64(realRate) / 100) {
		msgType := strings.ToLower(*event.Event.Message.MessageType)
		if msgType == "text" {
			m, err := utils.JSON2Map(*event.Event.Message.Content)
			if err != nil {
				return err
			}
			for _, mention := range event.Event.Message.Mentions {
				m["text"] = strings.ReplaceAll(m["text"].(string), *mention.Key, larkmsg.AtUser(*mention.Id.OpenId, *mention.Name))
			}
			err = larkmsg.CreateMsgTextRaw(
				ctx,
				utils.MustMarshalString(m),
				*event.Event.Message.MessageId,
				*event.Event.Message.ChatId,
			)
			if err != nil {
				logs.L().Ctx(ctx).Error("repeatMessage error", zap.Error(err), zap.String("TraceID", span.SpanContext().TraceID().String()))
			}
		} else {
			repeatReq := larkim.NewCreateMessageReqBuilder().
				Body(
					larkim.NewCreateMessageReqBodyBuilder().
						Content(*event.Event.Message.Content).
						ReceiveId(*event.Event.Message.ChatId).
						MsgType(*event.Event.Message.MessageType).
						Build(),
				).
				ReceiveIdType(larkim.ReceiveIdTypeChatId).
				Build()
			resp, err := lark_dal.Client().Im.V1.Message.Create(ctx, repeatReq)
			if err != nil {
				return err
			}
			if !resp.Success() {
				if strings.Contains(resp.Error(), "invalid image_key") {
					logs.L().Ctx(ctx).Error("repeatMessage error", zap.Error(err), zap.String("TraceID", span.SpanContext().TraceID().String()))
					return nil
				}
				return errors.New(resp.Error())
			}
			go larkmsg.RecordMessage2Opensearch(ctx, resp)
		}
	}
	return nil
}

func RebuildAtMsg(input string, substrings []string) []string {
	result := []string{}
	start := 0

	// Keep track of the positions to split
	splitPositions := []int{}

	// Iterate through the input to find all occurrences of substrings
	for _, sub := range substrings {
		start = 0
		for {
			pos := strings.Index(input[start:], sub)
			if pos == -1 {
				break
			}
			actualPos := start + pos
			splitPositions = append(splitPositions, actualPos, actualPos+len(sub))
			start = actualPos + len(sub)
		}
	}

	// Sort the positions to split
	sort.Slice(splitPositions, func(i, j int) bool { return splitPositions[i] < splitPositions[j] })

	if len(splitPositions) > 0 {
		// Remove duplicate positions
		uniquePositions := []int{}
		for i, pos := range splitPositions {
			if i == 0 || pos != splitPositions[i-1] {
				uniquePositions = append(uniquePositions, pos)
			}
		}

		// Add start and end of the string to the positions if not already present
		if uniquePositions[0] != 0 {
			uniquePositions = append([]int{0}, uniquePositions...)
		}
		if uniquePositions[len(uniquePositions)-1] != len(input) {
			uniquePositions = append(uniquePositions, len(input))
		}

		// Extract substrings based on split positions
		for i := 0; i < len(uniquePositions)-1; i++ {
			result = append(result, input[uniquePositions[i]:uniquePositions[i+1]])
		}
	} else {
		result = append(result, input)
	}
	return result
}
