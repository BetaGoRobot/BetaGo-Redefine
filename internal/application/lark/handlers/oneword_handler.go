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

const (
	yiyanURL     = "https://api.fanlisky.cn/niuren/getSen"
	yiyanPoemURL = "https://v1.jinrishici.com/all.json"
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
	Type string `json:"type"`
}

type oneWordHandler struct{}

var OneWord oneWordHandler

func (oneWordHandler) ParseCLI(args []string) (OneWordArgs, error) {
	argMap, _ := parseArgs(args...)
	return OneWordArgs{Type: argMap["type"]}, nil
}

func (oneWordHandler) ParseTool(raw string) (OneWordArgs, error) {
	parsed := OneWordArgs{}
	if err := utils.UnmarshalStringPre(raw, &parsed); err != nil {
		return OneWordArgs{}, err
	}
	return parsed, nil
}

func (oneWordHandler) ToolSpec() xcommand.ToolSpec {
	return xcommand.ToolSpec{
		Name: "oneword_get",
		Desc: "获取一句一言/诗词并发送到当前对话",
		Params: arktools.NewParams("object").
			AddProp("type", &arktools.Prop{
				Type: "string",
				Desc: "一言类型，可选值：二次元、游戏、文学、原创、网络、其他、影视、诗词、网易云、哲学、抖机灵",
			}),
	}
}

func (oneWordHandler) Handle(ctx context.Context, data *larkim.P2MessageReceiveV1, metaData *xhandler.BaseMetaData, arg OneWordArgs) (err error) {
	ctx, span := otel.Start(ctx)
	defer span.End()
	defer func() { otel.RecordError(span, err) }()

	oneWordArgs := []string{}

	switch arg.Type {
	case "二次元":
		oneWordArgs = append(oneWordArgs, []string{"a", "b"}...)
	case "游戏":
		oneWordArgs = append(oneWordArgs, "c")
	case "文学":
		oneWordArgs = append(oneWordArgs, "d")
	case "原创":
		oneWordArgs = append(oneWordArgs, "e")
	case "网络":
		oneWordArgs = append(oneWordArgs, "f")
	case "其他":
		oneWordArgs = append(oneWordArgs, "g")
	case "影视":
		oneWordArgs = append(oneWordArgs, "h")
	case "诗词":
		oneWordArgs = append(oneWordArgs, "i")
	case "网易云":
		oneWordArgs = append(oneWordArgs, "j")
	case "哲学":
		oneWordArgs = append(oneWordArgs, "k")
	case "抖机灵":
		oneWordArgs = append(oneWordArgs, "l")
	}

	hitokotoRes, err := hitokoto.GetHitokoto(oneWordArgs...)
	if err != nil {
		return err
	}
	msg := fmt.Sprintf("%s 很喜欢《%s》中的一句话\n%s", emoji.Mountain.String(), hitokotoRes.From, hitokotoRes.Hitokoto)
	return sendCompatibleText(ctx, data, metaData, msg, "_oneWord", false)
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
