package chatflow

import (
	"context"
	"errors"
	"iter"

	appconfig "github.com/BetaGoRobot/BetaGo-Redefine/internal/application/config"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/ark_dal"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
	"github.com/volcengine/volcengine-go-sdk/service/arkruntime/model/responses"
)

// Plan carries chat flow state.
type Plan struct {
	ModelID                     string                         `json:"model_id,omitempty"`
	Mode                        appconfig.ChatMode             `json:"mode,omitempty"`
	ReasoningEffort             responses.ReasoningEffort_Enum `json:"reasoning_effort,omitempty"`
	Size                        int                            `json:"size,omitempty"`
	Files                       []string                       `json:"files,omitempty"`
	Args                        []string                       `json:"args,omitempty"`
	EnableDeferredToolCollector bool                           `json:"enable_deferred_tool_collector,omitempty"`
}

// PlanGenerator names a chat flow type.
type PlanGenerator func(context.Context, *larkim.P2MessageReceiveV1, Plan) (iter.Seq[*ark_dal.ModelStreamRespReasoning], error)

// PlanExecutor defines a chat flow contract.
type PlanExecutor interface {
	Generate(context.Context, *larkim.P2MessageReceiveV1, Plan) (iter.Seq[*ark_dal.ModelStreamRespReasoning], error)
}

type planGeneratorFunc PlanGenerator

// Generate implements chat flow behavior.
func (f planGeneratorFunc) Generate(ctx context.Context, event *larkim.P2MessageReceiveV1, plan Plan) (iter.Seq[*ark_dal.ModelStreamRespReasoning], error) {
	return f(ctx, event, plan)
}

var planExecutor PlanExecutor = planGeneratorFunc(func(context.Context, *larkim.P2MessageReceiveV1, Plan) (iter.Seq[*ark_dal.ModelStreamRespReasoning], error) {
	return nil, errors.New("chat generation plan executor is not configured")
})

// SetPlanExecutor implements chat flow behavior.
func SetPlanExecutor(executor PlanExecutor) {
	if executor == nil {
		planExecutor = planGeneratorFunc(func(context.Context, *larkim.P2MessageReceiveV1, Plan) (iter.Seq[*ark_dal.ModelStreamRespReasoning], error) {
			return nil, errors.New("chat generation plan executor is not configured")
		})
		return
	}
	planExecutor = executor
}

// SetPlanGenerator implements chat flow behavior.
func SetPlanGenerator(generator PlanGenerator) {
	if generator == nil {
		SetPlanExecutor(nil)
		return
	}
	SetPlanExecutor(planGeneratorFunc(generator))
}

// PlanExecutorOrDefault implements chat flow behavior.
func PlanExecutorOrDefault() PlanExecutor {
	return planExecutor
}

// Generate implements chat flow behavior.
func (p Plan) Generate(ctx context.Context, event *larkim.P2MessageReceiveV1) (iter.Seq[*ark_dal.ModelStreamRespReasoning], error) {
	return planExecutor.Generate(ctx, event, p)
}

// ClonePlan implements chat flow behavior.
func ClonePlan(plan Plan) Plan {
	plan.Files = append([]string(nil), plan.Files...)
	plan.Args = append([]string(nil), plan.Args...)
	return plan
}
