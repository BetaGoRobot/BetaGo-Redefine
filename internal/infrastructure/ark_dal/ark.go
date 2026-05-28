package ark_dal

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/config"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/llmusage"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/otel"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/logs"
	"github.com/volcengine/volcengine-go-sdk/service/arkruntime"
	"github.com/volcengine/volcengine-go-sdk/service/arkruntime/model/responses"
	arkutils "github.com/volcengine/volcengine-go-sdk/service/arkruntime/utils"
	"go.opentelemetry.io/otel/attribute"
	"go.uber.org/zap"
)

var (
	client        *arkruntime.Client
	arkConfig     *config.ArkConfig
	disableReason string
	warnOnce      sync.Once
)

var errUnavailable = errors.New("ark runtime unavailable")

func Init(config *config.ArkConfig) {
	if config == nil || config.APIKey == "" {
		setNoop("ark config missing or api key empty")
		return
	}
	arkConfig = config
	client = arkruntime.NewClientWithApiKey(config.APIKey)
	disableReason = ""
}

func ErrUnavailable() error {
	if disableReason == "" {
		return errUnavailable
	}
	return fmt.Errorf("%w: %s", errUnavailable, disableReason)
}

func Status() (bool, string) {
	if client != nil && arkConfig != nil {
		return true, ""
	}
	return false, disableReason
}

func IsUnavailable(err error) bool {
	return errors.Is(err, errUnavailable)
}

func setNoop(reason string) {
	client = nil
	arkConfig = nil
	disableReason = reason
	warnOnce.Do(func() {
		logs.L().Warn("Ark runtime disabled, falling back to noop",
			zap.String("reason", reason),
		)
	})
}

func runtimeClient() (*arkruntime.Client, *config.ArkConfig, error) {
	if client == nil || arkConfig == nil {
		return nil, nil, ErrUnavailable()
	}
	return client, arkConfig, nil
}

func CreateResponses(ctx context.Context, body *responses.ResponsesRequest, scope llmusage.Scope) (*responses.ResponseObject, error) {
	ctx, span := otel.StartNamed(ctx, "ark.responses.create")
	if body != nil {
		span.SetAttributes(
			attribute.String("model.id", body.Model),
			attribute.Int("tools.count", len(body.Tools)),
			attribute.String("thinking.type", responseThinkingType(body.Thinking)),
			attribute.String("reasoning.effort", responseReasoningEffort(body.Reasoning)),
			attribute.String("caching.type", responseCachingType(body.Caching)),
			attribute.Bool("caching.prefix", body.GetCaching().GetPrefix()),
			attribute.Bool("previous_response_id.set", body.PreviousResponseId != nil),
		)
		if body.PreviousResponseId != nil {
			span.SetAttributes(attribute.String("previous_response_id.preview", otel.PreviewString(*body.PreviousResponseId, 128)))
		}
	}
	defer span.End()

	runtime, _, err := runtimeClient()
	if err != nil {
		otel.RecordError(span, err)
		return nil, err
	}
	resp, err := runtime.CreateResponses(ctx, body)
	otel.RecordError(span, err)
	if resp != nil {
		span.SetAttributes(attribute.String("response.id.preview", otel.PreviewString(resp.Id, 128)))
	}
	recordResponseUsage(ctx, scope, bodyModel(body), llmusage.KindResponses, resp, err)
	return resp, err
}

func CreateResponsesStream(ctx context.Context, body *responses.ResponsesRequest, scope llmusage.Scope) (*arkutils.ResponsesStreamReader, error) {
	ctx, span := otel.StartNamed(ctx, "ark.responses.stream_create")
	if body != nil {
		span.SetAttributes(
			attribute.String("model.id", body.Model),
			attribute.Int("tools.count", len(body.Tools)),
			attribute.String("thinking.type", responseThinkingType(body.Thinking)),
			attribute.String("reasoning.effort", responseReasoningEffort(body.Reasoning)),
			attribute.String("caching.type", responseCachingType(body.Caching)),
			attribute.Bool("caching.prefix", body.GetCaching().GetPrefix()),
			attribute.Bool("previous_response_id.set", body.PreviousResponseId != nil),
		)
		if body.PreviousResponseId != nil {
			span.SetAttributes(attribute.String("previous_response_id.preview", otel.PreviewString(*body.PreviousResponseId, 128)))
		}
	}
	defer span.End()

	runtime, _, err := runtimeClient()
	if err != nil {
		otel.RecordError(span, err)
		recordResponseUsage(ctx, scope, bodyModel(body), llmusage.KindResponsesStream, nil, err)
		return nil, err
	}
	resp, err := runtime.CreateResponsesStream(ctx, body)
	otel.RecordError(span, err)
	if err != nil {
		recordResponseUsage(ctx, scope, bodyModel(body), llmusage.KindResponsesStream, nil, err)
	}
	return resp, err
}

func bodyModel(body *responses.ResponsesRequest) string {
	if body == nil {
		return ""
	}
	return body.Model
}

func recordResponseUsage(ctx context.Context, scope llmusage.Scope, model string, kind llmusage.Kind, resp *responses.ResponseObject, callErr error) {
	record := llmusage.Record{
		Scope:     scope,
		Provider:  "ark",
		Model:     model,
		Kind:      kind,
		Status:    llmusage.StatusSuccess,
		CreatedAt: utilsNow(),
	}
	if resp != nil {
		record.ResponseID = resp.Id
		if usage := resp.GetUsage(); usage != nil {
			record.PromptTokens = usage.GetInputTokens()
			record.CompletionTokens = usage.GetOutputTokens()
			record.TotalTokens = usage.GetTotalTokens()
		} else if callErr == nil {
			record.Status = llmusage.StatusUsageMissing
		}
	}
	if callErr != nil {
		record.Status = llmusage.StatusError
		record.Error = callErr.Error()
	}
	_ = llmusage.RecordUsage(ctx, record)
}

var utilsNow = func() time.Time {
	return time.Now()
}

func responseThinkingType(thinking *responses.ResponsesThinking) string {
	if thinking == nil {
		return "unset"
	}
	return thinking.GetType().String()
}

func responseReasoningEffort(reasoning *responses.ResponsesReasoning) string {
	if reasoning == nil {
		return "unset"
	}
	return reasoning.GetEffort().String()
}

func responseCachingType(caching *responses.ResponsesCaching) string {
	if caching == nil {
		return "unset"
	}
	return caching.GetType().String()
}
