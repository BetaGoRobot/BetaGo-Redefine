package handlers

import (
	"context"
	"errors"
	"strconv"

	arktools "github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/ark_dal/tools"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/db/model"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/db/query"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/lark_dal/larkmsg/larktpl"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/otel"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/logs"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/utils"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/xcommand"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/xhandler"

	larkcore "github.com/larksuite/oapi-sdk-go/v3/core"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
	"go.uber.org/zap"
	"gorm.io/gorm/clause"
)

type WordAddArgs struct {
	Word string `json:"word"`
	Rate int    `json:"rate"`
}

type WordGetArgs struct{}

type (
	wordAddHandler struct{}
	wordGetHandler struct{}
)

var (
	WordAdd wordAddHandler
	WordGet wordGetHandler
)

const wordActionToolResultKey = "word_action_result"

func (wordAddHandler) ParseCLI(args []string) (WordAddArgs, error) {
	argMap, _ := parseArgs(args...)
	word := argMap["word"]
	if word == "" {
		return WordAddArgs{}, errors.New("word is required")
	}
	rateStr := argMap["rate"]
	if rateStr == "" {
		return WordAddArgs{}, errors.New("rate is required")
	}
	rate, err := strconv.Atoi(rateStr)
	if err != nil {
		return WordAddArgs{}, err
	}
	return WordAddArgs{Word: word, Rate: rate}, nil
}

func (wordAddHandler) ParseTool(raw string) (WordAddArgs, error) {
	parsed := WordAddArgs{}
	if err := utils.UnmarshalStringPre(raw, &parsed); err != nil {
		return WordAddArgs{}, err
	}
	if parsed.Word == "" {
		return WordAddArgs{}, errors.New("word is required")
	}
	if parsed.Rate == 0 {
		return WordAddArgs{}, errors.New("rate is required")
	}
	return parsed, nil
}

func (wordAddHandler) ToolSpec() xcommand.ToolSpec {
	return xcommand.ToolSpec{
		Name: "word_add",
		Desc: "新增或更新复读词条",
		Params: arktools.NewParams("object").
			AddProp("word", &arktools.Prop{
				Type: "string",
				Desc: "触发词",
			}).
			AddProp("rate", &arktools.Prop{
				Type: "number",
				Desc: "触发概率/权重",
			}).
			AddRequired("word").
			AddRequired("rate"),
		Result: func(metaData *xhandler.BaseMetaData) string {
			result, _ := metaData.GetExtra(wordActionToolResultKey)
			return result
		},
	}
}

func (wordAddHandler) Handle(ctx context.Context, data *larkim.P2MessageReceiveV1, metaData *xhandler.BaseMetaData, arg WordAddArgs) (err error) {
	ctx, span := otel.Start(ctx)
	span.SetAttributes(otel.PreviewAttrs("event", larkcore.Prettify(data), 256)...)
	defer span.End()
	defer func() { otel.RecordError(span, err) }()
	logs.L().Ctx(ctx).Info("args", zap.Any("args", arg))

	ChatID := currentChatID(data, metaData)
	if err := query.Q.RepeatWordsRateCustom.WithContext(ctx).Clauses(clause.OnConflict{
		UpdateAll: true,
	}).Create(&model.RepeatWordsRateCustom{
		GuildID: ChatID,
		Word:    arg.Word,
		Rate:    int64(arg.Rate),
	}); err != nil {
		return err
	}
	metaData.SetExtra(wordActionToolResultKey, "复读词条更新成功")
	return nil
}

func (wordGetHandler) ParseCLI(args []string) (WordGetArgs, error) {
	return WordGetArgs{}, nil
}

func (wordGetHandler) ParseTool(raw string) (WordGetArgs, error) {
	if err := parseEmptyToolArgs(raw); err != nil {
		return WordGetArgs{}, err
	}
	return WordGetArgs{}, nil
}

func (wordGetHandler) ToolSpec() xcommand.ToolSpec {
	return xcommand.ToolSpec{
		Name:   "word_get",
		Desc:   "查看当前群聊的复读词条配置",
		Params: arktools.NewParams("object"),
	}
}

func (wordGetHandler) Handle(ctx context.Context, data *larkim.P2MessageReceiveV1, metaData *xhandler.BaseMetaData, arg WordGetArgs) (err error) {
	ctx, span := otel.Start(ctx)
	span.SetAttributes(otel.PreviewAttrs("event", larkcore.Prettify(data), 256)...)
	defer span.End()
	defer func() { otel.RecordError(span, err) }()
	logs.L().Ctx(ctx).Info("args", zap.Any("args", arg))
	ChatID := currentChatID(data, metaData)

	lines := make([]map[string]string, 0)
	ins := query.Q.RepeatWordsRateCustom
	resListCustom, err := ins.WithContext(ctx).
		Where(ins.GuildID.Eq(ChatID)).
		Find()
	if err != nil {
		return err
	}
	for _, res := range resListCustom {
		if res.GuildID == ChatID {
			lines = append(lines, map[string]string{
				"title1": "Custom",
				"title2": res.Word,
				"title3": strconv.Itoa(int(res.Rate)),
			})
		}
	}
	ins2 := query.Q.RepeatWordsRate
	resListGlobal, err := ins2.WithContext(ctx).
		Find()
	if err != nil {
		return err
	}
	for _, res := range resListGlobal {
		lines = append(lines, map[string]string{
			"title1": "Global",
			"title2": res.Word,
			"title3": strconv.Itoa(int(res.Rate)),
		})
	}
	cardContent := larktpl.NewCardContent(
		ctx,
		larktpl.ThreeColSheetTemplate,
	).
		AddVariable("title1", "Scope").
		AddVariable("title2", "Keyword").
		AddVariable("title3", "Rate").
		AddVariable("table_raw_array_1", lines)

	return sendCompatibleCard(ctx, data, metaData, cardContent, "_wordGet", false)
}

func (wordAddHandler) CommandDescription() string {
	return "新增复读词条"
}

func (wordGetHandler) CommandDescription() string {
	return "查看复读词条"
}

func (wordAddHandler) CommandExamples() []string {
	return []string{
		"/word add --word=收到 --rate=80",
	}
}

func (wordGetHandler) CommandExamples() []string {
	return []string{
		"/word get",
	}
}
