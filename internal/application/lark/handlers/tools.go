package handlers

import (
	"context"
	"strconv"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/ark_dal/tools"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/utils"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/xhandler"
	"github.com/bytedance/gg/goption"
	"github.com/bytedance/gg/gresult"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
)

func larktools() *tools.Impl[larkim.P2MessageReceiveV1] {
	ins := tools.New[larkim.P2MessageReceiveV1]().WebSearch()
	musicSearch(ins)
	muteBot(ins)
	goldReport(ins)
	revert(ins)
	return ins
}

func musicSearch(ins *tools.Impl[larkim.P2MessageReceiveV1]) {
	unit := tools.NewUnit[larkim.P2MessageReceiveV1]()
	params := tools.NewParams("object").
		AddProp("keywords", &tools.Prop{
			Type: "string",
			Desc: "音乐搜索的关键词, 多个关键词之间用空格隔开",
		}).AddRequired("keywords")
	ins.Add(unit.Name("music_search").Desc("根据输入的关键词搜索相关的音乐并发送卡片").Params(params).Func(musicSearchWrap))
}

func muteBot(ins *tools.Impl[larkim.P2MessageReceiveV1]) {
	unit := tools.NewUnit[larkim.P2MessageReceiveV1]()
	params := tools.NewParams("object").
		AddProp("time", &tools.Prop{
			Type: "string",
			Desc: "禁言的时间 duration 格式, 例如 3m 表示禁言三分钟",
		}).AddProp("cancel", &tools.Prop{
		Type: "boolean",
		Desc: "是否取消禁言, 默认为 false",
	})
	ins.Add(unit.Name("mute_robot").Desc("为机器人设置或解除禁言.当用户要求机器人说话时，可以先尝试调用此函数取消禁言。当用户要求机器人闭嘴或者不要说话时，需要调用此函数设置禁言").
		Params(params).Func(muteWrap))
}

func goldReport(ins *tools.Impl[larkim.P2MessageReceiveV1]) {
	unit := tools.NewUnit[larkim.P2MessageReceiveV1]()
	params := tools.NewParams("object").
		AddProp("start_time", &tools.Prop{
			Type: "string",
			Desc: "开始时间，默认可以不穿，格式为RFC3339: 2006-01-02T15:04:05Z07:00",
		}).
		AddProp("end_time", &tools.Prop{
			Type: "string",
			Desc: "结束时间，默认可以不传，格式为RFC3339: 2006-01-02T15:04:05Z07:00",
		}).
		AddProp("hours", &tools.Prop{
			Type: "number",
			Desc: "查询的小时数，默认1小时",
		}).
		AddProp("days", &tools.Prop{
			Type: "number",
			Desc: "查询的天数，默认30天",
		})
	ins.Add(unit.Name("gold_price_get").
		Desc("搜索指定时间范围内的金价变化情况，可选相对时间天或小时，也可以指定时间范围").
		Params(params).Func(goldWrap))
}

func revert(ins *tools.Impl[larkim.P2MessageReceiveV1]) {
	params := tools.NewParams("object")
	fcu := tools.NewUnit[larkim.P2MessageReceiveV1]().
		Name("revert_message").Desc("可以撤回指定消息,调用时不需要任何参数，工具会判断要撤回的消息是什么，并且返回撤回的结果。如果不是机器人发出的消息,是不能撤回的").Params(params).Func(revertWrap)
	ins.Add(fcu)
}

func musicSearchWrap(ctx context.Context, args string, meta tools.FCMeta[larkim.P2MessageReceiveV1]) gresult.R[string] {
	s := struct {
		Keywords string `json:"keywords"`
	}{}
	err := utils.UnmarshalStringPre(args, &s)
	if err != nil {
		return gresult.Err[string](err)
	}
	metaData := xhandler.NewBaseMetaDataWithChatIDUID(ctx, meta.ChatID, meta.UserID)
	return gresult.Of("执行成功", MusicSearchHandler(ctx, meta.Data, metaData, s.Keywords))
}

func muteWrap(ctx context.Context, args string, meta tools.FCMeta[larkim.P2MessageReceiveV1]) gresult.R[string] {
	s := struct {
		Time   string `json:"time"`
		Cancel bool   `json:"cancel"`
	}{}
	err := utils.UnmarshalStringPre(args, &s)
	if err != nil {
		return gresult.Err[string](err)
	}
	argsSlice := make([]string, 0)
	if s.Cancel {
		argsSlice = append(argsSlice, "--cancel")
	}
	if s.Time != "" {
		argsSlice = append(argsSlice, "--t="+s.Time)
	}
	metaData := xhandler.NewBaseMetaDataWithChatIDUID(ctx, meta.ChatID, meta.UserID)
	if err := MuteHandler(ctx, meta.Data, metaData, argsSlice...); err != nil {
		return gresult.Err[string](err)
	}
	return gresult.Of(goption.Of(metaData.GetExtra("mute_result")).ValueOr("执行完成但没有结果"), nil)
}

func goldWrap(ctx context.Context, args string, meta tools.FCMeta[larkim.P2MessageReceiveV1]) gresult.R[string] {
	s := struct {
		StartTime string `json:"start_time"`
		EndTime   string `json:"end_time"`
		Days      *int   `json:"days"`
		Hours     *int   `json:"hours"`
	}{}
	err := utils.UnmarshalStringPre(args, &s)
	if err != nil {
		return gresult.Err[string](err)
	}
	argsSlice := make([]string, 0)
	if s.Days != nil && *s.Days > 0 {
		argsSlice = append(argsSlice, "--d="+strconv.Itoa(*s.Days))
	}
	if s.Hours != nil && *s.Hours > 0 {
		argsSlice = append(argsSlice, "--h="+strconv.Itoa(*s.Hours))
	}
	if s.StartTime != "" {
		argsSlice = append(argsSlice, "--st="+s.StartTime)
	}
	if s.EndTime != "" {
		argsSlice = append(argsSlice, "--et="+s.EndTime)
	}
	metaData := xhandler.NewBaseMetaDataWithChatIDUID(ctx, meta.ChatID, meta.UserID)
	if err := GoldHandler(ctx, meta.Data, metaData, argsSlice...); err != nil {
		return gresult.Err[string](err)
	}

	return gresult.Of(goption.Of(metaData.GetExtra("gold_result")).ValueOr("执行完成但没有结果"), nil)
}

func revertWrap(ctx context.Context, args string, meta tools.FCMeta[larkim.P2MessageReceiveV1]) gresult.R[string] {
	s := struct {
		Time   string `json:"time"`
		Cancel bool   `json:"cancel"`
	}{}
	err := utils.UnmarshalStringPre(args, &s)
	if err != nil {
		return gresult.Err[string](err)
	}
	argsSlice := make([]string, 0)
	if s.Cancel {
		argsSlice = append(argsSlice, "--cancel")
	}
	if s.Time != "" {
		argsSlice = append(argsSlice, "--t="+s.Time)
	}
	metaData := xhandler.NewBaseMetaDataWithChatIDUID(ctx, meta.ChatID, meta.UserID)
	if err := DebugRevertHandler(ctx, meta.Data, metaData, argsSlice...); err != nil {
		return gresult.Err[string](err)
	}
	return gresult.Of(goption.Of(metaData.GetExtra("revert_result")).ValueOr("执行完成但没有结果"), nil)
}
