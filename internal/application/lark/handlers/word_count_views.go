package handlers

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	appconfig "github.com/BetaGoRobot/BetaGo-Redefine/internal/application/config"
	arktools "github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/ark_dal/tools"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/lark_dal/larkchat"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/lark_dal/larkmsg"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/lark_dal/larkmsg/larkcard"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/lark_dal/larkmsg/larktpl"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/opensearch"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/vadvisor"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/xmodel"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/utils"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/xcommand"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/xhandler"
	"github.com/bytedance/sonic"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
	"github.com/olivere/elastic/v7"
)

const (
	defaultWordCloudGraphTop = 40
	defaultWordChunkListTop  = 8
	maxWordChunkListTop      = 20
	maxWordCloudGraphTop     = 100
)

type WordCloudGraphArgs struct {
	Days      int    `json:"days"`
	Top       int    `json:"top"`
	ChatID    string `json:"chat_id"`
	StartTime string `json:"start_time" cli:"st"`
	EndTime   string `json:"end_time" cli:"et"`
}

type WordChunksArgs struct {
	Days         int                    `json:"days"`
	Top          int                    `json:"top"`
	ChatID       string                 `json:"chat_id"`
	Sort         WordCloudSortType      `json:"sort"`
	Intent       WordChunkIntentType    `json:"intent"`
	Sentiment    WordChunkSentimentType `json:"sentiment"`
	QuestionMode WordChunkQuestionMode  `json:"question_mode"`
	StartTime    string                 `json:"start_time" cli:"st"`
	EndTime      string                 `json:"end_time" cli:"et"`
}

type wordCloudGraphHandler struct{}
type wordChunksHandler struct{}

var (
	WordCloudGraph wordCloudGraphHandler
	WordChunks     wordChunksHandler
)

type wordCountScope struct {
	ChatID string
	Start  time.Time
	End    time.Time
	Days   int
}

type wordChunkQuery struct {
	ChatID       string
	ChunkID      string
	Start        time.Time
	End          time.Time
	Limit        int
	Offset       int
	Sort         WordCloudSortType
	Intent       WordChunkIntentType
	Sentiment    WordChunkSentimentType
	QuestionMode WordChunkQuestionMode
}

func (wordCloudGraphHandler) ParseCLI(args []string) (WordCloudGraphArgs, error) {
	argMap, _ := parseArgs(args...)
	parsed := WordCloudGraphArgs{
		Days:      7,
		Top:       defaultWordCloudGraphTop,
		ChatID:    argMap["chat_id"],
		StartTime: argMap["st"],
		EndTime:   argMap["et"],
	}
	if daysStr := argMap["days"]; daysStr != "" {
		if days, err := strconv.Atoi(daysStr); err == nil && days > 0 {
			parsed.Days = days
		}
	}
	if topStr := argMap["top"]; topStr != "" {
		if top, err := strconv.Atoi(topStr); err == nil && top > 0 {
			parsed.Top = top
		}
	}
	return parsed, nil
}

func (wordCloudGraphHandler) ParseTool(raw string) (WordCloudGraphArgs, error) {
	parsed := WordCloudGraphArgs{
		Days: 7,
		Top:  defaultWordCloudGraphTop,
	}
	if err := utils.UnmarshalStringPre(raw, &parsed); err != nil {
		return WordCloudGraphArgs{}, err
	}
	if parsed.Days <= 0 {
		parsed.Days = 7
	}
	if parsed.Top <= 0 {
		parsed.Top = defaultWordCloudGraphTop
	}
	if parsed.StartTime != "" && parsed.EndTime != "" {
		parsed.StartTime = normalizeRFC3339(parsed.StartTime)
		parsed.EndTime = normalizeRFC3339(parsed.EndTime)
	}
	return parsed, nil
}

func (wordCloudGraphHandler) ToolSpec() xcommand.ToolSpec {
	return xcommand.ToolSpec{
		Name: "word_cloud_graph_get",
		Desc: "单独生成群聊词云图，不附带热点 chunk 和活跃用户列表",
		Params: arktools.NewParams("object").
			AddProp("days", &arktools.Prop{
				Type: "number",
				Desc: "统计近几天的词云，默认 7",
			}).
			AddProp("top", &arktools.Prop{
				Type: "number",
				Desc: "词云保留的高频词数量，默认 40",
			}).
			AddProp("chat_id", &arktools.Prop{
				Type: "string",
				Desc: "目标群聊 ID，不填则使用当前群聊",
			}).
			AddProp("start_time", &arktools.Prop{
				Type: "string",
				Desc: "开始时间，支持 RFC3339 或 YYYY-MM-DD HH:MM:SS",
			}).
			AddProp("end_time", &arktools.Prop{
				Type: "string",
				Desc: "结束时间，支持 RFC3339 或 YYYY-MM-DD HH:MM:SS",
			}),
	}
}

func (wordCloudGraphHandler) Handle(ctx context.Context, data *larkim.P2MessageReceiveV1, metaData *xhandler.BaseMetaData, arg WordCloudGraphArgs) error {
	scope, err := resolveWordCountScope(data, metaData, arg.ChatID, arg.Days, arg.StartTime, arg.EndTime)
	if err != nil {
		return err
	}

	wc, err := genWordCount(ctx, scope.ChatID, scope.Start, scope.End)
	if err != nil {
		return err
	}

	graph := buildWordCloudGraph(ctx, wc, arg.Top, true)
	title := fmt.Sprintf("[%s]词云", displayWordCountChat(ctx, scope.ChatID))
	cardContent := larkcard.NewCardBuildGraphHelper(graph).
		SetStartTime(scope.Start).
		SetEndTime(scope.End).
		SetTitle(title).
		Build(ctx)
	return sendCompatibleCard(ctx, data, metaData, cardContent, "_wordCloudGraph", false)
}

func (wordCloudGraphHandler) CommandDescription() string {
	return "单独查看群聊词云图"
}

func (wordCloudGraphHandler) CommandExamples() []string {
	return []string{
		"/wordcount cloud --days=7 --top=40",
		"/wordcount cloud --chat_id=oc_xxx --st=2026-03-01T00:00:00+08:00 --et=2026-03-07T23:59:59+08:00",
	}
}

func (wordChunksHandler) ParseCLI(args []string) (WordChunksArgs, error) {
	argMap, _ := parseArgs(args...)
	sortType, err := xcommand.ParseEnum[WordCloudSortType](argMap["sort"])
	if err != nil {
		return WordChunksArgs{}, err
	}
	intentType, err := xcommand.ParseEnum[WordChunkIntentType](argMap["intent"])
	if err != nil {
		return WordChunksArgs{}, err
	}
	sentimentType, err := xcommand.ParseEnum[WordChunkSentimentType](argMap["sentiment"])
	if err != nil {
		return WordChunksArgs{}, err
	}
	questionMode, err := xcommand.ParseEnum[WordChunkQuestionMode](argMap["question_mode"])
	if err != nil {
		return WordChunksArgs{}, err
	}

	parsed := WordChunksArgs{
		Days:         7,
		Top:          defaultWordChunkListTop,
		ChatID:       argMap["chat_id"],
		Sort:         sortType,
		Intent:       intentType,
		Sentiment:    sentimentType,
		QuestionMode: questionMode,
		StartTime:    argMap["st"],
		EndTime:      argMap["et"],
	}
	if daysStr := argMap["days"]; daysStr != "" {
		if days, err := strconv.Atoi(daysStr); err == nil && days > 0 {
			parsed.Days = days
		}
	}
	if topStr := argMap["top"]; topStr != "" {
		if top, err := strconv.Atoi(topStr); err == nil && top > 0 {
			parsed.Top = top
		}
	}
	return parsed, nil
}

func (wordChunksHandler) ParseTool(raw string) (WordChunksArgs, error) {
	parsed := WordChunksArgs{
		Days:         7,
		Top:          defaultWordChunkListTop,
		Sort:         WordCloudSortTypeRelevance,
		Intent:       WordChunkIntentTypeAll,
		Sentiment:    WordChunkSentimentTypeAll,
		QuestionMode: WordChunkQuestionModeAll,
	}
	if err := utils.UnmarshalStringPre(raw, &parsed); err != nil {
		return WordChunksArgs{}, err
	}
	if parsed.Days <= 0 {
		parsed.Days = 7
	}
	if parsed.Top <= 0 {
		parsed.Top = defaultWordChunkListTop
	}
	sortType, err := xcommand.ParseEnum[WordCloudSortType](string(parsed.Sort))
	if err != nil {
		return WordChunksArgs{}, err
	}
	intentType, err := xcommand.ParseEnum[WordChunkIntentType](string(parsed.Intent))
	if err != nil {
		return WordChunksArgs{}, err
	}
	sentimentType, err := xcommand.ParseEnum[WordChunkSentimentType](string(parsed.Sentiment))
	if err != nil {
		return WordChunksArgs{}, err
	}
	questionMode, err := xcommand.ParseEnum[WordChunkQuestionMode](string(parsed.QuestionMode))
	if err != nil {
		return WordChunksArgs{}, err
	}
	parsed.Sort = sortType
	parsed.Intent = intentType
	parsed.Sentiment = sentimentType
	parsed.QuestionMode = questionMode
	if parsed.StartTime != "" && parsed.EndTime != "" {
		parsed.StartTime = normalizeRFC3339(parsed.StartTime)
		parsed.EndTime = normalizeRFC3339(parsed.EndTime)
	}
	return parsed, nil
}

func (wordChunksHandler) ToolSpec() xcommand.ToolSpec {
	return xcommand.ToolSpec{
		Name: "word_chunks_get",
		Desc: "查看群聊 chunk 摘要列表，并按意图、情绪、是否有问题进行筛选",
		Params: arktools.NewParams("object").
			AddProp("days", &arktools.Prop{
				Type: "number",
				Desc: "统计近几天的 chunk，默认 7",
			}).
			AddProp("top", &arktools.Prop{
				Type: "number",
				Desc: "返回前 N 条 chunk，默认 8，最大 20",
			}).
			AddProp("chat_id", &arktools.Prop{
				Type: "string",
				Desc: "目标群聊 ID，不填则使用当前群聊",
			}).
			AddProp("sort", &arktools.Prop{
				Type: "string",
				Desc: "排序方式：相关度或时间",
			}).
			AddProp("intent", &arktools.Prop{
				Type: "string",
				Desc: "按意图筛选",
			}).
			AddProp("sentiment", &arktools.Prop{
				Type: "string",
				Desc: "按情绪筛选",
			}).
			AddProp("question_mode", &arktools.Prop{
				Type: "string",
				Desc: "按是否包含未决问题筛选",
			}).
			AddProp("start_time", &arktools.Prop{
				Type: "string",
				Desc: "开始时间，支持 RFC3339 或 YYYY-MM-DD HH:MM:SS",
			}).
			AddProp("end_time", &arktools.Prop{
				Type: "string",
				Desc: "结束时间，支持 RFC3339 或 YYYY-MM-DD HH:MM:SS",
			}),
	}
}

func (wordChunksHandler) Handle(ctx context.Context, data *larkim.P2MessageReceiveV1, metaData *xhandler.BaseMetaData, arg WordChunksArgs) error {
	scope, err := resolveWordCountScope(data, metaData, arg.ChatID, arg.Days, arg.StartTime, arg.EndTime)
	if err != nil {
		return err
	}

	view := normalizeWordChunkCardView(wordChunkCardView{
		ChatID:             scope.ChatID,
		Start:              scope.Start,
		End:                scope.End,
		Sort:               arg.Sort,
		Intent:             arg.Intent,
		Sentiment:          arg.Sentiment,
		QuestionMode:       arg.QuestionMode,
		Page:               defaultWordChunkPage,
		PageSize:           arg.Top,
		MessageID:          currentMessageID(data),
		LastModifierOpenID: currentOpenID(data, metaData),
	})
	result, view, err := loadWordChunkPage(ctx, view)
	if err != nil {
		return err
	}

	card := buildWordChunkListCardWithState(ctx, view, result)
	return sendCompatibleCardJSON(ctx, data, metaData, map[string]any(card), "_wordChunks", false)
}

func (wordChunksHandler) CommandDescription() string {
	return "单独查看并筛选群聊 chunk 摘要"
}

func (wordChunksHandler) CommandExamples() []string {
	return []string{
		"/wordcount chunks --top=8 --sort=relevance",
		"/wordcount chunks --sort=time --question_mode=question",
		"/wordcount chunks --intent=CASUAL_CHITCHAT --sentiment=NEGATIVE",
	}
}

func resolveWordCountScope(data *larkim.P2MessageReceiveV1, metaData *xhandler.BaseMetaData, chatIDOverride string, days int, startInput, endInput string) (wordCountScope, error) {
	chatID := firstNonEmpty(chatIDOverride, currentChatID(data, metaData))
	if strings.TrimSpace(chatID) == "" {
		return wordCountScope{}, fmt.Errorf("chat_id is required")
	}

	start, end := GetBackDays(days)
	if strings.TrimSpace(startInput) != "" || strings.TrimSpace(endInput) != "" {
		if strings.TrimSpace(startInput) == "" || strings.TrimSpace(endInput) == "" {
			return wordCountScope{}, fmt.Errorf("start_time and end_time must be provided together")
		}
		var err error
		start, err = parseFlexibleTime(startInput)
		if err != nil {
			return wordCountScope{}, err
		}
		end, err = parseFlexibleTime(endInput)
		if err != nil {
			return wordCountScope{}, err
		}
	}
	if end.Before(start) {
		return wordCountScope{}, fmt.Errorf("end_time must be after start_time")
	}

	return wordCountScope{
		ChatID: strings.TrimSpace(chatID),
		Start:  start,
		End:    end,
		Days:   days,
	}, nil
}

func parseFlexibleTime(value string) (time.Time, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return time.Time{}, fmt.Errorf("time value is empty")
	}
	if t, err := time.Parse(time.RFC3339, value); err == nil {
		return t.In(utils.UTC8Loc()), nil
	}
	if t, err := time.ParseInLocation(time.DateTime, value, utils.UTC8Loc()); err == nil {
		return t, nil
	}
	return time.Time{}, fmt.Errorf("unsupported time format: %s", value)
}

func buildWordCloudGraph(ctx context.Context, wc WordCountType, top int, withPlayer bool) *vadvisor.WordCloudChartsWithPlayer[string, int] {
	if top <= 0 {
		top = defaultWordCloudGraphTop
	}
	if top > maxWordCloudGraphTop {
		top = maxWordCloudGraphTop
	}
	graph := vadvisor.NewWordCloudChartsGraphWithPlayer[string, int]()
	buckets := wc.Dimension.Dimension.Dimension.Buckets
	if len(buckets) > top {
		buckets = buckets[:top]
	}
	for _, bucket := range buckets {
		graph.AddData("word_cloud",
			&vadvisor.ValueUnit[string, int]{
				XField:      bucket.Key,
				YField:      bucket.DocCount,
				SeriesField: strconv.Itoa(bucket.DocCount),
			})
	}
	if withPlayer {
		return graph.BuildPlayer(ctx)
	}
	return graph.Build(ctx)
}

func searchChunkLogs(ctx context.Context, query wordChunkQuery) ([]*xmodel.MessageChunkLogV3, error) {
	result, err := searchChunkLogsPage(ctx, query)
	if err != nil {
		return nil, err
	}
	return result.Items, nil
}

func searchChunkLogsPage(ctx context.Context, query wordChunkQuery) (wordChunkSearchResult, error) {
	query = normalizeWordChunkQuery(query)

	boolQuery := elastic.NewBoolQuery().Must(elastic.NewTermQuery("group_id", query.ChatID))
	if strings.TrimSpace(query.ChunkID) != "" {
		boolQuery = boolQuery.Must(elastic.NewTermQuery("id", strings.TrimSpace(query.ChunkID)))
	} else {
		boolQuery = boolQuery.Must(
			elastic.NewRangeQuery("timestamp_v2").Gte(query.Start.Format(time.RFC3339)).Lte(query.End.Format(time.RFC3339)),
		)
	}
	if query.Intent != WordChunkIntentTypeAll {
		boolQuery = boolQuery.Must(elastic.NewTermQuery("intent", string(query.Intent)))
	}
	if query.Sentiment != WordChunkSentimentTypeAll {
		boolQuery = boolQuery.Must(elastic.NewTermQuery("sentiment_and_tone.sentiment", string(query.Sentiment)))
	}
	switch query.QuestionMode {
	case WordChunkQuestionModeQuestion:
		boolQuery = boolQuery.Must(elastic.NewTermQuery("interaction_analysis.is_question_present", true))
	case WordChunkQuestionModeNoQuestion:
		boolQuery = boolQuery.Must(elastic.NewTermQuery("interaction_analysis.is_question_present", false))
	}

	req := elastic.NewSearchRequest().
		Query(boolQuery).
		FetchSourceIncludeExclude([]string{}, []string{"conversation_embedding", "msg_list"}).
		SortBy(chunkSorter(query.Sort)).
		From(query.Offset).
		Size(query.Limit)

	body, err := req.Body()
	if err != nil {
		return wordChunkSearchResult{}, err
	}
	resp, err := opensearch.SearchDataStr(ctx, appconfig.GetLarkChunkIndex(ctx, query.ChatID, ""), body)
	if err != nil {
		return wordChunkSearchResult{}, err
	}

	result := make([]*xmodel.MessageChunkLogV3, 0, len(resp.Hits.Hits))
	for _, hit := range resp.Hits.Hits {
		chunkLog := &xmodel.MessageChunkLogV3{}
		if err := sonic.Unmarshal(hit.Source, chunkLog); err != nil {
			return wordChunkSearchResult{}, err
		}
		result = append(result, chunkLog)
	}
	return wordChunkSearchResult{
		Items: result,
		Total: int(resp.Hits.Total.Value),
	}, nil
}

func normalizeWordChunkQuery(query wordChunkQuery) wordChunkQuery {
	query.ChatID = strings.TrimSpace(query.ChatID)
	query.ChunkID = strings.TrimSpace(query.ChunkID)
	if query.Limit <= 0 {
		query.Limit = defaultWordChunkListTop
	}
	if query.Limit > maxWordChunkListTop {
		query.Limit = maxWordChunkListTop
	}
	if query.Offset < 0 {
		query.Offset = 0
	}
	if strings.TrimSpace(string(query.Sort)) == "" {
		query.Sort = WordCloudSortTypeRelevance
	}
	if strings.TrimSpace(string(query.Intent)) == "" {
		query.Intent = WordChunkIntentTypeAll
	}
	if strings.TrimSpace(string(query.Sentiment)) == "" {
		query.Sentiment = WordChunkSentimentTypeAll
	}
	if strings.TrimSpace(string(query.QuestionMode)) == "" {
		query.QuestionMode = WordChunkQuestionModeAll
	}
	return query
}

func chunkSorter(sortType WordCloudSortType) elastic.Sorter {
	if sortType == WordCloudSortTypeTime {
		return elastic.NewFieldSort("timestamp_v2").Desc()
	}
	return elastic.NewScriptSort(
		elastic.NewScript("doc['msg_ids'].size()").Lang("painless"), "number",
	).Order(false)
}

func buildChunkTemplateData(chunkLog *xmodel.MessageChunkLogV3) *larktpl.ChunkData[xmodel.MessageChunkLogV3] {
	if chunkLog == nil {
		return nil
	}
	chunkCopy := *chunkLog
	chunkData := &larktpl.ChunkData[xmodel.MessageChunkLogV3]{
		ChunkLog: &chunkCopy,
	}
	chunkData.ChunkLog.Intent = larkmsg.TagText(GetIntentPhraseWithFallback(chunkLog.Intent))
	if chunkData.ChunkLog.SentimentAndTone != nil {
		chunkData.Sentiment = larkmsg.TagText(SentimentColor(chunkData.ChunkLog.SentimentAndTone.Sentiment))
		toneTags := transStrings(chunkData.ChunkLog.SentimentAndTone.Tones, func(s string) string {
			return larkmsg.TagText(GetToneStyle(s))
		})
		if len(toneTags) > 2 {
			toneTags = toneTags[:2]
		}
		chunkData.Tones = strings.Join(toneTags, "")
	}
	chunkData.ChunkLog.SentimentAndTone = nil

	chunkData.UserIDs4Lark = transParticipants(chunkLog)
	chunkData.ChunkLog.OpenIDs = nil
	if chunkLog.InteractionAnalysis != nil {
		chunkData.UnresolvedQuestions = summarizeUnresolvedQuestions(chunkLog.InteractionAnalysis.UnresolvedQuestions)
	}
	return chunkData
}

func transParticipants(chunkLog *xmodel.MessageChunkLogV3) []*larktpl.UserUnit {
	if chunkLog == nil || chunkLog.InteractionAnalysis == nil {
		return nil
	}
	users := make([]*larktpl.UserUnit, 0, len(chunkLog.InteractionAnalysis.Participants))
	seen := make(map[string]struct{}, len(chunkLog.InteractionAnalysis.Participants))
	for _, participant := range chunkLog.InteractionAnalysis.Participants {
		if participant == nil || participant.User == nil || strings.TrimSpace(participant.OpenID) == "" {
			continue
		}
		if _, ok := seen[participant.OpenID]; ok {
			continue
		}
		seen[participant.OpenID] = struct{}{}
		users = append(users, &larktpl.UserUnit{ID: participant.OpenID})
	}
	return users
}

func transStrings[T any](items []T, mapper func(T) string) []string {
	result := make([]string, 0, len(items))
	for _, item := range items {
		result = append(result, mapper(item))
	}
	return result
}

func summarizeUnresolvedQuestions(items []string) string {
	trimmed := make([]string, 0, len(items))
	for _, item := range items {
		if text := strings.TrimSpace(item); text != "" {
			trimmed = append(trimmed, text)
		}
	}
	switch len(trimmed) {
	case 0:
		return "暂时没挂着的问题"
	case 1:
		return trimmed[0]
	case 2:
		return trimmed[0] + " 等 2 个待回答问题"
	default:
		return trimmed[0] + " 等 " + strconv.Itoa(len(trimmed)) + " 个待回答问题"
	}
}

func buildWordChunkListCard(ctx context.Context, scope wordCountScope, query wordChunkQuery, chunks []*xmodel.MessageChunkLogV3) larkmsg.RawCard {
	view := newWordChunkCardView(scope, query)
	return buildWordChunkListCardWithState(ctx, view, wordChunkSearchResult{
		Items: chunks,
		Total: len(chunks),
	})
}

func displayWordCountChat(ctx context.Context, chatID string) string {
	chatName := strings.TrimSpace(larkchat.GetChatName(ctx, chatID))
	if chatName != "" {
		return chatName
	}
	return strings.TrimSpace(chatID)
}

func displayWordChunkSort(sortType WordCloudSortType) string {
	if sortType == WordCloudSortTypeTime {
		return "时间"
	}
	return "相关度"
}

func displayWordChunkIntent(intent WordChunkIntentType) string {
	if intent == WordChunkIntentTypeAll || strings.TrimSpace(string(intent)) == "" {
		return "全部"
	}
	phrase, _ := GetIntentPhraseWithFallback(string(intent))
	return phrase
}

func displayWordChunkSentiment(sentiment WordChunkSentimentType) string {
	if sentiment == WordChunkSentimentTypeAll || strings.TrimSpace(string(sentiment)) == "" {
		return "全部"
	}
	label, _ := SentimentColor(string(sentiment))
	return label
}

func displayWordChunkQuestionMode(mode WordChunkQuestionMode) string {
	switch mode {
	case WordChunkQuestionModeQuestion:
		return "仅有问题"
	case WordChunkQuestionModeNoQuestion:
		return "仅无问题"
	default:
		return "全部"
	}
}

func displayChunkSentiment(chunk *xmodel.MessageChunkLogV3) string {
	if chunk == nil || chunk.SentimentAndTone == nil {
		return "-"
	}
	label, _ := SentimentColor(chunk.SentimentAndTone.Sentiment)
	return label
}

func displayChunkTones(chunk *xmodel.MessageChunkLogV3) string {
	if chunk == nil || chunk.SentimentAndTone == nil || len(chunk.SentimentAndTone.Tones) == 0 {
		return ""
	}
	tones := make([]string, 0, len(chunk.SentimentAndTone.Tones))
	for _, tone := range chunk.SentimentAndTone.Tones {
		label, _ := GetToneStyle(tone)
		tones = append(tones, label)
	}
	return joinPreview(tones, 3)
}

func displayChunkTimestamp(chunk *xmodel.MessageChunkLogV3) string {
	if chunk == nil {
		return "-"
	}
	for _, candidate := range []string{derefString(chunk.TimestampV2), chunk.Timestamp} {
		if strings.TrimSpace(candidate) == "" {
			continue
		}
		if t, err := parseFlexibleTime(candidate); err == nil {
			return t.In(utils.UTC8Loc()).Format("2006-01-02 15:04")
		}
	}
	return firstNonEmpty(strings.TrimSpace(chunk.Timestamp), derefString(chunk.TimestampV2), "-")
}

func countChunkParticipants(chunk *xmodel.MessageChunkLogV3) int {
	if chunk == nil || chunk.InteractionAnalysis == nil {
		return 0
	}
	return len(chunk.InteractionAnalysis.Participants)
}

func joinPreview(items []string, limit int) string {
	filtered := make([]string, 0, len(items))
	for _, item := range items {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		filtered = append(filtered, item)
		if limit > 0 && len(filtered) >= limit {
			break
		}
	}
	if len(filtered) == 0 {
		return "-"
	}
	return strings.Join(filtered, " / ")
}

func firstNonEmptySlice[T any, R ~[]string](value *T, getter func(*T) R) []string {
	if value == nil {
		return nil
	}
	result := getter(value)
	return result
}

func derefString(value *string) string {
	if value == nil {
		return ""
	}
	return strings.TrimSpace(*value)
}
