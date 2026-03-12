package handlers

import (
	"context"
	"sort"
	"strconv"
	"strings"
	"time"

	appconfig "github.com/BetaGoRobot/BetaGo-Redefine/internal/application/config"
	arktools "github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/ark_dal/tools"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/db/query"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/lark_dal/larkmsg"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/lark_dal/larkmsg/larktpl"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/xmodel"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/opensearch"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/otel"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/vadvisor"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/logs"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/utils"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/xcommand"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/xhandler"
	commonutils "github.com/BetaGoRobot/go_utils/common_utils"
	"github.com/bytedance/sonic"
	"github.com/defensestation/osquery"
	larkcore "github.com/larksuite/oapi-sdk-go/v3/core"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
	"github.com/olivere/elastic/v7"
	. "github.com/olivere/elastic/v7"
	"github.com/opensearch-project/opensearch-go/v4/opensearchapi"
	"go.uber.org/zap"
)

type WordCloudArgs struct {
	Days       int               `json:"days"`
	Interval   string            `json:"interval"`
	MessageTop int               `json:"message_top" cli:"mtop"`
	ChunkTop   int               `json:"chunk_top" cli:"ctop"`
	ChatID     string            `json:"chat_id"`
	Sort       WordCloudSortType `json:"sort"`
	StartTime  string            `json:"start_time" cli:"st"`
	EndTime    string            `json:"end_time" cli:"et"`
}

type wordCloudHandler struct{}

var WordCloud wordCloudHandler

func (wordCloudHandler) ParseCLI(args []string) (WordCloudArgs, error) {
	argMap, _ := parseArgs(args...)
	sortType, err := xcommand.ParseEnum[WordCloudSortType](argMap["sort"])
	if err != nil {
		return WordCloudArgs{}, err
	}
	parsed := WordCloudArgs{
		Days:       7,
		Interval:   "1d",
		MessageTop: 10,
		ChunkTop:   5,
		ChatID:     argMap["chat_id"],
		Sort:       sortType,
		StartTime:  argMap["st"],
		EndTime:    argMap["et"],
	}
	if argMap["interval"] != "" {
		parsed.Interval = argMap["interval"]
	}
	if daysStr := argMap["days"]; daysStr != "" {
		if days, err := strconv.Atoi(daysStr); err == nil && days > 0 {
			parsed.Days = days
		}
	}
	if mTopStr := argMap["mtop"]; mTopStr != "" {
		if mTop, err := strconv.Atoi(mTopStr); err == nil && mTop > 0 {
			parsed.MessageTop = mTop
		} else {
			parsed.MessageTop = 15
		}
	}
	if cTopStr := argMap["ctop"]; cTopStr != "" {
		if cTop, err := strconv.Atoi(cTopStr); err == nil && cTop > 0 {
			parsed.ChunkTop = cTop
		}
	}
	return parsed, nil
}

func (wordCloudHandler) ParseTool(raw string) (WordCloudArgs, error) {
	parsed := WordCloudArgs{
		Days:       7,
		Interval:   "1d",
		MessageTop: 10,
		ChunkTop:   5,
	}
	if err := utils.UnmarshalStringPre(raw, &parsed); err != nil {
		return WordCloudArgs{}, err
	}
	if parsed.Days <= 0 {
		parsed.Days = 7
	}
	if parsed.Interval == "" {
		parsed.Interval = "1d"
	}
	if parsed.MessageTop <= 0 {
		parsed.MessageTop = 10
	}
	if parsed.ChunkTop <= 0 {
		parsed.ChunkTop = 5
	}
	sortType, err := xcommand.ParseEnum[WordCloudSortType](string(parsed.Sort))
	if err != nil {
		return WordCloudArgs{}, err
	}
	parsed.Sort = sortType
	if parsed.StartTime != "" && parsed.EndTime != "" {
		parsed.StartTime = normalizeDateTime(parsed.StartTime)
		parsed.EndTime = normalizeDateTime(parsed.EndTime)
	}
	return parsed, nil
}

func (wordCloudHandler) ToolSpec() xcommand.ToolSpec {
	return xcommand.ToolSpec{
		Name: "word_cloud_get",
		Desc: "生成群聊词云、活跃用户和热点话题摘要",
		Params: arktools.NewParams("object").
			AddProp("days", &arktools.Prop{
				Type: "number",
				Desc: "统计近几天的词云，默认 7",
			}).
			AddProp("interval", &arktools.Prop{
				Type: "string",
				Desc: "聚合间隔，例如 1d、12h、1h",
			}).
			AddProp("message_top", &arktools.Prop{
				Type: "number",
				Desc: "活跃用户 Top N，默认 10",
			}).
			AddProp("chunk_top", &arktools.Prop{
				Type: "number",
				Desc: "热点话题块 Top N，默认 5",
			}).
			AddProp("chat_id", &arktools.Prop{
				Type: "string",
				Desc: "目标群聊 ID，不填则使用当前群聊",
			}).
			AddProp("sort", &arktools.Prop{
				Type: "string",
				Desc: "摘要排序方式",
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

func (wordCloudHandler) Handle(ctx context.Context, data *larkim.P2MessageReceiveV1, metaData *xhandler.BaseMetaData, arg WordCloudArgs) (err error) {
	ctx, span := otel.Start(ctx)
	span.SetAttributes(otel.PreviewAttrs("event", larkcore.Prettify(data), 256)...)
	defer span.End()
	defer func() { otel.RecordError(span, err) }()

	var (
		st, et time.Time
	)
	chatID := currentChatID(data, metaData)
	if arg.ChatID != "" {
		chatID = arg.ChatID
	}
	var (
		sort Sorter = NewScriptSort(
			NewScript("script").Script("doc['msg_ids'].size()").Lang("painless"), "number",
		).Order(false)
	)
	if arg.Sort == WordCloudSortTypeTime {
		sort = NewFieldSort("timestamp_v2").Desc()
	}

	st, et = GetBackDays(arg.Days)
	if arg.StartTime != "" && arg.EndTime != "" {
		st, err = time.ParseInLocation(time.DateTime, arg.StartTime, utils.UTC8Loc())
		if err != nil {
			return err
		}
		et, err = time.ParseInLocation(time.DateTime, arg.EndTime, utils.UTC8Loc())
		if err != nil {
			return err
		}
	}

	helper := &trendInternalHelper{
		days: arg.Days, st: st, et: et, msgID: currentMessageID(data), chatID: chatID, interval: arg.Interval,
	}

	userList, err := genHotRate(ctx, helper, arg.MessageTop)
	if err != nil {
		return
	}

	wc, err := genWordCount(ctx, chatID, st, et)
	if err != nil {
		return
	}

	chunks, err := getChunks(ctx, chatID, st, et, arg.ChunkTop, sort)
	if err != nil {
		return
	}
	wordCloud := vadvisor.NewWordCloudChartsGraphWithPlayer[string, int]()
	for _, bucket := range wc.Dimension.Dimension.Dimension.Buckets {
		wordCloud.AddData("user_name",
			&vadvisor.ValueUnit[string, int]{
				XField:      bucket.Key,
				YField:      bucket.DocCount,
				SeriesField: strconv.Itoa(bucket.DocCount),
			})
	}
	wordCloud.Build(ctx)

	tpl := larktpl.GetTemplateV2[larktpl.WordCountCardVars[xmodel.MessageChunkLogV3]](ctx, larktpl.WordCountTemplate)
	cardVar := &larktpl.WordCountCardVars[xmodel.MessageChunkLogV3]{
		UserList:  userList,
		WordCloud: wordCloud,
		Chunks:    chunks,
		StartTime: st.Format("2006-01-02 15:04"),
		EndTime:   et.Format("2006-01-02 15:04"),
	}
	tpl.WithData(cardVar)
	cardContent := larktpl.NewCardContentV2(ctx, tpl)
	return sendCompatibleCard(ctx, data, metaData, cardContent, "", false)
}

type WordCountType struct {
	Dimension struct {
		DocCount  int `json:"doc_count"`
		Dimension struct {
			DocCount  int `json:"doc_count"`
			Dimension struct {
				DocCountErrorUpperBound int `json:"doc_count_error_upper_bound"`
				SumOtherDocCount        int `json:"sum_other_doc_count"`
				Buckets                 []struct {
					Key      string `json:"key"`
					DocCount int    `json:"doc_count"`
				} `json:"buckets"`
			} `json:"dimension"`
		} `json:"dimension"`
	} `json:"dimension"`
}

// Style 定义了每个意图的展示风格，包括短语和颜色。
type Style struct {
	Phrase string
	Color  string
}

// IntentStyleMap 存储了意图与其对应风格的映射。
var IntentStyleMap = map[string]*Style{
	"SOCIAL_COORDINATION": {
		Phrase: "共商议事",
		Color:  "blue",
	},
	"INFORMATION_SHARING": {
		Phrase: "见闻共飨",
		Color:  "neutral",
	},
	"SEEKING_HELP_OR_ADVICE": {
		Phrase: "求教问策",
		Color:  "green",
	},
	"DEBATE_OR_DISCUSSION": {
		Phrase: "明辨事理",
		Color:  "indigo",
	},
	"EMOTIONAL_SHARING_OR_SUPPORT": {
		Phrase: "悲欢与共",
		Color:  "violet",
	},
	"REQUESTING_RECOMMENDATION": {
		Phrase: "求珍问宝",
		Color:  "orange",
	},
	"CASUAL_CHITCHAT": {
		Phrase: "谈天说地",
		Color:  "yellow",
	},
}

// GetIntentPhraseWithFallback 是一个更简洁的转换函数。
// 它接受一个意图 key，如果找到则返回对应的中文短语。
// 如果未找到，它会返回原始的 key 作为备用值，这样调用方总能获得一个可显示的字符串。
func GetIntentPhraseWithFallback(intentKey string) (phrase string, color string) {
	if phrase, ok := IntentStyleMap[intentKey]; ok {
		return phrase.Phrase, phrase.Color
	}
	// 返回原始 key 作为备用
	return intentKey, "neutral"
}

// ToneStyleMap 存储了语气与其对应风格的映射。
var ToneStyleMap = map[string]*Style{
	"HUMOROUS":      {Phrase: "妙语连珠", Color: "lime"},
	"SUPPORTIVE":    {Phrase: "暖心慰藉", Color: "turquoise"},
	"CURIOUS":       {Phrase: "寻根究底", Color: "purple"},
	"EXCITED":       {Phrase: "兴高采烈", Color: "carmine"},
	"URGENT":        {Phrase: "迫在眉睫", Color: "red"},
	"FORMAL":        {Phrase: "严谨庄重", Color: "indigo"},
	"INFORMAL":      {Phrase: "随心而谈", Color: "wathet"},
	"SARCASTIC":     {Phrase: "反语相讥", Color: "yellow"},
	"ARGUMENTATIVE": {Phrase: "唇枪舌剑", Color: "orange"},
	"NOSTALGIC":     {Phrase: "追忆往昔", Color: "violet"},
}

// GetToneStyle 函数用于安全地获取语气的风格。
func GetToneStyle(key string) (phrase string, color string) {
	if phrase, ok := ToneStyleMap[key]; ok {
		return phrase.Phrase, phrase.Color
	}
	// 返回原始 key 作为备用
	return key, "neutral"
}

func getChunks(ctx context.Context, chatID string, st, et time.Time, size int, sort elastic.Sorter) (chunks []*larktpl.ChunkData[xmodel.MessageChunkLogV3], err error) {
	chunks = make([]*larktpl.ChunkData[xmodel.MessageChunkLogV3], 0)
	queryReq := NewSearchRequest().
		Query(NewBoolQuery().Must(
			NewTermQuery("group_id", chatID),
			NewRangeQuery("timestamp_v2").Gte(st.Format(time.RFC3339)).Lte(et.Format(time.RFC3339)),
		)).
		FetchSourceIncludeExclude(
			[]string{}, []string{"conversation_embedding", "msg_ids", "msg_list"},
		).
		SortBy(sort).Size(size)
	// SortBy(
	// 	NewFieldSort("timestamp").Desc(),
	// ).
	// Size(5)

	data, err := queryReq.Body()
	if err != nil {
		return
	}
	resp, err := opensearch.SearchDataStr(ctx, appconfig.GetLarkChunkIndex(ctx, chatID, ""), data)
	if err != nil {
		return
	}

	return commonutils.TransSlice(resp.Hits.Hits, func(hit opensearchapi.SearchHit) (target *larktpl.ChunkData[xmodel.MessageChunkLogV3]) {
		chunkLog := &xmodel.MessageChunkLogV3{}
		sonic.Unmarshal(hit.Source, chunkLog)
		chunkData := &larktpl.ChunkData[xmodel.MessageChunkLogV3]{
			ChunkLog: chunkLog,
		}
		chunkData.ChunkLog.Intent = larkmsg.TagText(GetIntentPhraseWithFallback(chunkLog.Intent))
		chunkData.Sentiment = larkmsg.TagText(SentimentColor(chunkData.ChunkLog.SentimentAndTone.Sentiment))
		chunkData.Tones = strings.Join(commonutils.TransSlice(chunkData.ChunkLog.SentimentAndTone.Tones, func(s string) string { return larkmsg.TagText(GetToneStyle(s)) }), "")
		chunkData.ChunkLog.SentimentAndTone = nil

		chunkData.UserIDs4Lark = commonutils.TransSlice(chunkLog.InteractionAnalysis.Participants, func(p *xmodel.Participant) *larktpl.UserUnit { return &larktpl.UserUnit{ID: p.OpenID} })
		chunkData.UserIDs4Lark = utils.Dedup(chunkData.UserIDs4Lark)
		chunkData.ChunkLog.OpenIDs = nil

		chunkData.UnresolvedQuestions = strings.Join(commonutils.TransSlice(chunkLog.InteractionAnalysis.UnresolvedQuestions, func(q string) string { return larkmsg.TagText(q, "red") }), "")
		return chunkData
	}), err
}

func genHotRate(ctx context.Context, helper *trendInternalHelper, top int) (userList []*larktpl.UserListItem, err error) {
	// 统计用户发送的消息数量
	trendMap := make(map[string]*larktpl.UserListItem)
	msgTrend, err := helper.TrendRate(ctx, appconfig.GetLarkMsgIndex(ctx, helper.chatID, ""), "user_id", uint64(top))
	for _, bucket := range msgTrend.Dimension.Buckets {
		trendMap[bucket.Key] = &larktpl.UserListItem{Number: -1, User: []*larktpl.UserUnit{{ID: bucket.Key}}, MsgCnt: bucket.DocCount}
	}
	type GroupResult struct {
		OpenID string // 根据你的实际 OpenID 类型定义 (string, int 等)
		Total  int64  `gorm:"column:total"` // 必须对应 As("total") 的名称
	}
	actionRes := make([]*GroupResult, 0)
	ins := query.Q.InteractionStat
	err = ins.WithContext(ctx).
		Select(ins.OpenID, ins.ALL.Count().As("total")).
		Where(ins.GuildID.Eq(helper.chatID), ins.CreatedAt.Gt(helper.st), ins.CreatedAt.Lt(helper.et)).
		Group(ins.OpenID).Scan(&actionRes)
	if err != nil {
		return
	}

	for _, res := range actionRes {
		if item, ok := trendMap[res.OpenID]; ok {
			item.ActionCnt = int(res.Total)
		} else {
			trendMap[res.OpenID] = &larktpl.UserListItem{Number: -1, User: []*larktpl.UserUnit{{ID: res.OpenID}}, ActionCnt: int(res.Total)}
		}
	}

	userList = make([]*larktpl.UserListItem, 0, len(trendMap))
	for _, item := range trendMap {
		userList = append(userList, item)
	}

	sort.Slice(userList, func(i, j int) bool {
		return userList[i].MsgCnt*10+userList[i].ActionCnt > userList[j].MsgCnt*10+userList[j].ActionCnt
	})
	for idx, item := range userList {
		item.Number = idx + 1
	}
	if len(userList) > top {
		userList = userList[:top]
	}
	return
}

func genWordCount(ctx context.Context, chatID string, st, et time.Time) (wc WordCountType, err error) {
	// 统计用户发送的
	tagsToInclude := []interface{}{
		"n", "nr", "ns", "nt", "nz",
		"v", "vd", "vn",
		"a", "ad", "an",
		"i", "l",
	}
	// 1. 构建最内层的聚合：统计词频 (word_counts)
	// 这是一个 terms aggregation
	wordCountsAgg := osquery.TermsAgg("dimension", "raw_message_jieba_tag.word").Size(100) // 返回前 50 个

	// 2. 构建中间层的聚合：根据词性进行过滤 (filtered_tags)
	// 这是一个 filter aggregation
	filteredTagsAgg := osquery.FilterAgg(
		"dimension",
		osquery.Bool().Must(
			osquery.Terms("raw_message_jieba_tag.tag", tagsToInclude...),
			osquery.CustomAgg("script", map[string]any{
				"script": map[string]any{
					"script": map[string]any{
						"source": "doc['raw_message_jieba_tag.word'].value.length() > 1",
						"lang":   "painless",
					},
				},
			}),
		),
	).Aggs(wordCountsAgg)

	// 3. 构建最外层的聚合：处理嵌套字段 (word_cloud_tags)
	// 这是一个 nested aggregation
	wordCloudTagsAgg := osquery.NestedAgg(
		"dimension",
		"raw_message_jieba_tag",
	).Aggs(filteredTagsAgg)

	// 4. 构建最终的查询对象
	query := osquery.Query(osquery.Bool().
		Must(
			osquery.Term("chat_id", chatID),
			osquery.Range("create_time_v2").
				Gte(st.Format(time.RFC3339)).
				Lte(et.Format(time.RFC3339)),
		)).
		Size(0). // 设置 size 为 0，表示不返回任何文档，只关心聚合结果
		Aggs(wordCloudTagsAgg)

	rawQuery, err := query.MarshalJSON()
	if err != nil {
		return
	}
	logs.L().Ctx(ctx).Info("wordCloudTagsAgg query", zap.String("query", string(rawQuery)))
	// 统计一下词频
	resp, err := opensearch.SearchData(ctx, appconfig.GetLarkMsgIndex(ctx, chatID, ""), query)
	if err != nil {
		return
	}

	wc = WordCountType{}
	err = sonic.Unmarshal(resp.Aggregations, &wc)
	if err != nil {
		return
	}
	return
}

func SentimentColor(sentiment string) (string, string) {
	// `POSITIVE`, `NEGATIVE`, `NEUTRAL`, `MIXED`
	switch sentiment {
	case "POSITIVE":
		return "正向", "green"
	case "NEGATIVE":
		return "负向", "red"
	case "NEUTRAL":
		return "中性", "blue"
	case "MIXED":
		return "混合", "yellow"
	default:
		return sentiment, "lime"
	}
}

func (wordCloudHandler) CommandDescription() string {
	return "生成词云和热点摘要"
}

func (wordCloudHandler) CommandExamples() []string {
	return []string{
		"/wc --days=7 --mtop=10 --ctop=5",
		"/wc --sort=time --chat_id=oc_xxx",
	}
}
