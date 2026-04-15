package history

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	appconfig "github.com/BetaGoRobot/BetaGo-Redefine/internal/application/config"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/botidentity"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/db/model"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/db/query"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/lark_dal/larkmsg/larkcontent"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/opensearch"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/otel"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/xmodel"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/logs"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/utils"
	commonutils "github.com/BetaGoRobot/go_utils/common_utils"
	"github.com/bytedance/sonic"
	"github.com/defensestation/osquery"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
	"go.uber.org/zap"

	"github.com/opensearch-project/opensearch-go/v4/opensearchapi"
	"go.opentelemetry.io/otel/attribute"
)

// Helper  to be filled
//
//	@author kevinmatthe
//	@update 2025-04-30 13:10:06
type Helper struct {
	context.Context

	index  string
	query  osquery.Mappable
	source []string
	size   *uint64
	aggs   []osquery.Aggregation
	sorts  []sortField
}

type sortField struct {
	name  string
	order osquery.Order
}

// New to be filled
//
//	@return *HistoryHelper
//	@author kevinmatthe
//	@update 2025-04-30 13:10:02
func New(ctx context.Context) *Helper {
	return &Helper{
		Context: ctx,
	}
}

// Index to be filled
//
//	@receiver h *Helper
//	@param index string
//	@return *Helper
//	@author kevinmatthe
//	@update 2025-04-30 13:11:28
func (h *Helper) Index(index string) *Helper {
	h.index = index
	return h
}

// Size to be filled
//
//	@receiver h *Helper
//	@param size uint64
//	@return *Helper
//	@author kevinmatthe
//	@update 2025-04-30 13:16:42
func (h *Helper) Size(size uint64) *Helper {
	h.size = &size
	return h
}

// Query to be filled
//
//	@receiver h *HistoryHelper
//	@param query osquery.Mappable
//	@return *HistoryHelper
//	@author kevinmatthe
//	@update 2025-04-30 13:09:55
func (h *Helper) Query(query osquery.Mappable) *Helper {
	h.query = query
	return h
}

// Aggs to be filled
//
//	@receiver h *HistoryHelper
//	@param query osquery.Mappable
//	@return *HistoryHelper
//	@author kevinmatthe
//	@update 2025-04-30 13:09:55
func (h *Helper) Aggs(aggs ...osquery.Aggregation) *Helper {
	h.aggs = append(h.aggs, aggs...)
	return h
}

// Source to be filled
//
//	@receiver h *HistoryHelper
//	@param source []string
//	@return *HistoryHelper
//	@author kevinmatthe
//	@update 2025-04-30 13:10:00
func (h *Helper) Source(source ...string) *Helper {
	h.source = append([]string(nil), source...)
	return h
}

// Sort to be filled
//
//	@receiver h *Helper
//	@param name string
//	@param order osquery.Order
//	@return *Helper
//	@author kevinmatthe
//	@update 2025-04-30 13:14:55
func (h *Helper) Sort(name string, order osquery.Order) *Helper {
	h.sorts = append(h.sorts, sortField{name: name, order: order})
	return h
}

func (h *Helper) buildRequest() *osquery.SearchRequest {
	req := osquery.Search()
	if h.query != nil {
		req.Query(h.query)
	}
	if h.size != nil {
		req.Size(*h.size)
	}
	if len(h.source) > 0 {
		req.SourceIncludes(h.source...)
	}
	if len(h.aggs) > 0 {
		req.Aggs(h.aggs...)
	}
	for _, sort := range h.sorts {
		req.Sort(sort.name, sort.order)
	}
	return req
}

func (h *Helper) resolvedIndex() string {
	if strings.TrimSpace(h.index) != "" {
		return h.index
	}
	return appconfig.GetLarkMsgIndex(h.Context, "", "")
}

func (h *Helper) traceSize() int64 {
	if h.size == nil {
		return 0
	}
	return int64(*h.size)
}

func (h *Helper) GetMsg() (messageList OpensearchMsgLogList, err error) {
	_, span := otel.Start(h.Context)
	defer span.End()
	defer func() { otel.RecordError(span, err) }()

	resp, err := h.GetRaw()
	if err != nil {
		return
	}
	messageList = FilterMessage(h, resp.Hits.Hits)
	return
}

func (h *Helper) GetRaw() (resp *opensearchapi.SearchResp, err error) {
	ctx, span := otel.Start(h.Context)
	defer span.End()
	defer func() { otel.RecordError(span, err) }()
	req := h.buildRequest()
	index := h.resolvedIndex()
	span.SetAttributes(
		attribute.Key("index").String(index),
		attribute.Key("query").String(utils.MustMarshalString(h.query)),
		attribute.Key("source").StringSlice(h.source),
		attribute.Key("size").Int64(h.traceSize()),
	)

	resp, err = opensearch.
		SearchData(
			ctx,
			index,
			req,
		)
	return
}

func (h *Helper) GetAll() (messageList []*xmodel.MessageIndex, err error) {
	_, span := otel.Start(h.Context)
	defer span.End()
	defer func() { otel.RecordError(span, err) }()

	resp, err := h.GetRaw()
	return commonutils.TransSlice(resp.Hits.Hits, func(hit opensearchapi.SearchHit) *xmodel.MessageIndex {
		messageIndex := &xmodel.MessageIndex{}
		err := sonic.Unmarshal(hit.Source, messageIndex)
		if err != nil {
			return nil
		}
		return messageIndex
	}), nil
}

type (
	TrendSeries []*TrendItem
	TrendItem   struct {
		Time  string `json:"time"`  // x轴
		Value int64  `json:"value"` // y轴
		Key   string `json:"key"`   // 序列
	}
	TrendAggData struct {
		Agg1 struct {
			Buckets []struct {
				KeyAsString string `json:"key_as_string"`
				Key         int64  `json:"key"`
				DocCount    int    `json:"doc_count"`
				Agg2        struct {
					DocCountErrorUpperBound int `json:"doc_count_error_upper_bound"`
					SumOtherDocCount        int `json:"sum_other_doc_count"`
					Buckets                 []struct {
						Key      string `json:"key"`
						DocCount int    `json:"doc_count"`
					} `json:"buckets"`
				} `json:"agg2"`
			} `json:"buckets"`
		} `json:"agg1"`
	}
	Bucket struct {
		Key      string `json:"key"`
		DocCount int    `json:"doc_count"`
	}

	SingleDimAggregate struct {
		Dimension struct {
			DocCountErrorUpperBound int       `json:"doc_count_error_upper_bound"`
			SumOtherDocCount        int       `json:"sum_other_doc_count"`
			Buckets                 []*Bucket `json:"buckets"`
		} `json:"dimension"`
	}
)

func (h *Helper) GetTrend(interval, termField string) (trendList TrendSeries, err error) {
	ctx, span := otel.Start(h.Context)
	defer span.End()
	defer func() { otel.RecordError(span, err) }()

	span.SetAttributes(
		attribute.Key("index").String(h.resolvedIndex()),
		attribute.Key("query").String(utils.MustMarshalString(h.query)),
		attribute.Key("source").StringSlice(h.source),
		attribute.Key("size").Int64(h.traceSize()),
	)

	aggKey1 := "agg1"
	aggKey2 := "agg2"
	req := h.buildRequest()
	req.Aggs(
		osquery.CustomAgg(
			aggKey1,
			map[string]any{
				"date_histogram": map[string]any{
					"field":    "create_time_v2",
					"interval": interval,
				},
				"aggs": map[string]any{
					aggKey2: map[string]any{
						"terms": map[string]any{
							"field": termField,
						},
					},
				},
			},
		),
	)
	resp, err := opensearch.
		SearchData(
			ctx,
			h.resolvedIndex(),
			req,
		)
	if err != nil {
		return
	}

	trendList = make(TrendSeries, 0)

	jsonBytes, err := resp.Aggregations.MarshalJSON()
	if err != nil {
		return
	}
	aggData := &TrendAggData{}
	err = sonic.ConfigStd.Unmarshal(jsonBytes, aggData)
	if err != nil {
		return
	}

	for _, bucket := range aggData.Agg1.Buckets {
		for _, item := range bucket.Agg2.Buckets {
			trendList = append(trendList, &TrendItem{
				Time:  bucket.KeyAsString,
				Value: int64(item.DocCount),
				Key:   item.Key,
			})
		}
	}
	return
}

type OpensearchMsgLogList []*OpensearchMsgLog

func (o OpensearchMsgLogList) ToLines() (msgList []string) {
	msgList = make([]string, 0)
	for _, item := range o {
		msgList = append(msgList, item.ToLine())
	}
	return
}

// ToThreadLines converts messages to lines with thread/reply structure.
// Topics (ThreadID != "") are grouped together with indentation.
// Non-topic reply chains are grouped by temporary thread keys.
func (o OpensearchMsgLogList) ToThreadLines() []string {
	if len(o) == 0 {
		return nil
	}

	// Build messageID -> index map for parent lookup
	idToIdx := make(map[string]int, len(o))
	for i, msg := range o {
		idToIdx[msg.MessageID] = i
	}

	// Assign temporary thread keys to group reply chains
	// Returns the thread key and whether this message is a reply within the result set
	type threadAssign struct {
		ThreadKey string
		IsReply   bool
		Depth     int
	}
	threadAssigns := make([]threadAssign, len(o))

	// Track which messages are roots (not replied to by any other message in result)
	isRepliedTo := make(map[string]bool)
	for _, msg := range o {
		if msg.ParentID != "" {
			isRepliedTo[msg.ParentID] = true
		}
	}

	// First pass: assign thread keys
	for i, msg := range o {
		if msg.ThreadID != "" {
			// This is a topic group message
			threadAssigns[i] = threadAssign{
				ThreadKey: msg.ThreadID,
				IsReply:   false,
				Depth:     0,
			}
		} else if msg.ParentID == "" {
			// Root message (no parent), not in a topic
			threadAssigns[i] = threadAssign{
				ThreadKey: msg.MessageID, // Use own ID as key
				IsReply:   false,
				Depth:     0,
			}
		} else {
			// Has parent - need to determine depth and thread key
			parentIdx, parentExists := idToIdx[msg.ParentID]
			if parentExists {
				// Direct parent is in result set - inherit thread key and increase depth
				parentAssign := threadAssigns[parentIdx]
				threadAssigns[i] = threadAssign{
					ThreadKey: parentAssign.ThreadKey,
					IsReply:   true,
					Depth:     parentAssign.Depth + 1,
				}
			} else {
				// Parent not in result set - this is a root of a missing chain
				// Generate temp key based on parent
				threadAssigns[i] = threadAssign{
					ThreadKey: "orphan:" + msg.ParentID,
					IsReply:   false,
					Depth:     0,
				}
			}
		}
	}

	// Group by thread key, maintaining reverse chronological order (as returned by OpenSearch)
	type threadGroup struct {
		msgs    []*OpensearchMsgLog
		depths  []int
		isReply []bool
	}
	groups := make(map[string]*threadGroup)

	// Messages are already in reverse chronological order (newest first)
	// We want to present them grouped, with replies indented under their parent
	// Since OpenSearch returns newest first, and FilterMessage reverses to oldest first,
	// o is in chronological order (oldest first)
	for i, msg := range o {
		key := threadAssigns[i].ThreadKey
		if groups[key] == nil {
			groups[key] = &threadGroup{}
		}
		groups[key].msgs = append(groups[key].msgs, msg)
		groups[key].depths = append(groups[key].depths, threadAssigns[i].Depth)
		groups[key].isReply = append(groups[key].isReply, threadAssigns[i].IsReply)
	}

	// Build output lines
	var lines []string

	// Track which thread keys we've already added headers for
	addedThreadHeader := make(map[string]bool)

	for i, msg := range o {
		assign := threadAssigns[i]

		// Add thread header for topic groups (ThreadID != "")
		if msg.ThreadID != "" && !addedThreadHeader[msg.ThreadID] {
			if len(lines) > 0 {
				lines = append(lines, "") // blank line before new topic
			}
			lines = append(lines, fmt.Sprintf("=== 话题讨论 [%s] ===", msg.ThreadID))
			addedThreadHeader[msg.ThreadID] = true
		}

		// Format the message line with indentation for replies
		baseLine := fmt.Sprintf("[%s](%s) <%s>: %s",
			msg.CreateTime, msg.OpenID, msg.UserName, strings.Join(msg.MsgList, ";"))

		if assign.Depth == 0 && !assign.IsReply {
			lines = append(lines, baseLine)
		} else {
			// Indent with └ to show reply relationship
			indent := strings.Repeat("  ", assign.Depth) + "└ "
			lines = append(lines, indent+baseLine)
		}

		// If this is a reply to a message not in result set, note it
		if msg.ParentID != "" && !isRepliedTo[msg.MessageID] {
			parentIdx, parentExists := idToIdx[msg.ParentID]
			if !parentExists {
				// Parent is outside result window
				lines = append(lines, strings.Repeat("  ", assign.Depth+1)+"   (回复对象不在当前历史范围内)")
			} else {
				// Check if parent was processed - if parent is newer (later in chronological order)
				// it would have been processed already, so we show this as continuation
				if parentIdx < i {
					// Parent is earlier in our list (older message)
					parentMsg := o[parentIdx]
					lines = append(lines, strings.Repeat("  ", assign.Depth+1)+fmt.Sprintf("   (回复: %s)", strings.Join(parentMsg.MsgList, ";")))
				}
			}
		}
	}

	return lines
}

type OpensearchMsgLog struct {
	CreateTime  string            `json:"create_time"`
	CreateTimeV2 string           `json:"create_time_v2"`
	OpenID      string            `json:"user_id"`
	UserName    string            `json:"user_name"`
	MsgList     []string          `json:"msg_list"`
	MentionList []*larkim.Mention `json:"mention_list"`
	MessageID   string            `json:"message_id"`
	ParentID    string            `json:"parent_id"`
	RootID      string            `json:"root_id"`
	ThreadID    string            `json:"thread_id"`
}

func (o *OpensearchMsgLog) ToLine() (msgList string) {
	return fmt.Sprintf("[%s](%s) <%s>: %s", o.CreateTime, o.OpenID, o.UserName, strings.Join(o.MsgList, ";"))
}

func FilterMessage(ctx context.Context, hits []opensearchapi.SearchHit) (msgList []*OpensearchMsgLog) {
	msgList = make([]*OpensearchMsgLog, 0)
	for _, hit := range hits {
		res := &xmodel.MessageIndex{}
		b, _ := hit.Source.MarshalJSON()
		err := sonic.ConfigStd.Unmarshal(b, res)
		if err != nil {
			continue
		}
		mentions := make([]*larkim.Mention, 0)
		utils.UnmarshalStrPre(res.Mentions, &mentions)
		logs.L().Ctx(ctx).Debug("filteringMsg",
			zap.Any("mentions", mentions),
			zap.Any("rawMsg", res),
		)
		tmpList := make([]string, 0)
		for msgItem := range larkcontent.
			GetContentItemsSeq(
				&larkim.EventMessage{
					Content:     &res.RawMessage,
					MessageType: &res.MessageType,
				},
			) {
			switch msgItem.Tag {
			case "at", "text":
				if len(mentions) > 0 {
					currentBot := botidentity.Current()
					for _, mention := range mentions {
						if mention.Key != nil {
							if mention.Id != nil && currentBot.BotOpenID != "" && *mention.Id == currentBot.BotOpenID {
								*mention.Name = "你"
							}
							msgItem.Content = strings.ReplaceAll(msgItem.Content, *mention.Key, fmt.Sprintf("@%s", *mention.Name))
						}
					}
				}
				fallthrough
			default:
				content := strings.ReplaceAll(msgItem.Content, "\n", "<换行>")
				if strings.TrimSpace(content) != "" {
					tmpList = append(tmpList, content)
				}
			}
		}
		if len(tmpList) == 0 {
			continue
		}
		currentBot := botidentity.Current()
		if currentBot.BotOpenID != "" {
			if strings.TrimSpace(res.OpenID) == "你" {
				res.OpenID = currentBot.BotOpenID
			}
			if strings.TrimSpace(res.OpenID) == currentBot.BotOpenID {
				res.UserName = "你"
			}
		}
		l := &OpensearchMsgLog{
			CreateTime:   res.CreateTime,
			CreateTimeV2: res.CreateTimeV2,
			OpenID:      res.OpenID,
			UserName:    res.UserName,
			MsgList:     tmpList,
			MentionList: mentions,
			MessageID:   res.MessageID,
			ParentID:    res.ParentID,
			RootID:      res.RootID,
			ThreadID:    res.ThreadID,
		}
		if r := l.ToLine(); r != "" {
			msgList = append(msgList, l)
		}
	}
	slices.Reverse(msgList)
	return msgList
}

// GetMsgFromPG queries PG for message relationships first, then fetches content from OpenSearch.
// This approach uses PG as source of truth for relationships and OpenSearch only for content.
func GetMsgFromPG(ctx context.Context, chatID string, cutoffTime string, size int, currentMsgThreadID string, currentMsgParentID string) (OpensearchMsgLogList, error) {
	_, span := otel.Start(ctx)
	defer span.End()

	var err error

	// 1. Query PG for messages in chat within time range
	pgQuery := query.Q.MessageLog.WithContext(ctx).Where(
		query.Q.MessageLog.ChatID.Eq(chatID),
	)
	if cutoffTime != "" {
		cutoff, parseErr := time.Parse(time.RFC3339, cutoffTime)
		if parseErr != nil {
			cutoff, parseErr = time.Parse("2006-01-02 15:04:05", cutoffTime)
			if parseErr != nil {
				return nil, fmt.Errorf("invalid cutoff time: %w", parseErr)
			}
		}
		pgQuery = pgQuery.Where(query.Q.MessageLog.CreatedAt.Gte(cutoff))
	}
	pgQuery = pgQuery.Order(query.Q.MessageLog.CreatedAt.Desc())

	var pgMessages []*model.MessageLog
	pgMessages, err = pgQuery.Limit(size * 3).Find()
	if err != nil {
		return nil, fmt.Errorf("query PG for message meta failed: %w", err)
	}

	if len(pgMessages) == 0 {
		return nil, nil
	}

	// 2. Build ID list and expand relationships from PG
	msgIDSet := make(map[string]bool)
	var msgIDs []string

	for _, m := range pgMessages {
		if !msgIDSet[m.MessageID] {
			msgIDSet[m.MessageID] = true
			msgIDs = append(msgIDs, m.MessageID)
		}
	}

	// If current message has parent, expand from PG
	if currentMsgParentID != "" && !msgIDSet[currentMsgParentID] {
		expandFromPG(ctx, currentMsgParentID, msgIDSet, &msgIDs)
	}

	// If current message has thread, expand thread from PG
	if currentMsgThreadID != "" {
		expandThreadFromPG(ctx, chatID, currentMsgThreadID, msgIDSet, &msgIDs)
	}

	// Expand missing parents for reply chains
	for _, m := range pgMessages {
		if m.ParentID != "" && !msgIDSet[m.ParentID] {
			expandFromPG(ctx, m.ParentID, msgIDSet, &msgIDs)
		}
	}

	// 3. Query OpenSearch for content by message IDs (with retry)
	osResults, err := fetchOpenSearchWithRetry(ctx, appconfig.GetLarkMsgIndex(ctx, chatID, ""), msgIDs)
	if err != nil {
		return nil, err
	}
	if len(osResults) == 0 {
		return nil, nil
	}

	// 4. Build message list, maintaining chronological order
	msgIDToMeta := make(map[string]*model.MessageLog)
	for _, m := range pgMessages {
		msgIDToMeta[m.MessageID] = m
	}

	msgList := make([]*OpensearchMsgLog, 0, len(osResults))
	for _, hit := range osResults {
		res := &xmodel.MessageIndex{}
		if err := sonic.Unmarshal(hit.Source, res); err != nil {
			continue
		}
		meta := msgIDToMeta[res.MessageID]

		mentions := make([]*larkim.Mention, 0)
		utils.UnmarshalStrPre(res.Mentions, &mentions)
		tmpList := make([]string, 0)
		for msgItem := range larkcontent.GetContentItemsSeq(
			&larkim.EventMessage{
				Content:     &res.RawMessage,
				MessageType: &res.MessageType,
			},
		) {
			switch msgItem.Tag {
			case "at", "text":
				if len(mentions) > 0 {
					currentBot := botidentity.Current()
					for _, mention := range mentions {
						if mention.Key != nil {
							if mention.Id != nil && currentBot.BotOpenID != "" && *mention.Id == currentBot.BotOpenID {
								*mention.Name = "你"
							}
							msgItem.Content = strings.ReplaceAll(msgItem.Content, *mention.Key, fmt.Sprintf("@%s", *mention.Name))
						}
					}
				}
				fallthrough
			default:
				content := strings.ReplaceAll(msgItem.Content, "\n", "<换行>")
				if strings.TrimSpace(content) != "" {
					tmpList = append(tmpList, content)
				}
			}
		}
		if len(tmpList) == 0 {
			continue
		}

		currentBot := botidentity.Current()
		if currentBot.BotOpenID != "" {
			if strings.TrimSpace(res.OpenID) == "你" {
				res.OpenID = currentBot.BotOpenID
			}
			if strings.TrimSpace(res.OpenID) == currentBot.BotOpenID {
				res.UserName = "你"
			}
		}

		var createTimeV2 string
		if meta != nil {
			createTimeV2 = meta.CreatedAt.Format(time.RFC3339)
		}

		l := &OpensearchMsgLog{
			CreateTime:   res.CreateTime,
			CreateTimeV2: createTimeV2,
			OpenID:      res.OpenID,
			UserName:    res.UserName,
			MsgList:     tmpList,
			MentionList: mentions,
			MessageID:   res.MessageID,
			ParentID:    res.ParentID,
			RootID:      res.RootID,
			ThreadID:    res.ThreadID,
		}
		msgList = append(msgList, l)
	}

	// Sort by CreateTimeV2 ascending (chronological order)
	slices.SortFunc(msgList, func(a, b *OpensearchMsgLog) int {
		if a.CreateTimeV2 < b.CreateTimeV2 {
			return -1
		}
		if a.CreateTimeV2 > b.CreateTimeV2 {
			return 1
		}
		return 0
	})

	return msgList, nil
}

// expandFromPG fetches a message from PG by ID and adds to the set
func expandFromPG(ctx context.Context, msgID string, msgIDSet map[string]bool, msgIDs *[]string) {
	if msgID == "" || msgIDSet[msgID] {
		return
	}
	result, err := query.Q.MessageLog.WithContext(ctx).
		Where(query.Q.MessageLog.MessageID.Eq(msgID)).First()
	if err != nil {
		return
	}
	msgIDSet[result.MessageID] = true
	*msgIDs = append(*msgIDs, result.MessageID)
	// Recursively expand parent's parent if exists
	if result.ParentID != "" && !msgIDSet[result.ParentID] {
		expandFromPG(ctx, result.ParentID, msgIDSet, msgIDs)
	}
}

// expandThreadFromPG fetches all messages in a thread from PG
func expandThreadFromPG(ctx context.Context, chatID, threadID string, msgIDSet map[string]bool, msgIDs *[]string) {
	if threadID == "" {
		return
	}
	var messages []*model.MessageLog
	messages, err := query.Q.MessageLog.WithContext(ctx).Where(
		query.Q.MessageLog.ChatID.Eq(chatID),
		query.Q.MessageLog.ThreadID.Eq(threadID),
	).Find()
	if err != nil {
		return
	}
	for _, m := range messages {
		if !msgIDSet[m.MessageID] {
			msgIDSet[m.MessageID] = true
			*msgIDs = append(*msgIDs, m.MessageID)
		}
	}
}

// fetchOpenSearchWithRetry fetches messages from OpenSearch by message IDs, retrying once after 15s if empty
func fetchOpenSearchWithRetry(ctx context.Context, index string, msgIDs []string) ([]opensearchapi.SearchHit, error) {
	if len(msgIDs) == 0 {
		return nil, nil
	}

	// Deduplicate
	seen := make(map[string]bool)
	uniqueIDs := make([]string, 0, len(msgIDs))
	for _, id := range msgIDs {
		if !seen[id] {
			seen[id] = true
			uniqueIDs = append(uniqueIDs, id)
		}
	}

	osQuery := osquery.Bool().Must(osquery.Terms("message_id", uniqueIDs))
	resp, err := opensearch.SearchData(ctx, index, osquery.Search().Query(osQuery).Size(uint64(len(uniqueIDs))))
	if err != nil {
		return nil, fmt.Errorf("OpenSearch query failed: %w", err)
	}

	if len(resp.Hits.Hits) == 0 {
		// Retry once after 15s
		logs.L().Ctx(ctx).Info("fetchOpenSearchWithRetry: query returned empty, retrying in 15s...")
		time.Sleep(15 * time.Second)
		resp, err = opensearch.SearchData(ctx, index, osquery.Search().Query(osQuery).Size(uint64(len(uniqueIDs))))
		if err != nil {
			return nil, fmt.Errorf("OpenSearch retry query failed: %w", err)
		}
	}

	return resp.Hits.Hits, nil
}
