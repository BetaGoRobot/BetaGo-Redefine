package command

import (
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/handlers"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/xcommand"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
)

var larkCommandNilFunc xcommand.CommandFunc[*larkim.P2MessageReceiveV1]

// LarkRootCommand lark root command node
var LarkRootCommand = NewLarkRootCommand()
var AgenticLarkRootCommand = NewAgenticLarkRootCommand()

var newCmd = xcommand.NewCommand[*larkim.P2MessageReceiveV1]

func newTypedCmd[TArgs any](name string, handler xcommand.CLIArgHandler[*larkim.P2MessageReceiveV1, TArgs]) *xcommand.Command[*larkim.P2MessageReceiveV1] {
	return xcommand.NewTypedCommand(name, handler)
}

func NewLarkRootCommand() *xcommand.Command[*larkim.P2MessageReceiveV1] {
	root := RegisterLarkCommands(xcommand.NewRootCommand(larkCommandNilFunc), handlers.Chat)
	root.AddSubCommand(newTypedCmd("help", newHelpHandler(root)))
	root.BuildChain()
	return root
}

func NewAgenticLarkRootCommand() *xcommand.Command[*larkim.P2MessageReceiveV1] {
	root := RegisterLarkCommands(xcommand.NewRootCommand(larkCommandNilFunc), handlers.AgenticChat)
	root.AddSubCommand(newTypedCmd("help", newHelpHandler(root)))
	root.BuildChain()
	return root
}

func RegisterLarkCommands(root *xcommand.Command[*larkim.P2MessageReceiveV1], chatHandler xcommand.CLIArgHandler[*larkim.P2MessageReceiveV1, handlers.ChatArgs]) *xcommand.Command[*larkim.P2MessageReceiveV1] {
	return root.
		AddSubCommand(
			newCmd("debug", larkCommandNilFunc).
				AddDescription("调试命令").
				AddExamples("/help debug", "/debug trace", "/debug revert").
				AddSubCommand(
					newTypedCmd("msgid", handlers.DebugGetID),
				).
				AddSubCommand(
					newTypedCmd("chatid", handlers.DebugGetGroupID),
				).
				AddSubCommand(
					newTypedCmd("panic", handlers.DebugTryPanic),
				).
				AddSubCommand(
					newTypedCmd("trace", handlers.DebugTrace),
				).
				AddSubCommand(
					newTypedCmd("revert", handlers.DebugRevert),
				).
				AddSubCommand(
					newTypedCmd("repeat", handlers.DebugRepeat),
				).
				AddSubCommand(
					newTypedCmd("image", handlers.DebugImage),
				).
				AddSubCommand(
					newTypedCmd("conver", handlers.DebugConversation),
				).
				AddSubCommand(
					newTypedCmd("card", handlers.DebugCard),
				),
		).
		AddSubCommand(
			newCmd("config", larkCommandNilFunc).
				AddDescription("配置管理").
				AddExamples("/help config", "/config list", "/config set --key=intent_recognition_enabled --value=true").
				SetDefaultSubCommand("list").
				AddSubCommand(
					newTypedCmd("list", handlers.ConfigList),
				).
				AddSubCommand(
					newTypedCmd("set", handlers.ConfigSet),
				).
				AddSubCommand(
					newTypedCmd("delete", handlers.ConfigDelete),
				),
		).
		AddSubCommand(
			newCmd("feature", larkCommandNilFunc).
				AddDescription("功能开关管理").
				AddExamples("/feature list", "/feature block --feature=chat").
				SetDefaultSubCommand("list").
				AddSubCommand(
					newTypedCmd("list", handlers.FeatureList),
				).
				AddSubCommand(
					newTypedCmd("block", handlers.FeatureBlock),
				).
				AddSubCommand(
					newTypedCmd("unblock", handlers.FeatureUnblock),
				),
		).
		AddSubCommand(
			newCmd("word", larkCommandNilFunc).
				AddDescription("词库管理").
				AddExamples("/word add --word=收到 --rate=80", "/word get").
				AddSubCommand(
					newTypedCmd("add", handlers.WordAdd),
				).
				AddSubCommand(
					newTypedCmd("get", handlers.WordGet),
				),
		).
		AddSubCommand(
			newCmd("reply", larkCommandNilFunc).
				AddDescription("回复词库管理").
				AddExamples("/reply add --word=早安 --reply=早安早安", "/reply get").
				AddSubCommand(
					newTypedCmd("add", handlers.ReplyAdd),
				).
				AddSubCommand(
					newTypedCmd("get", handlers.ReplyGet),
				),
		).
		AddSubCommand(
			newCmd("image", larkCommandNilFunc).
				AddDescription("图片词库管理").
				AddExamples("/image add --url=https://example.com/demo.png", "/image get").
				AddSubCommand(
					newTypedCmd("add", handlers.ImageAdd),
				).
				AddSubCommand(
					newTypedCmd("get", handlers.ImageGet),
				).
				AddSubCommand(newTypedCmd("del", handlers.ImageDelete)),
		).
		AddSubCommand(
			newTypedCmd("music", handlers.MusicSearch),
		).
		AddSubCommand(
			newTypedCmd("oneword", handlers.OneWord),
		).
		AddSubCommand(
			newTypedCmd("bb", chatHandler),
		).
		AddSubCommand(
			newTypedCmd("mute", handlers.Mute),
		).
		AddSubCommand(
			newCmd("stock", larkCommandNilFunc).
				AddDescription("行情查询").
				AddExamples("/stock gold --d=7", "/stock zh_a --code=600519").
				AddSubCommand(
					newTypedCmd("gold", handlers.Gold),
				).
				AddSubCommand(
					newTypedCmd("zh_a", handlers.ZhAStock),
				),
		).
		AddSubCommand(
			newTypedCmd("talkrate", handlers.Trend),
		).
		AddSubCommand(
			newCmd("wordcount", larkCommandNilFunc).
				AddAliases("wc").
				AddDescription("群聊词云与 chunk 分析").
				AddExamples(
					"/wordcount summary --days=7",
					"/wordcount cloud --top=40",
					"/wordcount chunks --sort=time --question_mode=question",
					"/wordcount chunk --id=9f35b54e-7af4-11ef-bbaa-acde48001122",
					"/wordcount talkrate --play=bar",
				).
				SetDefaultSubCommand("summary").
				AddSubCommand(
					newTypedCmd("summary", handlers.WordCloud),
				).
				AddSubCommand(
					newTypedCmd("cloud", handlers.WordCloudGraph),
				).
				AddSubCommand(
					newTypedCmd("chunks", handlers.WordChunks),
				).
				AddSubCommand(
					newTypedCmd("chunk", handlers.WordChunkDetail),
				).
				AddSubCommand(
					newTypedCmd("talkrate", handlers.Trend),
				),
		).
		AddSubCommand(
			newCmd("ratelimit", larkCommandNilFunc).
				AddDescription("频控管理").
				AddExamples("/ratelimit stats", "/ratelimit list").
				SetDefaultSubCommand("stats").
				AddSubCommand(
					newTypedCmd("stats", handlers.RateLimitStats),
				).
				AddSubCommand(
					newTypedCmd("list", handlers.RateLimitList),
				),
		).
		AddSubCommand(
			newTypedCmd("permission", handlers.PermissionManage),
		).
		AddSubCommand(
			newCmd("schedule", larkCommandNilFunc).
				AddDescription("schedule 管理").
				AddExamples(
					"/schedule manage",
					"/schedule create --name=午休提醒 --type=once --run_at=2026-03-11T13:00:00+08:00 --message=记得午休",
					"/schedule list", "/schedule query --name=提醒",
					"/schedule pause --id=task_id",
					"/schedule resume --id=task_id",
					"/schedule delete --id=task_id",
				).
				SetDefaultSubCommand("manage").
				AddSubCommand(
					newTypedCmd("create", handlers.ScheduleCreate),
				).
				AddSubCommand(
					newTypedCmd("manage", handlers.ScheduleManage),
				).
				AddSubCommand(
					newTypedCmd("list", handlers.ScheduleList),
				).
				AddSubCommand(
					newTypedCmd("query", handlers.ScheduleQuery),
				).
				AddSubCommand(
					newTypedCmd("pause", handlers.SchedulePause),
				).
				AddSubCommand(
					newTypedCmd("resume", handlers.ScheduleResume),
				).
				AddSubCommand(
					newTypedCmd("delete", handlers.ScheduleDelete),
				),
		)
}
