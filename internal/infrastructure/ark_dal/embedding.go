package ark_dal

import (
	"context"

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
func EmbeddingText(ctx context.Context, input string) (embedded []float32, tokenUsage model.Usage, err error) {
	runtime, cfg, err := runtimeClient()
	if err != nil {
		return nil, model.Usage{}, err
	}
	ctx, span := otel.StartNamed(ctx, "ark.embedding.create")
	span.SetAttributes(
		attribute.String("model.id", cfg.EmbeddingModel),
	)
	span.SetAttributes(otel.PreviewAttrs("input", input, 256)...)
	defer span.End()
	defer func() { otel.RecordError(span, err) }()

	req := model.EmbeddingRequestStrings{
		Input: []string{input},
		Model: cfg.EmbeddingModel,
	}
	resp, err := runtime.CreateEmbeddings(
		ctx,
		req,
		arkruntime.WithCustomHeader("x-is-encrypted", "true"),
	)
	if err != nil {
		logs.L().Ctx(ctx).Error("embeddings error", zap.Error(err), zap.String("input", input))
		return
	}
	embedded = resp.Data[0].Embedding
	tokenUsage = resp.Usage
	return
}
