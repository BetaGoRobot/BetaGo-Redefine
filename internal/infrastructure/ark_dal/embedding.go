package ark_dal

import (
	"context"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/llmusage"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/otel"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/logs"
	"github.com/volcengine/volcengine-go-sdk/service/arkruntime"
	"github.com/volcengine/volcengine-go-sdk/service/arkruntime/model"
	"go.opentelemetry.io/otel/attribute"
	"go.uber.org/zap"
)

// EmbeddingText returns the embedding of the input text.
//
//	@param ctx
//	@param input
//	@return embedded
//	@return err
func EmbeddingText(ctx context.Context, input string, scope llmusage.Scope) (embedded []float32, tokenUsage model.Usage, err error) {
	runtime, cfg, err := runtimeClient()
	if err != nil {
		recordEmbeddingUsage(ctx, scope, "", model.Usage{}, err)
		return nil, model.Usage{}, err
	}
	ctx, span := otel.StartNamed(ctx, "ark.embedding.create")
	span.SetAttributes(
		attribute.String("model.id", cfg.EmbeddingModel),
	)
	span.SetAttributes(otel.PreviewAttrs("input", input, 256)...)
	defer span.End()
	defer func() { otel.RecordError(span, err) }()

	req := model.MultiModalEmbeddingRequest{
		Input: []model.MultimodalEmbeddingInput{
			{
				Type: model.MultiModalEmbeddingInputTypeText,
				Text: &input,
			},
		},
		Model: cfg.EmbeddingModel,
	}
	resp, err := runtime.CreateMultiModalEmbeddings(
		ctx,
		req,
		arkruntime.WithCustomHeader("x-is-encrypted", "true"),
	)
	if err != nil {
		logs.L().Ctx(ctx).Error("embeddings error", zap.Error(err), zap.String("input", input))
		recordEmbeddingUsage(ctx, scope, cfg.EmbeddingModel, model.Usage{}, err)
		return
	}
	embedded = resp.Data.Embedding
	tokenUsage = Muda2Usage(resp.Usage)
	recordEmbeddingUsage(ctx, scope, cfg.EmbeddingModel, tokenUsage, nil)
	return
}

func recordEmbeddingUsage(ctx context.Context, scope llmusage.Scope, modelID string, usage model.Usage, callErr error) {
	record := llmusage.Record{
		Scope:            scope,
		Provider:         "ark",
		Model:            modelID,
		Kind:             llmusage.KindEmbedding,
		Status:           llmusage.StatusSuccess,
		PromptTokens:     int64(usage.PromptTokens),
		CompletionTokens: int64(usage.CompletionTokens),
		TotalTokens:      int64(usage.TotalTokens),
		CreatedAt:        utilsNow(),
	}
	if callErr != nil {
		record.Status = llmusage.StatusError
		record.Error = callErr.Error()
	}
	_ = llmusage.RecordUsage(ctx, record)
}

func Muda2Usage(u model.MultimodalEmbeddingUsage) model.Usage {
	return model.Usage{
		PromptTokens:            u.PromptTokens,
		CompletionTokens:        u.TotalTokens,
		TotalTokens:             u.TotalTokens,
		PromptTokensDetails:     model.PromptTokensDetail{},
		CompletionTokensDetails: model.CompletionTokensDetails{},
	}
}
