package history

import (
	"context"
	"fmt"
	"slices"
	"strings"

	appconfig "github.com/BetaGoRobot/BetaGo-Redefine/internal/application/config"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/botidentity"
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

type OpensearchMsgLog struct {
	CreateTime  string            `json:"create_time"`
	OpenID      string            `json:"user_id"`
	UserName    string            `json:"user_name"`
	MsgList     []string          `json:"msg_list"`
	MentionList []*larkim.Mention `json:"mention_list"`
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
			CreateTime:  res.CreateTime,
			OpenID:      res.OpenID,
			UserName:    res.UserName,
			MsgList:     tmpList,
			MentionList: mentions,
		}
		if r := l.ToLine(); r != "" {
			msgList = append(msgList, l)
		}
	}
	slices.Reverse(msgList)
	return msgList
}
