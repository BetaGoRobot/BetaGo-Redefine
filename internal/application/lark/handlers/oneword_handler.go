package handlers

import (
	"context"
	"fmt"

	arktools "github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/ark_dal/tools"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/hitokoto"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/otel"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/utils"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/xcommand"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/xhandler"
	"github.com/enescakir/emoji"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
)

// RespBody  一言返回体
type RespBody struct {
	ID         int         `json:"id"`
	UUID       string      `json:"uuid"`
	Hitokoto   string      `json:"hitokoto"`
	Type       string      `json:"type"`
	From       string      `json:"from"`
	FromWho    interface{} `json:"from_who"`
	Creator    string      `json:"creator"`
	CreatorUID int         `json:"creator_uid"`
	Reviewer   int         `json:"reviewer"`
	CommitFrom string      `json:"commit_from"`
	CreatedAt  string      `json:"created_at"`
	Length     int         `json:"length"`
}

type OneWordArgs struct {
	Type OneWordType `json:"type"`
}

type oneWordHandler struct{}

var OneWord oneWordHandler

const oneWordToolResultKey = "oneword_result"

func (oneWordHandler) ParseCLI(args []string) (OneWordArgs, error) {
	argMap, _ := parseArgs(args...)
	oneWordType, err := xcommand.ParseEnum[OneWordType](argMap["type"])
	if err != nil {
		return OneWordArgs{}, err
	}
	return OneWordArgs{Type: oneWordType}, nil
}

func (oneWordHandler) ParseTool(raw string) (OneWordArgs, error) {
	parsed := OneWordArgs{}
	if err := utils.UnmarshalStringPre(raw, &parsed); err != nil {
		return OneWordArgs{}, err
	}
	oneWordType, err := xcommand.ParseEnum[OneWordType](string(parsed.Type))
	if err != nil {
		return OneWordArgs{}, err
	}
	parsed.Type = oneWordType
	return parsed, nil
}

func (oneWordHandler) ToolSpec() xcommand.ToolSpec {
	return xcommand.ToolSpec{
		Name: "oneword_get",
		Desc: "获取一句一言/诗词并发送到当前对话",
		Params: arktools.NewParams("object").
			AddProp("type", &arktools.Prop{
				Type: "string",
				Desc: "一言分类",
			}),
		Result: func(metaData *xhandler.BaseMetaData) string {
			result, _ := metaData.GetExtra(oneWordToolResultKey)
			return result
		},
	}
}

func (oneWordHandler) Handle(ctx context.Context, data *larkim.P2MessageReceiveV1, metaData *xhandler.BaseMetaData, arg OneWordArgs) (err error) {
	ctx, span := otel.Start(ctx)
	defer span.End()
	defer func() { otel.RecordError(span, err) }()

	oneWordArgs := []string{}

	switch arg.Type {
	case OneWordTypeAnime:
		oneWordArgs = append(oneWordArgs, []string{"a", "b"}...)
	case OneWordTypeGame:
		oneWordArgs = append(oneWordArgs, "c")
	case OneWordTypeLiterary:
		oneWordArgs = append(oneWordArgs, "d")
	case OneWordTypeOriginal:
		oneWordArgs = append(oneWordArgs, "e")
	case OneWordTypeNetwork:
		oneWordArgs = append(oneWordArgs, "f")
	case OneWordTypeOther:
		oneWordArgs = append(oneWordArgs, "g")
	case OneWordTypeFilm:
		oneWordArgs = append(oneWordArgs, "h")
	case OneWordTypePoetry:
		oneWordArgs = append(oneWordArgs, "i")
	case OneWordTypeNetease:
		oneWordArgs = append(oneWordArgs, "j")
	case OneWordTypePhilo:
		oneWordArgs = append(oneWordArgs, "k")
	case OneWordTypeJoke:
		oneWordArgs = append(oneWordArgs, "l")
	}

	hitokotoRes, err := hitokoto.GetHitokoto(oneWordArgs...)
	if err != nil {
		return err
	}
	msg := fmt.Sprintf("%s 很喜欢《%s》中的一句话\n%s", emoji.Mountain.String(), hitokotoRes.From, hitokotoRes.Hitokoto)
	if err := sendCompatibleText(ctx, data, metaData, msg, "_oneWord", false); err != nil {
		return err
	}
	metaData.SetExtra(oneWordToolResultKey, "一言已发送")
	return nil
}

func resolveOneWordApprovalSummary(arg OneWordArgs) string {
	if arg.Type == "" {
		return "将向当前对话发送一言"
	}
	return "将向当前对话发送「" + string(arg.Type) + "」分类的一言"
}

func (oneWordHandler) CommandDescription() string {
	return "发送一言或诗词"
}

func (oneWordHandler) CommandExamples() []string {
	return []string{
		"/oneword",
		"/oneword --type=诗词",
	}
}
