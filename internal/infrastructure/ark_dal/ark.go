package ark_dal

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/config"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/logs"
	"github.com/volcengine/volcengine-go-sdk/service/arkruntime"
	"github.com/volcengine/volcengine-go-sdk/service/arkruntime/model/responses"
	arkutils "github.com/volcengine/volcengine-go-sdk/service/arkruntime/utils"
	"go.uber.org/zap"
)

var (
	client         *arkruntime.Client
	arkConfig      *config.ArkConfig
	reasoningModel string
	normalModel    string
	embeddingModel string
	visionModel    string
	disableReason  string
	warnOnce       sync.Once
)

var errUnavailable = errors.New("ark runtime unavailable")

func Init(config *config.ArkConfig) {
	if config == nil || config.APIKey == "" {
		setNoop("ark config missing or api key empty")
		return
	}
	arkConfig = config
	client = arkruntime.NewClientWithApiKey(config.APIKey)
	reasoningModel = config.ReasoningModel
	normalModel = config.NormalModel
	embeddingModel = config.EmbeddingModel
	visionModel = config.VisionModel
	disableReason = ""
}

func ErrUnavailable() error {
	if disableReason == "" {
		return errUnavailable
	}
	return fmt.Errorf("%w: %s", errUnavailable, disableReason)
}

func IsUnavailable(err error) bool {
	return errors.Is(err, errUnavailable)
}

func setNoop(reason string) {
	client = nil
	arkConfig = nil
	reasoningModel = ""
	normalModel = ""
	embeddingModel = ""
	visionModel = ""
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
	runtime, _, err := runtimeClient()
	if err != nil {
		return nil, err
	}
	return runtime.CreateResponses(ctx, body)
}

func CreateResponsesStream(ctx context.Context, body *responses.ResponsesRequest) (*arkutils.ResponsesStreamReader, error) {
	runtime, _, err := runtimeClient()
	if err != nil {
		return nil, err
	}
	return runtime.CreateResponsesStream(ctx, body)
}
