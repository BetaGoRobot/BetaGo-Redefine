package command

import (
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/handlers"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/xcommand"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
)

var larkCommandNilFunc xcommand.CommandFunc[*larkim.P2MessageReceiveV1]

// LarkRootCommand lark root command node
var LarkRootCommand *xcommand.Command[*larkim.P2MessageReceiveV1]

var newCmd = xcommand.NewCommand[*larkim.P2MessageReceiveV1]

func init() {
	LarkRootCommand = xcommand.
		NewRootCommand(larkCommandNilFunc).
		AddSubCommand(
			newCmd("debug", larkCommandNilFunc).
				AddSubCommand(
					newCmd("msgid", handlers.DebugGetIDHandler),
				).
				AddSubCommand(
					newCmd("chatid", handlers.DebugGetGroupIDHandler),
				).
				AddSubCommand(
					newCmd("panic", handlers.DebugTryPanicHandler),
				).
				AddSubCommand(
					newCmd("trace", handlers.DebugTraceHandler),
				).
				AddSubCommand(
					newCmd("revert", handlers.DebugRevertHandler),
				).
				AddSubCommand(
					newCmd("repeat", handlers.DebugRepeatHandler),
				).
				AddSubCommand(
					newCmd("image", handlers.DebugImageHandler),
				).
				AddSubCommand(
					newCmd("conver", handlers.DebugConversationHandler),
				),
		).
		AddSubCommand(
			newCmd("config", larkCommandNilFunc).
				AddSubCommand(
					newCmd("list", handlers.ConfigListHandler).AddArgs("scope"),
				).
				AddSubCommand(
					newCmd("set", handlers.ConfigSetHandler).AddArgs("key", "value", "scope"),
				).
				AddSubCommand(
					newCmd("delete", handlers.ConfigDeleteHandler).AddArgs("key", "scope"),
				),
		).
		AddSubCommand(
			newCmd("feature", larkCommandNilFunc).
				AddSubCommand(
					newCmd("list", handlers.FeatureListHandler),
				).
				AddSubCommand(
					newCmd("block", handlers.FeatureBlockHandler).AddArgs("feature", "scope"),
				).
				AddSubCommand(
					newCmd("unblock", handlers.FeatureUnblockHandler).AddArgs("feature", "scope"),
				),
		).
		AddSubCommand(
			newCmd("word", larkCommandNilFunc).
				AddSubCommand(
					newCmd("add", handlers.WordAddHandler).AddArgs("word", "rate"),
				).
				AddSubCommand(
					newCmd("get", handlers.WordGetHandler),
				),
		).
		AddSubCommand(
			newCmd("reply", larkCommandNilFunc).
				AddSubCommand(
					newCmd("add", handlers.ReplyAddHandler).AddArgs("word", "reply", "type"),
				).
				AddSubCommand(
					newCmd("get", handlers.ReplyGetHandler),
				),
		).
		AddSubCommand(
			newCmd("image", larkCommandNilFunc).
				AddSubCommand(
					newCmd("add", handlers.ImageAddHandler).AddArgs("url").AddArgs("img_key"),
				).
				AddSubCommand(
					newCmd("get", handlers.ImageGetHandler),
				).
				AddSubCommand(newCmd("del", handlers.ImageDelHandler)),
		).
		AddSubCommand(
			newCmd("music", handlers.MusicSearchHandler).AddArgs("type", "keywords"),
		).
		AddSubCommand(
			newCmd("oneword", handlers.OneWordHandler).AddArgs("type"),
		).
		AddSubCommand(
			newCmd("bb", handlers.ChatHandler("chat")).AddArgs("r", "c"),
		).
		AddSubCommand(
			newCmd("mute", handlers.MuteHandler).AddArgs("t", "cancel"),
		).
		AddSubCommand(
			newCmd("stock", larkCommandNilFunc).
				AddSubCommand(
					newCmd("gold", handlers.StockHandler("gold")).
						AddArgs(
							"r", "h",
						),
				).
				AddSubCommand(
					newCmd("zh_a", handlers.StockHandler("a")).
						AddArgs(
							"code", "days",
						),
				),
		).
		AddSubCommand(
			newCmd("talkrate", handlers.TrendHandler).
				AddArgs("days", "interval"),
		).AddSubCommand(newCmd("wc", handlers.WordCloudHandler).AddArgs("mtop", "ctop", "interval", "days", "st", "et", "chat_id", "sort"))
	LarkRootCommand.BuildChain()
}
