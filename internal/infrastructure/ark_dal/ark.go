package ark_dal

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/config"
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

func CreateResponses(ctx context.Context, body *responses.ResponsesRequest) (*responses.ResponseObject, error) {
	ctx, span := otel.StartNamed(ctx, "ark.responses.create")
	if body != nil {
		span.SetAttributes(
			attribute.String("model.id", body.Model),
			attribute.Int("tools.count", len(body.Tools)),
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
	return resp, err
}

func CreateResponsesStream(ctx context.Context, body *responses.ResponsesRequest) (*arkutils.ResponsesStreamReader, error) {
	ctx, span := otel.StartNamed(ctx, "ark.responses.stream_create")
	if body != nil {
		span.SetAttributes(
			attribute.String("model.id", body.Model),
			attribute.Int("tools.count", len(body.Tools)),
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
	resp, err := runtime.CreateResponsesStream(ctx, body)
	otel.RecordError(span, err)
	return resp, err
}
