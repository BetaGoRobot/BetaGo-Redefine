package llmusage

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/db/model"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/db/query"
	"github.com/VictoriaMetrics/metrics"
	"go.opentelemetry.io/otel/trace"
	"gorm.io/gorm"
)

type UsageRecordRow = model.LlmTokenUsageRecord

type Store interface {
	CreateUsageRecord(context.Context, *UsageRecordRow) error
}

// BotIDProvider 返回当前进程的 bot 标识，写入 token 表的 bot_id 列。
// 留空表示当前实例尚未初始化身份；recorder 会原样写空字符串，由后续回刷脚本兜底。
type BotIDProvider func() string

// defaultBotIDProvider 由 SetDefaultBotIDProvider 配置；未配置时所有记录的 bot_id
// 写入空字符串（与回刷前的旧行为一致）。
var (
	botIDMu        sync.RWMutex
	botIDProvider  BotIDProvider
)

// SetDefaultBotIDProvider 注入一个返回当前 bot 标识的函数；建议在 db 模块初始化
// 之后、写入开始之前调用一次（典型位置：cmd/larkrobot bootstrap）。
func SetDefaultBotIDProvider(p BotIDProvider) {
	botIDMu.Lock()
	defer botIDMu.Unlock()
	botIDProvider = p
}

func currentBotID() string {
	botIDMu.RLock()
	p := botIDProvider
	botIDMu.RUnlock()
	if p == nil {
		return ""
	}
	return strings.TrimSpace(p())
}

type GormStore struct {
	db *gorm.DB
}

func NewGormStore(db *gorm.DB) *GormStore {
	if db == nil {
		return nil
	}
	return &GormStore{db: db}
}

func (s *GormStore) CreateUsageRecord(ctx context.Context, row *UsageRecordRow) error {
	if s == nil || s.db == nil {
		return nil
	}
	return query.Use(s.db).LlmTokenUsageRecord.WithContext(ctx).Create(row)
}

type Recorder struct {
	store Store
}

var (
	defaultMu       sync.RWMutex
	defaultRecorder = NewRecorderWithStore(nil)
)

func NewRecorder(db *gorm.DB) *Recorder {
	return NewRecorderWithStore(NewGormStore(db))
}

func NewRecorderWithStore(store Store) *Recorder {
	return &Recorder{store: store}
}

func SetDefaultRecorder(recorder *Recorder) {
	defaultMu.Lock()
	defer defaultMu.Unlock()
	if recorder == nil {
		defaultRecorder = NewRecorderWithStore(nil)
		return
	}
	defaultRecorder = recorder
}

func DefaultRecorder() *Recorder {
	defaultMu.RLock()
	defer defaultMu.RUnlock()
	return defaultRecorder
}

func RecordUsage(ctx context.Context, record Record) error {
	return DefaultRecorder().Record(ctx, record)
}

func (r *Recorder) Record(ctx context.Context, record Record) error {
	if r == nil {
		return nil
	}
	if strings.TrimSpace(record.TraceID) == "" {
		spanCtx := trace.SpanContextFromContext(ctx)
		if spanCtx.HasTraceID() {
			record.TraceID = spanCtx.TraceID().String()
		}
	}
	row := record.toRow()
	recordMetrics(row)
	if r.store == nil {
		return nil
	}
	return r.store.CreateUsageRecord(ctx, &row)
}

func (record Record) toRow() UsageRecordRow {
	scope := NormalizeScope(record.Scope)
	createdAt := record.CreatedAt
	if createdAt.IsZero() {
		createdAt = time.Now()
	}
	buckets := BucketTimes(createdAt)
	return UsageRecordRow{
		CreatedAt:        createdAt,
		BucketMinute:     buckets.Minute,
		BucketHour:       buckets.Hour,
		BucketDay:        buckets.Day,
		BotID:            currentBotID(),
		Provider:         nonEmpty(record.Provider, "unknown"),
		Model:            nonEmpty(record.Model, "unknown"),
		Kind:             nonEmpty(string(record.Kind), "unknown"),
		SourceType:       string(scope.SourceType),
		Source:           scope.Source,
		ChatID:           scope.ChatID,
		ChatName:         scope.ChatName,
		OpenID:           scope.OpenID,
		UserName:         scope.UserName,
		Status:           nonEmpty(string(record.Status), string(StatusSuccess)),
		PromptTokens:     record.PromptTokens,
		CompletionTokens: record.CompletionTokens,
		TotalTokens:      record.TotalTokens,
		ResponseID:       strings.TrimSpace(record.ResponseID),
		TraceID:          strings.TrimSpace(record.TraceID),
		Error:            strings.TrimSpace(record.Error),
	}
}

func recordMetrics(row UsageRecordRow) {
	requestCounterName := fmt.Sprintf(
		`betago_llm_requests_total{provider=%q,model=%q,kind=%q,source_type=%q,source=%q,status=%q,chat_id=%q,chat_name=%q,open_id=%q,user_name=%q}`,
		sanitizeLabel(row.Provider),
		sanitizeLabel(row.Model),
		sanitizeLabel(row.Kind),
		sanitizeLabel(row.SourceType),
		sanitizeLabel(row.Source),
		sanitizeLabel(row.Status),
		sanitizeLabel(row.ChatID),
		sanitizeLabel(row.ChatName),
		sanitizeLabel(row.OpenID),
		sanitizeLabel(row.UserName),
	)
	metrics.GetOrCreateCounter(requestCounterName).Inc()
	recordTokenMetric(row, "prompt", row.PromptTokens)
	recordTokenMetric(row, "completion", row.CompletionTokens)
	recordTokenMetric(row, "total", row.TotalTokens)
}

func recordTokenMetric(row UsageRecordRow, tokenType string, tokens int64) {
	if tokens <= 0 {
		return
	}
	counterName := fmt.Sprintf(
		`betago_llm_token_usage_total{provider=%q,model=%q,kind=%q,source_type=%q,source=%q,token_type=%q,chat_id=%q,chat_name=%q,open_id=%q,user_name=%q}`,
		sanitizeLabel(row.Provider),
		sanitizeLabel(row.Model),
		sanitizeLabel(row.Kind),
		sanitizeLabel(row.SourceType),
		sanitizeLabel(row.Source),
		tokenType,
		sanitizeLabel(row.ChatID),
		sanitizeLabel(row.ChatName),
		sanitizeLabel(row.OpenID),
		sanitizeLabel(row.UserName),
	)
	metrics.GetOrCreateCounter(counterName).AddInt64(tokens)
}

func nonEmpty(value, fallback string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return fallback
	}
	return value
}
