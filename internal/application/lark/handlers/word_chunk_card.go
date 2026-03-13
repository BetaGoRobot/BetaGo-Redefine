package handlers

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	appconfig "github.com/BetaGoRobot/BetaGo-Redefine/internal/application/config"
	apphistory "github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/history"
	arktools "github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/ark_dal/tools"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/lark_dal/larkmsg"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/xmodel"
	cardactionproto "github.com/BetaGoRobot/BetaGo-Redefine/pkg/cardaction"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/utils"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/xcommand"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/xhandler"
	"github.com/defensestation/osquery"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
)

const (
	defaultWordChunkPage = 1

	wordChunkActionPageSizeField = "page_size"
)

type WordChunkDetailArgs struct {
	ID     string `json:"id"`
	ChatID string `json:"chat_id"`
}

type wordChunkDetailHandler struct{}

var WordChunkDetail wordChunkDetailHandler

type WordChunkCardBuildOptions struct {
	MessageID          string
	LastModifierOpenID string
	PendingHistory     []larkmsg.CardActionHistoryRecord
}

type wordChunkCardView struct {
	ChatID             string
	Start              time.Time
	End                time.Time
	Sort               WordCloudSortType
	Intent             WordChunkIntentType
	Sentiment          WordChunkSentimentType
	QuestionMode       WordChunkQuestionMode
	Page               int
	PageSize           int
	MessageID          string
	LastModifierOpenID string
	PendingHistory     []larkmsg.CardActionHistoryRecord
}

type wordChunkSearchResult struct {
	Items []*xmodel.MessageChunkLogV3
	Total int
}

type wordChunkDetailRequest struct {
	ID   string
	View wordChunkCardView
}

func (wordChunkDetailHandler) ParseCLI(args []string) (WordChunkDetailArgs, error) {
	argMap, _ := parseArgs(args...)
	return WordChunkDetailArgs{
		ID:     strings.TrimSpace(argMap["id"]),
		ChatID: strings.TrimSpace(argMap["chat_id"]),
	}, nil
}

func (wordChunkDetailHandler) ParseTool(raw string) (WordChunkDetailArgs, error) {
	parsed := WordChunkDetailArgs{}
	if err := utils.UnmarshalStringPre(raw, &parsed); err != nil {
		return WordChunkDetailArgs{}, err
	}
	parsed.ID = strings.TrimSpace(parsed.ID)
	parsed.ChatID = strings.TrimSpace(parsed.ChatID)
	return parsed, nil
}

func (wordChunkDetailHandler) ToolSpec() xcommand.ToolSpec {
	return xcommand.ToolSpec{
		Name: "word_chunk_detail_get",
		Desc: "查看单条群聊 chunk 的详情，包括摘要、标签、问题和消息片段",
		Params: arktools.NewParams("object").
			AddProp("id", &arktools.Prop{
				Type: "string",
				Desc: "chunk 的逻辑 ID",
			}).
			AddProp("chat_id", &arktools.Prop{
				Type: "string",
				Desc: "目标群聊 ID，不填则使用当前群聊",
			}).
			AddRequired("id"),
	}
}

func (wordChunkDetailHandler) Handle(ctx context.Context, data *larkim.P2MessageReceiveV1, metaData *xhandler.BaseMetaData, arg WordChunkDetailArgs) error {
	if strings.TrimSpace(arg.ID) == "" {
		return fmt.Errorf("id is required")
	}
	chatID := firstNonEmpty(arg.ChatID, currentChatID(data, metaData))
	if strings.TrimSpace(chatID) == "" {
		return fmt.Errorf("chat_id is required")
	}
	chunk, err := loadWordChunkByID(ctx, chatID, arg.ID)
	if err != nil {
		return err
	}
	messages, err := loadChunkMessages(ctx, chatID, chunk.MsgIDs)
	if err != nil {
		return err
	}
	view := normalizeWordChunkCardView(wordChunkCardView{
		ChatID:             chatID,
		MessageID:          currentMessageID(data),
		LastModifierOpenID: currentOpenID(data, metaData),
	})
	card := buildWordChunkDetailCard(ctx, view, chunk, messages)
	return sendCompatibleCardJSON(ctx, data, metaData, map[string]any(card), "_wordChunkDetail", false)
}

func (wordChunkDetailHandler) CommandDescription() string {
	return "查看单条 chunk 的详细内容"
}

func (wordChunkDetailHandler) CommandExamples() []string {
	return []string{
		"/wordcount chunk --id=9f35b54e-7af4-11ef-bbaa-acde48001122",
		"/wordcount chunk --id=9f35b54e-7af4-11ef-bbaa-acde48001122 --chat_id=oc_xxx",
	}
}

func BuildWordChunkViewCardPayload(ctx context.Context, parsed *cardactionproto.Parsed, fallbackChatID string, opts WordChunkCardBuildOptions) (map[string]any, error) {
	view, err := parseWordChunkCardView(parsed, fallbackChatID)
	if err != nil {
		return nil, err
	}
	view.MessageID = strings.TrimSpace(opts.MessageID)
	view.LastModifierOpenID = strings.TrimSpace(opts.LastModifierOpenID)
	view.PendingHistory = append([]larkmsg.CardActionHistoryRecord(nil), opts.PendingHistory...)

	result, view, err := loadWordChunkPage(ctx, view)
	if err != nil {
		return nil, err
	}
	card := buildWordChunkListCardWithState(ctx, view, result)
	return map[string]any(card), nil
}

func BuildWordChunkDetailCardPayload(ctx context.Context, parsed *cardactionproto.Parsed, fallbackChatID string, opts WordChunkCardBuildOptions) (map[string]any, error) {
	req, err := parseWordChunkDetailRequest(parsed, fallbackChatID)
	if err != nil {
		return nil, err
	}
	req.View.MessageID = strings.TrimSpace(opts.MessageID)
	req.View.LastModifierOpenID = strings.TrimSpace(opts.LastModifierOpenID)
	req.View.PendingHistory = append([]larkmsg.CardActionHistoryRecord(nil), opts.PendingHistory...)

	chunk, err := loadWordChunkByID(ctx, req.View.ChatID, req.ID)
	if err != nil {
		return nil, err
	}
	messages, err := loadChunkMessages(ctx, req.View.ChatID, chunk.MsgIDs)
	if err != nil {
		return nil, err
	}
	card := buildWordChunkDetailCard(ctx, req.View, chunk, messages)
	return map[string]any(card), nil
}

func BuildWordChunkViewValue(view wordChunkCardView) map[string]string {
	view = normalizeWordChunkCardView(view)
	builder := cardactionproto.New(cardactionproto.ActionWordChunksView).
		WithValue(cardactionproto.ChatIDField, view.ChatID).
		WithValue(cardactionproto.PageField, strconv.Itoa(view.Page)).
		WithValue(cardactionproto.PageSizeField, strconv.Itoa(view.PageSize)).
		WithValue("sort", string(view.Sort)).
		WithValue("intent", string(view.Intent)).
		WithValue("sentiment", string(view.Sentiment)).
		WithValue("question_mode", string(view.QuestionMode))
	if !view.Start.IsZero() {
		builder.WithValue("start_time", view.Start.Format(time.RFC3339))
	}
	if !view.End.IsZero() {
		builder.WithValue("end_time", view.End.Format(time.RFC3339))
	}
	return builder.Payload()
}

func BuildWordChunkDetailValue(chunkID string, view wordChunkCardView) map[string]string {
	payload := BuildWordChunkViewValue(view)
	payload[cardactionproto.ActionField] = cardactionproto.ActionWordChunkDetail
	payload[cardactionproto.IDField] = strings.TrimSpace(chunkID)
	return payload
}

func newWordChunkCardView(scope wordCountScope, query wordChunkQuery) wordChunkCardView {
	return normalizeWordChunkCardView(wordChunkCardView{
		ChatID:       scope.ChatID,
		Start:        scope.Start,
		End:          scope.End,
		Sort:         query.Sort,
		Intent:       query.Intent,
		Sentiment:    query.Sentiment,
		QuestionMode: query.QuestionMode,
		Page:         defaultWordChunkPage,
		PageSize:     query.Limit,
	})
}

func normalizeWordChunkCardView(view wordChunkCardView) wordChunkCardView {
	view.ChatID = strings.TrimSpace(view.ChatID)
	if view.Page <= 0 {
		view.Page = defaultWordChunkPage
	}
	if view.PageSize <= 0 {
		view.PageSize = defaultWordChunkListTop
	}
	if view.PageSize > maxWordChunkListTop {
		view.PageSize = maxWordChunkListTop
	}
	if strings.TrimSpace(string(view.Sort)) == "" {
		view.Sort = WordCloudSortTypeRelevance
	}
	if strings.TrimSpace(string(view.Intent)) == "" {
		view.Intent = WordChunkIntentTypeAll
	}
	if strings.TrimSpace(string(view.Sentiment)) == "" {
		view.Sentiment = WordChunkSentimentTypeAll
	}
	if strings.TrimSpace(string(view.QuestionMode)) == "" {
		view.QuestionMode = WordChunkQuestionModeAll
	}
	view.MessageID = strings.TrimSpace(view.MessageID)
	view.LastModifierOpenID = strings.TrimSpace(view.LastModifierOpenID)
	return view
}

func (view wordChunkCardView) HasListContext() bool {
	return !view.Start.IsZero() && !view.End.IsZero()
}

func (view wordChunkCardView) Query() wordChunkQuery {
	view = normalizeWordChunkCardView(view)
	return wordChunkQuery{
		ChatID:       view.ChatID,
		Start:        view.Start,
		End:          view.End,
		Limit:        view.PageSize,
		Offset:       (view.Page - 1) * view.PageSize,
		Sort:         view.Sort,
		Intent:       view.Intent,
		Sentiment:    view.Sentiment,
		QuestionMode: view.QuestionMode,
	}
}

func parseWordChunkCardView(parsed *cardactionproto.Parsed, fallbackChatID string) (wordChunkCardView, error) {
	if parsed == nil {
		return wordChunkCardView{}, fmt.Errorf("word chunk view action is nil")
	}
	if parsed.Name != cardactionproto.ActionWordChunksView && parsed.Name != cardactionproto.ActionWordChunkDetail {
		return wordChunkCardView{}, fmt.Errorf("unsupported word chunk action: %s", parsed.Name)
	}

	chatID := firstNonEmpty(readActionValue(parsed, cardactionproto.ChatIDField), fallbackChatID)
	if strings.TrimSpace(chatID) == "" {
		return wordChunkCardView{}, fmt.Errorf("chat_id is required")
	}

	view := wordChunkCardView{
		ChatID: chatID,
		Page:   parseActionInt(readActionValue(parsed, cardactionproto.PageField), defaultWordChunkPage),
		PageSize: parseActionInt(
			firstNonEmpty(readFormValue(parsed, wordChunkActionPageSizeField), readActionValue(parsed, cardactionproto.PageSizeField)),
			defaultWordChunkListTop,
		),
	}

	if len(parsed.FormValue) > 0 {
		view.Page = defaultWordChunkPage
	}

	sortType, err := parseWordChunkSort(firstNonEmpty(readFormValue(parsed, "sort"), readActionValue(parsed, "sort")))
	if err != nil {
		return wordChunkCardView{}, err
	}
	intentType, err := parseWordChunkIntent(firstNonEmpty(readFormValue(parsed, "intent"), readActionValue(parsed, "intent")))
	if err != nil {
		return wordChunkCardView{}, err
	}
	sentimentType, err := parseWordChunkSentiment(firstNonEmpty(readFormValue(parsed, "sentiment"), readActionValue(parsed, "sentiment")))
	if err != nil {
		return wordChunkCardView{}, err
	}
	questionMode, err := parseWordChunkQuestionMode(firstNonEmpty(readFormValue(parsed, "question_mode"), readActionValue(parsed, "question_mode")))
	if err != nil {
		return wordChunkCardView{}, err
	}
	view.Sort = sortType
	view.Intent = intentType
	view.Sentiment = sentimentType
	view.QuestionMode = questionMode

	startRaw := readActionValue(parsed, "start_time")
	endRaw := readActionValue(parsed, "end_time")
	if startRaw != "" || endRaw != "" {
		if startRaw == "" || endRaw == "" {
			return wordChunkCardView{}, fmt.Errorf("start_time and end_time must be provided together")
		}
		view.Start, err = parseFlexibleTime(startRaw)
		if err != nil {
			return wordChunkCardView{}, err
		}
		view.End, err = parseFlexibleTime(endRaw)
		if err != nil {
			return wordChunkCardView{}, err
		}
	}

	return normalizeWordChunkCardView(view), nil
}

func parseWordChunkDetailRequest(parsed *cardactionproto.Parsed, fallbackChatID string) (wordChunkDetailRequest, error) {
	view, err := parseWordChunkCardView(parsed, fallbackChatID)
	if err != nil {
		return wordChunkDetailRequest{}, err
	}
	id, err := parsed.RequiredString(cardactionproto.IDField)
	if err != nil {
		return wordChunkDetailRequest{}, err
	}
	id = strings.TrimSpace(id)
	if id == "" {
		return wordChunkDetailRequest{}, fmt.Errorf("id is required")
	}
	return wordChunkDetailRequest{
		ID:   id,
		View: view,
	}, nil
}

func loadWordChunkPage(ctx context.Context, view wordChunkCardView) (wordChunkSearchResult, wordChunkCardView, error) {
	view = normalizeWordChunkCardView(view)
	result, err := searchChunkLogsPage(ctx, view.Query())
	if err != nil {
		return wordChunkSearchResult{}, view, err
	}
	totalPages := wordChunkTotalPages(result.Total, view.PageSize)
	if result.Total > 0 && view.Page > totalPages {
		view.Page = totalPages
		result, err = searchChunkLogsPage(ctx, view.Query())
		if err != nil {
			return wordChunkSearchResult{}, view, err
		}
	}
	return result, view, nil
}

func loadWordChunkByID(ctx context.Context, chatID, chunkID string) (*xmodel.MessageChunkLogV3, error) {
	result, err := searchChunkLogsPage(ctx, wordChunkQuery{
		ChatID:  strings.TrimSpace(chatID),
		ChunkID: strings.TrimSpace(chunkID),
		Limit:   1,
	})
	if err != nil {
		return nil, err
	}
	if len(result.Items) == 0 {
		return nil, fmt.Errorf("chunk %s not found", chunkID)
	}
	return result.Items[0], nil
}

func loadChunkMessages(ctx context.Context, chatID string, msgIDs []string) ([]*xmodel.MessageIndex, error) {
	if len(msgIDs) == 0 {
		return nil, nil
	}
	values := make([]any, 0, len(msgIDs))
	for _, msgID := range msgIDs {
		msgID = strings.TrimSpace(msgID)
		if msgID == "" {
			continue
		}
		values = append(values, msgID)
	}
	if len(values) == 0 {
		return nil, nil
	}
	messages, err := apphistory.New(ctx).
		Index(appconfig.GetLarkMsgIndex(ctx, chatID, "")).
		Query(osquery.Bool().Must(osquery.Terms("message_id", values...))).
		Source("message_str", "raw_message", "create_time", "create_time_v2", "user_id", "user_name", "message_type").
		GetAll()
	if err != nil {
		return nil, err
	}
	sort.Slice(messages, func(i, j int) bool {
		return wordChunkMessageTime(messages[i]).Before(wordChunkMessageTime(messages[j]))
	})
	return messages, nil
}

func buildWordChunkListCardWithState(ctx context.Context, view wordChunkCardView, result wordChunkSearchResult) larkmsg.RawCard {
	view = normalizeWordChunkCardView(view)
	pageCount := wordChunkTotalPages(result.Total, view.PageSize)
	pageSummary := fmt.Sprintf("第 `%d/%d` 页，共 `%d` 条 chunk", view.Page, pageCount, result.Total)
	if result.Total == 0 {
		pageSummary = "当前筛选条件下没有匹配的 chunk。"
	}

	elements := []any{
		larkmsg.Markdown(buildWordChunkListHeader(ctx, view)),
		buildWordChunkFilterForm(view),
		larkmsg.HintMarkdown(pageSummary),
	}
	if pager := buildWordChunkPaginationRow(view, result.Total); pager != nil {
		elements = append(elements, pager)
	}

	if len(result.Items) == 0 {
		elements = append(elements, larkmsg.Markdown("暂无匹配的 chunk"))
	} else {
		sections := make([][]any, 0, len(result.Items))
		for idx, chunk := range result.Items {
			sections = append(sections, []any{buildWordChunkSection((view.Page-1)*view.PageSize+idx+1, chunk, view)})
		}
		elements = larkmsg.AppendSectionsWithDividers(elements, sections...)
	}

	return larkmsg.NewStandardPanelCard(ctx, "Chunk 列表", elements, larkmsg.StandardCardFooterOptions{
		RefreshPayload:     larkmsg.StringMapToAnyMap(BuildWordChunkViewValue(view)),
		LastModifierOpenID: view.LastModifierOpenID,
		ActionHistory: larkmsg.CardActionHistoryOptions{
			Enabled:        true,
			OpenMessageID:  view.MessageID,
			PendingRecords: view.PendingHistory,
		},
	})
}

func buildWordChunkDetailCard(ctx context.Context, view wordChunkCardView, chunk *xmodel.MessageChunkLogV3, messages []*xmodel.MessageIndex) larkmsg.RawCard {
	elements := []any{
		larkmsg.Markdown(buildWordChunkDetailHeader(ctx, chunk, view)),
	}
	if participants := buildWordChunkParticipantsRow(chunk); participants != nil {
		elements = append(elements, participants)
	}
	elements = append(elements, buildWordChunkDetailSummary(chunk))

	for _, block := range buildWordChunkDetailBlocks(chunk) {
		if block != nil {
			elements = append(elements, larkmsg.Divider(), block)
		}
	}
	if len(messages) > 0 {
		elements = append(elements, larkmsg.Divider(), larkmsg.Markdown("**消息片段**"))
		elements = append(elements, buildWordChunkMessageElements(messages)...)
	}
	if actions := buildWordChunkDetailActionRow(view); actions != nil {
		elements = append(elements, larkmsg.Divider(), actions)
	}

	return larkmsg.NewStandardPanelCard(ctx, "Chunk 详情", elements, larkmsg.StandardCardFooterOptions{
		RefreshPayload:     larkmsg.StringMapToAnyMap(BuildWordChunkDetailValue(chunk.ID, view)),
		LastModifierOpenID: view.LastModifierOpenID,
		ActionHistory: larkmsg.CardActionHistoryOptions{
			Enabled:        true,
			OpenMessageID:  view.MessageID,
			PendingRecords: view.PendingHistory,
		},
	})
}

func buildWordChunkListHeader(ctx context.Context, view wordChunkCardView) string {
	lines := []string{
		fmt.Sprintf("**群聊**: `%s`", displayWordCountChat(ctx, view.ChatID)),
	}
	if view.HasListContext() {
		lines = append(lines, fmt.Sprintf(
			"时间范围: `%s ~ %s`",
			view.Start.In(utils.UTC8Loc()).Format("2006-01-02 15:04"),
			view.End.In(utils.UTC8Loc()).Format("2006-01-02 15:04"),
		))
	}
	lines = append(lines,
		fmt.Sprintf("排序: `%s`", displayWordChunkSort(view.Sort)),
		fmt.Sprintf("意图: `%s`  情绪: `%s`  问题: `%s`", displayWordChunkIntent(view.Intent), displayWordChunkSentiment(view.Sentiment), displayWordChunkQuestionMode(view.QuestionMode)),
	)
	return strings.Join(lines, "\n")
}

func buildWordChunkDetailHeader(ctx context.Context, chunk *xmodel.MessageChunkLogV3, view wordChunkCardView) string {
	lines := []string{
		fmt.Sprintf("**群聊**: `%s`", displayWordCountChat(ctx, view.ChatID)),
		fmt.Sprintf("**Chunk ID**: `%s`", firstNonEmpty(strings.TrimSpace(chunk.ID), "-")),
		fmt.Sprintf("**摘要**: %s", firstNonEmpty(strings.TrimSpace(chunk.Summary), "无摘要")),
	}
	if ts := displayChunkTimestamp(chunk); ts != "-" {
		lines = append(lines, fmt.Sprintf("**时间**: `%s`", ts))
	}
	return strings.Join(lines, "\n")
}

func buildWordChunkFilterForm(view wordChunkCardView) map[string]any {
	applyButton := larkmsg.Button("应用筛选", larkmsg.ButtonOptions{
		Type:           "primary_filled",
		Name:           "word_chunk_apply_filters",
		FormActionType: "submit",
		Payload:        larkmsg.StringMapToAnyMap(BuildWordChunkViewValue(view)),
	})

	return map[string]any{
		"tag":                "form",
		"name":               "form_wordcount_chunks",
		"vertical_spacing":   "8px",
		"horizontal_spacing": "8px",
		"elements": []any{
			larkmsg.HintMarkdown("筛选后会保留当前卡片状态，详情页可返回当前列表。"),
			larkmsg.ColumnSet([]any{
				wordChunkFilterColumn("排序", larkmsg.SelectStatic("sort", larkmsg.SelectStaticOptions{
					Width:         "fill",
					InitialOption: string(view.Sort),
					Options:       enumDescriptorOptions(WordCloudSortType("").CommandEnum()),
					ElementID:     "wc_sort",
				})),
				wordChunkFilterColumn("意图", larkmsg.SelectStatic("intent", larkmsg.SelectStaticOptions{
					Width:         "fill",
					InitialOption: string(view.Intent),
					Options:       enumDescriptorOptions(WordChunkIntentType("").CommandEnum()),
					ElementID:     "wc_intent",
				})),
			}, larkmsg.ColumnSetOptions{
				HorizontalSpacing: "8px",
				FlexMode:          "stretch",
			}),
			larkmsg.ColumnSet([]any{
				wordChunkFilterColumn("情绪", larkmsg.SelectStatic("sentiment", larkmsg.SelectStaticOptions{
					Width:         "fill",
					InitialOption: string(view.Sentiment),
					Options:       enumDescriptorOptions(WordChunkSentimentType("").CommandEnum()),
					ElementID:     "wc_sentiment",
				})),
				wordChunkFilterColumn("问题", larkmsg.SelectStatic("question_mode", larkmsg.SelectStaticOptions{
					Width:         "fill",
					InitialOption: string(view.QuestionMode),
					Options:       enumDescriptorOptions(WordChunkQuestionMode("").CommandEnum()),
					ElementID:     "wc_question_mode",
				})),
				wordChunkFilterColumn("每页", larkmsg.SelectStatic(wordChunkActionPageSizeField, larkmsg.SelectStaticOptions{
					Width:         "fill",
					InitialOption: strconv.Itoa(view.PageSize),
					Options:       wordChunkPageSizeOptions(view.PageSize),
					ElementID:     "wc_page_size",
				})),
			}, larkmsg.ColumnSetOptions{
				HorizontalSpacing: "8px",
				FlexMode:          "stretch",
			}),
			larkmsg.ButtonRow("flow", applyButton),
		},
	}
}

func wordChunkFilterColumn(label string, element map[string]any) map[string]any {
	return larkmsg.Column([]any{
		larkmsg.TextDiv(label, larkmsg.CardTextOptions{
			Size:  "notation",
			Color: "grey",
		}),
		element,
	}, larkmsg.ColumnOptions{
		Width:           "weighted",
		Weight:          1,
		VerticalAlign:   "top",
		VerticalSpacing: "4px",
	})
}

func buildWordChunkPaginationRow(view wordChunkCardView, total int) map[string]any {
	pageCount := wordChunkTotalPages(total, view.PageSize)
	buttons := make([]map[string]any, 0, 2)
	if view.Page > 1 {
		prev := view
		prev.Page--
		buttons = append(buttons, larkmsg.Button("上一页", larkmsg.ButtonOptions{
			Type:    "default",
			Payload: larkmsg.StringMapToAnyMap(BuildWordChunkViewValue(prev)),
		}))
	}
	if view.Page < pageCount {
		next := view
		next.Page++
		buttons = append(buttons, larkmsg.Button("下一页", larkmsg.ButtonOptions{
			Type:    "default",
			Payload: larkmsg.StringMapToAnyMap(BuildWordChunkViewValue(next)),
		}))
	}
	if len(buttons) == 0 {
		return nil
	}

	return larkmsg.SplitColumns(
		[]any{larkmsg.TextDiv(fmt.Sprintf("页码 %d/%d", view.Page, pageCount), larkmsg.CardTextOptions{
			Size:  "notation",
			Color: "grey",
		})},
		[]any{larkmsg.ButtonRow("flow", buttons...)},
		larkmsg.SplitColumnsOptions{
			Left: larkmsg.ColumnOptions{
				Weight:        1,
				VerticalAlign: "center",
			},
			Right: larkmsg.ColumnOptions{
				Weight:        1,
				VerticalAlign: "top",
			},
			Row: larkmsg.ColumnSetOptions{
				HorizontalSpacing: "8px",
				FlexMode:          "stretch",
			},
		},
	)
}

func buildWordChunkDetailSummary(chunk *xmodel.MessageChunkLogV3) map[string]any {
	leftLines := []string{
		fmt.Sprintf("意图: `%s`", displayWordChunkIntent(WordChunkIntentType(chunk.Intent))),
		fmt.Sprintf("情绪: `%s`", displayChunkSentiment(chunk)),
		fmt.Sprintf("语气: `%s`", firstNonEmpty(displayChunkTones(chunk), "-")),
	}
	rightLines := []string{
		fmt.Sprintf("参与者: `%d`", countChunkParticipants(chunk)),
		fmt.Sprintf("消息数: `%d`", len(chunk.MsgIDs)),
		fmt.Sprintf("问题存在: `%t`", firstNonEmptyInteraction(chunk, func(ia *xmodel.InteractionAnalysis) bool { return ia.IsQuestionPresent })),
	}
	return larkmsg.SplitColumns(
		[]any{larkmsg.Markdown(strings.Join(leftLines, "\n"))},
		[]any{larkmsg.Markdown(strings.Join(rightLines, "\n"))},
		larkmsg.SplitColumnsOptions{
			Left:  larkmsg.ColumnOptions{Weight: 1, VerticalAlign: "top"},
			Right: larkmsg.ColumnOptions{Weight: 1, VerticalAlign: "top"},
			Row: larkmsg.ColumnSetOptions{
				HorizontalSpacing: "12px",
				FlexMode:          "stretch",
			},
		},
	)
}

func buildWordChunkDetailBlocks(chunk *xmodel.MessageChunkLogV3) []map[string]any {
	if chunk == nil {
		return nil
	}
	blocks := make([]map[string]any, 0, 6)
	if markdown := buildWordChunkDetailMarkdownBlock("主题与活动", firstNonEmptySlice(chunk.Entities, func(e *xmodel.Entities) []string { return e.MainTopicsOrActivities })); markdown != nil {
		blocks = append(blocks, markdown)
	}
	if markdown := buildWordChunkDetailMarkdownBlock("关键词", firstNonEmptySlice(chunk.Entities, func(e *xmodel.Entities) []string { return e.KeyConceptsAndNouns })); markdown != nil {
		blocks = append(blocks, markdown)
	}
	if markdown := buildWordChunkDetailMarkdownBlock("待解问题", firstNonEmptySlice(chunk.InteractionAnalysis, func(a *xmodel.InteractionAnalysis) []string { return a.UnresolvedQuestions })); markdown != nil {
		blocks = append(blocks, markdown)
	}
	if markdown := buildWordChunkDetailMarkdownBlock("社交动态", firstNonEmptySlice(chunk.InteractionAnalysis, func(a *xmodel.InteractionAnalysis) []string { return a.SocialDynamics })); markdown != nil {
		blocks = append(blocks, markdown)
	}
	if markdown := buildWordChunkDetailMarkdownBlock("结论与共识", firstNonEmptySlice(chunk.Outcomes, func(o *xmodel.Outcome) []string { return o.ConclusionsOrAgreements })); markdown != nil {
		blocks = append(blocks, markdown)
	}
	if markdown := buildWordChunkDetailMarkdownBlock("开放议题", firstNonEmptySlice(chunk.Outcomes, func(o *xmodel.Outcome) []string { return o.OpenThreadsOrPendingPoints })); markdown != nil {
		blocks = append(blocks, markdown)
	}
	return blocks
}

func buildWordChunkParticipantsRow(chunk *xmodel.MessageChunkLogV3) map[string]any {
	participantIDs := chunkParticipantIDs(chunk, 8)
	if len(participantIDs) == 0 {
		return nil
	}
	showAvatar := true
	showName := false
	columns := make([]any, 0, len(participantIDs)+1)
	columns = append(columns, larkmsg.Column([]any{
		larkmsg.TextDiv("参与者", larkmsg.CardTextOptions{
			Size:  "notation",
			Color: "grey",
		}),
	}, larkmsg.ColumnOptions{
		Width:         "auto",
		VerticalAlign: "center",
	}))
	for _, openID := range participantIDs {
		columns = append(columns, larkmsg.Column([]any{
			larkmsg.Person(openID, larkmsg.PersonOptions{
				Size:       "extra_small",
				ShowAvatar: &showAvatar,
				ShowName:   &showName,
				Style:      "capsule",
				Margin:     "0",
			}),
		}, larkmsg.ColumnOptions{
			Width:         "auto",
			VerticalAlign: "center",
		}))
	}
	return larkmsg.ColumnSet(columns, larkmsg.ColumnSetOptions{
		HorizontalSpacing: "6px",
		FlexMode:          "none",
	})
}

func buildWordChunkDetailActionRow(view wordChunkCardView) map[string]any {
	buttons := []map[string]any{}
	if view.HasListContext() {
		buttons = append(buttons, larkmsg.Button("返回列表", larkmsg.ButtonOptions{
			Type:    "default",
			Payload: larkmsg.StringMapToAnyMap(BuildWordChunkViewValue(view)),
		}))
	}
	if len(buttons) == 0 {
		return nil
	}
	return larkmsg.ButtonRow("flow", buttons...)
}

func buildWordChunkMessageElements(messages []*xmodel.MessageIndex) []any {
	elements := make([]any, 0, len(messages))
	for idx, msg := range messages {
		if msg == nil {
			continue
		}
		elements = append(elements, larkmsg.Markdown(fmt.Sprintf(
			"**%d. [%s] %s**\n%s",
			idx+1,
			firstNonEmpty(wordChunkDisplayMessageTime(msg), "-"),
			firstNonEmpty(strings.TrimSpace(msg.UserName), strings.TrimSpace(msg.OpenID), "未知用户"),
			wordChunkPreviewText(msg),
		)))
	}
	return elements
}

func buildWordChunkSection(index int, chunk *xmodel.MessageChunkLogV3, view wordChunkCardView) map[string]any {
	if chunk == nil {
		return larkmsg.Markdown("空 chunk")
	}

	leftLines := []string{
		fmt.Sprintf("**%d. %s**", index, firstNonEmpty(strings.TrimSpace(chunk.Summary), "无摘要")),
		fmt.Sprintf("主题: `%s`", joinPreview(firstNonEmptySlice(chunk.Entities, func(e *xmodel.Entities) []string { return e.MainTopicsOrActivities }), 3)),
	}
	if unresolved := firstNonEmptySlice(chunk.InteractionAnalysis, func(a *xmodel.InteractionAnalysis) []string { return a.UnresolvedQuestions }); len(unresolved) > 0 {
		leftLines = append(leftLines, fmt.Sprintf("待解问题: %s", joinPreview(unresolved, 2)))
	}
	if concepts := firstNonEmptySlice(chunk.Entities, func(e *xmodel.Entities) []string { return e.KeyConceptsAndNouns }); len(concepts) > 0 {
		leftLines = append(leftLines, fmt.Sprintf("关键词: `%s`", joinPreview(concepts, 4)))
	}

	rightLines := []string{
		fmt.Sprintf("时间: `%s`", displayChunkTimestamp(chunk)),
		fmt.Sprintf("意图: `%s`", displayWordChunkIntent(WordChunkIntentType(chunk.Intent))),
		fmt.Sprintf("情绪: `%s`", displayChunkSentiment(chunk)),
		fmt.Sprintf("参与者: `%d`  消息数: `%d`", countChunkParticipants(chunk), len(chunk.MsgIDs)),
	}
	if tones := displayChunkTones(chunk); tones != "" {
		rightLines = append(rightLines, fmt.Sprintf("语气: `%s`", tones))
	}

	rightElements := []any{
		larkmsg.Markdown(strings.Join(rightLines, "\n")),
		larkmsg.ButtonRow("flow", larkmsg.Button("详情", larkmsg.ButtonOptions{
			Type:    "default",
			Payload: larkmsg.StringMapToAnyMap(BuildWordChunkDetailValue(chunk.ID, view)),
		})),
	}

	return larkmsg.SplitColumns(
		[]any{larkmsg.Markdown(strings.Join(leftLines, "\n"))},
		rightElements,
		larkmsg.SplitColumnsOptions{
			Left: larkmsg.ColumnOptions{
				Weight:        3,
				VerticalAlign: "top",
			},
			Right: larkmsg.ColumnOptions{
				Weight:          2,
				VerticalAlign:   "top",
				VerticalSpacing: "6px",
			},
			Row: larkmsg.ColumnSetOptions{
				HorizontalSpacing: "12px",
				FlexMode:          "stretch",
			},
		},
	)
}

func enumDescriptorOptions(desc xcommand.EnumDescriptor) []larkmsg.SelectStaticOption {
	options := make([]larkmsg.SelectStaticOption, 0, len(desc.Options))
	for _, option := range desc.Options {
		value := strings.TrimSpace(option.Value)
		if value == "" {
			continue
		}
		label := strings.TrimSpace(option.Label)
		if label == "" {
			label = value
		}
		options = append(options, larkmsg.SelectStaticOption{
			Text:  label,
			Value: value,
		})
	}
	return options
}

func wordChunkPageSizeOptions(current int) []larkmsg.SelectStaticOption {
	values := map[int]struct{}{
		5:  {},
		8:  {},
		10: {},
		20: {},
	}
	if current > 0 && current <= maxWordChunkListTop {
		values[current] = struct{}{}
	}
	sizes := make([]int, 0, len(values))
	for size := range values {
		sizes = append(sizes, size)
	}
	sort.Ints(sizes)
	options := make([]larkmsg.SelectStaticOption, 0, len(sizes))
	for _, size := range sizes {
		options = append(options, larkmsg.SelectStaticOption{
			Text:  fmt.Sprintf("%d 条", size),
			Value: strconv.Itoa(size),
		})
	}
	return options
}

func wordChunkTotalPages(total, pageSize int) int {
	if total <= 0 {
		return 1
	}
	if pageSize <= 0 {
		pageSize = defaultWordChunkListTop
	}
	pages := total / pageSize
	if total%pageSize != 0 {
		pages++
	}
	if pages <= 0 {
		return 1
	}
	return pages
}

func parseWordChunkSort(raw string) (WordCloudSortType, error) {
	return xcommand.ParseEnum[WordCloudSortType](strings.TrimSpace(raw))
}

func parseWordChunkIntent(raw string) (WordChunkIntentType, error) {
	return xcommand.ParseEnum[WordChunkIntentType](strings.TrimSpace(raw))
}

func parseWordChunkSentiment(raw string) (WordChunkSentimentType, error) {
	return xcommand.ParseEnum[WordChunkSentimentType](strings.TrimSpace(raw))
}

func parseWordChunkQuestionMode(raw string) (WordChunkQuestionMode, error) {
	return xcommand.ParseEnum[WordChunkQuestionMode](strings.TrimSpace(raw))
}

func parseActionInt(raw string, fallback int) int {
	if value, err := strconv.Atoi(strings.TrimSpace(raw)); err == nil && value > 0 {
		return value
	}
	return fallback
}

func readActionValue(parsed *cardactionproto.Parsed, key string) string {
	if parsed == nil {
		return ""
	}
	value, _ := parsed.String(key)
	return strings.TrimSpace(value)
}

func readFormValue(parsed *cardactionproto.Parsed, key string) string {
	if parsed == nil {
		return ""
	}
	value, _ := parsed.FormString(key)
	return strings.TrimSpace(value)
}

func chunkParticipantIDs(chunk *xmodel.MessageChunkLogV3, limit int) []string {
	if chunk == nil || chunk.InteractionAnalysis == nil {
		return nil
	}
	seen := map[string]struct{}{}
	result := make([]string, 0, len(chunk.InteractionAnalysis.Participants))
	for _, participant := range chunk.InteractionAnalysis.Participants {
		if participant == nil || participant.User == nil {
			continue
		}
		openID := strings.TrimSpace(participant.OpenID)
		if openID == "" {
			openID = strings.TrimSpace(participant.User.OpenID)
		}
		if openID == "" {
			continue
		}
		if _, ok := seen[openID]; ok {
			continue
		}
		seen[openID] = struct{}{}
		result = append(result, openID)
		if limit > 0 && len(result) >= limit {
			break
		}
	}
	return result
}

func wordChunkPreviewText(msg *xmodel.MessageIndex) string {
	if msg == nil {
		return "-"
	}
	text := firstNonEmpty(strings.TrimSpace(msg.Content), strings.TrimSpace(msg.RawMessage))
	text = strings.Join(strings.Fields(text), " ")
	if text == "" {
		text = fmt.Sprintf("[%s]", firstNonEmpty(strings.TrimSpace(msg.MessageType), "unknown"))
	}
	if runes := []rune(text); len(runes) > 160 {
		text = string(runes[:157]) + "..."
	}
	return text
}

func wordChunkDisplayMessageTime(msg *xmodel.MessageIndex) string {
	if msg == nil {
		return ""
	}
	if t := wordChunkMessageTime(msg); !t.IsZero() {
		return t.In(utils.UTC8Loc()).Format("2006-01-02 15:04")
	}
	return firstNonEmpty(strings.TrimSpace(msg.CreateTime), strings.TrimSpace(msg.CreateTimeV2))
}

func wordChunkMessageTime(msg *xmodel.MessageIndex) time.Time {
	if msg == nil {
		return time.Time{}
	}
	for _, candidate := range []string{strings.TrimSpace(msg.CreateTimeV2), strings.TrimSpace(msg.CreateTime)} {
		if candidate == "" {
			continue
		}
		if t, err := parseFlexibleTime(candidate); err == nil {
			return t
		}
	}
	return time.Time{}
}

func firstNonEmptyInteraction(chunk *xmodel.MessageChunkLogV3, getter func(*xmodel.InteractionAnalysis) bool) bool {
	if chunk == nil || chunk.InteractionAnalysis == nil {
		return false
	}
	return getter(chunk.InteractionAnalysis)
}

func buildWordChunkDetailMarkdownBlock(title string, items []string) map[string]any {
	items = trimNonEmptyStrings(items)
	if len(items) == 0 {
		return nil
	}
	lines := make([]string, 0, len(items)+1)
	lines = append(lines, fmt.Sprintf("**%s**", strings.TrimSpace(title)))
	for _, item := range items {
		lines = append(lines, fmt.Sprintf("- %s", item))
	}
	return larkmsg.Markdown(strings.Join(lines, "\n"))
}

func trimNonEmptyStrings(items []string) []string {
	result := make([]string, 0, len(items))
	for _, item := range items {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		result = append(result, item)
	}
	return result
}
