package handlers

import (
	"context"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/history"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/ark_dal"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/llmusage"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/retriever"
	"github.com/tmc/langchaingo/schema"
	"github.com/volcengine/volcengine-go-sdk/service/arkruntime/model"
)

var topicRecallDocsFn = func(
	ctx context.Context,
	suffix, query string,
	k int,
	startTime, endTime string,
) ([]schema.Document, error) {
	return retriever.Cli().RecallDocs(ctx, suffix, query, k, startTime, endTime)
}

var topicHybridSearchFn = history.HybridSearch

func recallTopicDocsForMode(
	ctx context.Context,
	chatID, query string,
	topK int,
	cutoffTime string,
	anchor time.Time,
	captureEnabled bool,
	embeddingFunc history.EmbeddingFunc,
) ([]schema.Document, error) {
	if !captureEnabled {
		return topicRecallDocsFn(ctx, chatID, query, topK, cutoffTime, "")
	}

	results, err := topicHybridSearchFn(ctx, history.HybridSearchRequest{
		QueryText:        []string{query},
		TopK:             topK,
		ChatID:           chatID,
		EndTime:          anchor.Format(time.RFC3339Nano),
		CutoffTime:       cutoffTime,
		MessageIndexOnly: true,
	}, embeddingFunc)
	if err != nil {
		return nil, err
	}

	docs := make([]schema.Document, 0, len(results))
	for _, result := range results {
		if result == nil {
			continue
		}
		if _, reason := parseCausalContextTime(result.CreateTimeV2, anchor); reason != "" {
			continue
		}
		docs = append(docs, schema.Document{
			PageContent: result.RawMessage,
			Metadata: map[string]any{
				"chat_id":        chatID,
				"user_id":        result.OpenID,
				"user_name":      result.UserName,
				"msg_id":         result.MessageID,
				"create_time":    result.CreateTime,
				"create_time_v2": result.CreateTimeV2,
			},
			Score: float32(result.Score),
		})
	}
	return docs, nil
}

func topicRecallEmbedding(scope llmusage.Scope) history.EmbeddingFunc {
	return func(ctx context.Context, text string) ([]float32, model.Usage, error) {
		return ark_dal.EmbeddingText(ctx, text, scope)
	}
}
