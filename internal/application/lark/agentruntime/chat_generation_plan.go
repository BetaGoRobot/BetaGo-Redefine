package agentruntime

import (
	"context"
	"errors"
	"iter"

	appconfig "github.com/BetaGoRobot/BetaGo-Redefine/internal/application/config"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/ark_dal"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
	"github.com/volcengine/volcengine-go-sdk/service/arkruntime/model/responses"
)

type ChatGenerationPlan struct {
	ModelID                     string                         `json:"model_id,omitempty"`
	Mode                        appconfig.ChatMode             `json:"mode,omitempty"`
	ReasoningEffort             responses.ReasoningEffort_Enum `json:"reasoning_effort,omitempty"`
	Size                        int                            `json:"size,omitempty"`
	Files                       []string                       `json:"files,omitempty"`
	Args                        []string                       `json:"args,omitempty"`
	EnableDeferredToolCollector bool                           `json:"enable_deferred_tool_collector,omitempty"`
}

type ChatGenerationPlanGenerator func(context.Context, *larkim.P2MessageReceiveV1, ChatGenerationPlan) (iter.Seq[*ark_dal.ModelStreamRespReasoning], error)

type ChatGenerationPlanExecutor interface {
	Generate(context.Context, *larkim.P2MessageReceiveV1, ChatGenerationPlan) (iter.Seq[*ark_dal.ModelStreamRespReasoning], error)
}

type chatGenerationPlanGeneratorFunc ChatGenerationPlanGenerator

func (f chatGenerationPlanGeneratorFunc) Generate(ctx context.Context, event *larkim.P2MessageReceiveV1, plan ChatGenerationPlan) (iter.Seq[*ark_dal.ModelStreamRespReasoning], error) {
	return f(ctx, event, plan)
}

var chatGenerationPlanExecutor ChatGenerationPlanExecutor = chatGenerationPlanGeneratorFunc(func(context.Context, *larkim.P2MessageReceiveV1, ChatGenerationPlan) (iter.Seq[*ark_dal.ModelStreamRespReasoning], error) {
	return nil, errors.New("chat generation plan executor is not configured")
})

func SetChatGenerationPlanExecutor(executor ChatGenerationPlanExecutor) {
	if executor == nil {
		chatGenerationPlanExecutor = chatGenerationPlanGeneratorFunc(func(context.Context, *larkim.P2MessageReceiveV1, ChatGenerationPlan) (iter.Seq[*ark_dal.ModelStreamRespReasoning], error) {
			return nil, errors.New("chat generation plan executor is not configured")
		})
		return
	}
	chatGenerationPlanExecutor = executor
}

func SetChatGenerationPlanGenerator(generator ChatGenerationPlanGenerator) {
	if generator == nil {
		SetChatGenerationPlanExecutor(nil)
		return
	}
	SetChatGenerationPlanExecutor(chatGenerationPlanGeneratorFunc(generator))
}

func (p ChatGenerationPlan) Generate(ctx context.Context, event *larkim.P2MessageReceiveV1) (iter.Seq[*ark_dal.ModelStreamRespReasoning], error) {
	return chatGenerationPlanExecutor.Generate(ctx, event, p)
}
