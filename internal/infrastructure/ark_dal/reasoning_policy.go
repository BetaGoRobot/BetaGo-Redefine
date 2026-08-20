package ark_dal

import (
	"strings"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/config"
	"github.com/volcengine/volcengine-go-sdk/service/arkruntime/model/responses"
)

func prepareResponsesRequest(
	cfg *config.ArkConfig,
	request *responses.ResponsesRequest,
) *responses.ResponsesRequest {
	if request == nil {
		return nil
	}
	prepared := *request
	prepared.Reasoning = effectiveResponsesReasoning(
		cfg,
		request.GetModel(),
		request.GetReasoning(),
	)
	return &prepared
}

func effectiveResponsesReasoning(
	cfg *config.ArkConfig,
	modelID string,
	reasoning *responses.ResponsesReasoning,
) *responses.ResponsesReasoning {
	if cfg == nil || reasoning == nil {
		return nil
	}
	modelID = strings.TrimSpace(modelID)
	if modelID == "" {
		return nil
	}
	for _, allowed := range cfg.ReasoningEffortModels {
		if strings.TrimSpace(allowed) == modelID {
			return reasoning
		}
	}
	return nil
}
