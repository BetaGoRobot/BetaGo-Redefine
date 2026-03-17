package carddebug

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	appconfig "github.com/BetaGoRobot/BetaGo-Redefine/internal/application/config"
	appbotidentity "github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/botidentity"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/cardregression"
	appratelimit "github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/ratelimit"
	scheduleapp "github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/schedule"
	apppermission "github.com/BetaGoRobot/BetaGo-Redefine/internal/application/permission"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/db"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/db/model"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/lark_dal/larkmsg"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/lark_dal/larkmsg/larktpl"
	scheduleinfra "github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/schedule"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/vadvisor"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/xmodel"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/utils"
	"github.com/bytedance/sonic"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
)

const (
	SpecConfig          = "config"
	SpecFeature         = "feature"
	SpecPermission      = "permission"
	SpecRateLimit       = "ratelimit"
	SpecRateLimitSample = "ratelimit.sample"
	SpecScheduleList    = "schedule.list"
	SpecScheduleTask    = "schedule.task"
	SpecScheduleSample  = "schedule.sample"
	SpecWordCountSample = "wordcount.sample"
	SpecChunkSample     = "chunk.sample"
)

type BuiltCardMode = cardregression.BuiltCardMode

const (
	BuiltCardModeTemplate = cardregression.BuiltCardModeTemplate
	BuiltCardModeCardJSON = cardregression.BuiltCardModeCardJSON
)

type ReceiveTarget = cardregression.ReceiveTarget

type SpecInfo struct {
	Name        string
	Description string
}

type TemplateInfo struct {
	Name string
	ID   string
}

type BuildRequest struct {
	Spec         string
	Template     string
	VarsJSON     string
	ChatID       string
	ID           string
	ActorOpenID  string
	TargetOpenID string
	Scope        string
	Case         string
}

type BuiltCard = cardregression.BuiltCard

type sceneAlias struct {
	SceneKey    string
	DefaultCase string
}

var regressionRegistry = cardregression.DefaultRegistry()

var legacySceneAliases = map[string]sceneAlias{
	SpecConfig:          {SceneKey: "config.list", DefaultCase: "live-default"},
	SpecFeature:         {SceneKey: "feature.list", DefaultCase: "live-default"},
	SpecPermission:      {SceneKey: "permission.manage", DefaultCase: "live-default"},
	SpecRateLimit:       {SceneKey: "ratelimit.stats", DefaultCase: "live-default"},
	SpecRateLimitSample: {SceneKey: "ratelimit.stats", DefaultCase: "smoke-default"},
	SpecScheduleList:    {SceneKey: "schedule.list", DefaultCase: "live-default"},
	SpecScheduleSample:  {SceneKey: "schedule.list", DefaultCase: "smoke-default"},
	SpecScheduleTask:    {SceneKey: "schedule.query", DefaultCase: "live-default"},
	SpecWordCountSample: {SceneKey: "wordcount.chunks", DefaultCase: "sample-default"},
	SpecChunkSample:     {SceneKey: "wordchunk.detail", DefaultCase: "sample-default"},
}

func ResolveReceiveTarget(toChatID, toOpenID, fallbackChatID string) (ReceiveTarget, error) {
	if openID := strings.TrimSpace(toOpenID); openID != "" {
		return ReceiveTarget{
			ReceiveIDType: larkim.ReceiveIdTypeOpenId,
			ReceiveID:     openID,
		}, nil
	}
	if chatID := strings.TrimSpace(toChatID); chatID != "" {
		return ReceiveTarget{
			ReceiveIDType: larkim.ReceiveIdTypeChatId,
			ReceiveID:     chatID,
		}, nil
	}
	if chatID := strings.TrimSpace(fallbackChatID); chatID != "" {
		return ReceiveTarget{
			ReceiveIDType: larkim.ReceiveIdTypeChatId,
			ReceiveID:     chatID,
		}, nil
	}
	return ReceiveTarget{}, fmt.Errorf("missing send target: provide to_open_id or to_chat_id")
}

func ListSpecs() []SpecInfo {
	specs := []SpecInfo{
		{Name: SpecConfig, Description: "构建当前群聊/用户上下文的配置管理卡"},
		{Name: SpecFeature, Description: "构建当前群聊/用户上下文的功能开关卡"},
		{Name: SpecPermission, Description: "构建权限管理卡"},
		{Name: SpecRateLimit, Description: "构建指定 chat_id 的实时频控统计卡"},
		{Name: SpecRateLimitSample, Description: "构建带完整示例数据的频控统计卡"},
		{Name: SpecScheduleList, Description: "构建指定 chat_id 的实时 schedule 列表卡"},
		{Name: SpecScheduleTask, Description: "构建指定 schedule task_id 的查询卡"},
		{Name: SpecScheduleSample, Description: "构建带完整示例数据的 schedule 管理卡"},
		{Name: SpecWordCountSample, Description: "构建带词云/热点话题样例数据的词云卡"},
		{Name: SpecChunkSample, Description: "构建带样例摘要数据的 chunk 分析卡"},
	}
	sort.Slice(specs, func(i, j int) bool {
		return specs[i].Name < specs[j].Name
	})
	return specs
}

func ListTemplates() []TemplateInfo {
	templates := make([]TemplateInfo, 0, len(templateCatalog))
	for name, id := range templateCatalog {
		templates = append(templates, TemplateInfo{Name: name, ID: id})
	}
	sort.Slice(templates, func(i, j int) bool {
		return templates[i].Name < templates[j].Name
	})
	return templates
}

func ResolveTemplate(input string) (TemplateInfo, bool) {
	input = strings.TrimSpace(input)
	if input == "" {
		return TemplateInfo{}, false
	}
	if id, ok := templateCatalog[input]; ok {
		return TemplateInfo{Name: input, ID: id}, true
	}
	for name, id := range templateCatalog {
		if id == input {
			return TemplateInfo{Name: name, ID: id}, true
		}
	}
	return TemplateInfo{Name: input, ID: input}, false
}

func Build(ctx context.Context, req BuildRequest) (*BuiltCard, error) {
	if strings.TrimSpace(req.Template) != "" {
		return buildTemplateCard(ctx, req)
	}
	if built, ok, err := buildSceneCard(ctx, req); ok {
		return built, err
	}

	switch strings.TrimSpace(req.Spec) {
	case SpecConfig:
		return buildConfigCard(ctx, req)
	case SpecFeature:
		return buildFeatureCard(ctx, req)
	case SpecPermission:
		return buildPermissionCard(ctx, req)
	case SpecRateLimit:
		return buildRateLimitCard(ctx, req)
	case SpecRateLimitSample:
		return buildRateLimitSampleCard(ctx, req)
	case SpecScheduleList:
		return buildScheduleListCard(ctx, req)
	case SpecScheduleTask:
		return buildScheduleTaskCard(ctx, req)
	case SpecScheduleSample:
		return buildScheduleSampleCard(ctx, req)
	case SpecWordCountSample:
		return buildWordCountSampleCard(ctx, req)
	case SpecChunkSample:
		return buildChunkSampleCard(ctx, req)
	default:
		return nil, fmt.Errorf("unsupported card spec %q", strings.TrimSpace(req.Spec))
	}
}

func buildSceneCard(ctx context.Context, req BuildRequest) (*BuiltCard, bool, error) {
	sceneKey, caseName, ok := resolveSceneRequest(req.Spec, req.Case)
	if !ok || regressionRegistry == nil {
		return nil, false, nil
	}
	scene, ok := regressionRegistry.Get(sceneKey)
	if !ok {
		return nil, false, nil
	}
	selectedCase, err := resolveRegisteredCase(scene.TestCases(), caseName)
	if err != nil {
		return nil, true, err
	}
	built, err := scene.BuildTestCard(ctx, cardregression.TestCardBuildRequest{
		Business: cardregression.CardBusinessContext{
			ChatID:       strings.TrimSpace(req.ChatID),
			ActorOpenID:  strings.TrimSpace(req.ActorOpenID),
			TargetOpenID: strings.TrimSpace(req.TargetOpenID),
			Scope:        strings.TrimSpace(req.Scope),
			ObjectID:     strings.TrimSpace(req.ID),
		},
		Case:   selectedCase,
		DryRun: false,
	})
	return built, true, err
}

func resolveSceneRequest(spec, caseName string) (sceneKey, resolvedCase string, ok bool) {
	spec = strings.TrimSpace(spec)
	caseName = strings.TrimSpace(caseName)
	if spec == "" {
		return "", "", false
	}
	if alias, found := legacySceneAliases[spec]; found {
		return alias.SceneKey, firstNonEmpty(caseName, alias.DefaultCase), true
	}
	return spec, firstNonEmpty(caseName, "smoke-default"), true
}

func resolveRegisteredCase(cases []cardregression.CardRegressionCase, caseName string) (cardregression.CardRegressionCase, error) {
	caseName = strings.TrimSpace(caseName)
	if caseName == "" {
		caseName = "smoke-default"
	}
	for _, c := range cases {
		if strings.TrimSpace(c.Name) == caseName {
			return c, nil
		}
	}
	return cardregression.CardRegressionCase{}, fmt.Errorf("case %q not found for registered scene", caseName)
}

func buildTemplateCard(ctx context.Context, req BuildRequest) (*BuiltCard, error) {
	info, _ := ResolveTemplate(req.Template)
	vars := make(map[string]any)
	if strings.TrimSpace(req.VarsJSON) != "" {
		if err := sonic.UnmarshalString(req.VarsJSON, &vars); err != nil {
			return nil, fmt.Errorf("parse template vars: %w", err)
		}
	}

	card := larktpl.NewCardContent(ctx, info.ID)
	card.UpdateVariables(vars)
	return &BuiltCard{
		Mode:         BuiltCardModeTemplate,
		Label:        "template:" + firstNonEmpty(info.Name, info.ID),
		TemplateID:   info.ID,
		TemplateName: info.Name,
		TemplateCard: card,
	}, nil
}

func buildConfigCard(ctx context.Context, req BuildRequest) (*BuiltCard, error) {
	chatID := requireValue("chat_id", req.ChatID)
	if chatID == "" {
		return nil, fmt.Errorf("chat_id is required for %s", SpecConfig)
	}
	actorOpenID := requireValue("actor_open_id", req.ActorOpenID)
	if actorOpenID == "" {
		return nil, fmt.Errorf("actor_open_id is required for %s", SpecConfig)
	}
	scope := strings.TrimSpace(req.Scope)
	if scope == "" {
		scope = "chat"
	}
	card, err := appconfig.BuildConfigCardJSON(ctx, scope, chatID, actorOpenID)
	if err != nil {
		return nil, err
	}
	return &BuiltCard{
		Mode:     BuiltCardModeCardJSON,
		Label:    SpecConfig,
		CardJSON: card,
	}, nil
}

func buildFeatureCard(ctx context.Context, req BuildRequest) (*BuiltCard, error) {
	chatID := requireValue("chat_id", req.ChatID)
	if chatID == "" {
		return nil, fmt.Errorf("chat_id is required for %s", SpecFeature)
	}
	actorOpenID := requireValue("actor_open_id", req.ActorOpenID)
	if actorOpenID == "" {
		return nil, fmt.Errorf("actor_open_id is required for %s", SpecFeature)
	}
	card, err := appconfig.BuildFeatureCard(ctx, chatID, actorOpenID)
	if err != nil {
		return nil, err
	}
	return &BuiltCard{
		Mode:     BuiltCardModeCardJSON,
		Label:    SpecFeature,
		CardJSON: map[string]any(card),
	}, nil
}

func buildPermissionCard(ctx context.Context, req BuildRequest) (*BuiltCard, error) {
	chatID := requireValue("chat_id", req.ChatID)
	if chatID == "" {
		return nil, fmt.Errorf("chat_id is required for %s", SpecPermission)
	}
	actorOpenID := requireValue("actor_open_id", req.ActorOpenID)
	if actorOpenID == "" {
		return nil, fmt.Errorf("actor_open_id is required for %s", SpecPermission)
	}
	targetOpenID := strings.TrimSpace(firstNonEmpty(req.TargetOpenID, actorOpenID))
	card, err := apppermission.BuildPermissionCardJSON(ctx, chatID, actorOpenID, targetOpenID)
	if err != nil {
		return nil, err
	}
	return &BuiltCard{
		Mode:     BuiltCardModeCardJSON,
		Label:    SpecPermission,
		CardJSON: card,
	}, nil
}

func buildRateLimitCard(ctx context.Context, req BuildRequest) (*BuiltCard, error) {
	chatID := requireValue("chat_id", req.ChatID)
	if chatID == "" {
		return nil, fmt.Errorf("chat_id is required for %s", SpecRateLimit)
	}
	card, err := appratelimit.BuildStatsCardJSON(ctx, chatID)
	if err != nil {
		return nil, err
	}
	return &BuiltCard{
		Mode:     BuiltCardModeCardJSON,
		Label:    SpecRateLimit,
		CardJSON: card,
	}, nil
}

func buildRateLimitSampleCard(ctx context.Context, req BuildRequest) (*BuiltCard, error) {
	chatID := strings.TrimSpace(firstNonEmpty(req.ChatID, "oc_sample_debug_chat"))
	card := appratelimit.BuildStatsCardJSONFromData(ctx, buildSampleStatsCardData(chatID))
	return &BuiltCard{
		Mode:     BuiltCardModeCardJSON,
		Label:    SpecRateLimitSample,
		CardJSON: card,
	}, nil
}

func buildScheduleSampleCard(ctx context.Context, req BuildRequest) (*BuiltCard, error) {
	card := scheduleapp.BuildTaskListCard(
		ctx,
		"Schedule 示例",
		buildSampleScheduleTasks(req),
		scheduleapp.NewTaskListCardView(10),
	)
	return &BuiltCard{
		Mode:     BuiltCardModeCardJSON,
		Label:    SpecScheduleSample,
		CardJSON: map[string]any(card),
	}, nil
}

func buildScheduleListCard(ctx context.Context, req BuildRequest) (*BuiltCard, error) {
	chatID := requireValue("chat_id", req.ChatID)
	if chatID == "" {
		return nil, fmt.Errorf("chat_id is required for %s", SpecScheduleList)
	}
	repo, err := newScheduleRepository()
	if err != nil {
		return nil, err
	}
	tasks, err := repo.ListTasksByChatID(ctx, chatID, 50, 0)
	if err != nil {
		return nil, err
	}
	view := scheduleapp.NewTaskListCardView(50)
	card := scheduleapp.BuildTaskListCard(ctx, view.Title(), tasks, view)
	return &BuiltCard{
		Mode:     BuiltCardModeCardJSON,
		Label:    SpecScheduleList,
		CardJSON: map[string]any(card),
	}, nil
}

func buildScheduleTaskCard(ctx context.Context, req BuildRequest) (*BuiltCard, error) {
	taskID := strings.TrimSpace(req.ID)
	if taskID == "" {
		return nil, fmt.Errorf("id is required for %s", SpecScheduleTask)
	}
	repo, err := newScheduleRepository()
	if err != nil {
		return nil, err
	}
	task, err := repo.GetTaskByID(ctx, taskID)
	if err != nil {
		return nil, err
	}
	view := scheduleapp.NewTaskQueryCardView(taskID, scheduleapp.TaskQuery{}, 100)
	card := scheduleapp.BuildTaskListCard(ctx, view.Title(), []*model.ScheduledTask{task}, view)
	return &BuiltCard{
		Mode:     BuiltCardModeCardJSON,
		Label:    SpecScheduleTask,
		CardJSON: map[string]any(card),
	}, nil
}

func buildWordCountSampleCard(ctx context.Context, req BuildRequest) (*BuiltCard, error) {
	if err := ensureDBAvailable(SpecWordCountSample); err != nil {
		return nil, err
	}
	tpl := larktpl.GetTemplateV2[larktpl.WordCountCardVars[xmodel.MessageChunkLogV3]](ctx, larktpl.WordCountTemplate)
	tpl.WithData(buildSampleWordCountCardData())
	card := larktpl.NewCardContentV2(ctx, tpl)
	return &BuiltCard{
		Mode:         BuiltCardModeTemplate,
		Label:        SpecWordCountSample,
		TemplateID:   larktpl.WordCountTemplate,
		TemplateName: "WordCountTemplate",
		TemplateCard: card,
	}, nil
}

func buildChunkSampleCard(ctx context.Context, req BuildRequest) (*BuiltCard, error) {
	if err := ensureDBAvailable(SpecChunkSample); err != nil {
		return nil, err
	}
	tpl := larktpl.GetTemplateV2[larktpl.ChunkMetaData](ctx, larktpl.ChunkMetaTemplate)
	tpl.WithData(buildSampleChunkMetaData())
	card := larktpl.NewCardContentV2(ctx, tpl)
	return &BuiltCard{
		Mode:         BuiltCardModeTemplate,
		Label:        SpecChunkSample,
		TemplateID:   larktpl.ChunkMetaTemplate,
		TemplateName: "ChunkMetaTemplate",
		TemplateCard: card,
	}, nil
}

func buildSampleStatsCardData(chatID string) *appratelimit.StatsCardData {
	now := time.Now().In(utils.UTC8Loc())
	return &appratelimit.StatsCardData{
		ChatID:         chatID,
		Status:         "冷却中",
		StatusDetail:   "剩余 18 秒",
		OverviewTitle:  "发送压力偏高",
		OverviewDetail: "当前卡片使用的是样例数据，便于在没有真实消息流量时也能直接验证版式与字段密度。",
		OverviewColor:  "red",
		HeroMetrics: []appratelimit.StatsCardMetric{
			{Label: "当前状态", Value: "冷却中", Note: "剩余 18 秒", ValueColor: "red", BackgroundStyle: "grey"},
			{Label: "冷却等级", Value: "3", Note: "已触发连续退避", ValueColor: "orange", BackgroundStyle: "grey"},
			{Label: "拒绝率", Value: "32.4%", Note: "通过率 67.6%", ValueColor: "orange", BackgroundStyle: "grey"},
		},
		SummaryMetrics: []appratelimit.StatsCardMetric{
			{Label: "历史总发送", Value: "842", Note: "累计发送次数", ValueColor: "blue", BackgroundStyle: "grey"},
			{Label: "近24小时", Value: "119", Note: "较昨日高 14%", ValueColor: "orange", BackgroundStyle: "grey"},
			{Label: "近1小时", Value: "27", Note: "峰值时段", ValueColor: "red", BackgroundStyle: "grey"},
			{Label: "活跃度评分", Value: "8.40", Note: "群内互动高", ValueColor: "orange", BackgroundStyle: "grey"},
			{Label: "爆发因子", Value: "2.70", Note: "存在短时集中发送", ValueColor: "red", BackgroundStyle: "grey"},
			{Label: "更新时间", Value: now.Format("15:04:05"), Note: "示例快照生成时间", ValueColor: "grey", BackgroundStyle: "grey"},
		},
		HasDiagnostics: true,
		DiagnosticMetrics: []appratelimit.StatsCardMetric{
			{Label: "检查次数", Value: "311", Note: "进入频控判定", ValueColor: "blue", BackgroundStyle: "grey"},
			{Label: "通过次数", Value: "210", ValueColor: "green", BackgroundStyle: "grey"},
			{Label: "拒绝次数", Value: "101", ValueColor: "red", BackgroundStyle: "grey"},
			{Label: "实际发送", Value: "198", Note: "最终真正发出的消息数", ValueColor: "blue", BackgroundStyle: "grey"},
			{Label: "拒绝率", Value: "32.4%", ValueColor: "orange", BackgroundStyle: "grey"},
			{Label: "通过率", Value: "67.6%", ValueColor: "green", BackgroundStyle: "grey"},
			{Label: "冷却中", Value: "是", ValueColor: "red", BackgroundStyle: "grey"},
			{Label: "最后更新", Value: now.Format("15:04:05"), Note: "Metrics 最近刷新时间", ValueColor: "grey", BackgroundStyle: "grey"},
		},
		RecentSendRecords: []appratelimit.StatsCardRecentSend{
			{Trigger: "command", Time: now.Add(-8 * time.Minute).Format("15:04:05")},
			{Trigger: "reply", Time: now.Add(-6 * time.Minute).Format("15:04:05")},
			{Trigger: "schedule", Time: now.Add(-4 * time.Minute).Format("15:04:05")},
			{Trigger: "tool", Time: now.Add(-2 * time.Minute).Format("15:04:05")},
			{Trigger: "card.refresh", Time: now.Add(-1 * time.Minute).Format("15:04:05")},
		},
		UpdatedAt: now.Format("15:04:05"),
	}
}

func buildSampleScheduleTasks(req BuildRequest) []*model.ScheduledTask {
	now := time.Now().In(utils.UTC8Loc())
	chatID := strings.TrimSpace(firstNonEmpty(req.ChatID, "oc_sample_debug_chat"))
	creatorOpenID := strings.TrimSpace(firstNonEmpty(req.ActorOpenID, req.TargetOpenID, "ou_sample_creator"))

	onceRunAt := now.Add(20 * time.Minute)
	onceLastRunAt := now.Add(-2 * time.Hour)
	cronNextRunAt := now.Add(47 * time.Minute)
	cronLastRunAt := now.Add(-23 * time.Hour)

	onceTask := &model.ScheduledTask{
		ID:              "20260312093000-debugA",
		Name:            "午休提醒",
		Type:            model.ScheduleTaskTypeOnce,
		ChatID:          chatID,
		CreatorID:       creatorOpenID,
		ToolName:        "send_message",
		ToolArgs:        `{"message":"记得午休 20 分钟"}`,
		RunAt:           &onceRunAt,
		Timezone:        "Asia/Shanghai",
		Status:          model.ScheduleTaskStatusEnabled,
		NotifyOnError:   true,
		NotifyResult:    false,
		LastRunAt:       &onceLastRunAt,
		NextRunAt:       onceRunAt,
		LastResult:      "已发送提醒消息，用户点击了卡片中的确认按钮。",
		RunCount:        2,
		CreatedAt:       now.Add(-48 * time.Hour),
		UpdatedAt:       now.Add(-6 * time.Minute),
		AppID:           "cli_sample_debug",
		BotOpenID:       "ou_sample_bot",
		SourceMessageID: "om_sample_source_1",
	}
	cronTask := &model.ScheduledTask{
		ID:              "20260312094500-debugB",
		Name:            "日报汇总",
		Type:            model.ScheduleTaskTypeCron,
		ChatID:          chatID,
		CreatorID:       creatorOpenID,
		ToolName:        "report_daily",
		ToolArgs:        `{"channel":"ops","format":"markdown"}`,
		CronExpr:        "0 18 * * *",
		Timezone:        "Asia/Shanghai",
		Status:          model.ScheduleTaskStatusPaused,
		NotifyOnError:   true,
		NotifyResult:    true,
		LastRunAt:       &cronLastRunAt,
		NextRunAt:       cronNextRunAt,
		LastError:       "上次执行时下游报表服务超时，已自动暂停等待人工确认。",
		RunCount:        14,
		CreatedAt:       now.Add(-7 * 24 * time.Hour),
		UpdatedAt:       now.Add(-28 * time.Minute),
		AppID:           "cli_sample_debug",
		BotOpenID:       "ou_sample_bot",
		SourceMessageID: "om_sample_source_2",
	}
	return []*model.ScheduledTask{onceTask, cronTask}
}

func buildSampleWordCountCardData() *larktpl.WordCountCardVars[xmodel.MessageChunkLogV3] {
	now := time.Now().In(utils.UTC8Loc())
	wordCloud := vadvisor.NewWordCloudChartsGraphWithPlayer[string, int]()
	wordCloud.
		AddData("word_cloud",
			&vadvisor.ValueUnit[string, int]{XField: "BetaGo", YField: 42, SeriesField: "42"},
			&vadvisor.ValueUnit[string, int]{XField: "卡片", YField: 37, SeriesField: "37"},
			&vadvisor.ValueUnit[string, int]{XField: "调试", YField: 34, SeriesField: "34"},
			&vadvisor.ValueUnit[string, int]{XField: "飞书", YField: 28, SeriesField: "28"},
			&vadvisor.ValueUnit[string, int]{XField: "重构", YField: 21, SeriesField: "21"},
		).
		Build(context.Background())

	return &larktpl.WordCountCardVars[xmodel.MessageChunkLogV3]{
		UserList: []*larktpl.UserListItem{
			{Number: 1, User: []*larktpl.UserUnit{{ID: "ou_sample_alice"}}, MsgCnt: 56, ActionCnt: 14},
			{Number: 2, User: []*larktpl.UserUnit{{ID: "ou_sample_bob"}}, MsgCnt: 41, ActionCnt: 9},
			{Number: 3, User: []*larktpl.UserUnit{{ID: "ou_sample_cathy"}}, MsgCnt: 33, ActionCnt: 6},
		},
		WordCloud: wordCloud,
		Chunks: []*larktpl.ChunkData[xmodel.MessageChunkLogV3]{
			{
				ChunkLog: &xmodel.MessageChunkLogV3{
					Summary: "大家在讨论 schema v2 卡片统一 footer、refresh payload，以及如何给开发过程补一条直接发卡的调试链路。",
					Intent:  larkmsg.TagText("共商议事", "blue"),
				},
				Sentiment:           larkmsg.TagText("正向", "green"),
				Tones:               strings.Join([]string{larkmsg.TagText("寻根究底", "purple"), larkmsg.TagText("暖心慰藉", "turquoise")}, ""),
				UserIDs4Lark:        []*larktpl.UserUnit{{ID: "ou_sample_alice"}, {ID: "ou_sample_bob"}},
				UnresolvedQuestions: strings.Join([]string{larkmsg.TagText("是否要把更多业务卡补成 spec？", "red"), larkmsg.TagText("skill 是否需要默认 dry-run？", "red")}, ""),
			},
			{
				ChunkLog: &xmodel.MessageChunkLogV3{
					Summary: "另一段讨论聚焦在 schedule 管理卡的可回放视图，以及卡片动作如何在多人协作时保留 last modifier 语义。",
					Intent:  larkmsg.TagText("明辨事理", "indigo"),
				},
				Sentiment:           larkmsg.TagText("中性", "blue"),
				Tones:               larkmsg.TagText("严谨庄重", "indigo"),
				UserIDs4Lark:        []*larktpl.UserUnit{{ID: "ou_sample_cathy"}},
				UnresolvedQuestions: larkmsg.TagText("是否需要补 schedule.task 的 CLI 参数？", "red"),
			},
		},
		TimeStamp: now.Format(time.DateTime),
		StartTime: now.Add(-7 * 24 * time.Hour).Format("2006-01-02 15:04"),
		EndTime:   now.Format("2006-01-02 15:04"),
	}
}

func buildSampleChunkMetaData() *larktpl.ChunkMetaData {
	now := time.Now().In(utils.UTC8Loc())
	return &larktpl.ChunkMetaData{
		Summary:      "BetaGo 团队正在讨论如何把现有飞书卡片收口成统一调试入口，并让开发代理在任务执行过程中直接把卡片发到测试账号。",
		Intent:       "共商议事",
		Participants: []*larktpl.User{{UserID: "ou_sample_alice"}, {UserID: "ou_sample_bob"}, {UserID: "ou_sample_cathy"}},
		Sentiment:    "正向",
		Tones:        []*larktpl.ToneData{{Tone: "理性"}, {Tone: "积极"}},
		Questions: []*larktpl.Questions{
			{Question: "哪些卡片值得优先做成 spec？"},
			{Question: "目标账号和业务上下文是否应该拆开传？"},
		},
		MsgList: []*larktpl.MsgLine{
			{Time: now.Add(-12 * time.Minute).Format(time.DateTime), User: &larktpl.User{UserID: "ou_sample_alice"}, Content: "我们先把统一的发卡通道做出来，不要每种卡片各搞一套。"},
			{Time: now.Add(-9 * time.Minute).Format(time.DateTime), User: &larktpl.User{UserID: "ou_sample_bob"}, Content: "同意，至少要支持 open_id 直发，不然开发态很难验收。"},
			{Time: now.Add(-4 * time.Minute).Format(time.DateTime), User: &larktpl.User{UserID: "ou_sample_cathy"}, Content: "另外把 schedule、ratelimit、permission 这些复杂卡先收进 spec。"},
		},
		MainTopicsOrActivities:         []*larktpl.ObjTextArray{{Text: "卡片调试入口"}, {Text: "spec 设计"}},
		KeyConceptsAndNouns:            []*larktpl.ObjTextArray{{Text: "schema v2"}, {Text: "open_id"}, {Text: "refresh payload"}},
		MentionedGroupsOrOrganizations: []*larktpl.ObjTextArray{{Text: "BetaGo"}},
		MentionedPeople:                []*larktpl.ObjTextArray{{Text: "Alice"}, {Text: "Bob"}, {Text: "Cathy"}},
		LocationsAndVenues:             []*larktpl.ObjTextArray{{Text: "飞书群聊"}},
		MediaAndWorks:                  []*larktpl.MediaAndWork{{Title: "Schedule 卡片", Type: "管理面板"}},
		Timestamp:                      now.Format(time.DateTime),
		MsgID:                          "om_sample_chunk_debug",
	}
}

func newScheduleRepository() (*scheduleinfra.Repository, error) {
	if err := ensureDBAvailable("schedule"); err != nil {
		return nil, err
	}
	identity := appbotidentity.Current()
	if err := identity.Validate(); err != nil {
		return nil, err
	}
	return scheduleinfra.NewRepository(db.DB(), identity), nil
}

func ensureDBAvailable(spec string) error {
	if db.DB() == nil {
		return fmt.Errorf("db is required for %s", spec)
	}
	return nil
}

func requireValue(_ string, value string) string {
	return strings.TrimSpace(value)
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

var templateCatalog = map[string]string{
	"AlbumListTemplate":            larktpl.AlbumListTemplate,
	"ChunkMetaTemplate":            larktpl.ChunkMetaTemplate,
	"FourColSheetTemplate":         larktpl.FourColSheetTemplate,
	"FullLyricsTemplate":           larktpl.FullLyricsTemplate,
	"NormalCardGraphReplyTemplate": larktpl.NormalCardGraphReplyTemplate,
	"NormalCardReplyTemplate":      larktpl.NormalCardReplyTemplate,
	"SingleSongDetailTemplate":     larktpl.SingleSongDetailTemplate,
	"StreamingReasonTemplate":      larktpl.StreamingReasonTemplate,
	"ThreeColSheetTemplate":        larktpl.ThreeColSheetTemplate,
	"TwoColPicTemplate":            larktpl.TwoColPicTemplate,
	"TwoColSheetTemplate":          larktpl.TwoColSheetTemplate,
	"WordCountTemplate":            larktpl.WordCountTemplate,
}

func Send(ctx context.Context, target ReceiveTarget, built *BuiltCard) error {
	if err := target.Valid(); err != nil {
		return err
	}
	if built == nil {
		return fmt.Errorf("built card is nil")
	}
	switch built.Mode {
	case BuiltCardModeTemplate:
		if built.TemplateCard == nil {
			return fmt.Errorf("template card is nil")
		}
		return larkmsg.CreateMsgCardByReceiveID(ctx, built.TemplateCard, target.ReceiveIDType, target.ReceiveID)
	case BuiltCardModeCardJSON:
		if built.CardJSON == nil {
			return fmt.Errorf("card json is nil")
		}
		return larkmsg.CreateCardJSONByReceiveID(ctx, target.ReceiveIDType, target.ReceiveID, built.CardJSON, "", "_card_debug")
	default:
		return fmt.Errorf("unsupported built card mode %q", built.Mode)
	}
}
