package larkmsg

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/config"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/opensearch"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/xmodel"
	cardactionproto "github.com/BetaGoRobot/BetaGo-Redefine/pkg/cardaction"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/logs"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/utils"
	"github.com/bytedance/sonic"
	"github.com/larksuite/oapi-sdk-go/v3/event/dispatcher/callback"
	"go.uber.org/zap"
)

type CardActionHistoryOptions struct {
	Enabled        bool
	MessageID      string
	OpenMessageID  string
	Title          string
	Limit          int
	Expanded       bool
	PendingRecords []CardActionHistoryRecord
}

type CardActionHistoryRecord struct {
	Key            string
	OpenMessageID  string
	OpenChatID     string
	ActionName     string
	ActionValue    map[string]any
	OpenID         string
	UserName       string
	CreateTime     string
	CreateTimeUnix int64
}

func NewCardActionHistoryRecord(event *callback.CardActionTriggerEvent) CardActionHistoryRecord {
	record := CardActionHistoryRecord{
		Key: cardActionDocID(event),
	}
	if event == nil || event.Event == nil {
		return record
	}
	if event.Event.Context != nil {
		record.OpenMessageID = strings.TrimSpace(event.Event.Context.OpenMessageID)
		record.OpenChatID = strings.TrimSpace(event.Event.Context.OpenChatID)
	}
	if event.Event.Operator != nil {
		record.OpenID = strings.TrimSpace(event.Event.Operator.OpenID)
	}
	if event.Event.Action != nil {
		record.ActionValue = maps.Clone(event.Event.Action.Value)
	}
	if parsed, err := cardactionproto.Parse(event); err == nil {
		record.ActionName = strings.TrimSpace(parsed.Name)
	}
	if event.EventV2Base != nil && event.EventV2Base.Header != nil {
		createTimeRaw := strings.TrimSpace(event.EventV2Base.Header.CreateTime)
		record.CreateTimeUnix = parseInt64(createTimeRaw)
		record.CreateTime = formatCardActionTime(createTimeRaw, record.CreateTimeUnix)
	}
	return record
}

func CardActionHistoryPanel(ctx context.Context, opts CardActionHistoryOptions) map[string]any {
	opts = normalizeCardActionHistoryOptions(opts)
	records, note := loadCardActionHistoryRecords(ctx, opts.OpenMessageID, opts.Limit)
	records = mergeCardActionHistoryRecords(records, opts.PendingRecords, opts.Limit)

	capHint := len(records)
	if capHint == 0 {
		capHint = 1
	}
	elements := make([]any, 0, capHint)
	if len(records) == 0 {
		elements = append(elements, HintMarkdown(note))
	} else {
		for _, record := range records {
			elements = append(elements, buildCardActionHistoryRow(record))
		}
	}

	title := strings.TrimSpace(opts.Title)
	if title == "" {
		title = "操作记录"
	}
	if len(records) > 0 {
		title = fmt.Sprintf("**%s** <font color='grey'>(%d)</font>", title, len(records))
	}
	return CollapsiblePanel(title, elements, CollapsiblePanelOptions{
		ElementID:       "card_action_log",
		Expanded:        opts.Expanded,
		VerticalSpacing: "4px",
		Padding:         "8px",
	})
}

func normalizeCardActionHistoryOptions(opts CardActionHistoryOptions) CardActionHistoryOptions {
	if opts.MessageID == "" {
		opts.MessageID = strings.TrimSpace(opts.OpenMessageID)
	}
	opts.OpenMessageID = strings.TrimSpace(opts.MessageID)
	opts.OpenMessageID = strings.TrimSpace(opts.OpenMessageID)
	if opts.Limit <= 0 {
		opts.Limit = 8
	}
	return opts
}

func AppendCardActionHistory(ctx context.Context, elements []any, opts CardActionHistoryOptions) []any {
	opts.Enabled = true
	panel := CardActionHistoryPanel(ctx, opts)
	if panel == nil {
		return append([]any{}, elements...)
	}
	result := append([]any{}, elements...)
	result = append(result, Divider(), panel)
	return result
}

func loadCardActionHistoryRecords(ctx context.Context, openMessageID string, limit int) ([]CardActionHistoryRecord, string) {
	openMessageID = strings.TrimSpace(openMessageID)
	if openMessageID == "" {
		return nil, "首次发送后可在此查看操作记录"
	}

	index := strings.TrimSpace(config.Get().OpensearchConfig.LarkCardActionIndex)
	if index == "" {
		return nil, "未配置操作审计索引"
	}
	if enabled, _ := opensearch.Status(); !enabled {
		return nil, "操作记录暂不可用"
	}

	resp, err := opensearch.SearchData(ctx, cardActionHistorySearchIndex(index), map[string]any{
		"size": limit,
		"query": map[string]any{
			"bool": map[string]any{
				"filter": []any{
					map[string]any{
						"bool": map[string]any{
							"should": []any{
								map[string]any{
									"term": map[string]any{
										"open_message_id.keyword": openMessageID,
									},
								},
								map[string]any{
									"term": map[string]any{
										"open_message_id": openMessageID,
									},
								},
							},
							"minimum_should_match": 1,
						},
					},
				},
			},
		},
		"sort": []any{
			map[string]any{"create_time_unix": map[string]any{"order": "desc"}},
			// map[string]any{"create_time.keyword": map[string]any{"order": "desc"}},
		},
	})
	if err != nil {
		logs.L().Ctx(ctx).Warn("search card action history failed", zap.String("message_id", openMessageID), zap.Error(err))
		return nil, "操作记录加载失败"
	}
	if resp == nil || len(resp.Hits.Hits) == 0 {
		return nil, "暂无操作记录"
	}

	records := make([]CardActionHistoryRecord, 0, len(resp.Hits.Hits))
	for _, hit := range resp.Hits.Hits {
		doc := &xmodel.CardActionIndex{}
		if err := sonic.Unmarshal(hit.Source, doc); err != nil {
			continue
		}
		records = append(records, CardActionHistoryRecord{
			Key:            strings.TrimSpace(hit.ID),
			OpenMessageID:  strings.TrimSpace(doc.OpenMessageID),
			OpenChatID:     strings.TrimSpace(doc.OpenChatID),
			ActionName:     strings.TrimSpace(doc.ActionName),
			ActionValue:    maps.Clone(doc.ActionValue),
			OpenID:         strings.TrimSpace(doc.OpenID),
			UserName:       strings.TrimSpace(doc.UserName),
			CreateTime:     strings.TrimSpace(doc.CreateTime),
			CreateTimeUnix: doc.CreateTimeUnix,
		})
	}
	if len(records) == 0 {
		return nil, "暂无操作记录"
	}
	return records, ""
}

func cardActionHistorySearchIndex(index string) string {
	index = strings.TrimSpace(index)
	if index == "" || strings.Contains(index, "*") {
		return index
	}
	return index + "*"
}

func mergeCardActionHistoryRecords(records, pending []CardActionHistoryRecord, limit int) []CardActionHistoryRecord {
	if len(records) == 0 && len(pending) == 0 {
		return nil
	}
	merged := make([]CardActionHistoryRecord, 0, len(records)+len(pending))
	seen := make(map[string]struct{}, len(records)+len(pending))
	appendRecord := func(record CardActionHistoryRecord) {
		record = normalizeCardActionHistoryRecord(record)
		key := cardActionHistoryRecordKey(record)
		if key != "" {
			if _, ok := seen[key]; ok {
				return
			}
			seen[key] = struct{}{}
		}
		merged = append(merged, record)
	}
	for _, record := range pending {
		appendRecord(record)
	}
	for _, record := range records {
		appendRecord(record)
	}
	slices.SortFunc(merged, func(a, b CardActionHistoryRecord) int {
		switch {
		case a.CreateTimeUnix > b.CreateTimeUnix:
			return -1
		case a.CreateTimeUnix < b.CreateTimeUnix:
			return 1
		case a.CreateTime > b.CreateTime:
			return -1
		case a.CreateTime < b.CreateTime:
			return 1
		default:
			return strings.Compare(cardActionHistoryRecordKey(a), cardActionHistoryRecordKey(b))
		}
	})
	if limit > 0 && len(merged) > limit {
		merged = merged[:limit]
	}
	return merged
}

func normalizeCardActionHistoryRecord(record CardActionHistoryRecord) CardActionHistoryRecord {
	record.Key = strings.TrimSpace(record.Key)
	record.OpenMessageID = strings.TrimSpace(record.OpenMessageID)
	record.OpenChatID = strings.TrimSpace(record.OpenChatID)
	record.ActionName = strings.TrimSpace(record.ActionName)
	record.OpenID = strings.TrimSpace(record.OpenID)
	record.UserName = strings.TrimSpace(record.UserName)
	record.CreateTime = strings.TrimSpace(record.CreateTime)
	if record.CreateTime == "" && record.CreateTimeUnix > 0 {
		record.CreateTime = formatCardActionTime("", record.CreateTimeUnix)
	}
	return record
}

func cardActionHistoryRecordKey(record CardActionHistoryRecord) string {
	if key := strings.TrimSpace(record.Key); key != "" {
		return key
	}
	if record.OpenMessageID == "" && record.ActionName == "" && record.OpenID == "" && record.CreateTime == "" {
		return ""
	}
	return strings.Join([]string{
		record.OpenMessageID,
		record.ActionName,
		record.OpenID,
		record.CreateTime,
		strconv.FormatInt(record.CreateTimeUnix, 10),
	}, "|")
}

func buildCardActionHistoryRow(record CardActionHistoryRecord) map[string]any {
	label, detail := describeCardAction(record.ActionName, record.ActionValue)
	content := "**" + label + "**"
	if detail != "" {
		content += " · " + detail
	}
	return ColumnSet([]any{
		Column([]any{Markdown(content)}, ColumnOptions{
			Width:         "weighted",
			Weight:        4,
			VerticalAlign: "center",
		}),
		Column([]any{cardActionActorElement(record)}, ColumnOptions{
			Width:         "auto",
			VerticalAlign: "center",
		}),
		Column([]any{TextDiv(compactCardActionTime(record), CardTextOptions{
			Size:  "notation",
			Color: "grey",
			Align: "right",
		})}, ColumnOptions{
			Width:         "auto",
			VerticalAlign: "center",
		}),
	}, ColumnSetOptions{
		HorizontalSpacing: "8px",
		FlexMode:          "none",
	})
}

func cardActionActorElement(record CardActionHistoryRecord) map[string]any {
	record = normalizeCardActionHistoryRecord(record)
	if record.OpenID != "" {
		showAvatar := true
		showName := true
		return Person(record.OpenID, PersonOptions{
			Size:       "extra_small",
			ShowAvatar: &showAvatar,
			ShowName:   &showName,
			Style:      "capsule",
		})
	}
	name := record.UserName
	if name == "" {
		name = "-"
	}
	return TextDiv(name, CardTextOptions{
		Size:  "notation",
		Color: "grey",
		Align: "left",
	})
}

func compactCardActionTime(record CardActionHistoryRecord) string {
	record = normalizeCardActionHistoryRecord(record)
	if record.CreateTimeUnix > 0 {
		return utils.Epo2DateZoneMicro(record.CreateTimeUnix, utils.UTC8Loc(), "01-02 15:04:05")
	}
	if len(record.CreateTime) >= len("2006-01-02 15:04:05") {
		return record.CreateTime[5:]
	}
	if record.CreateTime == "" {
		return "-"
	}
	return record.CreateTime
}

func describeCardAction(actionName string, value map[string]any) (string, string) {
	switch strings.TrimSpace(actionName) {
	case cardactionproto.ActionConfigViewScope:
		return "刷新配置", codeDetail(stringValueFromMap(value, cardactionproto.ScopeField))
	case cardactionproto.ActionConfigSet:
		return "更新配置", joinCodeDetails(
			stringValueFromMap(value, cardactionproto.KeyField),
			stringValueFromMap(value, cardactionproto.ScopeField),
		)
	case cardactionproto.ActionConfigDelete:
		return "恢复默认配置", joinCodeDetails(
			stringValueFromMap(value, cardactionproto.KeyField),
			stringValueFromMap(value, cardactionproto.ScopeField),
		)
	case cardactionproto.ActionFeatureView:
		return "刷新功能开关", ""
	case cardactionproto.ActionFeatureBlockChat:
		return "屏蔽功能", joinCodeDetails(stringValueFromMap(value, cardactionproto.FeatureField), "chat")
	case cardactionproto.ActionFeatureUnblockChat:
		return "取消屏蔽功能", joinCodeDetails(stringValueFromMap(value, cardactionproto.FeatureField), "chat")
	case cardactionproto.ActionFeatureBlockUser:
		return "屏蔽功能", joinCodeDetails(stringValueFromMap(value, cardactionproto.FeatureField), "user")
	case cardactionproto.ActionFeatureUnblockUser:
		return "取消屏蔽功能", joinCodeDetails(stringValueFromMap(value, cardactionproto.FeatureField), "user")
	case cardactionproto.ActionFeatureBlockChatUser:
		return "屏蔽功能", joinCodeDetails(stringValueFromMap(value, cardactionproto.FeatureField), "chat_user")
	case cardactionproto.ActionFeatureUnblockChatUser:
		return "取消屏蔽功能", joinCodeDetails(stringValueFromMap(value, cardactionproto.FeatureField), "chat_user")
	case cardactionproto.ActionPermissionView:
		target := previewCardActionID(stringValueFromMap(value, cardactionproto.TargetUserIDField))
		if target == "" {
			return "查看权限", ""
		}
		return "查看权限", codeDetail(target)
	case cardactionproto.ActionPermissionGrant:
		return "授予权限", joinCodeDetails(
			permissionActionDetail(value),
			previewCardActionID(stringValueFromMap(value, cardactionproto.TargetUserIDField)),
		)
	case cardactionproto.ActionPermissionRevoke:
		return "回收权限", joinCodeDetails(
			permissionActionDetail(value),
			previewCardActionID(stringValueFromMap(value, cardactionproto.TargetUserIDField)),
		)
	case cardactionproto.ActionRateLimitView:
		return "刷新频控", codeDetail(previewCardActionID(stringValueFromMap(value, cardactionproto.ChatIDField)))
	case cardactionproto.ActionScheduleView:
		return "刷新 Schedule", scheduleActionDetail(value)
	case cardactionproto.ActionSchedulePause:
		return "暂停 Schedule", codeDetail(previewCardActionID(stringValueFromMap(value, cardactionproto.IDField)))
	case cardactionproto.ActionScheduleResume:
		return "恢复 Schedule", codeDetail(previewCardActionID(stringValueFromMap(value, cardactionproto.IDField)))
	case cardactionproto.ActionScheduleDelete:
		return "删除 Schedule", codeDetail(previewCardActionID(stringValueFromMap(value, cardactionproto.IDField)))
	case cardactionproto.ActionCardWithdraw:
		return "撤回卡片", ""
	default:
		if actionName == "" {
			return "未知操作", ""
		}
		return actionName, ""
	}
}

func permissionActionDetail(value map[string]any) string {
	point := strings.TrimSpace(stringValueFromMap(value, cardactionproto.PermissionPointField))
	scope := strings.TrimSpace(stringValueFromMap(value, cardactionproto.ScopeField))
	if point == "" {
		return ""
	}
	if scope == "" {
		return point
	}
	return point + "@" + scope
}

func scheduleActionDetail(value map[string]any) string {
	if id := previewCardActionID(stringValueFromMap(value, cardactionproto.IDField)); id != "" {
		return codeDetail(id)
	}
	parts := make([]string, 0, 3)
	if status := strings.TrimSpace(stringValueFromMap(value, "schedule_view_status")); status != "" {
		parts = append(parts, status)
	}
	if creator := previewCardActionID(stringValueFromMap(value, "schedule_view_creator_open_id")); creator != "" {
		parts = append(parts, creator)
	}
	if name := strings.TrimSpace(stringValueFromMap(value, "schedule_view_name")); name != "" {
		parts = append(parts, name)
	}
	return joinCodeDetails(parts...)
}

func joinCodeDetails(values ...string) string {
	parts := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		parts = append(parts, codeDetail(value))
	}
	return strings.Join(parts, " ")
}

func codeDetail(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	return "`" + strings.ReplaceAll(value, "`", "'") + "`"
}

func stringValueFromMap(values map[string]any, key string) string {
	if len(values) == 0 {
		return ""
	}
	raw, ok := values[key]
	if !ok || raw == nil {
		return ""
	}
	switch value := raw.(type) {
	case string:
		return strings.TrimSpace(value)
	default:
		return strings.TrimSpace(fmt.Sprintf("%v", raw))
	}
}

func previewCardActionID(value string) string {
	value = strings.TrimSpace(value)
	if len(value) <= 24 {
		return value
	}
	return value[:24]
}

func parseInt64(raw string) int64 {
	value, _ := strconv.ParseInt(strings.TrimSpace(raw), 10, 64)
	return value
}

func formatCardActionTime(raw string, unixMicros int64) string {
	raw = strings.TrimSpace(raw)
	switch {
	case raw != "":
		return utils.EpoMicro2DateStr(raw)
	case unixMicros > 0:
		return utils.Epo2DateZoneMicro(unixMicros, utils.UTC8Loc(), "2006-01-02 15:04:05")
	default:
		return ""
	}
}
