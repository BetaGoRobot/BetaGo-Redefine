package handlers

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/history"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/ark_dal/tools"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/lark_dal/larkuser"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/utils"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/xcommand"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/xhandler"
	"github.com/defensestation/osquery"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
)

type ChatMembersArgs struct {
	Limit int `json:"limit"`
}

type RecentActiveMembersArgs struct {
	TopK             int `json:"top_k"`
	LookbackMessages int `json:"lookback_messages"`
}

type chatMembersHandler struct{}

type recentActiveMembersHandler struct{}

type chatMemberResult struct {
	OpenID string `json:"open_id"`
	Name   string `json:"name"`
}

type chatMembersResult struct {
	Total   int                `json:"total"`
	Members []chatMemberResult `json:"members"`
}

type recentActiveMemberResult struct {
	OpenID               string `json:"open_id"`
	Name                 string `json:"name"`
	LatestCreateTime     string `json:"latest_create_time"`
	LatestMessagePreview string `json:"latest_message_preview"`
	MessageCountInWindow int    `json:"message_count_in_window"`
}

type recentActiveMembersResult struct {
	WindowSize int                        `json:"window_size"`
	Members    []recentActiveMemberResult `json:"members"`
}

var (
	ChatMembers         chatMembersHandler
	RecentActiveMembers recentActiveMembersHandler

	chatMembersLoader                = larkuser.GetUserMapFromChatIDCache
	recentActiveMembersHistoryLoader = func(ctx context.Context, chatID string, size int) (history.OpensearchMsgLogList, error) {
		return history.New(ctx).
			Query(osquery.Bool().Must(osquery.Term("chat_id", chatID))).
			Source("raw_message", "mentions", "create_time", "user_id", "chat_id", "user_name", "message_type").
			Size(uint64(size)).
			Sort("create_time", "desc").
			GetMsg()
	}
)

func (chatMembersHandler) ParseTool(raw string) (ChatMembersArgs, error) {
	parsed := ChatMembersArgs{}
	if err := utils.UnmarshalStringPre(raw, &parsed); err != nil {
		return ChatMembersArgs{}, err
	}
	return parsed, nil
}

func (chatMembersHandler) ToolSpec() xcommand.ToolSpec {
	return xcommand.ToolSpec{
		Name: "get_chat_members",
		Desc: "获取当前 chat_id 内的群成员列表，返回成员 open_id 和姓名，便于点名或确认谁在群里",
		Params: tools.NewParams("object").
			AddProp("limit", &tools.Prop{
				Type: "number",
				Desc: "最多返回多少个成员，默认50，最大200",
			}),
		Result: func(metaData *xhandler.BaseMetaData) string {
			result, _ := metaData.GetExtra("chat_members_result")
			return result
		},
	}
}

func (chatMembersHandler) Handle(ctx context.Context, data *larkim.P2MessageReceiveV1, metaData *xhandler.BaseMetaData, arg ChatMembersArgs) error {
	chatID := strings.TrimSpace(metaData.ChatID)
	if chatID == "" {
		return fmt.Errorf("chat_id is required for get_chat_members")
	}
	memberMap, err := chatMembersLoader(ctx, chatID)
	if err != nil {
		return err
	}
	members := make([]chatMemberResult, 0, len(memberMap))
	for _, member := range memberMap {
		if member == nil {
			continue
		}
		openID := strings.TrimSpace(memberToolsString(member.MemberId))
		name := strings.TrimSpace(memberToolsString(member.Name))
		if openID == "" && name == "" {
			continue
		}
		members = append(members, chatMemberResult{
			OpenID: openID,
			Name:   name,
		})
	}
	sort.SliceStable(members, func(i, j int) bool {
		if members[i].Name == members[j].Name {
			return members[i].OpenID < members[j].OpenID
		}
		if members[i].Name == "" || members[j].Name == "" {
			return members[i].OpenID < members[j].OpenID
		}
		return members[i].Name < members[j].Name
	})

	total := len(members)
	limit := normalizeMembersLimit(arg.Limit)
	if len(members) > limit {
		members = members[:limit]
	}
	metaData.SetExtra("chat_members_result", utils.MustMarshalString(chatMembersResult{
		Total:   total,
		Members: members,
	}))
	return nil
}

func (recentActiveMembersHandler) ParseTool(raw string) (RecentActiveMembersArgs, error) {
	parsed := RecentActiveMembersArgs{}
	if err := utils.UnmarshalStringPre(raw, &parsed); err != nil {
		return RecentActiveMembersArgs{}, err
	}
	return parsed, nil
}

func (recentActiveMembersHandler) ToolSpec() xcommand.ToolSpec {
	return xcommand.ToolSpec{
		Name: "get_recent_active_members",
		Desc: "获取当前 chat_id 最近一段消息窗口内的活跃发言成员，按最近发言时间去重排序，返回 open_id、姓名、最近发言时间和窗口内发言次数",
		Params: tools.NewParams("object").
			AddProp("top_k", &tools.Prop{
				Type: "number",
				Desc: "返回多少个最近活跃成员，默认10，最大50",
			}).
			AddProp("lookback_messages", &tools.Prop{
				Type: "number",
				Desc: "从最近多少条消息里统计活跃成员，默认30，最大200",
			}),
		Result: func(metaData *xhandler.BaseMetaData) string {
			result, _ := metaData.GetExtra("recent_active_members_result")
			return result
		},
	}
}

func (recentActiveMembersHandler) Handle(ctx context.Context, data *larkim.P2MessageReceiveV1, metaData *xhandler.BaseMetaData, arg RecentActiveMembersArgs) error {
	chatID := strings.TrimSpace(metaData.ChatID)
	if chatID == "" {
		return fmt.Errorf("chat_id is required for get_recent_active_members")
	}
	lookback := normalizeLookbackMessages(arg.LookbackMessages)
	messageList, err := recentActiveMembersHistoryLoader(ctx, chatID, lookback)
	if err != nil {
		return err
	}

	countByOpenID := make(map[string]int, len(messageList))
	for _, msg := range messageList {
		if msg == nil {
			continue
		}
		openID := strings.TrimSpace(msg.OpenID)
		if openID == "" {
			continue
		}
		countByOpenID[openID]++
	}

	topK := normalizeRecentActiveTopK(arg.TopK)
	seen := make(map[string]struct{}, len(messageList))
	members := make([]recentActiveMemberResult, 0, topK)
	for _, msg := range messageList {
		if msg == nil {
			continue
		}
		openID := strings.TrimSpace(msg.OpenID)
		if openID == "" {
			continue
		}
		if _, exists := seen[openID]; exists {
			continue
		}
		seen[openID] = struct{}{}
		members = append(members, recentActiveMemberResult{
			OpenID:               openID,
			Name:                 strings.TrimSpace(msg.UserName),
			LatestCreateTime:     strings.TrimSpace(msg.CreateTime),
			LatestMessagePreview: memberToolsPreview(strings.Join(msg.MsgList, ";")),
			MessageCountInWindow: countByOpenID[openID],
		})
		if len(members) >= topK {
			break
		}
	}

	metaData.SetExtra("recent_active_members_result", utils.MustMarshalString(recentActiveMembersResult{
		WindowSize: lookback,
		Members:    members,
	}))
	return nil
}

func normalizeMembersLimit(limit int) int {
	switch {
	case limit <= 0:
		return 50
	case limit > 200:
		return 200
	default:
		return limit
	}
}

func normalizeRecentActiveTopK(topK int) int {
	switch {
	case topK <= 0:
		return 10
	case topK > 50:
		return 50
	default:
		return topK
	}
}

func normalizeLookbackMessages(size int) int {
	switch {
	case size <= 0:
		return 30
	case size > 200:
		return 200
	default:
		return size
	}
}

func memberToolsPreview(text string) string {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return ""
	}
	runes := []rune(trimmed)
	if len(runes) <= 80 {
		return trimmed
	}
	return string(runes[:80]) + "..."
}

func memberToolsString(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}
