package handlers

import (
	"context"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/botidentity"
	arktools "github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/ark_dal/tools"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/otel"
	redis "github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/redis"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/utils"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/xcommand"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/xhandler"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
	"github.com/pkg/errors"
)

const (
	MuteRedisKeyPrefix = "betago:mute"
)

func MuteRedisKey(chatID string) string {
	return botidentity.Current().NamespaceKey(MuteRedisKeyPrefix, chatID)
}

type MuteArgs struct {
	Time   string `json:"time" cli:"t"`
	Cancel bool   `json:"cancel" cli:"cancel,flag"`
}

type muteHandler struct{}

var Mute muteHandler

func (muteHandler) ParseCLI(args []string) (MuteArgs, error) {
	argMap, _ := parseArgs(args...)
	_, cancel := argMap["cancel"]
	return MuteArgs{
		Time:   argMap["t"],
		Cancel: cancel,
	}, nil
}

func (muteHandler) ParseTool(raw string) (MuteArgs, error) {
	parsed := MuteArgs{}
	if err := utils.UnmarshalStringPre(raw, &parsed); err != nil {
		return MuteArgs{}, err
	}
	return parsed, nil
}

func (muteHandler) ToolSpec() xcommand.ToolSpec {
	return xcommand.ToolSpec{
		Name: "mute_robot",
		Desc: "为机器人设置或解除禁言.当用户要求机器人说话时，可以先尝试调用此函数取消禁言。当用户要求机器人闭嘴或者不要说话时，需要调用此函数设置禁言",
		Params: arktools.NewParams("object").
			AddProp("time", &arktools.Prop{
				Type: "string",
				Desc: "禁言的时间 duration 格式, 例如 3m 表示禁言三分钟",
			}).
			AddProp("cancel", &arktools.Prop{
				Type: "boolean",
				Desc: "是否取消禁言, 默认为 false",
			}),
		Result: func(metaData *xhandler.BaseMetaData) string {
			result, _ := metaData.GetExtra("mute_result")
			return result
		},
	}
}

func (muteHandler) Handle(ctx context.Context, event *larkim.P2MessageReceiveV1, metaData *xhandler.BaseMetaData, arg MuteArgs) (err error) {
	ctx, span := otel.Start(ctx)
	defer span.End()
	defer func() { otel.RecordError(span, err) }()
	var (
		res              string
		muteTimeDuration time.Duration
	)
	defer func() { metaData.SetExtra("mute_result", res) }()
	chatID := currentChatID(event, metaData)
	if chatID == "" {
		return errors.New("chat_id is required")
	}
	if arg.Cancel {
		// 取消禁言
		// 先检查是否已经取消禁言
		if ext, err := redis.GetRedisClient().
			Exists(ctx, MuteRedisKey(chatID)).Result(); err != nil {
			return err
		} else if ext == 0 {
			res = "没有禁言，不需要取消, 直接发言即可"
			return nil // Do nothing
		}
		if err := redis.GetRedisClient().Del(ctx, MuteRedisKey(chatID)).Err(); err != nil {
			return err
		}
		res = "禁言已取消"
	} else if arg.Time != "" {
		muteTimeDuration, err = time.ParseDuration(arg.Time)
		if err != nil {
			return errors.Wrap(err, "parse time error")
		}
	} else {
		muteTimeDuration = time.Minute * 3 // 默认三分钟
	}
	if muteTimeDuration > 0 {
		if err := redis.GetRedisClient().
			Set(ctx, MuteRedisKey(chatID), 1, muteTimeDuration).
			Err(); err != nil {
			return err
		}
		res = "已启用" + muteTimeDuration.String() + "禁言"
	}
	return sendCompatibleText(ctx, event, metaData, res, "_mute", true)
}

func (muteHandler) CommandDescription() string {
	return "设置或解除禁言"
}

func (muteHandler) CommandExamples() []string {
	return []string{
		"/mute --t=10m",
		"/mute --cancel",
	}
}
