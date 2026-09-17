package ark_dal

import (
	"context"
	"strings"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/config"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/llmusage"
	"github.com/volcengine/volcengine-go-sdk/service/arkruntime/model/responses"
)

// ModelOptionsResolver is supplied by the application composition root so the
// infrastructure adapter does not depend on the application config manager.
type ModelOptionsResolver func(context.Context, llmusage.Scope) (config.ModelOptionsMap, error)

var modelOptionsResolver ModelOptionsResolver

func prepareConfiguredResponsesRequest(ctx context.Context, cfg *config.ArkConfig, request *responses.ResponsesRequest, scope llmusage.Scope) (*responses.ResponsesRequest, error) {
	if request == nil {
		return nil, nil
	}
	var options config.ModelOptionsMap
	if modelOptionsResolver != nil {
		var err error
		options, err = modelOptionsResolver(ctx, scope)
		if err != nil {
			return nil, err
		}
	} else if cfg != nil {
		options = cfg.ModelOptions
	}
	if err := options.Validate(); err != nil {
		return nil, err
	}
	prepared := prepareResponsesRequest(cfg, request)
	option := options[strings.TrimSpace(request.Model)]
	if option.ServiceTier != "" {
		prepared.ServiceTier = responses.ResponsesServiceTier_Enum(responses.ResponsesServiceTier_Enum_value[option.ServiceTier]).Enum()
	}
	if option.ReasoningEffort != "" {
		// Explicit per-model effort opts this model in, superseding the legacy
		// reasoning_effort_models allowlist while preserving other fields.
		reasoning := responses.ResponsesReasoning{}
		if request.Reasoning != nil {
			reasoning = *request.Reasoning
		}
		reasoning.Effort = responses.ReasoningEffort_Enum(responses.ReasoningEffort_Enum_value[option.ReasoningEffort])
		prepared.Reasoning = &reasoning
	}
	return prepared, nil
}
