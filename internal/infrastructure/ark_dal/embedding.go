package ark_dal

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/llmusage"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/otel"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/logs"
	"github.com/volcengine/volcengine-go-sdk/service/arkruntime"
	"github.com/volcengine/volcengine-go-sdk/service/arkruntime/model"
	"go.opentelemetry.io/otel/attribute"
	"go.uber.org/zap"
)

var embeddingRuntimeClientFn = runtimeClient
var embeddingLoggerFn = logs.L
var embeddingSafeCode = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_.]{0,95}$`)

// EmbeddingText returns the embedding of the input text.
//
//	@param ctx
//	@param input
//	@return embedded
//	@return err
func EmbeddingText(ctx context.Context, input string, scope llmusage.Scope) (embedded []float32, tokenUsage model.Usage, err error) {
	return embeddingText(ctx, input, scope, true)
}

// EmbeddingTextPrivate keeps the input and upstream error bodies out of logs, spans and usage records.
// The provider call, returned errors, vectors and token accounting match EmbeddingText.
func EmbeddingTextPrivate(ctx context.Context, input string, scope llmusage.Scope) (embedded []float32, tokenUsage model.Usage, err error) {
	return embeddingText(ctx, input, scope, false)
}

func embeddingText(ctx context.Context, input string, scope llmusage.Scope, recordPayload bool) (embedded []float32, tokenUsage model.Usage, err error) {
	if strings.TrimSpace(input) == "" {
		return nil, model.Usage{}, nil
	}
	runtime, cfg, err := embeddingRuntimeClientFn()
	if err != nil {
		recordEmbeddingUsage(ctx, scope, "", model.Usage{}, embeddingDiagnosticError(err, recordPayload))
		return nil, model.Usage{}, err
	}
	ctx, span := otel.StartNamed(ctx, "ark.embedding.create")
	span.SetAttributes(
		attribute.String("model.id", cfg.EmbeddingModel),
	)
	if recordPayload {
		span.SetAttributes(otel.PreviewAttrs("input", input, 256)...)
	}
	defer span.End()
	defer func() { otel.RecordError(span, embeddingDiagnosticError(err, recordPayload)) }()

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
		diagnosticErr := embeddingDiagnosticError(err, recordPayload)
		if recordPayload {
			embeddingLoggerFn().Ctx(ctx).Error("embeddings error", zap.Error(err), zap.String("input", input))
		} else {
			embeddingLoggerFn().Ctx(ctx).Error("embeddings error", zap.Error(diagnosticErr), zap.String("model", cfg.EmbeddingModel))
		}
		recordEmbeddingUsage(ctx, scope, cfg.EmbeddingModel, model.Usage{}, diagnosticErr)
		return
	}
	embedded = resp.Data.Embedding
	tokenUsage = Muda2Usage(resp.Usage)
	recordEmbeddingUsage(ctx, scope, cfg.EmbeddingModel, tokenUsage, nil)
	return
}

func embeddingDiagnosticError(err error, recordPayload bool) error {
	if err == nil || recordPayload {
		return err
	}
	status, code := 0, "transport_error"
	var api *model.APIError
	if errors.As(err, &api) {
		status = api.HTTPStatusCode
		if embeddingSafeCode.MatchString(api.Code) {
			code = api.Code
		}
	}
	var request *model.RequestError
	if errors.As(err, &request) {
		status = request.HTTPStatusCode
		code = "request_error"
	}
	if status < 100 || status > 599 {
		status = 0
	}
	if errors.Is(err, context.Canceled) {
		code = "canceled"
	}
	if errors.Is(err, context.DeadlineExceeded) {
		code = "timeout"
	}
	return fmt.Errorf("embedding provider failure (status=%d code=%s)", status, code)
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
