package xchunk

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"text/template"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/ark_dal"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/config"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/db/query"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/opensearch"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/otel"
	redis_dal "github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/redis"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/xmodel"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/logs"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/utils"
	"github.com/bytedance/gg/gptr"
	"github.com/bytedance/sonic"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"

	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
	"github.com/redis/go-redis/v9"
	uuid "github.com/satori/go.uuid"
)

// Constants for chunking behavior
const (
	// INACTIVITY_TIMEOUT 定义会话非活跃超时时间
	// INACTIVITY_TIMEOUT = 30 * time.Second
	INACTIVITY_TIMEOUT = 3 * time.Minute
	// MAX_CHUNK_SIZE 定义在强制合并前一个块中的最大消息数
	MAX_CHUNK_SIZE       = 50
	chunkMessageDedupTTL = 7 * 24 * time.Hour
)

func chunkRedisNamespace() []string {
	cfg := config.Get()
	if cfg == nil || cfg.LarkConfig == nil {
		return []string{"betago", "chunk"}
	}
	return []string{"betago", "chunk", cfg.LarkConfig.AppID, cfg.LarkConfig.BotOpenID}
}

func chunkRedisKey(parts ...string) string {
	keyParts := append(chunkRedisNamespace(), parts...)
	return strings.Join(keyParts, ":")
}

func redisSessionKey(groupID string) string {
	return chunkRedisKey("session", groupID)
}

func redisActiveSessionsKey() string {
	return chunkRedisKey("active_sessions")
}

func chunkMessageDedupKey(msgID string) string {
	return strings.Join([]string{"betago", "chunk", "message_dedup", msgID}, ":")
}

// SessionBuffer 代表在Redis中存储的会话缓冲区
type SessionBuffer struct {
	Messages     []StandardMsg `json:"messages"`
	LastActiveTs int64         `json:"last_active_ts"`
}

type StandardMsg struct {
	GroupID_   string `json:"group_id"`
	MsgID_     string `json:"msg_id"`
	TimeStamp_ int64  `json:"timestamp"`
	BuildLine_ string `json:"line"`
}

func (m *StandardMsg) BuildLine() string {
	return m.BuildLine_
}

func (m *StandardMsg) TimeStamp() int64 {
	return m.TimeStamp_
}

func (m *StandardMsg) GroupID() string {
	return m.GroupID_
}

func (m *StandardMsg) MsgID() string {
	return m.MsgID_
}

func BuildStdMsg(msg GenericMsg) (StandardMsg, bool) {
	res := StandardMsg{
		GroupID_:   msg.GroupID(),
		MsgID_:     msg.MsgID(),
		TimeStamp_: msg.TimeStamp(),
	}
	if line, ok := msg.BuildLine(); ok {
		res.BuildLine_ = line
		return res, true
	}
	return res, false
}

func dedupeMessagesByMsgID(messages []StandardMsg) []StandardMsg {
	if len(messages) <= 1 {
		return messages
	}

	seen := make(map[string]struct{}, len(messages))
	result := make([]StandardMsg, 0, len(messages))
	for _, message := range messages {
		msgID := strings.TrimSpace(message.MsgID())
		if msgID != "" {
			if _, ok := seen[msgID]; ok {
				continue
			}
			seen[msgID] = struct{}{}
		}
		result = append(result, message)
	}
	return result
}

func containsMessageID(messages []StandardMsg, msgID string) bool {
	msgID = strings.TrimSpace(msgID)
	if msgID == "" {
		return false
	}
	for _, message := range messages {
		if strings.TrimSpace(message.MsgID()) == msgID {
			return true
		}
	}
	return false
}

type Chunk struct {
	GroupID  string
	Messages []StandardMsg
}

// Management is the main struct for managing message chunking.
type Management struct {
	redisClient     *redis.Client
	processingQueue chan *Chunk
	enabled         bool
	disableReason   string
	executor        chunkSubmitter
}

type chunkSubmitter interface {
	Submit(context.Context, string, func(context.Context) error) error
}

type GenericMsg interface {
	GroupID() string
	MsgID() string
	TimeStamp() int64
	BuildLine() (string, bool)
}

type (
	// ChunkMessage is an alias for the Lark message event type.
	ChunkMessage larkim.P2MessageReceiveV1
	// ChunkMessageLark is a slice of Lark message events.
	ChunkMessageLark []*larkim.P2MessageReceiveV1
)

// NewManagement creates a new Management instance.
// getGroupIDFunc: A function to extract the group/chat ID from a message.
// getTimestampFunc: A function to extract the Unix timestamp from a message.
func NewManagement() *Management {
	redisClient := redis_dal.GetRedisClient()
	if redisClient == nil {
		return NewNoopManagement("redis client unavailable")
	}
	return &Management{
		redisClient:     redisClient,
		processingQueue: make(chan *Chunk, 100), // Buffered channel for processing chunks
		enabled:         true,
	}
}

func NewNoopManagement(reason string) *Management {
	return &Management{
		enabled:       false,
		disableReason: reason,
	}
}

func (m *Management) SetExecutor(executor chunkSubmitter) {
	if m == nil {
		return
	}
	m.executor = executor
}

func (m *Management) Enabled() bool {
	return m != nil && m.enabled
}

func (m *Management) DisableReason() string {
	if m == nil {
		return ""
	}
	return m.disableReason
}

// SubmitMessage 处理新的传入消息。它将消息添加到Redis中相应的会话缓冲区。
// 如果缓冲区达到MAX_CHUNK_SIZE，它会触发立即合并。否则，它会更新会话的
// 最后活动时间戳，以用于基于超时的机制。
func (m *Management) SubmitMessage(ctx context.Context, msg GenericMsg) (err error) {
	groupID := msg.GroupID()
	msgID := strings.TrimSpace(msg.MsgID())
	ctx, span := otel.StartNamed(ctx, "chunk.submit",
		trace.WithAttributes(
			attribute.String("group.id", groupID),
			attribute.String("message.id", msgID),
			attribute.Int64("message.timestamp", msg.TimeStamp()),
		),
	)
	defer span.End()
	defer otel.RecordErrorPtr(span, &err)

	if m == nil || !m.enabled {
		span.AddEvent("chunk_disabled")
		return nil
	}
	newTimestamp := msg.TimeStamp()
	if groupID == "" {
		return fmt.Errorf("group ID is empty, skipping message")
	}
	if msgID != "" {
		dedupeCtx, dedupeSpan := otel.StartNamed(ctx, "chunk.redis.claim_dedupe",
			trace.WithAttributes(attribute.String("message.id", msgID)),
		)
		claimed, claimErr := m.redisClient.SetNX(dedupeCtx, chunkMessageDedupKey(msgID), "1", chunkMessageDedupTTL).Result()
		otel.RecordError(dedupeSpan, claimErr)
		dedupeSpan.End()
		if claimErr != nil {
			logs.L().Ctx(ctx).Warn("Failed to claim chunk message dedup", zap.String("groupID", groupID), zap.String("msg_id", msgID), zap.Error(claimErr))
			span.AddEvent("dedupe_claim_failed", trace.WithAttributes(attribute.String("message.id", msgID)))
		} else if !claimed {
			logs.L().Ctx(ctx).Debug("Duplicate message skipped by global chunk dedup",
				zap.String("groupID", groupID),
				zap.String("msg_id", msgID),
			)
			span.AddEvent("duplicate_message", trace.WithAttributes(attribute.String("message.id", msgID)))
			return nil
		}
	}

	sessionKey := redisSessionKey(groupID)

	// 1. 从Redis获取当前会话
	sessionCtx, sessionSpan := otel.StartNamed(ctx, "chunk.redis.session_get",
		trace.WithAttributes(attribute.String("group.id", groupID)),
	)
	val, err := m.redisClient.Get(sessionCtx, sessionKey).Result()
	if err != nil && !errors.Is(err, redis.Nil) {
		otel.RecordError(sessionSpan, err)
	}
	sessionSpan.End()
	if err != nil && err != redis.Nil {
		logs.L().Ctx(ctx).Error("Failed to get session from Redis", zap.String("groupID", groupID), zap.Error(err), zap.String("val", val))
		return err
	}

	var buffer SessionBuffer
	// 如果会话存在，则反序列化它。否则，将使用一个新的空缓冲区。
	if err == nil || errors.Is(err, redis.Nil) {
		if err := sonic.Unmarshal([]byte(val), &buffer); err != nil {
			logs.L().Ctx(ctx).Warn("Failed to unmarshal session buffer, starting a new one", zap.String("groupID", groupID), zap.Error(err), zap.String("val", val))
			// 数据可能已损坏，从一个新缓冲区开始
			m.redisClient.Del(ctx, sessionKey)
			buffer = SessionBuffer{}
			span.AddEvent("session_buffer_reset", trace.WithAttributes(attribute.String("group.id", groupID)))
		}
	}
	if containsMessageID(buffer.Messages, msg.MsgID()) {
		logs.L().Ctx(ctx).Debug("Duplicate message skipped for chunk buffer",
			zap.String("groupID", groupID),
			zap.String("msg_id", msg.MsgID()),
		)
		span.AddEvent("duplicate_message_in_buffer", trace.WithAttributes(attribute.String("message.id", msg.MsgID())))
		return nil
	}
	// 2. 附加新消息并更新时间戳
	stdMsg, ok := BuildStdMsg(msg)
	if !ok {
		logs.L().Ctx(ctx).Warn("got invalid msg to submit, skip...")
		return nil
	}
	buffer.Messages = dedupeMessagesByMsgID(append(buffer.Messages, stdMsg))
	buffer.LastActiveTs = newTimestamp

	// 3. 检查缓冲区大小是否超过限制
	if len(buffer.Messages) >= MAX_CHUNK_SIZE {
		logs.L().Ctx(ctx).Info("Chunk reached max size, triggering immediate merge", zap.String("groupID", groupID), zap.Int("size", len(buffer.Messages)))
		span.AddEvent("chunk_max_size_reached", trace.WithAttributes(attribute.Int("message.count", len(buffer.Messages))))

		// 将完整的块发送到处理队列
		m.processingQueue <- &Chunk{
			GroupID:  groupID,
			Messages: buffer.Messages,
		}
		span.AddEvent("chunk_enqueued", trace.WithAttributes(attribute.Int("message.count", len(buffer.Messages))))

		// 通过删除会话键并将其从活动集合中移除来清理Redis
		cleanupCtx, cleanupSpan := otel.StartNamed(ctx, "chunk.redis.session_cleanup",
			trace.WithAttributes(attribute.String("group.id", groupID)),
		)
		pipe := m.redisClient.Pipeline()
		pipe.Del(cleanupCtx, sessionKey)
		pipe.ZRem(cleanupCtx, redisActiveSessionsKey(), groupID)
		_, err = pipe.Exec(cleanupCtx)
		otel.RecordError(cleanupSpan, err)
		cleanupSpan.End()
		if err != nil {
			// 记录错误但继续，因为块已排队等待处理。
			// 超时机制后续可能会尝试处理一个不存在的键，这是无害的。
			logs.L().Ctx(ctx).Error("Failed to execute Redis cleanup pipeline after max size merge", zap.String("groupID", groupID), zap.Error(err))
		}
		return nil // 触发合并，操作完成
	}

	// 4. 如果未达到大小限制，则在Redis中更新会话
	bufferJSON, err := sonic.Marshal(buffer)
	if err != nil {
		logs.L().Ctx(ctx).Error("Failed to marshal session buffer", zap.String("groupID", groupID), zap.Error(err))
		return err
	}

	updateCtx, updateSpan := otel.StartNamed(ctx, "chunk.redis.session_set",
		trace.WithAttributes(
			attribute.String("group.id", groupID),
			attribute.Int("message.count", len(buffer.Messages)),
		),
	)
	pipe := m.redisClient.Pipeline()
	// 持久化会话数据。后台任务将在超时时清理它。
	pipe.Set(updateCtx, sessionKey, bufferJSON, 0)
	// 更新有序集合中的分数以反映新的活动时间。
	pipe.ZAdd(updateCtx, redisActiveSessionsKey(), redis.Z{Score: float64(newTimestamp), Member: groupID})
	_, err = pipe.Exec(updateCtx)
	otel.RecordError(updateSpan, err)
	updateSpan.End()
	if err != nil {
		logs.L().Ctx(ctx).Error("Failed to execute Redis update pipeline", zap.String("groupID", groupID), zap.Error(err))
		return err
	}

	logs.L().Ctx(ctx).Debug("Message submitted and session updated", zap.String("groupID", groupID), zap.Int("buffer_size", len(buffer.Messages)))
	span.SetAttributes(attribute.Int("buffer.size", len(buffer.Messages)))
	return nil
}

// OnMerge is called when a chunk is ready to be processed.
// It builds a single string from the chunk and sends it to an LLM.
func (m *Management) OnMerge(ctx context.Context, chunk *Chunk) (err error) {
	groupID := ""
	messageCount := 0
	if chunk != nil {
		groupID = chunk.GroupID
		messageCount = len(chunk.Messages)
	}
	ctx, span := otel.StartNamed(ctx, "chunk.merge",
		trace.WithAttributes(
			attribute.String("group.id", groupID),
			attribute.Int("message.count", messageCount),
		),
	)
	defer span.End()
	defer otel.RecordErrorPtr(span, &err)

	if m == nil || !m.enabled {
		span.AddEvent("chunk_disabled")
		return nil
	}
	if chunk == nil || len(chunk.Messages) == 0 {
		span.AddEvent("chunk_empty")
		return nil
	}
	// 写入大模型
	messages := dedupeMessagesByMsgID(chunk.Messages)
	chunkLines := make([]string, len(messages))
	msgIDs := make([]string, len(messages))
	for idx, c := range messages {
		msgLine := c.BuildLine()
		chunkLines[idx] = msgLine
		msgIDs[idx] = c.MsgID()
	}

	// Note: It's better to fetch templates once, not on every merge. This is kept as per the original code.
	ins := query.Q.PromptTemplateArg
	templateCtx, templateSpan := otel.StartNamed(ctx, "chunk.prompt_template.load",
		trace.WithAttributes(attribute.Int("prompt.id", 3)),
	)
	templates, err := ins.WithContext(templateCtx).Where(ins.PromptID.Eq(3)).Find()
	otel.RecordError(templateSpan, err)
	templateSpan.End()
	if err != nil {
		return fmt.Errorf("prompt template with ID 3 not found: %w", err)
	}
	if len(templates) == 0 {
		return fmt.Errorf("prompt template with ID 3 not found")
	}
	promptTemplateStr := templates[0].TemplateStr
	tp, err := template.New("prompt").Parse(promptTemplateStr)
	if err != nil {
		return
	}
	sysPrompt := &strings.Builder{}
	err = tp.Execute(sysPrompt, map[string]string{"CurrentTimeStamp": time.Now().In(utils.UTC8Loc()).Format(time.DateTime)})
	if err != nil {
		return
	}
	chunkStr := strings.Join(chunkLines, "\n")
	span.SetAttributes(
		attribute.Int("chunk.lines.count", len(chunkLines)),
		attribute.Int("chunk.text.len", len(chunkStr)),
		attribute.String("chunk.text.preview", otel.PreviewString(chunkStr, 256)),
	)
	arkCtx, arkSpan := otel.StartNamed(ctx, "chunk.ark.response",
		trace.WithAttributes(attribute.String("model.id", config.Get().ArkConfig.ChunkModel)),
	)
	res, err := ark_dal.ResponseWithCache(arkCtx, sysPrompt.String(), chunkStr, config.Get().ArkConfig.ChunkModel)
	otel.RecordError(arkSpan, err)
	arkSpan.End()
	if err != nil {
		if ark_dal.IsUnavailable(err) {
			m.enabled = false
			m.disableReason = err.Error()
			logs.L().Ctx(ctx).Warn("Chunking disabled after ark unavailable",
				zap.String("reason", err.Error()),
			)
			return nil
		}
		return
	}
	res = strings.Trim(res, "```")
	res = strings.TrimLeft(res, "json")
	logs.L().Ctx(ctx).Info("OnMerge chunk processed by LLM", zap.String("groupID", chunk.GroupID), zap.String("chunkStr", chunkStr), zap.String("res", res))
	span.SetAttributes(
		attribute.Int("chunk.response.len", len(res)),
		attribute.String("chunk.response.preview", otel.PreviewString(res, 256)),
	)

	chunkLog := &xmodel.MessageChunkLogV3{
		ID:          uuid.NewV1().String(),
		Timestamp:   utils.UTC8Time().Format(time.RFC3339),
		TimestampV2: gptr.Of(utils.UTC8Time().Format(time.RFC3339)),
		GroupID:     chunk.GroupID,
		MsgIDs:      msgIDs,
		MsgList:     chunkLines,
	}
	err = sonic.UnmarshalString(res, &chunkLog)
	if err != nil {
		return
	}
	embeddingCtx, embeddingSpan := otel.StartNamed(ctx, "chunk.embedding")
	embedding, _, err := ark_dal.EmbeddingText(embeddingCtx, BuildEmbeddingInput(chunkLog))
	otel.RecordError(embeddingSpan, err)
	embeddingSpan.End()
	if err != nil {
		logs.L().Ctx(ctx).Error("embedding error", zap.String("groupID", chunk.GroupID), zap.Error(err))
		return
	}
	chunkLog.ConversationEmbedding = Normalize(embedding)
	searchCtx, searchSpan := otel.StartNamed(ctx, "chunk.search.insert",
		trace.WithAttributes(attribute.String("index.name", config.Get().OpensearchConfig.LarkChunkIndex)),
	)
	err = opensearch.InsertData(
		searchCtx, config.Get().OpensearchConfig.LarkChunkIndex, uuid.NewV4().String(),
		chunkLog,
	)
	otel.RecordError(searchSpan, err)
	searchSpan.End()
	if err != nil {
		logs.L().Ctx(ctx).Error("insert chunk log error", zap.String("groupID", chunk.GroupID), zap.Error(err))
		return
	}

	return
}

// StartBackgroundCleaner starts a goroutine to periodically scan for and process timed-out sessions.
func (m *Management) StartBackgroundCleaner(ctx context.Context) {
	_, span := otel.StartNamed(ctx, "chunk.cleaner.start")
	defer span.End()

	if m == nil || !m.enabled {
		reason := ""
		if m != nil {
			reason = m.disableReason
		}
		span.SetAttributes(attribute.String("disable.reason", reason))
		span.AddEvent("chunk_disabled")
		logs.L().Ctx(ctx).Warn("Chunking disabled, background cleaner not started",
			zap.String("reason", reason),
		)
		return
	}
	logs.L().Ctx(ctx).Info("Starting background cleaner for timed-out sessions...")
	span.AddEvent("cleaner_started")
	// Start the consumer goroutine
	go func() {
		for chunk := range m.processingQueue {
			if chunk == nil {
				continue
			}
			if m.executor != nil {
				submitErr := m.executor.Submit(ctx, "chunk_merge:"+chunk.GroupID, func(taskCtx context.Context) error {
					logs.L().Ctx(taskCtx).Info("Processing a merged chunk", zap.Int("message_count", len(chunk.Messages)))
					if err := m.OnMerge(taskCtx, chunk); err != nil {
						logs.L().Ctx(taskCtx).Error("Error during OnMerge", zap.Error(err))
						return err
					}
					return nil
				})
				if submitErr != nil {
					logs.L().Ctx(ctx).Error("Failed to submit merged chunk", zap.String("groupID", chunk.GroupID), zap.Error(submitErr))
				}
				continue
			}

			// Each chunk is processed in its own goroutine to avoid blocking the queue consumer
			go func(c *Chunk) {
				logs.L().Ctx(ctx).Info("Processing a merged chunk", zap.Int("message_count", len(c.Messages)))
				if err := m.OnMerge(ctx, c); err != nil {
					logs.L().Ctx(ctx).Error("Error during OnMerge", zap.Error(err))
				}
			}(chunk)
		}
	}()

	// Start the ticker for scanning Redis
	go func() {
		ticker := time.NewTicker(INACTIVITY_TIMEOUT / 10)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				logs.L().Ctx(ctx).Info("Stopping background cleaner...")
				return
			case <-ticker.C:
				m.scanAndProcessTimeouts(ctx)
			}
		}
	}()
}

// scanAndProcessTimeouts is the internal logic for the background cleaner.
func (m *Management) scanAndProcessTimeouts(ctx context.Context) {
	ctx, span := otel.StartNamed(ctx, "chunk.timeout.scan")
	defer span.End()

	if m == nil || !m.enabled {
		span.AddEvent("chunk_disabled")
		return
	}
	logs.L().Ctx(ctx).Debug("Scanning for timed-out sessions...")
	// Calculate the timestamp threshold for timeout. Sessions older than this will be processed.
	timeoutThreshold := time.Now().Add(-INACTIVITY_TIMEOUT).UnixMilli()
	span.SetAttributes(attribute.Int64("timeout.threshold", timeoutThreshold))

	// 1. Find all group IDs that have timed out using ZRangeByScore
	listCtx, listSpan := otel.StartNamed(ctx, "chunk.redis.timeout_scan")
	timedOutGroupIDs, err := m.redisClient.ZRangeByScore(listCtx, redisActiveSessionsKey(), &redis.ZRangeBy{
		Min: "0",
		Max: strconv.FormatInt(timeoutThreshold, 10),
	}).Result()
	otel.RecordError(listSpan, err)
	listSpan.End()
	if err != nil {
		otel.RecordError(span, err)
		logs.L().Ctx(ctx).Error("Failed to get timed-out sessions from Redis", zap.Error(err))
		return
	}

	if len(timedOutGroupIDs) == 0 {
		span.SetAttributes(attribute.Int("timeout.group.count", 0))
		logs.L().Ctx(ctx).Debug("not session is timed out, will do nothing...")
		return // Nothing to do
	}
	span.SetAttributes(attribute.Int("timeout.group.count", len(timedOutGroupIDs)))
	logs.L().Ctx(ctx).Info("Found timed-out sessions", zap.Int("count", len(timedOutGroupIDs)), zap.Strings("group_ids", timedOutGroupIDs))

	// 2. Process and clean up each timed-out session
	for _, groupID := range timedOutGroupIDs {
		sessionKey := redisSessionKey(groupID)
		// Atomically get the session data and delete the key.
		// This prevents a race condition where a new message arrives while we are processing the timeout.
		sessionCtx, sessionSpan := otel.StartNamed(ctx, "chunk.redis.session_getdel",
			trace.WithAttributes(attribute.String("group.id", groupID)),
		)
		val, err := m.redisClient.GetDel(sessionCtx, sessionKey).Result()
		otel.RecordError(sessionSpan, err)
		sessionSpan.End()
		if err == redis.Nil {
			// Session was already processed or removed. Clean up the sorted set entry just in case.
			m.redisClient.ZRem(ctx, redisActiveSessionsKey(), groupID)
			span.AddEvent("timeout_session_missing", trace.WithAttributes(attribute.String("group.id", groupID)))
			continue
		}
		if err != nil {
			span.AddEvent("timeout_session_getdel_failed", trace.WithAttributes(attribute.String("group.id", groupID)))
			logs.L().Ctx(ctx).Error("Failed to GetDel session from Redis", zap.String("groupID", groupID), zap.Error(err))
			continue
		}

		// At this point, the session key is deleted from Redis. Now we clean up the sorted set.
		m.redisClient.ZRem(ctx, redisActiveSessionsKey(), groupID)

		var buffer SessionBuffer
		if err := sonic.Unmarshal([]byte(val), &buffer); err != nil {
			otel.RecordError(span, err)
			logs.L().Ctx(ctx).Error("Failed to unmarshal timed-out session buffer", zap.String("groupID", groupID), zap.Error(err))
			continue
		}

		// Send the collected messages to the processing queue
		if len(buffer.Messages) > 0 {
			m.processingQueue <- &Chunk{
				GroupID:  groupID,
				Messages: buffer.Messages,
			}
			span.AddEvent("timed_out_chunk_enqueued",
				trace.WithAttributes(
					attribute.String("group.id", groupID),
					attribute.Int("message.count", len(buffer.Messages)),
				),
			)
		}
	}
}

// Normalize 函数接收一个 float32 类型的向量（切片），返回其归一化后的新向量。
// L2 归一化步骤：
// 1. 计算向量所有元素平方和的平方根（即 L2 范数或称“长度”）。
// 2. 向量中的每个元素都除以这个长度。
func Normalize(vec []float32) []float32 {
	// 1. 计算所有元素的平方和
	// 使用 float64 来进行中间计算，可以防止因累加大量数值而导致的精度损失。
	var sumOfSquares float64
	for _, val := range vec {
		sumOfSquares += float64(val) * float64(val)
	}

	// 2. 计算向量的长度（L2 范数）
	magnitude := math.Sqrt(sumOfSquares)

	// 处理零向量的特殊情况：如果向量长度为0，无法进行归一化（会导致除以零）。
	// 在这种情况下，直接返回一个与原向量等长的零向量。
	if magnitude == 0 {
		return make([]float32, len(vec))
	}

	// 3. 创建一个新的切片来存储归一化后的结果
	normalizedVec := make([]float32, len(vec))

	// 4. 将原向量的每个元素除以长度
	for i, val := range vec {
		normalizedVec[i] = val / float32(magnitude)
	}

	return normalizedVec
}

// BuildEmbeddingInput 函数接收一个更新后的对话文档，然后构建一个高质量的字符串用于生成embedding。
func BuildEmbeddingInput(doc *xmodel.MessageChunkLogV3) string {
	// 使用 strings.Builder 来高效地拼接字符串
	var builder strings.Builder

	// 1. 核心摘要和主要意图：这是对话最高级别的概括。
	if doc.Summary != "" {
		builder.WriteString("核心摘要: ")
		builder.WriteString(doc.Summary)
		builder.WriteString("\n")
	}
	if doc.Intent != "" {
		builder.WriteString("主要意图: ")
		builder.WriteString(doc.Intent)
		builder.WriteString("\n")
	}

	// 2. 实体 - 对话的具体内容和主体。
	// 将所有关键实体信息组合在一起，形成对“聊了什么”的全面描述。
	if len(doc.Entities.MainTopicsOrActivities) > 0 {
		builder.WriteString("核心议题与活动: ")
		builder.WriteString(strings.Join(doc.Entities.MainTopicsOrActivities, ", "))
		builder.WriteString("\n")
	}
	if len(doc.Entities.KeyConceptsAndNouns) > 0 {
		builder.WriteString("关键概念: ")
		builder.WriteString(strings.Join(doc.Entities.KeyConceptsAndNouns, ", "))
		builder.WriteString("\n")
	}
	if len(doc.Entities.MentionedPeople) > 0 {
		builder.WriteString("提及人物: ")
		builder.WriteString(strings.Join(doc.Entities.MentionedPeople, ", "))
		builder.WriteString("\n")
	}
	if len(doc.Entities.LocationsAndVenues) > 0 {
		builder.WriteString("涉及地点: ")
		builder.WriteString(strings.Join(doc.Entities.LocationsAndVenues, ", "))
		builder.WriteString("\n")
	}
	if len(doc.Entities.MediaAndWorks) > 0 {
		var works []string
		for _, w := range doc.Entities.MediaAndWorks {
			works = append(works, fmt.Sprintf("%s (%s)", w.Title, w.Type))
		}
		builder.WriteString("提及作品: ")
		builder.WriteString(strings.Join(works, ", "))
		builder.WriteString("\n")
	}

	// 3. 结果 - 对话产生了什么结论和计划。
	if len(doc.Outcomes.ConclusionsOrAgreements) > 0 {
		builder.WriteString("共识与结论: ")
		builder.WriteString(strings.Join(doc.Outcomes.ConclusionsOrAgreements, "; "))
		builder.WriteString("\n")
	}
	if len(doc.Outcomes.PlansAndSuggestions) > 0 {
		var plans []string
		for _, p := range doc.Outcomes.PlansAndSuggestions {
			plans = append(plans, p.ActivityOrSuggestion)
		}
		builder.WriteString("计划与提议: ")
		builder.WriteString(strings.Join(plans, "; "))
		builder.WriteString("\n")
	}
	if len(doc.Outcomes.OpenThreadsOrPendingPoints) > 0 {
		builder.WriteString("待定事项: ")
		builder.WriteString(strings.Join(doc.Outcomes.OpenThreadsOrPendingPoints, "; "))
		builder.WriteString("\n")
	}

	// 4. 情感与氛围：为对话添加情感色彩的上下文。
	if doc.SentimentAndTone.Sentiment != "" {
		builder.WriteString("整体情绪: ")
		builder.WriteString(doc.SentimentAndTone.Sentiment)
		if len(doc.SentimentAndTone.Tones) > 0 {
			builder.WriteString(fmt.Sprintf(" (主要语气: %s)", strings.Join(doc.SentimentAndTone.Tones, ", ")))
		}
		builder.WriteString("\n")
	}

	return builder.String()
}
